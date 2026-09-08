package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/VeVarunSharma/sodapop/internal/securefs"
)

const maxConfigBytes = 64 * 1024

type preferencesV1 struct {
	Version       int    `json:"version"`
	Theme         string `json:"theme"`
	Model         string `json:"model,omitempty"`
	ReducedMotion bool   `json:"reduced_motion"`
	NoColor       bool   `json:"no_color"`
	ASCII         bool   `json:"ascii"`
}

type preferencesV2 struct {
	Version       int    `json:"version"`
	Theme         string `json:"theme"`
	Model         string `json:"model,omitempty"`
	ReducedMotion bool   `json:"reduced_motion"`
	NoColor       bool   `json:"no_color"`
	ASCII         bool   `json:"ascii"`
	Personality   string `json:"personality"`
}

type Paths struct {
	ConfigFile string
	StateDir   string
}

func ResolvePaths() (Paths, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("locate Sodapop configuration directory: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("locate home directory: %w", err)
	}
	localData := ""
	if runtime.GOOS == "windows" {
		localData, err = os.UserCacheDir()
		if err != nil {
			return Paths{}, fmt.Errorf("locate Sodapop state directory: %w", err)
		}
	}
	return resolvePaths(runtime.GOOS, configDir, home, os.Getenv("XDG_STATE_HOME"), localData)
}

func resolvePaths(goos, configDir, home, stateHome, localData string) (Paths, error) {
	if !filepath.IsAbs(configDir) || !filepath.IsAbs(home) {
		return Paths{}, errors.New("Sodapop configuration and home directories must be absolute paths")
	}
	state := filepath.Join(configDir, "sodapop", "state")
	if goos == "linux" {
		if stateHome == "" {
			stateHome = filepath.Join(home, ".local", "state")
		}
		if !filepath.IsAbs(stateHome) {
			return Paths{}, errors.New("XDG_STATE_HOME must be an absolute path")
		}
		state = filepath.Join(stateHome, "sodapop")
	} else if goos == "windows" {
		if !filepath.IsAbs(localData) {
			return Paths{}, errors.New("Windows local application data directory must be an absolute path")
		}
		state = filepath.Join(localData, "sodapop", "state")
	}
	return Paths{
		ConfigFile: filepath.Join(configDir, "sodapop", "config.json"),
		StateDir:   state,
	}, nil
}

func (p Paths) AccountHome(accountID string) (string, error) {
	if strings.TrimSpace(accountID) == "" {
		return "", errors.New("cannot locate runtime state without a GitHub account identity")
	}
	sum := sha256.Sum256([]byte(accountID))
	return filepath.Join(p.StateDir, "copilot", hex.EncodeToString(sum[:])), nil
}

func Load(path string) (Preferences, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultPreferences(), nil
	}
	if err != nil {
		return Preferences{}, fmt.Errorf("open Sodapop preferences: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return Preferences{}, fmt.Errorf("read Sodapop preferences: %w", err)
	}
	if len(data) > maxConfigBytes {
		return Preferences{}, errors.New("Sodapop preferences exceed the supported size")
	}

	var header struct {
		Version int `json:"version"`
	}
	if err := decodeExactly(data, &header, false); err != nil {
		return Preferences{}, fmt.Errorf("decode Sodapop preferences: %w", err)
	}

	var prefs Preferences
	switch header.Version {
	case 1:
		var old preferencesV1
		if err := decodeExactly(data, &old, true); err != nil {
			return Preferences{}, fmt.Errorf("decode Sodapop preferences: %w", err)
		}
		prefs = migrateV1(old)
	case 2:
		var old preferencesV2
		if err := decodeExactly(data, &old, true); err != nil {
			return Preferences{}, fmt.Errorf("decode Sodapop preferences: %w", err)
		}
		prefs = migrateV2(old)
	case currentPreferencesVersion:
		if err := decodeExactly(data, &prefs, true); err != nil {
			return Preferences{}, fmt.Errorf("decode Sodapop preferences: %w", err)
		}
	default:
		return Preferences{}, fmt.Errorf(
			"unsupported Sodapop preferences version %d; expected version %d",
			header.Version,
			currentPreferencesVersion,
		)
	}
	if err := validate(prefs); err != nil {
		return Preferences{}, err
	}
	return prefs, nil
}

func decodeExactly(data []byte, destination any, rejectUnknown bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if rejectUnknown {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("Sodapop preferences must contain exactly one JSON object")
	}
	return nil
}

func migrateV1(old preferencesV1) Preferences {
	return Preferences{
		Version:       currentPreferencesVersion,
		Theme:         old.Theme,
		Model:         old.Model,
		ReducedMotion: old.ReducedMotion,
		NoColor:       old.NoColor,
		ASCII:         old.ASCII,
		Personality:   "playful",
		ModelSettings: make(map[string]ModelSettings),
	}
}

func migrateV2(old preferencesV2) Preferences {
	return Preferences{
		Version: currentPreferencesVersion, Theme: old.Theme, Model: old.Model,
		ReducedMotion: old.ReducedMotion, NoColor: old.NoColor, ASCII: old.ASCII,
		Personality: old.Personality, ModelSettings: make(map[string]ModelSettings),
	}
}

func Save(path string, prefs Preferences) error {
	if err := validate(prefs); err != nil {
		return err
	}
	data, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Sodapop preferences: %w", err)
	}
	if err := securefs.MkdirAllPrivate(filepath.Dir(path)); err != nil {
		return fmt.Errorf("create Sodapop configuration directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".sodapop-config-*.json")
	if err != nil {
		return fmt.Errorf("create temporary Sodapop preferences: %w", err)
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := securefs.ProtectFile(file); err != nil {
		file.Close()
		return fmt.Errorf("protect temporary Sodapop preferences: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return fmt.Errorf("write Sodapop preferences: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync Sodapop preferences: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Sodapop preferences: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("replace Sodapop preferences: %w", err)
	}
	return nil
}

func validate(prefs Preferences) error {
	if prefs.Version != currentPreferencesVersion {
		return fmt.Errorf(
			"unsupported Sodapop preferences version %d; expected version %d",
			prefs.Version,
			currentPreferencesVersion,
		)
	}
	if prefs.Theme == "" || len(prefs.Theme) > 64 {
		return errors.New("Sodapop preferences require a valid theme name")
	}
	switch prefs.Personality {
	case "quiet", "playful", "extra":
	default:
		return errors.New("Sodapop preferences require a valid personality")
	}
	if len(prefs.Model) > 256 {
		return errors.New("Sodapop model identifier exceeds the supported length")
	}
	for model, settings := range prefs.ModelSettings {
		if model == "" || len(model) > 256 || strings.IndexFunc(model, unicode.IsControl) >= 0 {
			return errors.New("Sodapop model settings require a valid model identifier")
		}
		if settings.ContextTier != "default" && settings.ContextTier != "long_context" {
			return errors.New("Sodapop model settings require a valid context tier")
		}
		if len(settings.ReasoningEffort) > 64 || strings.IndexFunc(settings.ReasoningEffort, unicode.IsControl) >= 0 {
			return errors.New("Sodapop model settings require a valid reasoning effort")
		}
	}
	for _, value := range []string{prefs.Theme, prefs.Model, prefs.Personality} {
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return errors.New("Sodapop preference identifiers cannot contain control characters")
		}
	}
	return nil
}
