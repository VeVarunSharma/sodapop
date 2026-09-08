package engine

import "context"

func (s *liveSession) beginCallbackLocked(id string, pending cancelableRequest) {
	if s.callbackCount == 0 {
		s.callbackIdle = make(chan struct{})
	}
	s.callbackCount++
	if id != "" {
		s.pending[id] = pending
	}
}

func (s *liveSession) waitCallbacks(ctx context.Context) error {
	s.mu.Lock()
	drained := s.callbackIdle
	s.mu.Unlock()
	select {
	case <-drained:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
