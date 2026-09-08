package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

const requestTimeout = 15 * time.Second

// Clock supplies time and cancellable waits. Wait must not return before the
// duration has elapsed unless its context is cancelled.
type Clock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

// Options injects trusted dependencies. Nil Scopes requests read:user; an
// explicitly empty slice requests no scopes. Scopes do not establish Copilot
// entitlement. HTTPClient is copied, redirects/cookies are disabled, and its
// request timeout is capped at 15 seconds.
type Options struct {
	Store      Store
	HTTPClient *http.Client
	Clock      Clock
	Scopes     []string
}

// Manager owns the currently active GitHub account, which Login can replace.
// Account-scoped consumers must synchronize their lifetime with Login/SignOut
// as described in the package documentation. Use New or NewWithOptions to
// initialize a Manager.
type Manager struct {
	state *managerState
}

type managerState struct {
	mu          *sync.Mutex
	useGate     chan struct{}
	clientID    string
	store       Store
	client      *http.Client
	clock       Clock
	scopes      []string
	epoch       uint64
	loaded      bool
	credential  *credential
	stateErr    error
	loginCancel context.CancelFunc
	useCancel   context.CancelFunc
}

var _ Service = (*Manager)(nil)

func (Manager) Format(w fmt.State, _ rune) {
	_, _ = io.WriteString(w, "auth.Manager{credentials:[redacted]}")
}

func (managerState) Format(w fmt.State, _ rune) {
	_, _ = io.WriteString(w, "auth.managerState{credentials:[redacted]}")
}

// New uses only the supplied Sodapop-owned public OAuth client ID. An empty ID is
// allowed at startup, but Login returns ErrNoClientID without doing any I/O.
// Construction performs no network or credential-store I/O.
func New(clientID string) *Manager {
	return NewWithOptions(clientID, Options{})
}

// NewWithOptions is New with dependency injection. Like New, it performs no I/O.
func NewWithOptions(clientID string, options Options) *Manager {
	store := options.Store
	if store == nil {
		store = systemStore{}
	}
	clock := options.Clock
	if clock == nil {
		clock = systemClock{}
	}
	client := http.Client{}
	if options.HTTPClient != nil {
		client = *options.HTTPClient
	}
	if client.Timeout <= 0 || client.Timeout > requestTimeout {
		client.Timeout = requestTimeout
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	scopes := options.Scopes
	if scopes == nil {
		scopes = []string{"read:user"}
	}
	return &Manager{state: &managerState{
		clientID: strings.TrimSpace(clientID), store: store, client: &client,
		clock: clock, scopes: append([]string(nil), scopes...),
		mu: &sync.Mutex{}, useGate: make(chan struct{}, 1),
	}}
}

func (m *Manager) Current(ctx context.Context) (Account, error) {
	c, err := m.currentCredential(ctx)
	if err != nil {
		return Account{}, err
	}
	return c.account, nil
}

// Token returns the active account's credential, not an account-bound lease.
// A preceding Current call does not make the account/token pair atomic.
func (m *Manager) Token(ctx context.Context) (string, error) {
	c, err := m.currentCredential(ctx)
	if err != nil {
		return "", err
	}
	return string(c.accessToken), nil
}

// TokenForAccount checks identity and token against the same credential snapshot.
func (m *Manager) TokenForAccount(ctx context.Context, accountID string) (string, error) {
	if accountID == "" {
		return "", ErrReauthenticationRequired
	}
	c, err := m.currentCredential(ctx)
	if err != nil {
		return "", err
	}
	if c.account.ID != accountID {
		return "", ErrReauthenticationRequired
	}
	return string(c.accessToken), nil
}

func (m *Manager) Login(ctx context.Context, sessionOnly bool, onCode func(DeviceCode)) (Account, error) {
	s := m.state
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	if s.clientID == "" {
		return Account{}, ErrNoClientID
	}
	if !validClientID(s.clientID) || !validScopes(s.scopes) {
		return Account{}, ErrOAuthClientRejected
	}
	s.mu.Lock()
	s.cancelOperationsLocked()
	s.epoch++
	epoch := s.epoch
	flowCtx, cancel := context.WithCancel(ctx)
	s.loginCancel = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		if s.epoch == epoch {
			s.loginCancel = nil
		}
		s.mu.Unlock()
	}()

	if !sessionOnly {
		// A read can detect a missing/locked Secret Service before asking the
		// user to authorize. A missing entry is normal; write access can still
		// fail later and is never treated as a successful persistent login.
		s.mu.Lock()
		err := s.checkOperationLocked(flowCtx, epoch)
		if err == nil {
			_, err = s.store.Get(credentialService, credentialAccount)
			if errors.Is(err, keyring.ErrNotFound) {
				err = nil
			} else if err != nil {
				err = storageFailure("access")
			}
			if flowCtx.Err() != nil {
				err = flowCtx.Err()
			}
		}
		s.mu.Unlock()
		if err != nil {
			return Account{}, err
		}
	}

	device, err := s.authorizeDevice(flowCtx)
	if err != nil {
		return Account{}, err
	}
	if err := flowCtx.Err(); err != nil {
		return Account{}, err
	}
	if onCode != nil {
		onCode(DeviceCode{
			UserCode: device.userCode, VerificationURI: githubVerificationURL,
			ExpiresAt: device.expiresAt,
		})
	}
	c, err := s.pollDevice(flowCtx, device)
	if err != nil {
		return Account{}, err
	}
	if c.expired(s.clock.Now()) {
		return Account{}, ErrReauthenticationRequired
	}
	c.account, err = s.identity(flowCtx, c.accessToken)
	if err != nil {
		return Account{}, err
	}
	c.account.SessionOnly = sessionOnly
	if c.expired(s.clock.Now()) {
		return Account{}, ErrReauthenticationRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOperationLocked(flowCtx, epoch); err != nil {
		return Account{}, err
	}
	if !sessionOnly {
		if err := s.saveLocked(flowCtx, c); err != nil {
			return Account{}, err
		}
	}
	if c.expired(s.clock.Now()) {
		s.failLocked(ErrReauthenticationRequired)
		return Account{}, ErrReauthenticationRequired
	}
	s.cancelOperationsLocked()
	s.epoch++
	s.loaded, s.credential, s.stateErr = true, &c, nil
	return c.account, nil
}

func (m *Manager) SignOut(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failLocked(ErrNotSignedIn)
	if err := ctx.Err(); err != nil {
		return err
	}
	err := s.store.Delete(credentialService, credentialAccount)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("this process is signed out, but Sodapop's saved credential could not be removed: %w", ErrSecureStorageUnavailable)
	}
	return ctx.Err()
}

