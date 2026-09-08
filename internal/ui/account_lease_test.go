package ui

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/engine"
)

func waitAccountWriter(t *testing.T, r *resources) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for r.accountMu.TryRLock() {
		r.accountMu.RUnlock()
		if ctx.Err() != nil {
			t.Fatal("account mutation did not wait for the outstanding account lease")
		}
		runtime.Gosched()
	}
}

func receiveCommand(t *testing.T, result <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-result:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("account transition did not finish")
		return nil
	}
}

func TestSignOutDrainsInFlightFactoryBeforeMutatingAuth(t *testing.T) {
	options := testOptions()
	f := newFakeEngine()
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	a := &fakeAuth{signOutFn: func(context.Context) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.closes != 1 {
			return errors.New("auth changed before the factory's engine was closed")
		}
		return nil
	}}
	options.Auth = a
	options.NewEngine = func(context.Context, auth.Account) (engine.Engine, error) {
		close(started)
		<-release
		return f, nil
	}
	m := testModel(t, options)
	m.account = auth.Account{ID: "account-a", Login: "octocat"}
	start := m.connect()
	factoryResult := make(chan tea.Msg, 1)
	go func() { factoryResult <- start() }()
	<-started
	signout := m.signOut()
	mutationResult := make(chan tea.Msg, 1)
	go func() { mutationResult <- signout() }()
	waitAccountWriter(t, m.life)
	if a.signOutCalls.Load() != 0 {
		t.Fatal("sign-out mutated credentials while the factory was still running")
	}
	releaseOnce.Do(func() { close(release) })
	m.Update(receiveCommand(t, factoryResult))
	runFinite(t, m, result(receiveCommand(t, mutationResult)))
	if a.signOutCalls.Load() != 1 || m.account.ID != "" || m.notice.error {
		t.Fatalf("factory drain/sign-out did not complete safely: %q", m.notice.text)
	}
}

func TestSignOutDrainsTokenConsumingWorkAfterClosingEngine(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session"}
	started, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var finished atomic.Bool
	f.sendFn = func(context.Context, engine.Message) error {
		close(started)
		<-release
		finished.Store(true)
		return nil
	}
	f.closeFn = func() error { close(closed); return nil }
	a := &fakeAuth{signOutFn: func(context.Context) error {
		if !finished.Load() {
			return errors.New("token-consuming work overlapped auth mutation")
		}
		return nil
	}}
	m.opts.Auth = a
	m.composer.SetValue("submitted prompt")
	send := m.submit()
	sendResult := make(chan tea.Msg, 1)
	go func() { sendResult <- send() }()
	<-started
	signout := m.signOut()
	mutationResult := make(chan tea.Msg, 1)
	go func() { mutationResult <- signout() }()
	<-closed
	waitAccountWriter(t, m.life)
	if a.signOutCalls.Load() != 0 {
		t.Fatal("sign-out did not drain a token consumer after closing the engine")
	}
	releaseOnce.Do(func() { close(release) })
	m.Update(receiveCommand(t, sendResult))
	runFinite(t, m, result(receiveCommand(t, mutationResult)))
	if m.account.ID != "" || m.notice.error {
		t.Fatalf("drained sign-out did not finish safely: %q", m.notice.text)
	}
}

func TestQueuedOldEngineWorkCannotRunAfterAuthMutation(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session"}
	m.composer.SetValue("not dispatched yet")
	queued := m.submit()
	runFinite(t, m, m.signOut())
	runFinite(t, m, queued)
	if len(f.sent) != 0 || m.account.ID != "" {
		t.Fatal("a queued old-account command ran after sign-out")
	}
}

