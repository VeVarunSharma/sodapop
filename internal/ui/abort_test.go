package ui

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/engine"
)

func TestAbortRequiresAcknowledgementAndTaggedIdleInEitherOrder(t *testing.T) {
	for _, barrierFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("barrier-first=%t", barrierFirst), func(t *testing.T) {
			m, f := readyModel(t)
			m.session = engine.Session{ID: "session"}
			m.turn, m.turnSequence = true, 1
			m.composer.SetValue("kept draft")
			cmd := m.cancelWork()
			event(m, engine.Event{Kind: engine.EventIdle, ID: "ordinary-idle"})
			event(m, engine.Event{Kind: engine.EventIdle, Name: "aborted", ID: "wrong-session", SessionID: "other"})
			if !m.canceling || m.abortBarrierSeen {
				t.Fatal("ordinary idle or another session's barrier unlocked cancellation")
			}
			barrier := engine.Event{Kind: engine.EventIdle, Name: "aborted", ID: "abort-barrier"}
			if barrierFirst {
				event(m, barrier)
			} else {
				runFinite(t, m, cmd)
			}
			if !m.canceling || !m.busy() {
				t.Fatal("cancellation unlocked before both completion signals")
			}
			runFinite(t, m, m.submit())
			if len(f.sent) != 0 || m.composer.Value() != "kept draft" {
				t.Fatal("an incomplete cancellation queued work or discarded a draft")
			}
			if barrierFirst {
				runFinite(t, m, cmd)
			} else {
				event(m, barrier)
			}
			if m.canceling || m.busy() || f.aborts != 1 {
				t.Fatal("both completion signals did not finish cancellation")
			}
			m.turn = true
			m.turnSequence++
			event(m, barrier)
			event(m, engine.Event{Kind: engine.EventIdle, Name: "aborted", ID: "late-barrier"})
			if !m.turn {
				t.Fatal("a late cancellation barrier ended a subsequent turn")
			}
		})
	}
}

func TestAbortFailureRequiresReconnectWithoutWaitingForBarrier(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session"}
	m.turn = true
	f.abortErr = errors.New("native idle could not be confirmed")
	runFinite(t, m, m.cancelWork())
	if m.canceling || !m.needsResume || !m.notice.error {
		t.Fatal("Abort error did not require reconnect or kept waiting for a success barrier")
	}
	event(m, engine.Event{Kind: engine.EventIdle, Name: "aborted", ID: "late"})
	if !m.needsResume {
		t.Fatal("a late barrier erased the failed-Abort reconnect requirement")
	}
}

func TestCancellationResponderErrorsAreExpectedDuringCleanup(t *testing.T) {
	for _, err := range []error{context.Canceled, engine.ErrAlreadyResolved} {
		t.Run(err.Error(), func(t *testing.T) {
			m, _ := readyModel(t)
			m.session = engine.Session{ID: "session"}
			m.turn, m.turnSequence = true, 1
			event(m, engine.Event{Kind: engine.EventPermission, Permission: &engine.Permission{
				ID: "pending", Respond: func(bool) error { return fmt.Errorf("late response: %w", err) },
			}})
			runFinite(t, m, m.cancelWork())
			event(m, engine.Event{Kind: engine.EventIdle, Name: "aborted", ID: "barrier"})
			if m.notice.error {
				t.Fatal("expected cancellation cleanup was displayed as a new error")
			}
			if err := m.Shutdown(); err != nil {
				t.Fatalf("expected late responder error failed shutdown: %v", err)
			}
		})
	}
}

func TestCanceledSessionOperationAcceptsItsUnknownSessionBarrier(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "old"}
	m.beginOperation("resuming", "new")
	runFinite(t, m, m.cancelWork())
	if !m.canceling || !m.needsResume {
		t.Fatal("canceled session operation did not retain its safety gate")
	}
	event(m, engine.Event{Kind: engine.EventIdle, Name: "aborted", ID: "barrier", SessionID: "new"})
	if m.canceling || !m.needsResume {
		t.Fatal("new session barrier did not finish cancellation while preserving the reconnect requirement")
	}
}
