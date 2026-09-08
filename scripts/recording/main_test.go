package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/ui"
)

func TestRecordingAuthCannotAccessAnAccountOrStartSignIn(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if cancelled {
			cancel()
		}
		want := auth.ErrNotSignedIn
		if cancelled {
			want = context.Canceled
		}
		service := signedOut{}
		account, err := service.Current(ctx)
		if account != (auth.Account{}) || !errors.Is(err, want) {
			t.Fatalf("Current = %+v, %v", account, err)
		}
		token, err := service.Token(ctx)
		if token != "" || !errors.Is(err, want) {
			t.Fatalf("Token returned a credential or the wrong error: %v", err)
		}
		if err := service.SignOut(ctx); !errors.Is(err, want) {
			t.Fatalf("SignOut = %v", err)
		}
		if !cancelled {
			want = errRecordingSignIn
		}
		for _, sessionOnly := range []bool{false, true} {
			called := false
			account, err := service.Login(ctx, sessionOnly, func(auth.DeviceCode) { called = true })
			if called || account != (auth.Account{}) || !errors.Is(err, want) {
				t.Fatalf("Login started a device flow or returned an identity: %+v, %v, callback=%v", account, err, called)
			}
		}
	}
}

func TestRecordingOptionsUseFreshPreferencesAndRealLocalWorkspace(t *testing.T) {
	project := t.TempDir()
	alias := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SODAPOP_GITHUB_CLIENT_ID", "must-not-be-used")
	t.Setenv("GITHUB_TOKEN", "must-not-be-used")
	ctx := context.Background()
	for _, reduced := range []bool{false, true} {
		options, err := recordingOptions(ctx, alias, reduced)
		if err != nil {
			t.Fatal(err)
		}
		if options.Project != canonical || options.Context != ctx || options.Version != "demo" {
			t.Fatalf("incorrect recording scope: %+v", options)
		}
		if options.NewEngine != nil || options.SavePreferences != nil || len(options.MCPServers) != 0 || len(options.Skills) != 0 {
			t.Fatal("recording options enabled a runtime, persistence, or external capabilities")
		}
		if options.Preferences.Theme != config.DefaultPreferences().Theme || options.Preferences.ReducedMotion != reduced {
			t.Fatalf("preferences are not fresh: %+v", options.Preferences)
		}
		if _, err := options.Auth.Current(ctx); !errors.Is(err, auth.ErrNotSignedIn) {
			t.Fatalf("recording attempted to restore an account: %v", err)
		}
		diff, err := options.Workspace.Diff(ctx, "all")
		if err != nil || diff.IsRepository || !strings.Contains(diff.Text, "Not a Git working tree.") {
			t.Fatalf("workspace did not inspect the real, empty project: %+v, %v", diff, err)
		}
	}
	for _, path := range []string{home, project} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatalf("recording construction wrote to %s: %v, %v", path, entries, err)
		}
	}
}

func TestRecordingRunRejectsInvalidInputsBeforeStartingUI(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		args    []string
		project string
		want    string
	}{
		{"unknown flag", []string{"--live"}, t.TempDir(), "flag provided but not defined"},
		{"positional argument", []string{"unexpected"}, t.TempDir(), "no positional arguments"},
		{"missing project", nil, filepath.Join(t.TempDir(), "missing"), "resolve demo project"},
		{"file project", nil, file, "must be a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			started := false
			err := run(context.Background(), tc.args, tc.project, &output, func(context.Context, *ui.Model) error {
				started = true
				return nil
			})
			if started || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid launch: started=%v, err=%v, output=%s", started, err, &output)
			}
		})
	}
}

func TestRecordingRunPropagatesFailuresAndHandlesCancellation(t *testing.T) {
	failure := errors.New("terminal failed")
	for _, tc := range []struct {
		name      string
		cancelled bool
		result    error
		want      error
	}{
		{"success", false, nil, nil},
		{"terminal failure", false, failure, failure},
		{"cancelled", true, context.Canceled, nil},
		{"killed after cancellation", true, tea.ErrProgramKilled, nil},
		{"unrelated failure after cancellation", true, failure, failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := false
			err := run(ctx, []string{"--reduced-motion"}, t.TempDir(), &bytes.Buffer{}, func(ctx context.Context, model *ui.Model) error {
				started = true
				model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
				if model.View().Content == "" {
					t.Fatal("the recording launcher did not construct the real UI")
				}
				if tc.cancelled {
					cancel()
				}
				return tc.result
			})
			if !started || !errors.Is(err, tc.want) {
				t.Fatalf("run = %v, started=%v; want %v", err, started, tc.want)
			}
		})
	}
}
