package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VeVarunSharma/sodapop/internal/engine"
)

func syntheticTurn(t *testing.T) (fixture, *turnTrace, chan engine.Event) {
	t.Helper()
	f := newFixture(t)
	trace := newTurnTrace(f, "synthetic-session")
	readArgs := `{"path":"fixture.go"}`
	editArgs := editArguments(fixtureName)
	permission := permissionEvent("hook", "Tool edit requires approval\n"+editArgs)
	events := []engine.Event{
		{Kind: engine.EventMessage, ID: "user-source", MessageID: "user-message", Role: "user", Text: livePrompt},
		{Kind: engine.EventToolStart, ID: "read-start", ToolID: "read-1", Role: "tool", Name: "view", Arguments: readArgs},
		{Kind: engine.EventToolEnd, ID: "read-source", ToolID: "read-1", Role: "tool", Name: "view", Arguments: readArgs, Text: fixtureBefore},
		{Kind: engine.EventToolStart, ID: "edit-start", ToolID: "edit-1", Role: "tool", Name: "edit", Arguments: editArgs},
		permission,
		{Kind: engine.EventToolEnd, ID: "edit-source", ToolID: "edit-1", Role: "tool", Name: "edit", Arguments: editArgs},
		{Kind: engine.EventToolStart, ID: "reread-start", ToolID: "read-2", Role: "tool", Name: "view", Arguments: readArgs},
		{Kind: engine.EventToolEnd, ID: "reread-source", ToolID: "read-2", Role: "tool", Name: "view", Arguments: readArgs, Text: fixtureAfter},
		{Kind: engine.EventMessage, ID: "assistant-source", MessageID: "assistant-message", Role: "assistant", Text: "Synthetic completion."},
		{Kind: engine.EventIdle, ID: "idle-source"},
	}
	stream := make(chan engine.Event, len(events))
	for _, event := range events {
		event.SessionID = trace.sessionID
		stream <- event
	}
	return f, trace, stream
}

func TestTurnConsumerRequiresIdleAndPreservesSourceIDs(t *testing.T) {
	f, trace, stream := syntheticTurn(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := awaitTurn(ctx, stream, trace, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(trace.history) != 5 || trace.history["user-source"].messageID != "user-message" ||
		trace.history["assistant-source"].messageID != "assistant-message" ||
		trace.history["edit-source"].messageID != "tool:edit-1" || trace.history["edit-source"].id != "edit-source" {
		t.Fatal("source IDs were confused with message/tool IDs")
	}
	if err := fixtureContents(f, fixtureAfter); err == nil {
		t.Fatal("synthetic tool events qualified an edit without an actual filesystem change")
	}
	if err := fixtureContents(f, fixtureBefore); err != nil {
		t.Fatal(err)
	}
}

func TestTurnConsumerFailsWithoutQualifiedIdle(t *testing.T) {
	for _, kind := range []engine.EventKind{engine.EventIdle, engine.EventError} {
		trace := newTurnTrace(newFixture(t), "synthetic-session")
		stream := make(chan engine.Event, 1)
		stream <- engine.Event{Kind: kind, ID: "early-source", SessionID: trace.sessionID, Text: "synthetic-private-payload"}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		err := awaitTurn(ctx, stream, trace, func(context.Context) error { return nil })
		cancel()
		if err == nil || strings.Contains(err.Error(), "synthetic-private-payload") {
			t.Fatal("early idle/error either qualified a turn or exposed a payload")
		}
	}
	t.Run("acknowledgment is not idle", func(t *testing.T) {
		trace := newTurnTrace(newFixture(t), "synthetic-session")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		err := awaitTurn(ctx, make(chan engine.Event), trace, func(context.Context) error {
			cancel()
			return nil
		})
		if err == nil {
			t.Fatal("send acknowledgment qualified a turn without idle")
		}
	})
	t.Run("closed stream", func(t *testing.T) {
		trace := newTurnTrace(newFixture(t), "synthetic-session")
		stream := make(chan engine.Event)
		close(stream)
		if awaitTurn(t.Context(), stream, trace, func(context.Context) error { return nil }) == nil {
			t.Fatal("closed event stream qualified a turn")
		}
	})
	t.Run("failed send is not replayed", func(t *testing.T) {
		trace := newTurnTrace(newFixture(t), "synthetic-session")
		calls := 0
		err := awaitTurn(t.Context(), make(chan engine.Event), trace, func(context.Context) error {
			calls++
			return errors.New("synthetic-private-payload")
		})
		if err == nil || calls != 1 || strings.Contains(err.Error(), "synthetic-private-payload") {
			t.Fatal("failed send was replayed, accepted, or exposed its payload")
		}
	})
}

func TestFixtureCompletionNeedsApprovalAndRealContents(t *testing.T) {
	f, trace, stream := syntheticTurn(t)
	for event := range stream {
		if event.Kind == engine.EventPermission {
			continue
		}
		err := trace.accept(event)
		if event.Kind == engine.EventToolEnd && event.Name == "edit" {
			if err == nil {
				t.Fatal("unapproved tool completion was accepted")
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(f.path, []byte(fixtureAfter), 0600); err != nil {
		t.Fatal(err)
	}
	if err := fixtureContents(f, fixtureAfter); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.project+"/extra", []byte("synthetic extra file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := fixtureContents(f, fixtureAfter); err == nil {
		t.Fatal("unexpected project files were ignored")
	}
}
