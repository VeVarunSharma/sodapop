package auth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCancelledOperationsPerformNoIO(t *testing.T) {
	store := newFakeStore()
	manager := NewWithOptions(testClientID, Options{Store: store, Clock: newFakeClock(), HTTPClient: offlineClient(t)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := manager.Login(ctx, false, nil)
	assertError(t, err, context.Canceled)
	_, err = manager.Current(ctx)
	assertError(t, err, context.Canceled)
	_, err = manager.Token(ctx)
	assertError(t, err, context.Canceled)
	assertError(t, manager.SignOut(ctx), context.Canceled)
	store.assertCalls(t)
}

func TestCancelAndSignOutFromDeviceCallbackDoNotPoll(t *testing.T) {
	for _, signOut := range []bool{false, true} {
		store, clock := newFakeStore(), newFakeClock()
		provider := newFakeProvider(t, clock)
		manager := newTestManager(t, store, clock, provider)
		ctx, cancel := context.WithCancel(context.Background())
		_, err := manager.Login(ctx, true, func(DeviceCode) {
			if signOut {
				if err := manager.SignOut(context.Background()); err != nil {
					t.Error(err)
				}
			} else {
				cancel()
			}
		})
		cancel()
		assertError(t, err, context.Canceled)
		if provider.count("/login/oauth/access_token") != 0 {
			t.Error("cancelled device code was polled")
		}
		if _, exists := store.saved(); exists {
			t.Error("cancelled authorization was saved")
		}
	}
}

func TestSignOutCancelsPollingWait(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	waiting := make(chan struct{})
	clock.onWait = func(ctx context.Context, _ time.Duration) error {
		close(waiting)
		<-ctx.Done()
		return ctx.Err()
	}
	provider := newFakeProvider(t, clock)
	manager := newTestManager(t, store, clock, provider)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := manager.Login(ctx, false, nil)
		result <- err
	}()
	await(t, waiting)
	if err := manager.SignOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertError(t, await(t, result), context.Canceled)
	if provider.count("/login/oauth/access_token") != 0 {
		t.Error("sign-out failed to interrupt the polling wait")
	}
	store.assertCalls(t, "get", "delete")
}

func TestSignOutCancelsEachInflightOAuthHTTPStage(t *testing.T) {
	for _, stage := range []string{"/login/device/code", "/login/oauth/access_token", "/user"} {
		t.Run(stage, func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			provider := newFakeProvider(t, clock)
			entered, requestCancelled := make(chan struct{}), make(chan struct{})
			provider.hook = func(_ http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != stage {
					return false
				}
				close(entered)
				<-r.Context().Done()
				close(requestCancelled)
				return true
			}
			manager := newTestManager(t, store, clock, provider)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := manager.Login(ctx, false, nil)
				result <- err
			}()
			await(t, entered)
			if err := manager.SignOut(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertError(t, await(t, result), context.Canceled)
			await(t, requestCancelled)
			store.assertCalls(t, "get", "delete")
			_, err := manager.Current(context.Background())
			assertError(t, err, ErrNotSignedIn)
		})
	}
}

