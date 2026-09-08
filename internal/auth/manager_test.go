package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestDeviceLoginPersistsOnlyInSecureStore(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	provider := newFakeProvider(t, clock)
	manager := newTestManager(t, store, clock, provider)
	started := clock.Now()
	callbacks := 0
	account, err := manager.Login(context.Background(), false, func(code DeviceCode) {
		callbacks++
		if code.UserCode != "ABCD-EFGH" || code.VerificationURI != githubVerificationURL ||
			!code.ExpiresAt.Equal(started.Add(900*time.Second)) {
			t.Error("incorrect public device-code information")
		}
		if provider.count("/login/oauth/access_token") != 0 {
			t.Error("polled before delivering the user code")
		}
		assertRedacted(t, fmt.Sprintf("%+v %#v", code, code))
	})
	if err != nil {
		t.Fatal(err)
	}
	if account != (Account{ID: testAccountID, Login: "sodapop-user"}) || callbacks != 1 {
		t.Fatalf("unexpected public account or callback count: %#v, %d", account, callbacks)
	}
	store.assertCalls(t, "get", "set")
	value, ok := store.saved()
	if !ok || !strings.Contains(value, testAccessToken) {
		t.Fatal("credential was not saved to the injected secure store")
	}
	c, err := decodeCredential(value, testClientID)
	if err != nil || c.account != account || !c.expiresAt.IsZero() || c.refreshToken != "" {
		t.Fatal("persistent credential did not retain the provider's actual lifetime")
	}
	restored := NewWithOptions(testClientID, Options{
		Store: store, Clock: clock, HTTPClient: manager.state.client,
	})
	current, err := restored.Current(context.Background())
	if err != nil || current != account {
		t.Fatalf("could not restore account: %v", err)
	}
	token, err := restored.Token(context.Background())
	if err != nil || token != testAccessToken {
		t.Fatalf("could not retrieve explicitly saved credential: %v", err)
	}
	if provider.count("/login/device/code") != 1 || provider.count("/login/oauth/access_token") != 1 {
		t.Error("restoring a valid credential restarted OAuth")
	}
	clock.assertWaits(t, 5*time.Second)
}

func TestTokenForAccountUsesOneValidatedIdentity(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	provider := newFakeProvider(t, clock)
	manager := newTestManager(t, store, clock, provider)
	if _, err := manager.Login(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	before := provider.count("/user")
	token, err := manager.TokenForAccount(context.Background(), testAccountID)
	if err != nil || token != testAccessToken {
		t.Fatalf("account-bound token failed: %v", err)
	}
	if provider.count("/user") != before+1 {
		t.Fatal("account binding required redundant identity requests")
	}
	token, err = manager.TokenForAccount(context.Background(), "another-account")
	if !errors.Is(err, ErrReauthenticationRequired) || token != "" {
		t.Fatal("token was released to a different account's engine")
	}
}

func TestSessionOnlyDoesNotAccessSecureStore(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	store.getErr, store.setErr, store.deleteErr = errors.New("unavailable"), errors.New("unavailable"), errors.New("unavailable")
	provider := newFakeProvider(t, clock)
	manager := newTestManager(t, store, clock, provider)
	account, err := manager.Login(context.Background(), true, nil)
	if err != nil || !account.SessionOnly {
		t.Fatalf("session-only login failed: %v", err)
	}
	if current, err := manager.Current(context.Background()); err != nil || current != account {
		t.Fatalf("session-only account was not cached: %v", err)
	}
	if token, err := manager.Token(context.Background()); err != nil || token != testAccessToken {
		t.Fatalf("session-only credential was not usable: %v", err)
	}
	store.assertCalls(t)
	if _, exists := store.saved(); exists {
		t.Error("session-only token was persisted")
	}
}

func TestMissingClientIDNeverUsesAmbientCredentials(t *testing.T) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "COPILOT_GITHUB_TOKEN", "SODAPOP_GITHUB_CLIENT_ID"} {
		t.Setenv(name, "ambient-credential-must-not-be-used")
	}
	store := newFakeStore()
	manager := NewWithOptions(" \t\n", Options{Store: store, HTTPClient: offlineClient(t), Clock: newFakeClock()})
	_, err := manager.Current(context.Background())
	assertError(t, err, ErrNotSignedIn)
	token, err := manager.Token(context.Background())
	assertError(t, err, ErrNotSignedIn)
	if token != "" {
		t.Error("missing Sodapop credential fell back to ambient authentication")
	}
	for _, sessionOnly := range []bool{false, true} {
		account, err := manager.Login(context.Background(), sessionOnly, func(DeviceCode) {
			t.Error("missing client ID produced a live sign-in prompt")
		})
		assertError(t, err, ErrNoClientID)
		if account != (Account{}) {
			t.Error("missing client ID produced an authenticated account")
		}
	}
	store.assertCalls(t, "get")
	_, err = New("").Login(context.Background(), false, nil)
	assertError(t, err, ErrNoClientID)
}

