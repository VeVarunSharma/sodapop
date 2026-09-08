package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VeVarunSharma/sodapop/internal/securefs"
)

func TestSessionIDNamespaceAndValidation(t *testing.T) {
	id, err := newSessionID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 40 || !strings.HasPrefix(id, "sodapop-") || !validSessionID(id) {
		t.Fatalf("generated invalid Sodapop session ID: %q", id)
	}
	next, err := newSessionID()
	if err != nil || next == id || !validSessionID(next) {
		t.Fatalf("second session ID: %q, %v", next, err)
	}
	for _, test := range []struct {
		name string
		id   string
		want bool
	}{
		{"lowercase hex", "sodapop-0123456789abcdef0123456789abcdef", true},
		{"empty", "", false},
		{"missing entropy", "sodapop-", false},
		{"short entropy", id[:37], false},
		{"long entropy", id + "0", false},
		{"other prefix", "another-0123456789abcdef0123456789abcdef", false},
		{"uppercase prefix", "SODAPOP-0123456789abcdef0123456789abcdef", false},
		{"uppercase hex", "sodapop-0123456789ABCDEF0123456789ABCDEF", false},
		{"non-hex entropy", "sodapop-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", false},
		{"path traversal", id + "/..", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validSessionID(test.id); got != test.want {
				t.Fatalf("validSessionID(%q) = %t, want %t", test.id, got, test.want)
			}
		})
	}
}

func indexSession(t *testing.T, project string) Session {
	t.Helper()
	id, err := newSessionID()
	if err != nil {
		t.Fatal(err)
	}
	return Session{
		ID: id, Title: "Fixture", Project: project, Model: "model-a",
		ContextTier: "default", UpdatedAt: time.Unix(100, 0).UTC(),
	}
}

func TestIndexScopeAndPersistence(t *testing.T) {
	project, outside := policyFixture(t)
	home := t.TempDir()
	index := &sessionIndex{home: home, project: project, account: "account-one"}
	session := indexSession(t, project)
	if err := index.save(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	if result, err := index.find(t.Context(), session.ID); err != nil || result != session {
		t.Fatalf("find = %+v, %v", result, err)
	}
	for _, foreign := range []*sessionIndex{
		{home: home, project: project, account: "account-two"},
		{home: home, project: outside, account: "account-one"},
	} {
		if sessions, err := foreign.list(t.Context()); err != nil || len(sessions) != 0 {
			t.Fatalf("foreign list = %+v, %v", sessions, err)
		}
		if _, err := foreign.find(t.Context(), session.ID); err == nil {
			t.Fatal("resumed foreign record")
		}
		copy := session
		copy.Project = foreign.project
		if err := foreign.save(t.Context(), copy); err == nil {
			t.Fatal("overwrote foreign record")
		}
	}
	file, err := os.Open(filepath.Join(home, "sodapop-sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	private, privacyErr := securefs.IsPrivateRegularFile(file)
	closeErr := file.Close()
	if privacyErr != nil || closeErr != nil || !private {
		t.Fatalf("index privacy: private=%t, err=%v, close=%v", private, privacyErr, closeErr)
	}
	for _, id := range []string{"", "../outside", "/tmp/file", session.ID + "/..", "copilot-session"} {
		if _, err := index.find(t.Context(), id); err == nil {
			t.Fatalf("accepted unsafe ID %q", id)
		}
	}
}

func TestIndexConcurrentWritersDoNotLoseMetadata(t *testing.T) {
	project, _ := policyFixture(t)
	home := t.TempDir()
	var workers sync.WaitGroup
	for i := range 24 {
		session := indexSession(t, project)
		session.UpdatedAt = session.UpdatedAt.Add(time.Duration(i) * time.Second)
		workers.Go(func() {
			index := &sessionIndex{home: home, project: project, account: "one"}
			if err := index.save(t.Context(), session); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	index := &sessionIndex{home: home, project: project, account: "one"}
	sessions, err := index.list(t.Context())
	if err != nil || len(sessions) != 24 {
		t.Fatalf("list returned %d, %v", len(sessions), err)
	}
	for i := 1; i < len(sessions); i++ {
		if sessions[i-1].UpdatedAt.Before(sessions[i].UpdatedAt) {
			t.Fatal("sessions are not sorted by recency")
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("left temporary index files: %+v", entries)
	}
}

func TestCorruptIndexIsNeverSilentlyReplaced(t *testing.T) {
	project, _ := policyFixture(t)
	for _, content := range []string{`broken`, `{"version":3,"sessions":[]}`, `{"version":1,"sessions":[{"id":"../escape"}]}`} {
		home := t.TempDir()
		path := filepath.Join(home, indexFilename)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		index := &sessionIndex{home: home, project: project, account: "one"}
		if err := index.save(t.Context(), indexSession(t, project)); err == nil {
			t.Fatal("overwrote corrupt index")
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, []byte(content)) {
			t.Fatal("corrupt metadata was modified")
		}
	}
}

func TestVersionOneIndexMigratesSelectionsOnNextWrite(t *testing.T) {
	project, _ := policyFixture(t)
	home := t.TempDir()
	session := indexSession(t, project)
	content := fmt.Sprintf(
		`{"version":1,"sessions":[{"id":%q,"title":"Fixture","canonicalProject":%q,"account":"one","model":"model-a","updatedAt":"1970-01-01T00:01:40Z"}]}`,
		session.ID, project,
	)
	path := filepath.Join(home, indexFilename)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	index := &sessionIndex{home: home, project: project, account: "one"}
	got, err := index.find(t.Context(), session.ID)
	if err != nil || got.ContextTier != "default" || got.ReasoningEffort != "" {
		t.Fatalf("migrated selection: %+v, %v", got, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != content {
		t.Fatal("read-only migration modified the existing index")
	}
	got.ReasoningEffort = "high"
	if err := index.save(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	var document indexDocument
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &document) != nil || document.Version != 2 ||
		document.Sessions[0].ReasoningEffort != "high" {
		t.Fatalf("migration was not persisted on write: %s, %v", data, err)
	}
}

func TestIndexRejectsMalformedModelSelections(t *testing.T) {
	project, _ := policyFixture(t)
	index := &sessionIndex{home: t.TempDir(), project: project, account: "one"}
	for name, mutate := range map[string]func(*Session){
		"context tier":     func(session *Session) { session.ContextTier = "enormous" },
		"reasoning effort": func(session *Session) { session.ReasoningEffort = "high\nforged" },
	} {
		t.Run(name, func(t *testing.T) {
			session := indexSession(t, project)
			mutate(&session)
			if err := index.save(t.Context(), session); err == nil {
				t.Fatal("stored malformed model selection")
			}
		})
	}
}

func TestIndexRefusesSymlinkLocksAndCanceledWrites(t *testing.T) {
	project, _ := policyFixture(t)
	home := t.TempDir()
	index := &sessionIndex{home: home, project: project, account: "one"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := index.save(ctx, indexSession(t, project)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, "sodapop-sessions.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := index.list(t.Context()); err == nil {
		t.Fatal("followed a symlink lock")
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "unchanged" {
		t.Fatal("modified lock symlink target")
	}
}
