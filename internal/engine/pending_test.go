package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPendingExactlyOnce(t *testing.T) {
	pending := newPendingResponse[bool](t.Context())
	var winners atomic.Int32
	var workers sync.WaitGroup
	for range 100 {
		workers.Go(func() {
			if err := pending.resolve(true, nil); err == nil {
				winners.Add(1)
			} else if !errors.Is(err, ErrAlreadyResolved) {
				t.Errorf("unexpected response error: %v", err)
			}
		})
	}
	workers.Wait()
	if winners.Load() != 1 {
		t.Fatalf("got %d winning responses", winners.Load())
	}
	if value, err := pending.wait(); err != nil || !value {
		t.Fatalf("wait = %v, %v", value, err)
	}
}

func TestPendingCancellationWinsOverQueuedApproval(t *testing.T) {
	for _, respondFirst := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		pending := newPendingResponse[bool](ctx)
		if respondFirst {
			if err := pending.resolve(true, nil); err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		if err := pending.resolve(true, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("response after cancellation = %v", err)
		}
		if allowed, err := pending.wait(); allowed || !errors.Is(err, context.Canceled) {
			t.Fatalf("wait after cancellation = %v, %v", allowed, err)
		}
	}
}

func TestPendingCancellationReleasesWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	pending := newPendingResponse[string](ctx)
	done := make(chan error, 1)
	go func() { _, err := pending.wait(); done <- err }()
	cancel()
	if err := receive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("worker result = %v", err)
	}
}