func TestLoginDrainsCanceledFactoryBeforeReplacingAccount(t *testing.T) {
	options := testOptions()
	f := newFakeEngine()
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	a := &fakeAuth{loginFn: func(context.Context, bool, func(auth.DeviceCode)) (auth.Account, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.closes != 1 {
			return auth.Account{}, errors.New("login replaced the account before the old factory closed")
		}
		return auth.Account{ID: "account-b", Login: "new-account", SessionOnly: true}, nil
	}}
	options.Auth = a
	options.NewEngine = func(context.Context, auth.Account) (engine.Engine, error) {
		close(started)
		<-release
		return f, nil
	}
	m := testModel(t, options)
	m.account = auth.Account{ID: "account-a", Login: "old-account"}
	start := m.connect()
	factoryResult := make(chan tea.Msg, 1)
	go func() { factoryResult <- start() }()
	<-started
	m.cancelWork()
	batch := m.startLogin(true)().(tea.BatchMsg)
	loginResult := make(chan tea.Msg, 1)
	go func() { loginResult <- batch[0]() }()
	waitAccountWriter(t, m.life)
	if a.loginCalls.Load() != 0 {
		t.Fatal("login replaced the account while a canceled factory was still running")
	}
	releaseOnce.Do(func() { close(release) })
	m.Update(receiveCommand(t, factoryResult))
	msg := receiveCommand(t, loginResult).(identityMsg)
	if msg.err != nil || msg.account.ID != "account-b" {
		t.Fatalf("drained login failed: %#v", msg)
	}
	m.cancelLogin()
	if batch[1]() != nil {
		t.Fatal("canceled code subscription remained active")
	}
}

type signaledContext struct {
	context.Context
	once    sync.Once
	reached chan struct{}
}

func (c *signaledContext) Err() error {
	c.once.Do(func() { close(c.reached) })
	return c.Context.Err()
}

func TestAccountMutationsAreSerialized(t *testing.T) {
	m := testModel(t, testOptions())
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var firstFinished atomic.Bool
	first := make(chan error, 1)
	go func() {
		_, err := m.life.mutateAccount(m.life.ctx, func() error {
			close(entered)
			<-release
			firstFinished.Store(true)
			return nil
		})
		first <- err
	}()
	<-entered
	ctx := &signaledContext{Context: m.life.ctx, reached: make(chan struct{})}
	second := make(chan error, 1)
	go func() {
		_, err := m.life.mutateAccount(ctx, func() error {
			if !firstFinished.Load() {
				return errors.New("auth mutations overlapped")
			}
			return nil
		})
		second <- err
	}()
	<-ctx.reached
	releaseOnce.Do(func() { close(release) })
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

func TestCloseFailurePreventsCredentialMutation(t *testing.T) {
	m, f := readyModel(t)
	a := &fakeAuth{}
	m.opts.Auth = a
	f.closeErr = errors.New("runtime cleanup could not be confirmed")
	runFinite(t, m, m.signOut())
	if a.signOutCalls.Load() != 0 || m.account.ID == "" || !m.notice.error {
		t.Fatal("credentials were mutated despite unconfirmed engine cleanup")
	}
}

func TestSignOutStorageWarningClearsAccountAndAllowsRecovery(t *testing.T) {
	m, f := readyModel(t)
	a := &fakeAuth{signOutErr: auth.ErrSecureStorageUnavailable}
	m.opts.Auth = a
	m.session = engine.Session{ID: "retained-session"}
	m.entries = append(m.entries, &entry{id: "retained", role: "assistant", raw: "retained history", final: true})
	m.composer.SetValue("retained draft")
	runFinite(t, m, m.signOut())
	if m.account.ID != "" || len(m.models) != 0 || m.model != "" || m.lease != nil || f.closes != 1 {
		t.Fatal("keyring deletion failure retained a stale signed-in UI or engine")
	}
	if m.session.ID != "retained-session" || m.entries[0].raw != "retained history" || m.composer.Value() != "retained draft" {
		t.Fatal("sign-out warning discarded history or draft")
	}
	if !strings.Contains(m.overlay.body, "Signed out for this run") || !strings.Contains(m.overlay.body, "keyring") {
		t.Fatal("the saved-credential deletion warning was not visible")
	}
	hasLogin, hasRetry := false, false
	for _, item := range m.overlay.items {
		hasLogin = hasLogin || item.id == "session-login"
		hasRetry = hasRetry || item.id == "signout"
	}
	if !hasLogin || !hasRetry {
		t.Fatal("deletion warning trapped the user without login or removal retry")
	}
	a.signOutErr = nil
	runFinite(t, m, m.signOut())
	if m.signOutWarning != "" || a.signOutCalls.Load() != 2 {
		t.Fatal("successful removal retry did not clear the warning")
	}
}
