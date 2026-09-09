package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/VeVarunSharma/sodapop/internal/securefs"
)

const (
	indexFilename   = "sodapop-sessions.json"
	sessionIDPrefix = "sodapop-"
	sessionIDBytes  = 16
)

type sessionRecord struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	CanonicalProject string    `json:"canonicalProject"`
	Account          string    `json:"account"`
	Model            string    `json:"model"`
	ContextTier      string    `json:"contextTier"`
	ReasoningEffort  string    `json:"reasoningEffort,omitempty"`
	SkillDigests     []string  `json:"skillDigests,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

func (r sessionRecord) session() Session {
	return Session{
		ID: r.ID, Title: r.Title, Project: r.CanonicalProject, Model: r.Model,
		ContextTier: r.ContextTier, ReasoningEffort: r.ReasoningEffort,
		SkillDigests: append([]string(nil), r.SkillDigests...), UpdatedAt: r.UpdatedAt,
	}
}

type sessionIndex struct {
	home    string
	project string
	account string
}

type indexDocument struct {
	Version  int             `json:"version"`
	Sessions []sessionRecord `json:"sessions"`
}

func newSessionID() (string, error) {
	var bytes [sessionIDBytes]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	return sessionIDPrefix + hex.EncodeToString(bytes[:]), nil
}

func validSessionID(id string) bool {
	if len(id) != len(sessionIDPrefix)+hex.EncodedLen(sessionIDBytes) || !strings.HasPrefix(id, sessionIDPrefix) {
		return false
	}
	_, err := hex.DecodeString(id[len(sessionIDPrefix):])
	return err == nil && strings.ToLower(id) == id
}

func validReasoningEffort(effort string) bool {
	return len(effort) <= 64 && strings.IndexFunc(effort, unicode.IsControl) < 0
}

func (s *sessionIndex) transaction(ctx context.Context, change func(*indexDocument) error, write bool) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.home)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	lock, err := lockIndex(ctx, root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	document := indexDocument{Version: 3, Sessions: []sessionRecord{}}
	file, err := root.Open(indexFilename)
	if err == nil {
		private, statErr := securefs.IsPrivateRegularFile(file)
		if statErr != nil || !private {
			closeErr := file.Close()
			return errors.Join(errors.New("Sodapop session index must be a private regular file"), statErr, closeErr)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 4*1024*1024+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return errors.Join(readErr, closeErr)
		}
		if len(data) > 4*1024*1024 {
			return errors.New("Sodapop session index exceeds the supported size")
		}
		if err := json.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("read Sodapop session index: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if document.Version == 1 {
		for i := range document.Sessions {
			document.Sessions[i].ContextTier = "default"
		}
		document.Version = 3
	} else if document.Version == 2 {
		document.Version = 3
	} else if document.Version != 3 {
		return errors.New("unsupported Sodapop session index version")
	}
	seen := make(map[string]bool)
	for _, record := range document.Sessions {
		if !validSessionID(record.ID) || seen[record.ID] || record.Account == "" ||
			!filepath.IsAbs(record.CanonicalProject) || filepath.Clean(record.CanonicalProject) != record.CanonicalProject ||
			record.Title == "" || record.UpdatedAt.IsZero() || validateModelID(record.Model) != nil ||
			record.ContextTier != "default" && record.ContextTier != "long_context" ||
			!validReasoningEffort(record.ReasoningEffort) {
			return errors.New("invalid or duplicate record in Sodapop session index")
		}
		if !validSkillDigests(record.SkillDigests) {
			return errors.New("invalid skill digest in Sodapop session index")
		}
		seen[record.ID] = true
	}
	if err := change(&document); err != nil {
		return err
	}
	if !write {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > 4*1024*1024 {
		return errors.New("Sodapop session index exceeds the supported size")
	}
	id, err := newSessionID()
	if err != nil {
		return err
	}
	tempName := "." + id + ".tmp"
	temp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			if removeErr := root.Remove(tempName); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, removeErr)
			}
		}
	}()
	if err := securefs.ProtectFile(temp); err != nil {
		return errors.Join(fmt.Errorf("protect temporary Sodapop session index: %w", err), temp.Close())
	}
	_, writeErr := temp.Write(append(data, '\n'))
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(tempName, indexFilename); err != nil {
		return err
	}
	renamed = true
	return syncIndexDirectory(root)
}

func (s *sessionIndex) list(ctx context.Context) ([]Session, error) {
	var sessions []Session
	err := s.transaction(ctx, func(document *indexDocument) error {
		for _, record := range document.Sessions {
			if record.Account == s.account && record.CanonicalProject == s.project {
				sessions = append(sessions, record.session())
			}
		}
		return nil
	}, false)
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, err
}

func (s *sessionIndex) find(ctx context.Context, id string) (Session, error) {
	if !validSessionID(id) {
		return Session{}, errors.New("invalid Sodapop session ID")
	}
	sessions, err := s.list(ctx)
	if err != nil {
		return Session{}, err
	}
	for _, session := range sessions {
		if session.ID == id {
			return session, nil
		}
	}
	return Session{}, errors.New("session is not a Sodapop conversation for this account and project")
}

func (s *sessionIndex) save(ctx context.Context, session Session) error {
	if !validSessionID(session.ID) || session.Project != s.project ||
		strings.TrimSpace(session.Title) == "" || session.UpdatedAt.IsZero() || validateModelID(session.Model) != nil ||
		session.ContextTier != "default" && session.ContextTier != "long_context" ||
		!validReasoningEffort(session.ReasoningEffort) || !validSkillDigests(session.SkillDigests) {
		return errors.New("refusing to store invalid or out-of-scope Sodapop session metadata")
	}
	record := sessionRecord{
		ID: session.ID, Title: session.Title, CanonicalProject: s.project,
		Account: s.account, Model: session.Model, ContextTier: session.ContextTier,
		ReasoningEffort: session.ReasoningEffort,
		SkillDigests:    append([]string(nil), session.SkillDigests...), UpdatedAt: session.UpdatedAt,
	}
	return s.transaction(ctx, func(document *indexDocument) error {
		for i, previous := range document.Sessions {
			if previous.ID == record.ID {
				if previous.Account != s.account || previous.CanonicalProject != s.project {
					return errors.New("refusing to overwrite another account or project's session")
				}
				document.Sessions[i] = record
				return nil
			}
		}
		document.Sessions = append(document.Sessions, record)
		return nil
	}, true)
}

func validSkillDigests(digests []string) bool {
	seen := make(map[string]bool, len(digests))
	for _, digest := range digests {
		if len(digest) != 64 || strings.ToLower(digest) != digest || seen[digest] {
			return false
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return false
		}
		seen[digest] = true
	}
	return true
}