func TestPersistedLoginIsBoundToConfiguredClientID(t *testing.T) {
	t.Setenv("SODAPOP_GITHUB_CLIENT_ID", testClientID)
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "COPILOT_GITHUB_TOKEN"} {
		t.Setenv(name, testAccessToken)
	}
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%t", expired), func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			provider := newFakeProvider(t, clock)
			provider.tokens = []providerReply{
				{body: `{"access_token":"sodapop-test-user-access-token","token_type":"bearer","expires_in":3600,"refresh_token":"sodapop-test-user-refresh-token","refresh_token_expires_in":86400}`},
				{body: testRenewedJSON},
			}
			original := newTestManager(t, store, clock, provider)
			account, err := original.Login(context.Background(), false, nil)
			if err != nil {
				t.Fatal(err)
			}
			saved, exists := store.saved()
			if !exists || !strings.Contains(saved, `"client_id":"`+testClientID+`"`) {
				t.Fatal("persistent sign-in did not record its issuing OAuth client ID")
			}
			if expired {
				clock.Advance(time.Hour)
			}

			for _, changed := range []struct {
				clientID string
				expected error
			}{
				{"another-sodapop-client", ErrReauthenticationRequired},
				{"", ErrNoClientID},
				{" \t\n", ErrNoClientID},
			} {
				reopened := NewWithOptions(changed.clientID, Options{
					Store: store, Clock: clock, HTTPClient: offlineClient(t),
				})
				current, err := reopened.Current(context.Background())
				assertError(t, err, changed.expected)
				if current != (Account{}) {
					t.Error("changed or blank client ID activated a saved account")
				}
				token, err := reopened.Token(context.Background())
				assertError(t, err, changed.expected)
				if token != "" {
					t.Error("changed or blank client ID released a saved or ambient token")
				}
				after, exists := store.saved()
				if !exists || after != saved {
					t.Error("rejecting a differently bound credential modified secure storage")
				}
			}
			store.assertCalls(t, "get", "set", "get", "get", "get")

			restored := NewWithOptions(testClientID, Options{
				Store: store, Clock: clock, HTTPClient: original.state.client,
			})
			current, err := restored.Current(context.Background())
			if err != nil || current != account {
				t.Fatalf("restoring the issuing client ID did not restore the account: %v", err)
			}
			expectedToken := testAccessToken
			if expired {
				expectedToken = testRenewedToken
			}
			token, err := restored.Token(context.Background())
			if err != nil || token != expectedToken {
				t.Fatalf("the issuing client could not use its own credential: %v", err)
			}
		})
	}
}

func TestConstructorDoesNotLoadStoredOrAmbientCredentials(t *testing.T) {
	t.Setenv("SODAPOP_GITHUB_CLIENT_ID", testClientID)
	t.Setenv("GH_TOKEN", testAccessToken)
	for _, clientID := range []string{"", testClientID, "another-sodapop-client"} {
		store := newFakeStore()
		store.put(t, testCredential())
		manager := NewWithOptions(clientID, Options{
			Store: store, Clock: newFakeClock(), HTTPClient: offlineClient(t),
		})
		store.assertCalls(t)
		if manager.state.loaded || manager.state.credential != nil {
			t.Error("constructor loaded credentials without a service operation")
		}
		if manager.state.clientID != clientID {
			t.Error("constructor replaced the explicitly supplied client ID")
		}
	}
}

