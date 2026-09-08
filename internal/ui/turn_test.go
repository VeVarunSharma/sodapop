package ui

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/engine"
)

func TestFailedSendRemainsGatedUntilExplicitAbort(t *testing.T) {
	m, f := readyModel(t)
	f.sendsErr = errors.New("delivery acknowledgement timed out")
	m.composer.SetValue("do something")
	runFinite(t, m, m.submit())
	if !m.needsAbort || !m.busy() || !strings.Contains(m.notice.text, "Ctrl+C") {
		t.Fatal("uncertain delivery was not visibly gated")
	}
	for _, draft := range []string{"another prompt", "/clear", "/model model-b"} {
		m.composer.SetValue(draft)
		runFinite(t, m, m.submit())
		if m.composer.Value() != draft || len(f.sent) != 1 {
			t.Fatal("a gated action was queued or discarded its draft")
		}
	}
	m.composer.SetValue("keep this next draft")
	runFinite(t, m, m.handleKey(keyPress("ctrl+c")))
	event(m, engine.Event{Kind: engine.EventIdle, Name: "aborted", ID: "abort-barrier"})
	if m.needsAbort || m.turn || m.canceling || f.aborts != 1 || len(f.sent) != 1 {
		t.Fatal("Ctrl+C did not explicitly abort without replaying")
	}
	if m.composer.Value() != "keep this next draft" {
		t.Fatal("aborting uncertain delivery lost the next draft")
	}
}

func TestLateAcknowledgementErrorCannotReopenAnIdleTurn(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session"}
	f.sendsErr = errors.New("late acknowledgement failure")
	m.composer.SetValue("first prompt")
	cmd := m.submit()
	event(m, engine.Event{Kind: engine.EventMessage, MessageID: "answer", Text: "The work finished."})
	runFinite(t, m, event(m, engine.Event{Kind: engine.EventIdle}))
	runFinite(t, m, cmd)
	if m.needsAbort || m.busy() || m.composer.Value() != "" {
		t.Fatal("late acknowledgement resurrected a completed turn or offered an automatic retry draft")
	}
	if !strings.Contains(m.notice.text, "turn already ended") || len(f.sent) != 1 {
		t.Fatal("late acknowledgement was not explained without replay")
	}
}

func TestLiveErrorRemainsCancelableUntilIdleAndDeniesNewRequests(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "session"}
	m.turn, m.turnSequence = true, 1
	runFinite(t, m, event(m, engine.Event{Kind: engine.EventError, Text: "mid-turn authentication failure"}))
	if !m.needsAbort || !m.turn {
		t.Fatal("live error incorrectly made the conversation idle")
	}
	var denied atomic.Int32
	runFinite(t, m, event(m, engine.Event{Kind: engine.EventPermission, Permission: &engine.Permission{
		ID: "after-error", Respond: func(allow bool) error {
			if !allow {
				denied.Add(1)
			}
			return nil
		},
	}}))
	if denied.Load() != 1 || len(m.requests) != 0 {
		t.Fatal("unresolved-error state presented a new approval instead of failing closed")
	}
	runFinite(t, m, event(m, engine.Event{Kind: engine.EventIdle}))
	if m.needsAbort || m.busy() || !strings.Contains(m.notice.text, "ended after an error") {
		t.Fatal("idle did not clear the gate while retaining an honest error outcome")
	}
}

func TestClosedStreamClearsUnresolvedGateAndRequiresReconnect(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session"}
	m.turn, m.needsAbort = true, true
	runFinite(t, m, m.receiveEvents(eventsMsg{generation: m.engineGeneration, closed: true}))
	if m.needsAbort || m.busy() || !m.needsResume || m.lease != nil || f.closes != 1 {
		t.Fatal("disconnection left a phantom turn or failed to require reconnect")
	}
}
