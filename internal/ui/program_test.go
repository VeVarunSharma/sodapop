package ui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/engine"
)

type observingModel struct {
	*Model
	observe func()
}

func (m *observingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.Model.Update(msg)
	m.observe()
	return m, cmd
}

func TestBubbleTeaProgramStreamsApprovesAndQuits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	options := testOptions()
	options.Context = ctx
	options.Preferences.Model = "model-a"
	options.Auth = &fakeAuth{account: auth.Account{ID: "account", Login: "octocat"}}
	f := newFakeEngine()
	var approvals atomic.Int32
	f.sendFn = func(ctx context.Context, _ engine.Message) error {
		send := func(e engine.Event) bool {
			select {
			case f.events <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for i := range 100 {
			if !send(engine.Event{Kind: engine.EventDelta, ID: fmt.Sprintf("delta-%d", i), SessionID: "new-session", MessageID: "response", Text: "x"}) {
				return ctx.Err()
			}
		}
		send(engine.Event{Kind: engine.EventMessage, ID: "final", SessionID: "new-session", MessageID: "response", Text: strings.Repeat("x", 100)})
		send(engine.Event{Kind: engine.EventPermission, ID: "permission-event", SessionID: "new-session", Permission: &engine.Permission{
			ID: "permission", Kind: "write", Path: "example.go",
			Respond: func(allow bool) error {
				if allow {
					approvals.Add(1)
				}
				send(engine.Event{Kind: engine.EventIdle, ID: "idle", SessionID: "new-session"})
				return nil
			},
		}})
		return nil
	}
	options.NewEngine = func(context.Context, auth.Account) (engine.Engine, error) { return f, nil }
	m := testModel(t, options)
	ready, permission, complete := make(chan struct{}), make(chan struct{}), make(chan string, 1)
	var readyOnce, permissionOnce, completeOnce sync.Once
	observed := &observingModel{Model: m}
	observed.observe = func() {
		if m.lease != nil && !m.connecting && m.model == "model-a" {
			readyOnce.Do(func() { close(ready) })
		}
		if len(m.requests) == 1 {
			permissionOnce.Do(func() { close(permission) })
		}
		if approvals.Load() == 1 && !m.turn && !m.sendPending && len(m.requests) == 0 {
			completeOnce.Do(func() {
				text := ""
				for _, entry := range m.entries {
					if entry.role == "assistant" {
						text += entry.raw
					}
				}
				complete <- text
			})
		}
	}
	program := tea.NewProgram(observed, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	wait := func(ch <-chan struct{}, phase string) {
		t.Helper()
		select {
		case <-ch:
		case err := <-done:
			t.Fatalf("program ended during %s: %v", phase, err)
		case <-ctx.Done():
			t.Fatalf("program timed out during %s", phase)
		}
	}
	wait(ready, "startup")
	program.Send(tea.WindowSizeMsg{Width: 80, Height: 24})
	program.Send(tea.PasteMsg{Content: "a deterministic prompt"})
	program.Send(keyPress("enter"))
	wait(permission, "stream and permission")
	program.Send(keyPress("a"))
	select {
	case text := <-complete:
		if text != strings.Repeat("x", 100) {
			t.Fatalf("event subscription dropped or duplicated deltas: %q", text)
		}
	case <-ctx.Done():
		t.Fatal("program did not finish the approved turn")
	}
	program.Send(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	program.Send(keyPress("down"))
	program.Send(keyPress("enter"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("program did not exit gracefully")
	}
	if approvals.Load() != 1 || f.closes != 1 || f.starts != 0 || len(f.newModels) != 1 || len(f.sent) != 1 {
		t.Fatalf("incorrect lifecycle: approvals=%d closes=%d starts=%d sessions=%v sent=%v", approvals.Load(), f.closes, f.starts, f.newModels, f.sent)
	}
}