func TestUnavailableStoreRequiresExplicitSessionOnlyRetry(t *testing.T) {
	for _, failure := range []error{keyring.ErrUnsupportedPlatform, errors.New("sensitive-error-marker " + testAccessToken)} {
		store, clock := newFakeStore(), newFakeClock()
		store.getErr = failure
		provider := newFakeProvider(t, clock)
		manager := newTestManager(t, store, clock, provider)
		_, err := manager.Login(context.Background(), false, func(DeviceCode) {
			t.Error("authorization started before discovering unavailable secure storage")
		})
		assertError(t, err, ErrSecureStorageUnavailable)
		if provider.count("/login/device/code") != 0 {
			t.Error("persistent sign-in acquired authorization before checking storage")
		}
		account, err := manager.Login(context.Background(), true, nil)
		if err != nil || !account.SessionOnly {
			t.Fatalf("explicit in-memory retry failed: %v", err)
		}
		store.assertCalls(t, "get")
	}
}

func TestStoreReadFailuresAreNotCachedAsMissing(t *testing.T) {
	store := newFakeStore()
	store.getErr = errors.New("sensitive-error-marker")
	manager := NewWithOptions(testClientID, Options{Store: store, Clock: newFakeClock(), HTTPClient: offlineClient(t)})
	_, err := manager.Current(context.Background())
	assertError(t, err, ErrSecureStorageUnavailable)
	store.mu.Lock()
	store.getErr = nil
	store.mu.Unlock()
	_, err = manager.Current(context.Background())
	assertError(t, err, ErrNotSignedIn)
	store.assertCalls(t, "get", "get")
}

func TestSaveFailureNeverClaimsPersistentOrSessionSuccess(t *testing.T) {
	for _, partialWrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partialWrite), func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			store.setErr = errors.New("sensitive-error-marker " + testAccessToken)
			store.writeOnError = partialWrite
			manager := newTestManager(t, store, clock, newFakeProvider(t, clock))
			account, err := manager.Login(context.Background(), false, nil)
			assertError(t, err, ErrSecureStorageUnavailable)
			if account != (Account{}) {
				t.Error("failed persistence returned an authenticated account")
			}
			if _, exists := store.saved(); exists {
				t.Error("failed persistence retained a partially saved credential")
			}
			token, err := manager.Token(context.Background())
			assertError(t, err, ErrNotSignedIn)
			if token != "" {
				t.Error("failed persistent login silently became session-only")
			}
			store.assertCalls(t, "get", "set", "delete")
		})
	}
}

func TestExpiredCredentialRequiresReauthenticationWithoutRefresh(t *testing.T) {
	for _, refresh := range []string{"absent", "expired"} {
		t.Run(refresh, func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			c := testCredential()
			c.expiresAt = clock.Now()
			if refresh == "expired" {
				c.refreshToken, c.refreshExpiresAt = secret(testRefreshToken), clock.Now()
			}
			store.put(t, c)
			manager := NewWithOptions(testClientID, Options{Store: store, Clock: clock, HTTPClient: offlineClient(t)})
			_, err := manager.Current(context.Background())
			assertError(t, err, ErrReauthenticationRequired)
			token, err := manager.Token(context.Background())
			assertError(t, err, ErrReauthenticationRequired)
			if token != "" || errors.Is(err, ErrNotSignedIn) {
				t.Error("expired credential was usable or confused with a missing entry")
			}
			store.assertCalls(t, "get")
		})
	}
}

func TestPublicDeviceRefreshRotatesSecureCredential(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	c := testCredential()
	c.expiresAt, c.refreshToken = clock.Now().Add(-time.Second), secret(testRefreshToken)
	c.refreshExpiresAt = clock.Now().Add(time.Hour)
	store.put(t, c)
	provider := newFakeProvider(t, clock)
	provider.tokens = []providerReply{{body: testRenewedJSON}}
	manager := newTestManager(t, store, clock, provider)
	started := clock.Now()
	token, err := manager.Token(context.Background())
	if err != nil || token != testRenewedToken {
		t.Fatalf("public-client refresh failed: %v", err)
	}
	value, exists := store.saved()
	if !exists {
		t.Fatal("rotated credential was not persisted")
	}
	renewed, err := decodeCredential(value, testClientID)
	if err != nil || renewed.refreshToken != secret(testRenewedRefresh) ||
		!renewed.expiresAt.Equal(started.Add(time.Hour)) ||
		!renewed.refreshExpiresAt.Equal(started.Add(24*time.Hour)) {
		t.Fatal("actual token rotation or provider expirations were not retained")
	}
	if provider.count("/login/device/code") != 0 || provider.count("/login/oauth/access_token") != 1 {
		t.Error("refresh incorrectly initiated device authorization")
	}
	store.assertCalls(t, "get", "set")
}

