package ui

import (
	"context"
	"errors"
	"fmt"
)

type factoryWork struct {
	cancel context.CancelFunc
}

func (r *resources) registerFactory(cancel context.CancelFunc) *factoryWork {
	work := &factoryWork{cancel: cancel}
	r.mu.Lock()
	r.factories[work] = struct{}{}
	closed := r.closed
	r.mu.Unlock()
	if closed {
		cancel()
	}
	return work
}

func (r *resources) finishFactory(work *factoryWork) {
	r.mu.Lock()
	delete(r.factories, work)
	r.mu.Unlock()
}

func (r *resources) cancelEngineWork() {
	r.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(r.engines)+len(r.factories))
	for lease := range r.engines {
		cancels = append(cancels, lease.cancel)
	}
	for factory := range r.factories {
		cancels = append(cancels, factory.cancel)
	}
	r.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (r *resources) closeEngines() error {
	r.mu.Lock()
	leases := make([]*engineLease, 0, len(r.engines))
	for lease := range r.engines {
		leases = append(leases, lease)
	}
	r.mu.Unlock()
	var err error
	for _, lease := range leases {
		err = errors.Join(err, lease.close())
	}
	return err
}

// The lease covers the entire factory/token-consuming call, not just fetching
// a token. A canceled command that Tea starts later must not touch old account
// services after a login/logout transition.
func accountRead[T any](r *resources, ctx context.Context, operation func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	r.accountMu.RLock()
	defer r.accountMu.RUnlock()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return operation()
}

func accountAction(r *resources, ctx context.Context, operation func() error) error {
	_, err := accountRead(r, ctx, func() (struct{}, error) {
		return struct{}{}, operation()
	})
	return err
}

// Close known engines first to unblock SDK calls, then drain all readers and
// close any engine returned by an in-flight factory. The exclusive lease also
// serializes a canceled login with the next auth mutation.
func (r *resources) mutateAccount(ctx context.Context, mutation func() error) (bool, error) {
	if err := r.closeEngines(); err != nil {
		return false, fmt.Errorf("stop the previous Copilot connection before changing accounts: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.accountMu.Lock()
	defer r.accountMu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := r.closeEngines(); err != nil {
		return false, fmt.Errorf("drain engine startup before changing accounts: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, mutation()
}
