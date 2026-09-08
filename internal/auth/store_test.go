package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMalformedAndStaleCredentialsDifferFromMissing(t *testing.T) {
	base, err := encodeCredential(testCredential(), testClientID)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		data     string
		clientID string
		expected error
	}{
		{"empty", "", testClientID, ErrInvalidCredential},
		{"broken-json", "{", testClientID, ErrInvalidCredential},
		{"null", "null", testClientID, ErrInvalidCredential},
		{"future-version", strings.Replace(base, `"version":1`, `"version":2`, 1), testClientID, ErrInvalidCredential},
		{"unknown-field", strings.TrimSuffix(base, "}") + `,"unknown":true}`, testClientID, ErrInvalidCredential},
		{"trailing-json", base + "{}", testClientID, ErrInvalidCredential},
		{"other-provider", strings.Replace(base, `"host":"github.com"`, `"host":"elsewhere.invalid"`, 1), testClientID, ErrInvalidCredential},
		{"missing-client", strings.Replace(base, testClientID, "", 1), testClientID, ErrInvalidCredential},
		{"invalid-client-metadata", strings.Replace(base, testClientID, "invalid client", 1), "invalid client", ErrInvalidCredential},
		{"changed-client", base, "another-sodapop-client", ErrReauthenticationRequired},
		{"no-configured-client", base, "", ErrNoClientID},
		{"unsafe-id", strings.Replace(base, testAccountID, "../account", 1), testClientID, ErrInvalidCredential},
		{"negative-id", strings.Replace(base, testAccountID, "-1", 1), testClientID, ErrInvalidCredential},
		{"leading-zero-id", strings.Replace(base, testAccountID, "00123", 1), testClientID, ErrInvalidCredential},
		{"unsafe-login", strings.Replace(base, "sodapop-user", `sodapop\u001b[31m`, 1), testClientID, ErrInvalidCredential},
		{"missing-token", strings.Replace(base, testAccessToken, "", 1), testClientID, ErrInvalidCredential},
		{"invalid-token", strings.Replace(base, testAccessToken, `sensitive-error-marker\n`, 1), testClientID, ErrInvalidCredential},
		{"invalid-timestamp", strings.Replace(base, `"expires_at":"0001-01-01T00:00:00Z"`, `"expires_at":"sensitive-error-marker"`, 1), testClientID, ErrInvalidCredential},
		{"orphan-refresh-expiry", strings.Replace(base, `"refresh_expires_at":"0001-01-01T00:00:00Z"`, `"refresh_expires_at":"2030-01-01T00:00:00Z"`, 1), testClientID, ErrInvalidCredential},
		{"oversized", strings.Repeat(" ", maxCredentialBytes) + base, testClientID, ErrInvalidCredential},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeStore()
			store.putRaw(test.data)
			manager := NewWithOptions(test.clientID, Options{Store: store, Clock: newFakeClock(), HTTPClient: offlineClient(t)})
			account, err := manager.Current(context.Background())
			assertError(t, err, test.expected)
			if errors.Is(err, ErrNotSignedIn) || account != (Account{}) {
				t.Error("invalid credential was treated as a missing entry or an authenticated account")
			}
			value, exists := store.saved()
			if !exists || value != test.data {
				t.Error("invalid saved credential was silently changed")
			}
			store.assertCalls(t, "get")
		})
	}
}

func TestCorruptCredentialCanBeReplacedByExplicitLogin(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	store.putRaw("{")
	manager := newTestManager(t, store, clock, newFakeProvider(t, clock))
	_, err := manager.Current(context.Background())
	assertError(t, err, ErrInvalidCredential)
	if _, err := manager.Login(context.Background(), false, nil); err != nil {
		t.Fatalf("explicit reauthorization could not replace an invalid record: %v", err)
	}
}

func TestSecretContainersAreRedactedForAllFormattingVerbs(t *testing.T) {
	store := newFakeStore()
	c := testCredential()
	c.refreshToken = secret(testRefreshToken)
	manager := NewWithOptions(testClientID, Options{Store: store, Clock: newFakeClock(), HTTPClient: offlineClient(t)})
	manager.state.credential = &c
	values := []any{
		secret(testAccessToken), c, &c, manager, *manager, *manager.state,
		persistenceRecord{AccessToken: testAccessToken, RefreshToken: testRefreshToken},
		deviceAuthorization{code: secret(testDeviceCode)},
		deviceResponse{DeviceCode: secret(testDeviceCode)},
		tokenResponse{AccessToken: secret(testAccessToken), RefreshToken: secret(testRefreshToken)},
	}
	for _, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			assertRedacted(t, fmt.Sprintf(format, value))
		}
	}
	for _, value := range []any{manager, c, secret(testAccessToken)} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		assertRedacted(t, string(data))
	}
}

func TestSessionOnlyCannotBeSerializedForPersistence(t *testing.T) {
	c := testCredential()
	c.account.SessionOnly = true
	value, err := encodeCredential(c, testClientID)
	assertError(t, err, ErrInvalidCredential)
	if value != "" {
		t.Error("session-only credential was serialized for persistence")
	}
}

func TestCredentialEncodingRequiresValidClientID(t *testing.T) {
	for _, test := range []struct {
		clientID string
		expected error
	}{
		{"", ErrNoClientID},
		{" \t", ErrInvalidCredential},
		{"invalid client", ErrInvalidCredential},
	} {
		value, err := encodeCredential(testCredential(), test.clientID)
		assertError(t, err, test.expected)
		if value != "" {
			t.Error("credential was serialized without a valid issuing client ID")
		}
	}
}

func TestSecureRecordSizeIsBounded(t *testing.T) {
	c := testCredential()
	c.accessToken, c.refreshToken = secret(strings.Repeat("a", 1024)), secret(strings.Repeat("r", 1024))
	_, err := encodeCredential(c, testClientID)
	assertError(t, err, ErrInvalidCredential)
}

func TestSecureStorePlatformSupport(t *testing.T) {
	for _, test := range []struct {
		goos string
		want bool
	}{
		{"darwin", true},
		{"linux", true},
		{"windows", true},
		{"freebsd", false},
	} {
		if got := secureStoreSupported(test.goos); got != test.want {
			t.Errorf("secureStoreSupported(%q) = %t, want %t", test.goos, got, test.want)
		}
	}
}

func TestCredentialRoundTripUsesOnlyRealExpirations(t *testing.T) {
	c := testCredential()
	c.refreshToken = secret(testRefreshToken)
	c.expiresAt = newFakeClock().Now().Add(time.Hour)
	c.refreshExpiresAt = c.expiresAt.Add(24 * time.Hour)
	value, err := encodeCredential(c, testClientID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeCredential(value, testClientID)
	if err != nil || got.account != c.account || got.accessToken != c.accessToken || got.refreshToken != c.refreshToken ||
		!got.expiresAt.Equal(c.expiresAt) || !got.refreshExpiresAt.Equal(c.refreshExpiresAt) {
		t.Fatal("secure credential round trip changed provider identity, tokens, or expiration")
	}
}