func TestSessionOnlyRefreshDoesNotPersist(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	provider := newFakeProvider(t, clock)
	provider.tokens = []providerReply{
		{body: `{"access_token":"sodapop-test-user-access-token","token_type":"bearer","expires_in":1,"refresh_token":"sodapop-test-user-refresh-token","refresh_token_expires_in":3600}`},
		{body: testRenewedJSON},
	}
	manager := newTestManager(t, store, clock, provider)
	if _, err := manager.Login(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if token, err := manager.Token(context.Background()); err != nil || token != testRenewedToken {
		t.Fatalf("in-memory refresh failed: %v", err)
	}
	account, err := manager.Current(context.Background())
	if err != nil || !account.SessionOnly {
		t.Fatalf("refresh lost session-only status: %v", err)
	}
	store.assertCalls(t)
}

func TestRejectedRefreshRequiresNewAuthorization(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	c := testCredential()
	c.expiresAt, c.refreshToken = clock.Now(), secret(testRefreshToken)
	store.put(t, c)
	provider := newFakeProvider(t, clock)
	provider.tokens = []providerReply{{status: http.StatusBadRequest, body: `{"error":"bad_refresh_token","error_description":"sensitive-error-marker"}`}}
	manager := newTestManager(t, store, clock, provider)
	_, err := manager.Token(context.Background())
	assertError(t, err, ErrReauthenticationRequired)
	_, err = manager.Current(context.Background())
	assertError(t, err, ErrReauthenticationRequired)
	if provider.count("/login/oauth/access_token") != 1 || provider.count("/user") != 0 {
		t.Error("rejected refresh was retried or an expired token was sent to the identity API")
	}
	store.assertCalls(t, "get")
}

func TestFailedRenewalVerificationCannotReuseRotatedRefreshToken(t *testing.T) {
	for _, expiredResponse := range []bool{false, true} {
		store, clock := newFakeStore(), newFakeClock()
		c := testCredential()
		c.expiresAt, c.refreshToken = clock.Now(), secret(testRefreshToken)
		store.put(t, c)
		provider := newFakeProvider(t, clock)
		provider.tokens = []providerReply{{body: testRenewedJSON}}
		if expiredResponse {
			provider.tokens[0].body = strings.Replace(testRenewedJSON, `"expires_in":3600`, `"expires_in":0`, 1)
		} else {
			provider.user = providerReply{status: 503, body: `{"message":"sensitive-error-marker"}`}
		}
		manager := newTestManager(t, store, clock, provider)
		token, err := manager.Token(context.Background())
		assertError(t, err, ErrReauthenticationRequired)
		if token != "" {
			t.Error("unverified or expired renewal was exposed")
		}
		_, err = manager.Current(context.Background())
		assertError(t, err, ErrReauthenticationRequired)
		if provider.count("/login/oauth/access_token") != 1 {
			t.Error("an already rotated refresh token was reused")
		}
		if expiredResponse && provider.count("/user") != 0 {
			t.Error("a known-expired renewed token was sent to GitHub")
		}
		store.assertCalls(t, "get")
	}
}

func TestRefreshPersistenceFailureDiscardsRotatedToken(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	c := testCredential()
	c.expiresAt, c.refreshToken = clock.Now(), secret(testRefreshToken)
	store.put(t, c)
	store.setErr = errors.New(testRenewedToken)
	provider := newFakeProvider(t, clock)
	provider.tokens = []providerReply{{body: testRenewedJSON}}
	manager := newTestManager(t, store, clock, provider)
	token, err := manager.Token(context.Background())
	assertError(t, err, ErrSecureStorageUnavailable)
	if token != "" {
		t.Error("unpersisted rotated token was returned to the SDK")
	}
	_, err = manager.Current(context.Background())
	assertError(t, err, ErrNotSignedIn)
	if _, exists := store.saved(); exists {
		t.Error("obsolete refresh credential remained after persistence failure")
	}
}

func TestNonExpiringTokenDoesNotAcquireFabricatedExpiry(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	store.put(t, testCredential())
	provider := newFakeProvider(t, clock)
	manager := newTestManager(t, store, clock, provider)
	clock.Advance(10 * 365 * 24 * time.Hour)
	if token, err := manager.Token(context.Background()); err != nil || token != testAccessToken {
		t.Fatalf("provider's non-expiring token was assigned a fabricated lifetime: %v", err)
	}
	if provider.count("/login/oauth/access_token") != 0 {
		t.Error("non-expiring token was spuriously refreshed")
	}
}

func TestIdentityValidationDistinguishesRevocationAndUnavailableAccount(t *testing.T) {
	tests := []struct {
		name       string
		reply      providerReply
		expected   error
		persistent bool
	}{
		{"revoked", providerReply{status: 401, body: `{"message":"sensitive-error-marker"}`}, ErrReauthenticationRequired, true},
		{"different-account", providerReply{body: `{"id":42,"login":"other-user"}`}, ErrReauthenticationRequired, true},
		{"forbidden", providerReply{status: 403, body: `{"message":"sensitive-error-marker"}`}, ErrIdentityUnavailable, false},
		{"server-error", providerReply{status: 503, body: `{"message":"sensitive-error-marker"}`}, ErrIdentityUnavailable, false},
		{"malformed-identity", providerReply{body: `{"id":0,"login":"sodapop-user"}`}, ErrInvalidResponse, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			store.put(t, testCredential())
			provider := newFakeProvider(t, clock)
			provider.user = test.reply
			manager := newTestManager(t, store, clock, provider)
			token, err := manager.Token(context.Background())
			assertError(t, err, test.expected)
			if token != "" {
				t.Error("failed account validation released a token")
			}
			provider.setUser(providerReply{body: testUserJSON})
			_, err = manager.Current(context.Background())
			if test.persistent {
				assertError(t, err, ErrReauthenticationRequired)
				if provider.count("/user") != 1 {
					t.Error("revoked credential was reused")
				}
			} else if err != nil {
				t.Fatalf("temporary identity failure incorrectly destroyed the login: %v", err)
			}
		})
	}
}

