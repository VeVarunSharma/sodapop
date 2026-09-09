package skills

import (
	"bufio"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/VeVarunSharma/sodapop/internal/securefs"
)

const (
	currentVersion   = 1
	maxDocumentBytes = 512 * 1024
	maxSkillFiles    = 256
	maxSkillBytes    = 4 * 1024 * 1024
	maxSkillFile     = 512 * 1024
)

var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	storeMu     sync.Mutex
)

//go:embed builtin/*/SKILL.md
var bundledSkills embed.FS

type Source struct {
	Kind     string `json:"kind"`
	Location string `json:"location"`
	Revision string `json:"revision,omitempty"`
}

type Entry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Digest      string `json:"digest"`
	Source      Source `json:"source"`
}

type Reference struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type Trust struct {
	LocalRoots      []string `json:"local_roots"`
	GitRepositories []string `json:"git_repositories"`
}

type State struct {
	Installed []Entry
	Enabled   []Reference
	Trust     Trust
}

type Resolved struct {
	Name      string
	Digest    string
	Directory string
	Enabled   bool
}

type Action struct {
	Operation string
	Value     string
}

type Manager struct {
	root        string
	projectKey  string
	runGit      func(context.Context, string, ...string) error
	gitRevision func(context.Context, string) (string, error)
}

type document struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

type activationDocument struct {
	Version int         `json:"version"`
	Enabled []Reference `json:"enabled"`
}

type trustDocument struct {
	Version int   `json:"version"`
	Trust   Trust `json:"trust"`
}

func New(stateDir, canonicalProject string) (*Manager, error) {
	if !filepath.IsAbs(stateDir) || !filepath.IsAbs(canonicalProject) {
		return nil, errors.New("skill state and project paths must be absolute")
	}
	sum := sha256.Sum256([]byte(filepath.Clean(canonicalProject)))
	return &Manager{
		root:       filepath.Join(filepath.Clean(stateDir), "skills"),
		projectKey: hex.EncodeToString(sum[:]),
		runGit:     runGit,
		gitRevision: func(ctx context.Context, directory string) (string, error) {
			return gitOutput(ctx, directory, "rev-parse", "HEAD")
		},
	}, nil
}

func (m *Manager) Load() (State, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	return m.load()
}

func (m *Manager) Apply(ctx context.Context, action Action) (State, error) {
	switch action.Operation {
	case "load":
		return m.Load()
	case "trust":
		return m.TrustSource(action.Value)
	case "install":
		return m.Install(ctx, action.Value)
	case "enable":
		return m.SetEnabled(action.Value, true)
	case "disable":
		return m.SetEnabled(action.Value, false)
	case "remove":
		return m.Remove(action.Value)
	default:
		return State{}, fmt.Errorf("unsupported skill operation %q", action.Operation)
	}
}

func (m *Manager) load() (State, error) {
	catalog := document{Version: currentVersion, Entries: []Entry{}}
	if err := loadJSON(m.catalogPath(), &catalog); err != nil {
		return State{}, err
	}
	activation := activationDocument{Version: currentVersion, Enabled: []Reference{}}
	if err := loadJSON(m.activationPath(), &activation); err != nil {
		return State{}, err
	}
	trust := trustDocument{Version: currentVersion, Trust: Trust{LocalRoots: []string{}, GitRepositories: []string{}}}
	if err := loadJSON(m.trustPath(), &trust); err != nil {
		return State{}, err
	}
	if err := validateState(catalog, activation, trust); err != nil {
		return State{}, err
	}
	if err := m.ensureBundled(&catalog); err != nil {
		return State{}, err
	}
	return State{Installed: catalog.Entries, Enabled: activation.Enabled, Trust: trust.Trust}, nil
}

