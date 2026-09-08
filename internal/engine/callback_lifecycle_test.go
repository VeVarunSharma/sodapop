package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestAbortDrainsOldCallbacksBeforeItsIdleBarrier(t *testing.T) {
	engine, _, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "work"}); err != nil {
		t.Fatal(err)
	}
	denialEntered := make(chan struct{})
	releaseDenial := make(chan struct{})
	session.decideHook = func(ctx context.Context, decision rpc.PermissionDecision) error {
		if _, rejected := decision.(*rpc.PermissionDecisionReject); !rejected {
			t.Error("cancellation attempted to approve the pending action")
		}
		close(denialEntered)
		select {
		case <-releaseDenial:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	session.emit(copilot.SessionEvent{ID: "permission", Data: &copilot.PermissionRequestedData{
		RequestID: "pending", PermissionRequest: &copilot.PermissionRequestShell{FullCommandText: "echo pending"},
	}})
	done := make(chan error, 1)
	go func() { done <- engine.Abort(t.Context()) }()
	awaitSignal(t, denialEntered)
	select {
	case err := <-done:
		t.Fatalf("Abort returned before its callback drained: %v", err)
	default:
	}
	if !engine.active.isBusy() {
		t.Fatal("new work became available before cancellation quiesced")
	}
	close(releaseDenial)
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	for {
		event := receive(t, engine.Events())
		if event.Kind == EventPermission || event.Kind == EventQuestion {
			t.Fatal("an unread canceled dialog survived the Abort barrier")
		}
		if event.Kind == EventIdle {
			if event.SessionID != session.id || event.ID == "" || event.Name != "aborted" {
				t.Fatalf("idle barrier lost session/event identity: %+v", event)
			}
			break
		}
	}
	if engine.active.isBusy() {
		t.Fatal("canceled session did not become available")
	}
}

func TestAlreadyIdleAbortStillEmitsCancellationBarrier(t *testing.T) {
	engine, _, session := startedEngine(t)
	if err := engine.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	event := receive(t, engine.Events())
	if event.Kind != EventIdle || event.SessionID != session.id || event.ID == "" || event.Name != "aborted" {
		t.Fatalf("missing idle cancellation barrier: %+v", event)
	}
}

func TestCloseReleasesUnreadQuestionAndPermission(t *testing.T) {
	engine, client, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "work"}); err != nil {
		t.Fatal(err)
	}
	session.emit(copilot.SessionEvent{ID: "permission", Data: &copilot.PermissionRequestedData{
		RequestID: "pending", PermissionRequest: &copilot.PermissionRequestWrite{FileName: "file.txt"},
	}})
	questionDone := make(chan error, 1)
	go func() {
		_, err := client.created[0].OnUserInputRequest(
			copilot.UserInputRequest{Question: "Which file?"},
			copilot.UserInputInvocation{SessionID: session.id},
		)
		questionDone <- err
	}()
	waitForCallbacks(t, engine.active, 2)
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := receive(t, questionDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("unread question was not canceled: %v", err)
	}
	if err := engine.active.waitCallbacks(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case call := <-session.decisions:
		if _, approved := call.decision.(*rpc.PermissionDecisionApproveOnce); approved {
			t.Fatal("Close approved an unread permission")
		}
	default:
	}
	if _, open := <-engine.Events(); open {
		t.Fatal("Close did not close its unread event stream")
	}
}

func TestDiscardingCanceledDialogsPreservesFinalsAndErrors(t *testing.T) {
	stream := newEventStream()
	t.Cleanup(stream.close)
	session := &liveSession{}
	stream.activate(session)
	stream.push(session,
		Event{ID: "permission", Kind: EventPermission},
		Event{ID: "final", Kind: EventMessage},
		Event{ID: "question", Kind: EventQuestion},
		Event{ID: "error", Kind: EventError},
		Event{ID: "tool", Kind: EventToolEnd},
	)
	stream.discardRequests(session)
	for _, id := range []string{"final", "error", "tool"} {
		if event := receive(t, stream.output); event.ID != id {
			t.Fatalf("lost or reordered critical event %s: %+v", id, event)
		}
	}
}

func waitForCallbacks(t *testing.T, session *liveSession, count int) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		session.mu.Lock()
		current := session.callbackCount
		session.mu.Unlock()
		if current == count {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("expected %d registered callbacks, got %d", count, current)
		}
	}
}