func TestGitHubRenamePreservesNumericIdentity(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	store.put(t, testCredential())
	provider := newFakeProvider(t, clock)
	provider.user.body = `{"id":9007199254740993,"login":"renamed_managed-user"}`
	manager := newTestManager(t, store, clock, provider)
	account, err := manager.Current(context.Background())
	if err != nil || account.ID != testAccountID || account.Login != "renamed_managed-user" {
		t.Fatalf("account rename changed stable identity: %#v, %v", account, err)
	}
	store.assertCalls(t, "get")
}

func TestSignOutRemovesOnlySodapopCredentialAndAllowsNewLogin(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	store.put(t, testCredential())
	store.values[storeKey("another-app", "github.com")] = "unrelated-entry"
	provider := newFakeProvider(t, clock)
	manager := newTestManager(t, store, clock, provider)
	if _, err := manager.Current(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.SignOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, exists := store.saved(); exists {
		t.Error("sign-out left Sodapop's saved credential behind")
	}
	if store.values[storeKey("another-app", "github.com")] != "unrelated-entry" {
		t.Error("sign-out modified a different application's credential")
	}
	_, err := manager.Token(context.Background())
	assertError(t, err, ErrNotSignedIn)
	if err := manager.SignOut(context.Background()); err != nil {
		t.Fatalf("sign-out was not idempotent: %v", err)
	}
	if _, err := manager.Login(context.Background(), false, nil); err != nil {
		t.Fatalf("could not sign in again after sign-out: %v", err)
	}
	for _, call := range store.calls {
		if call.service != "sodapop" || call.account != "github.com/oauth-user" {
			t.Fatalf("unexpected credential-store namespace: %q/%q", call.service, call.account)
		}
	}
}

func TestFailedSignOutCannotReloadCredentialInThisProcess(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	store.put(t, testCredential())
	store.deleteErr = errors.New("sensitive-error-marker " + testAccessToken)
	manager := NewWithOptions(testClientID, Options{Store: store, Clock: clock, HTTPClient: offlineClient(t)})
	err := manager.SignOut(context.Background())
	assertError(t, err, ErrSecureStorageUnavailable)
	token, err := manager.Token(context.Background())
	assertError(t, err, ErrNotSignedIn)
	if token != "" {
		t.Error("failed keyring deletion allowed a credential to be reloaded")
	}
	store.assertCalls(t, "delete")
}