func (m *Manager) ensureBundled(catalog *document) error {
	directories, err := bundledSkills.ReadDir("builtin")
	if err != nil {
		return err
	}
	changed := false
	for _, directory := range directories {
		if !directory.IsDir() {
			continue
		}
		path := filepath.ToSlash(filepath.Join("builtin", directory.Name(), "SKILL.md"))
		data, err := bundledSkills.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read bundled skill %q: %w", directory.Name(), err)
		}
		name, description, err := parseManifest(data)
		if err != nil {
			return fmt.Errorf("validate bundled skill %q: %w", directory.Name(), err)
		}
		file := skillFile{path: "SKILL.md", mode: 0600, data: data}
		hash := sha256.New()
		fmt.Fprintf(hash, "%s\x00%o\x00%d\x00", file.path, file.mode.Perm()&0111, len(file.data))
		_, _ = hash.Write(file.data)
		entry := Entry{
			Name: name, Description: description, Digest: hex.EncodeToString(hash.Sum(nil)),
			Source: Source{Kind: "bundled", Location: "sodapop"},
		}
		digestRoot := filepath.Join(m.storePath(), entry.Digest)
		destination := filepath.Join(digestRoot, entry.Name)
		if _, err := os.Stat(destination); errors.Is(err, os.ErrNotExist) {
			if err := securefs.MkdirAllPrivate(digestRoot); err != nil {
				return err
			}
			temp := filepath.Join(digestRoot, ".install-"+entry.Name)
			if err := copyFiles("", temp, []skillFile{file}); err != nil {
				return err
			}
			if err := os.Rename(temp, destination); err != nil {
				_ = os.RemoveAll(temp)
				return err
			}
		} else if err != nil {
			return err
		}
		replaced := false
		for index := range catalog.Entries {
			if strings.EqualFold(catalog.Entries[index].Name, entry.Name) {
				replaced = true
				if catalog.Entries[index] != entry {
					catalog.Entries[index] = entry
					changed = true
				}
				break
			}
		}
		if !replaced {
			catalog.Entries = append(catalog.Entries, entry)
			changed = true
		}
	}
	if changed {
		sort.Slice(catalog.Entries, func(i, j int) bool { return catalog.Entries[i].Name < catalog.Entries[j].Name })
		return saveJSON(m.catalogPath(), *catalog)
	}
	return nil
}

func (m *Manager) TrustSource(source string) (State, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	state, err := m.load()
	if err != nil {
		return State{}, err
	}
	if repo, ok, err := normalizeGitSource(source); err != nil {
		return State{}, err
	} else if ok {
		if !containsFold(state.Trust.GitRepositories, repo) {
			state.Trust.GitRepositories = append(state.Trust.GitRepositories, repo)
			sort.Strings(state.Trust.GitRepositories)
		}
	} else {
		root, err := canonicalDirectory(source)
		if err != nil {
			return State{}, fmt.Errorf("trust local skill root: %w", err)
		}
		if !containsPath(state.Trust.LocalRoots, root) {
			state.Trust.LocalRoots = append(state.Trust.LocalRoots, root)
			sort.Strings(state.Trust.LocalRoots)
		}
	}
	if err := saveJSON(m.trustPath(), trustDocument{Version: currentVersion, Trust: state.Trust}); err != nil {
		return State{}, err
	}
	return state, nil
}