func TestNewLoginCancelsPreviousAttempt(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	waiting := make(chan struct{})
	var waits atomic.Int32
	clock.onWait = func(ctx context.Context, _ time.Duration) error {
		if waits.Add(1) == 1 {
			close(waiting)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	manager := newTestManager(t, store, clock, newFakeProvider(t, clock))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() {
		_, err := manager.Login(ctx, false, nil)
		first <- err
	}()
	await(t, waiting)
	if _, err := manager.Login(context.Background(), false, nil); err != nil {
		t.Fatalf("replacement authorization failed: %v", err)
	}
	assertError(t, await(t, first), context.Canceled)
	store.assertCalls(t, "get", "get", "set")
}

func TestCancellationDuringPersistenceRollsBackCredential(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.onSet = cancel
	manager := newTestManager(t, store, clock, newFakeProvider(t, clock))
	_, err := manager.Login(ctx, false, nil)
	assertError(t, err, context.Canceled)
	if _, exists := store.saved(); exists {
		t.Error("cancellation during a native store write left a saved credential")
	}
	_, err = manager.Token(context.Background())
	assertError(t, err, ErrNotSignedIn)
	store.assertCalls(t, "get", "set", "delete")
}

func TestFailedRollbackIsReportedAndDoesNotReload(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.onSet = cancel
	store.deleteErr = errors.New("sensitive-error-marker")
	manager := newTestManager(t, store, clock, newFakeProvider(t, clock))
	_, err := manager.Login(ctx, false, nil)
	assertError(t, err, ErrSecureStorageUnavailable)
	_, err = manager.Current(context.Background())
	assertError(t, err, ErrNotSignedIn)
	store.assertCalls(t, "get", "set", "delete")
}

func TestSignOutWaitsForInflightSaveThenDeletesIt(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	store.onSet = func() {
		close(entered)
		<-release
	}
	manager := newTestManager(t, store, clock, newFakeProvider(t, clock))
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	loginResult := make(chan error, 1)
	go func() {
		_, err := manager.Login(context.Background(), false, nil)
		loginResult <- err
	}()
	await(t, entered)
	signOutStarted, signOutResult := make(chan struct{}), make(chan error, 1)
	go func() {
		close(signOutStarted)
		signOutResult <- manager.SignOut(context.Background())
	}()
	await(t, signOutStarted)
	releaseOnce.Do(func() { close(release) })
	if err := await(t, loginResult); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected concurrent login result: %v", err)
	}
	if err := await(t, signOutResult); err != nil {
		t.Fatal(err)
	}
	if _, exists := store.saved(); exists {
		t.Error("an in-flight login persisted a credential after sign-out")
	}
	_, err := manager.Current(context.Background())
	assertError(t, err, ErrNotSignedIn)
	store.assertCalls(t, "get", "set", "delete")
}

func TestConcurrentTokenRequestsRefreshOnlyOnce(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	c := testCredential()
	c.expiresAt, c.refreshToken = clock.Now(), secret(testRefreshToken)
	store.put(t, c)
	provider := newFakeProvider(t, clock)
	provider.tokens = []providerReply{{body: testRenewedJSON}}
	manager := newTestManager(t, store, clock, provider)
	const count = 24
	var workers sync.WaitGroup
	results := make(chan error, count)
	for range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			token, err := manager.Token(context.Background())
			if err == nil && token != testRenewedToken {
				err = errors.New("incorrect concurrent token result")
			}
			results <- err
		}()
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
	if provider.count("/login/oauth/access_token") != 1 {
		t.Error("concurrent requests reused a rotating refresh token")
	}
	store.assertCalls(t, "get", "set")
}

func TestSignOutCancelsCredentialValidationAndQueuedReaders(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	store.put(t, testCredential())
	provider := newFakeProvider(t, clock)
	entered := make(chan struct{})
	provider.hook = func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/user" {
			return false
		}
		close(entered)
		<-r.Context().Done()
		return true
	}
	manager := newTestManager(t, store, clock, provider)
	active := make(chan error, 1)
	go func() {
		_, err := manager.Current(context.Background())
		active <- err
	}()
	await(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, err := manager.Token(ctx)
		queued <- err
	}()
	cancel()
	assertError(t, await(t, queued), context.Canceled)
	if err := manager.SignOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertError(t, await(t, active), context.Canceled)
	_, err := manager.Current(context.Background())
	assertError(t, err, ErrNotSignedIn)
}

func TestCancelledRenewalDoesNotReuseConsumedRefresh(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	c := testCredential()
	c.expiresAt, c.refreshToken = clock.Now(), secret(testRefreshToken)
	store.put(t, c)
	provider := newFakeProvider(t, clock)
	provider.tokens = []providerReply{{body: testRenewedJSON}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider.hook = func(_ http.ResponseWriter, request *http.Request) bool {
		if request.URL.Path != "/user" {
			return false
		}
		cancel()
		<-request.Context().Done()
		return true
	}
	manager := newTestManager(t, store, clock, provider)
	_, err := manager.Token(ctx)
	assertError(t, err, context.Canceled)
	_, err = manager.Token(context.Background())
	assertError(t, err, ErrReauthenticationRequired)
	if provider.count("/login/oauth/access_token") != 1 {
		t.Error("cancellation allowed reuse of a consumed refresh credential")
	}
	store.assertCalls(t, "get")
}