func (m *Manager) currentCredential(ctx context.Context) (credential, error) {
	s := m.state
	select {
	case <-ctx.Done():
		return credential{}, ctx.Err()
	case s.useGate <- struct{}{}:
	}
	defer func() { <-s.useGate }()

	s.mu.Lock()
	original, err := s.loadLocked(ctx)
	if err != nil {
		s.mu.Unlock()
		return credential{}, err
	}
	epoch := s.epoch
	useCtx, cancel := context.WithCancel(ctx)
	s.useCancel = cancel
	c := *original
	s.mu.Unlock()
	defer cancel()

	refreshed := false
	if c.expired(s.clock.Now()) {
		if !c.canRefresh(s.clock.Now()) {
			err = ErrReauthenticationRequired
		} else {
			var renewed credential
			renewed, err = s.refresh(useCtx, c.refreshToken)
			if err == nil {
				renewed.account = c.account
				c, refreshed = renewed, true
			}
		}
	}
	if err == nil && c.expired(s.clock.Now()) {
		err = ErrReauthenticationRequired
	}
	if err == nil {
		var account Account
		account, err = s.identity(useCtx, c.accessToken)
		if err == nil {
			if account.ID != original.account.ID {
				err = ErrReauthenticationRequired
			} else {
				account.SessionOnly = c.account.SessionOnly
				c.account = account
			}
		}
	}
	if err == nil && c.expired(s.clock.Now()) {
		err = ErrReauthenticationRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	installed := false
	defer func() {
		// Refresh rotates the old credential at GitHub. A cancelled or failed
		// installation must not leave that consumed refresh token reusable.
		// Do not cancel a newer login that may already be replacing it.
		if refreshed && !installed && s.credential == original {
			s.loaded, s.credential, s.stateErr = true, nil, ErrReauthenticationRequired
		}
	}()
	if opErr := s.checkOperationLocked(useCtx, epoch); opErr != nil {
		return credential{}, opErr
	}
	if s.credential != original {
		return credential{}, context.Canceled
	}
	if err != nil {
		if refreshed && !errors.Is(err, ErrReauthenticationRequired) {
			err = fmt.Errorf("GitHub credential renewal could not be completed; sign in again: %w", errors.Join(ErrReauthenticationRequired, err))
		}
		if errors.Is(err, ErrReauthenticationRequired) {
			s.loaded, s.credential, s.stateErr = true, nil, err
		}
		return credential{}, err
	}
	if refreshed && !c.account.SessionOnly {
		if err := s.saveLocked(useCtx, c); err != nil {
			return credential{}, err
		}
	}
	if c.expired(s.clock.Now()) {
		s.failLocked(ErrReauthenticationRequired)
		return credential{}, ErrReauthenticationRequired
	}
	s.credential = &c
	installed = true
	return c, nil
}

func (s *managerState) loadLocked(ctx context.Context) (*credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.loaded {
		return s.credential, s.stateErr
	}
	value, err := s.store.Get(credentialService, credentialAccount)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			s.loaded, s.stateErr = true, ErrNotSignedIn
			return nil, ErrNotSignedIn
		}
		return nil, storageFailure("read")
	}
	s.credential, s.stateErr = decodeCredential(value, s.clientID)
	s.loaded = true
	return s.credential, s.stateErr
}

func (s *managerState) checkOperationLocked(ctx context.Context, epoch uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.epoch != epoch {
		return context.Canceled
	}
	return nil
}

func (s *managerState) cancelOperationsLocked() {
	if s.loginCancel != nil {
		s.loginCancel()
		s.loginCancel = nil
	}
	if s.useCancel != nil {
		s.useCancel()
		s.useCancel = nil
	}
}

func (s *managerState) failLocked(err error) {
	s.cancelOperationsLocked()
	s.epoch++
	s.loaded, s.credential, s.stateErr = true, nil, err
}

func (s *managerState) saveLocked(ctx context.Context, c credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	value, err := encodeCredential(c, s.clientID)
	if err != nil {
		return err
	}
	saveErr := s.store.Set(credentialService, credentialAccount, value)
	if saveErr == nil && ctx.Err() == nil {
		return nil
	}
	// A native store cannot cancel an in-progress write. Roll it back before
	// releasing the lock, so cancellation or a partial write cannot resurrect
	// an authorization the caller was told did not succeed.
	result := ctx.Err()
	if saveErr != nil {
		result = storageFailure("save")
	}
	s.failLocked(ErrNotSignedIn)
	deleteErr := s.store.Delete(credentialService, credentialAccount)
	if deleteErr != nil && !errors.Is(deleteErr, keyring.ErrNotFound) {
		result = fmt.Errorf("could not clean up Sodapop's incomplete saved credential: %w", ErrSecureStorageUnavailable)
	}
	return result
}
