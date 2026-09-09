package engine

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
)

var (
	ErrClosed             = errors.New("Sodapop engine is closed")
	ErrNotStarted         = errors.New("start the Sodapop engine before using Copilot")
	ErrNoSession          = errors.New("create or resume a Sodapop session first")
	ErrContextUnavailable = errors.New("context information is not yet available")
	ErrVariableContext    = errors.New("context usage is unavailable for a model with a per-turn context window")
	ErrBusy               = errors.New("Copilot is busy; finish or cancel the current operation first")
	ErrNoToken            = errors.New("no Sodapop authentication token is available; sign in to Sodapop first")
	ErrNoModels           = errors.New("no Copilot models are available for this Sodapop account; check entitlement and organization policy")
	ErrAlreadyResolved    = errors.New("this request has already been resolved")
	ErrQuestionCanceled   = errors.New("the user canceled the question")
)

var credentialPattern = regexp.MustCompile(`(?i)(?:gh[pousr]_[a-z0-9_]+|github_pat_[a-z0-9_]+|bearer[ \t]+[a-z0-9._~+/\-=]+)`)

type redactor struct {
	mu     sync.RWMutex
	tokens []string
}

func (r *redactor) remember(token string) {
	if token == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.tokens {
		if existing == token {
			return
		}
	}
	r.tokens = append(r.tokens, token)
}

func (r *redactor) text(value string) string {
	r.mu.RLock()
	for _, token := range r.tokens {
		value = strings.ReplaceAll(value, token, "[redacted]")
	}
	r.mu.RUnlock()
	return credentialPattern.ReplaceAllString(value, "[redacted]")
}

func (r *redactor) err(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &safeError{message: operation + ": " + r.text(err.Error()), cause: err}
}

// Keep errors.Is useful without exposing a credential-bearing SDK error via Unwrap.
type safeError struct {
	message string
	cause   error
}

func (e *safeError) Error() string       { return e.message }
func (e *safeError) Is(other error) bool { return errors.Is(e.cause, other) }

func linkedContext(parent, lifetime context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(lifetime, cancel)
	if lifetime.Err() != nil {
		cancel()
	}
	return ctx, func() {
		stop()
		cancel()
	}
}