func (m *Manager) Install(ctx context.Context, source string) (State, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	state, err := m.load()
	if err != nil {
		return State{}, err
	}
	staging, err := os.MkdirTemp("", "sodapop-skill-*")
	if err != nil {
		return State{}, fmt.Errorf("create skill staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := securefs.MkdirAllPrivate(staging); err != nil {
		return State{}, fmt.Errorf("protect skill staging directory: %w", err)
	}

	var root string
	var origin Source
	if repo, ok, err := normalizeGitSource(source); err != nil {
		return State{}, err
	} else if ok {
		location, revision := splitGitRevision(source)
		if !containsFold(state.Trust.GitRepositories, repo) {
			return State{}, fmt.Errorf("Git repository %q is not trusted; run /skill trust %s first", repo, repo)
		}
		root = filepath.Join(staging, "repository")
		if err := m.clone(ctx, location, revision, root); err != nil {
			return State{}, err
		}
		commit, err := m.gitRevision(ctx, root)
		if err != nil {
			return State{}, err
		}
		origin = Source{Kind: "git", Location: repo, Revision: commit}
	} else {
		root, err = canonicalDirectory(source)
		if err != nil {
			return State{}, fmt.Errorf("open local skill: %w", err)
		}
		if !withinTrustedRoot(state.Trust.LocalRoots, root) {
			return State{}, fmt.Errorf("local skill %q is outside trusted roots; run /skill trust <directory> first", root)
		}
		origin = Source{Kind: "local", Location: root}
	}

	entry, files, err := inspect(root, origin)
	if err != nil {
		return State{}, err
	}
	if previous, ok := findEntry(state.Installed, entry.Name); ok && previous.Digest != entry.Digest {
		for _, ref := range state.Enabled {
			if strings.EqualFold(ref.Name, entry.Name) {
				return State{}, fmt.Errorf("disable skill %q before installing a replacement version", entry.Name)
			}
		}
	}
	digestRoot := filepath.Join(m.storePath(), entry.Digest)
	destination := filepath.Join(digestRoot, entry.Name)
	if _, err := os.Stat(destination); errors.Is(err, os.ErrNotExist) {
		if err := securefs.MkdirAllPrivate(digestRoot); err != nil {
			return State{}, fmt.Errorf("create skill content directory: %w", err)
		}
		temp := filepath.Join(digestRoot, ".install-"+entry.Name)
		if err := copyFiles(root, temp, files); err != nil {
			return State{}, err
		}
		if err := os.Rename(temp, destination); err != nil {
			_ = os.RemoveAll(temp)
			return State{}, fmt.Errorf("publish skill content: %w", err)
		}
	} else if err != nil {
		return State{}, fmt.Errorf("inspect installed skill: %w", err)
	}

	replaced := false
	for i := range state.Installed {
		if strings.EqualFold(state.Installed[i].Name, entry.Name) {
			state.Installed[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		state.Installed = append(state.Installed, entry)
	}
	sort.Slice(state.Installed, func(i, j int) bool { return state.Installed[i].Name < state.Installed[j].Name })
	if err := saveJSON(m.catalogPath(), document{Version: currentVersion, Entries: state.Installed}); err != nil {
		return State{}, err
	}
	return state, nil
}

func (m *Manager) SetEnabled(name string, enabled bool) (State, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	state, err := m.load()
	if err != nil {
		return State{}, err
	}
	entry, ok := findEntry(state.Installed, name)
	if !ok {
		return State{}, fmt.Errorf("unknown skill %q", name)
	}
	next := make([]Reference, 0, len(state.Enabled)+1)
	for _, ref := range state.Enabled {
		if !strings.EqualFold(ref.Name, name) {
			next = append(next, ref)
		}
	}
	if enabled {
		next = append(next, Reference{Name: entry.Name, Digest: entry.Digest})
	}
	sort.Slice(next, func(i, j int) bool { return next[i].Name < next[j].Name })
	if err := saveJSON(m.activationPath(), activationDocument{Version: currentVersion, Enabled: next}); err != nil {
		return State{}, err
	}
	state.Enabled = next
	return state, nil
}

func (m *Manager) Remove(name string) (State, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	state, err := m.load()
	if err != nil {
		return State{}, err
	}
	for _, ref := range state.Enabled {
		if strings.EqualFold(ref.Name, name) {
			return State{}, fmt.Errorf("disable skill %q before removing it", name)
		}
	}
	index := -1
	for i := range state.Installed {
		if strings.EqualFold(state.Installed[i].Name, name) {
			index = i
			break
		}
	}
	if index < 0 {
		return State{}, fmt.Errorf("unknown skill %q", name)
	}
	state.Installed = append(state.Installed[:index], state.Installed[index+1:]...)
	if err := saveJSON(m.catalogPath(), document{Version: currentVersion, Entries: state.Installed}); err != nil {
		return State{}, err
	}
	return state, nil
}

func (m *Manager) Resolve() ([]Resolved, []string, error) {
	state, err := m.Load()
	if err != nil {
		return nil, nil, err
	}
	enabled := make(map[string]bool, len(state.Enabled))
	for _, ref := range state.Enabled {
		enabled[ref.Digest] = true
	}
	resolved := make([]Resolved, 0, len(state.Installed))
	known := make(map[string]bool, len(state.Installed))
	for _, entry := range state.Installed {
		parent := filepath.Join(m.storePath(), entry.Digest)
		if err := verifyInstalled(parent, entry); err != nil {
			return nil, nil, err
		}
		resolved = append(resolved, Resolved{
			Name: entry.Name, Digest: entry.Digest, Directory: parent, Enabled: enabled[entry.Digest],
		})
		known[entry.Digest] = true
	}
	versions, err := m.storedVersions(known)
	if err != nil {
		return nil, nil, err
	}
	resolved = append(resolved, versions...)
	active := make([]string, 0, len(state.Enabled))
	for _, ref := range state.Enabled {
		entry, ok := findEntry(state.Installed, ref.Name)
		if !ok || entry.Digest != ref.Digest {
			return nil, nil, fmt.Errorf("enabled skill %q is not installed at digest %s", ref.Name, ref.Digest)
		}
		active = append(active, ref.Digest)
	}
	return resolved, active, nil
}

func (m *Manager) clone(ctx context.Context, source, revision, destination string) error {
	if revision == "" {
		if err := m.runGit(ctx, "", "clone", "--quiet", "--depth", "1", "--", source, destination); err != nil {
			return fmt.Errorf("clone trusted skill repository: %w", err)
		}
		return nil
	}
	if err := m.runGit(ctx, "", "clone", "--quiet", "--filter=blob:none", "--no-checkout", "--", source, destination); err != nil {
		return fmt.Errorf("clone trusted skill repository: %w", err)
	}
	if err := m.runGit(ctx, destination, "fetch", "--quiet", "--depth", "1", "origin", revision); err != nil {
		return fmt.Errorf("fetch skill revision %q: %w", revision, err)
	}
	if err := m.runGit(ctx, destination, "checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("check out skill revision %q: %w", revision, err)
	}
	return nil
}

func (m *Manager) storedVersions(known map[string]bool) ([]Resolved, error) {
	digests, err := os.ReadDir(m.storePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var versions []Resolved
	for _, digestDir := range digests {
		digest := digestDir.Name()
		if !digestDir.IsDir() || !validDigest(digest) || known[digest] {
			continue
		}
		parent := filepath.Join(m.storePath(), digest)
		skillDirs, err := os.ReadDir(parent)
		if err != nil {
			return nil, err
		}
		for _, skillDir := range skillDirs {
			if !skillDir.IsDir() || strings.HasPrefix(skillDir.Name(), ".") {
				continue
			}
			entry, _, err := inspect(filepath.Join(parent, skillDir.Name()), Source{Kind: "local", Location: parent})
			if err != nil {
				return nil, fmt.Errorf("verify retained skill version %s: %w", digest, err)
			}
			if entry.Digest != digest {
				return nil, fmt.Errorf("retained skill %q does not match directory digest", entry.Name)
			}
			versions = append(versions, Resolved{Name: entry.Name, Digest: digest, Directory: parent})
		}
	}
	return versions, nil
}

func runGit(ctx context.Context, directory string, args ...string) error {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func gitOutput(ctx context.Context, directory string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("inspect cloned skill repository: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

type skillFile struct {
	path string
	mode os.FileMode
	data []byte
}

func inspect(root string, source Source) (Entry, []skillFile, error) {
	var files []skillFile
	total := 0
	err := filepath.WalkDir(root, func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if relative == ".git" && item.IsDir() {
			return filepath.SkipDir
		}
		if item.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill contains symbolic link %q", relative)
		}
		if item.IsDir() {
			return nil
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill contains unsupported file %q", relative)
		}
		if info.Size() > maxSkillFile {
			return fmt.Errorf("skill file %q exceeds %d bytes", relative, maxSkillFile)
		}
		if len(files) >= maxSkillFiles {
			return fmt.Errorf("skill exceeds %d files", maxSkillFiles)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		total += len(data)
		if total > maxSkillBytes {
			return fmt.Errorf("skill exceeds %d bytes", maxSkillBytes)
		}
		files = append(files, skillFile{path: filepath.ToSlash(relative), mode: info.Mode(), data: data})
		return nil
	})
	if err != nil {
		return Entry{}, nil, fmt.Errorf("validate skill contents: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	var manifest []byte
	for _, file := range files {
		if file.path == "SKILL.md" {
			manifest = file.data
			break
		}
	}
	if manifest == nil {
		return Entry{}, nil, errors.New("Copilot skill requires SKILL.md at its root")
	}
	name, description, err := parseManifest(manifest)
	if err != nil {
		return Entry{}, nil, err
	}
	hash := sha256.New()
	for _, file := range files {
		fmt.Fprintf(hash, "%s\x00%o\x00%d\x00", file.path, file.mode.Perm()&0111, len(file.data))
		_, _ = hash.Write(file.data)
	}
	return Entry{
		Name: name, Description: description, Digest: hex.EncodeToString(hash.Sum(nil)), Source: source,
	}, files, nil
}

func parseManifest(data []byte) (string, string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", "", errors.New("SKILL.md requires YAML frontmatter")
	}
	values := map[string]string{}
	closed := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			closed = true
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "name" && key != "description" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' {
			if decoded, err := strconv.Unquote(value); err == nil {
				value = decoded
			}
		} else if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if !closed {
		return "", "", errors.New("SKILL.md frontmatter is not closed")
	}
	name, description := values["name"], strings.TrimSpace(values["description"])
	if !namePattern.MatchString(name) {
		return "", "", errors.New("SKILL.md name must use lowercase letters, digits, and hyphens")
	}
	if description == "" || len(description) > 1024 || strings.IndexFunc(description, unicode.IsControl) >= 0 {
		return "", "", errors.New("SKILL.md requires a valid description")
	}
	return name, description, nil
}

func copyFiles(root, destination string, files []skillFile) error {
	if err := securefs.MkdirAllPrivate(destination); err != nil {
		return fmt.Errorf("create staged skill: %w", err)
	}
	for _, file := range files {
		path := filepath.Join(destination, filepath.FromSlash(file.path))
		if err := securefs.MkdirAllPrivate(filepath.Dir(path)); err != nil {
			return fmt.Errorf("create skill directory: %w", err)
		}
		mode := os.FileMode(0600)
		if file.mode.Perm()&0111 != 0 {
			mode = 0700
		}
		if err := os.WriteFile(path, file.data, mode); err != nil {
			return fmt.Errorf("copy skill file %q: %w", file.path, err)
		}
	}
	return nil
}

func verifyInstalled(parent string, entry Entry) error {
	root := filepath.Join(parent, entry.Name)
	found, _, err := inspect(root, entry.Source)
	if err != nil {
		return fmt.Errorf("verify installed skill %q: %w", entry.Name, err)
	}
	if found.Digest != entry.Digest || found.Name != entry.Name {
		return fmt.Errorf("installed skill %q does not match catalog digest", entry.Name)
	}
	return nil
}

func validateState(catalog document, activation activationDocument, trust trustDocument) error {
	if catalog.Version != currentVersion || activation.Version != currentVersion || trust.Version != currentVersion {
		return errors.New("unsupported Sodapop skill state version")
	}
	seenNames, seenDigests := map[string]bool{}, map[string]bool{}
	for _, entry := range catalog.Entries {
		if !namePattern.MatchString(entry.Name) || !validDigest(entry.Digest) || entry.Description == "" {
			return errors.New("invalid entry in Sodapop skill catalog")
		}
		key := strings.ToLower(entry.Name)
		if seenNames[key] || seenDigests[entry.Digest] {
			return errors.New("duplicate entry in Sodapop skill catalog")
		}
		seenNames[key], seenDigests[entry.Digest] = true, true
		if entry.Source.Kind != "local" && entry.Source.Kind != "git" && entry.Source.Kind != "bundled" {
			return errors.New("invalid skill source kind")
		}
	}
	enabled := map[string]bool{}
	for _, ref := range activation.Enabled {
		if !namePattern.MatchString(ref.Name) || !validDigest(ref.Digest) || enabled[strings.ToLower(ref.Name)] {
			return errors.New("invalid project skill activation")
		}
		enabled[strings.ToLower(ref.Name)] = true
	}
	for _, root := range trust.Trust.LocalRoots {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return errors.New("invalid trusted local skill root")
		}
	}
	for _, repo := range trust.Trust.GitRepositories {
		if normalized, ok, err := normalizeGitSource(repo); err != nil || !ok || normalized != repo {
			return errors.New("invalid trusted Git skill repository")
		}
	}
	return nil
}

func loadJSON(path string, destination any) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Sodapop skill state: %w", err)
	}
	defer file.Close()
	private, err := securefs.IsPrivateRegularFile(file)
	if err != nil || !private {
		return errors.Join(errors.New("Sodapop skill state must be a private regular file"), err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if err != nil {
		return fmt.Errorf("read Sodapop skill state: %w", err)
	}
	if len(data) > maxDocumentBytes {
		return errors.New("Sodapop skill state exceeds the supported size")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode Sodapop skill state: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("Sodapop skill state must contain exactly one JSON object")
	}
	return nil
}

func saveJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > maxDocumentBytes {
		return errors.New("Sodapop skill state exceeds the supported size")
	}
	if err := securefs.MkdirAllPrivate(filepath.Dir(path)); err != nil {
		return fmt.Errorf("create Sodapop skill state directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".sodapop-skills-*.json")
	if err != nil {
		return fmt.Errorf("create temporary Sodapop skill state: %w", err)
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := securefs.ProtectFile(file); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("replace Sodapop skill state: %w", err)
	}
	return nil
}

func normalizeGitSource(source string) (string, bool, error) {
	location, _ := splitGitRevision(strings.TrimSpace(source))
	if filepath.IsAbs(location) {
		return "", false, nil
	}
	parsed, err := url.Parse(location)
	if err != nil || parsed.Scheme == "" {
		return "", false, nil
	}
	if parsed.Scheme != "https" {
		return "", true, errors.New("Git skill repositories must use public https URLs")
	}
	if parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" {
		return "", true, errors.New("Git skill repository must not contain credentials or query parameters")
	}
	parsed.Fragment = ""
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed.String(), true, nil
}

func splitGitRevision(source string) (string, string) {
	source = strings.TrimSpace(source)
	parsed, err := url.Parse(source)
	if err != nil {
		return source, ""
	}
	revision := parsed.Fragment
	parsed.Fragment = ""
	return parsed.String(), revision
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return filepath.Clean(resolved), nil
}

func withinTrustedRoot(roots []string, path string) bool {
	for _, root := range roots {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func containsPath(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func containsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}

func findEntry(entries []Entry, name string) (Entry, bool) {
	for _, entry := range entries {
		if strings.EqualFold(entry.Name, name) {
			return entry, true
		}
	}
	return Entry{}, false
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (m *Manager) catalogPath() string {
	return filepath.Join(m.root, "catalog.json")
}

func (m *Manager) trustPath() string {
	return filepath.Join(m.root, "trust.json")
}

func (m *Manager) activationPath() string {
	return filepath.Join(m.root, "projects", m.projectKey+".json")
}

func (m *Manager) storePath() string {
	return filepath.Join(m.root, "store")
}
