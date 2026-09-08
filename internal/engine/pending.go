package engine

import (
	"context"
	"reflect"
	"sync"

	copilot "github.com/github/copilot-sdk/go"
)

type cancelableRequest interface {
	cancel(error)
}

// A response is linearized under its own lock, not the engine lock. Waiting
// happens only in a callback worker; Respond and Cancel never wait for RPC.
type pendingResponse[T any] struct {
	mu       sync.Mutex
	ctx      context.Context
	done     chan struct{}
	resolved bool
	value    T
	err      error
}

func newPendingResponse[T any](ctx context.Context) *pendingResponse[T] {
	return &pendingResponse[T]{ctx: ctx, done: make(chan struct{})}
}

func (p *pendingResponse[T]) resolve(value T, err error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil {
		p.finishLocked(value, p.ctx.Err())
		return p.ctx.Err()
	}
	if p.resolved {
		return ErrAlreadyResolved
	}
	p.finishLocked(value, err)
	return nil
}

func (p *pendingResponse[T]) finishLocked(value T, err error) {
	if p.resolved {
		return
	}
	p.resolved, p.value, p.err = true, value, err
	close(p.done)
}

func (p *pendingResponse[T]) cancel(err error) {
	p.mu.Lock()
	var zero T
	p.finishLocked(zero, err)
	p.mu.Unlock()
}

func (p *pendingResponse[T]) wait() (T, error) {
	select {
	case <-p.ctx.Done():
		p.cancel(p.ctx.Err())
	case <-p.done:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Cancellation also defeats a response accepted immediately before Abort.
	if p.ctx.Err() != nil {
		var zero T
		return zero, p.ctx.Err()
	}
	return p.value, p.err
}

func nilPermission(request copilot.PermissionRequest) bool {
	if request == nil {
		return true
	}
	value := reflect.ValueOf(request)
	return value.Kind() == reflect.Pointer && value.IsNil()
}
