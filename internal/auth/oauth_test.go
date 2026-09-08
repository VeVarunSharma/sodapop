package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPollingHonorsPendingSlowDownAndProviderInterval(t *testing.T) {
	tests := []struct {
		name   string
		tokens []providerReply
		waits  []time.Duration
	}{
		{
			"pending-and-provider-slowdown",
			[]providerReply{
				{body: `{"error":"authorization_pending"}`},
				{status: 400, body: `{"error":"slow_down","interval":20}`},
				{body: `{"error":"authorization_pending"}`},
				{body: testTokenJSON},
			},
			[]time.Duration{5 * time.Second, 5 * time.Second, 20 * time.Second, 20 * time.Second},
		},
		{
			"repeated-slowdown",
			[]providerReply{
				{body: `{"error":"slow_down"}`},
				{body: `{"error":"slow_down","interval":1}`},
				{body: testTokenJSON},
			},
			[]time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second},
		},
		{
			"server-backoff",
			[]providerReply{
				{status: 429, retryAfter: "30", body: `{"error":"rate_limited"}`},
				{status: 503, body: "temporarily unavailable"},
				{body: testTokenJSON},
			},
			[]time.Duration{5 * time.Second, 30 * time.Second, 60 * time.Second},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			provider := newFakeProvider(t, clock)
			provider.tokens = test.tokens
			manager := newTestManager(t, store, clock, provider)
			if _, err := manager.Login(context.Background(), true, nil); err != nil {
				t.Fatal(err)
			}
			clock.assertWaits(t, test.waits...)
			if provider.count("/login/oauth/access_token") != len(test.tokens) {
				t.Error("incorrect number of token polls")
			}
		})
	}
}

func TestPollingDoesNotCatchUpAfterSlowResponses(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	provider := newFakeProvider(t, clock)
	provider.tokens = []providerReply{{body: `{"error":"authorization_pending"}`}, {body: testTokenJSON}}
	provider.hook = func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/login/oauth/access_token" {
			clock.Advance(4 * time.Second)
		}
		return false
	}
	manager := newTestManager(t, store, clock, provider)
	if _, err := manager.Login(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.pollTimes) != 2 || provider.pollTimes[1].Sub(provider.pollTimes[0]) != 9*time.Second {
		t.Error("slow HTTP responses caused catch-up polling faster than the provider interval")
	}
}

type timeoutFailure struct{}

func (timeoutFailure) Error() string   { return "sensitive-error-marker " + testDeviceCode }
func (timeoutFailure) Timeout() bool   { return true }
func (timeoutFailure) Temporary() bool { return true }

func TestPollingBacksOffAfterConnectionTimeout(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	manager := newTestManager(t, store, clock, newFakeProvider(t, clock))
	transport := manager.state.client.Transport
	failures := 0
	manager.state.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == githubTokenURL && failures == 0 {
			failures++
			return nil, timeoutFailure{}
		}
		return transport.RoundTrip(request)
	})
	if _, err := manager.Login(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	clock.assertWaits(t, 5*time.Second, 10*time.Second)
}

func TestDeviceCodeExpiresWithoutAnExtraPoll(t *testing.T) {
	tests := []struct {
		name    string
		expires string
		tokens  []providerReply
		waits   []time.Duration
		polls   int
	}{
		{"before-first-poll", "3", nil, []time.Duration{3 * time.Second}, 0},
		{"while-pending", "6", []providerReply{{body: `{"error":"authorization_pending"}`}}, []time.Duration{5 * time.Second, time.Second}, 1},
		{"server-expiry", "900", []providerReply{{body: `{"error":"expired_token"}`}}, []time.Duration{5 * time.Second}, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			provider := newFakeProvider(t, clock)
			provider.device.body = strings.Replace(testDeviceJSON, `"expires_in":900`, `"expires_in":`+test.expires, 1)
			provider.tokens = test.tokens
			manager := newTestManager(t, store, clock, provider)
			_, err := manager.Login(context.Background(), true, nil)
			assertError(t, err, ErrDeviceCodeExpired)
			if provider.count("/login/oauth/access_token") != test.polls || provider.count("/user") != 0 {
				t.Error("expired device authorization made additional requests")
			}
			clock.assertWaits(t, test.waits...)
			store.assertCalls(t)
		})
	}
}

func TestDeviceExpiryIncludesAuthorizationRequestLatency(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	provider := newFakeProvider(t, clock)
	provider.hook = func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/login/device/code" {
			clock.Advance(901 * time.Second)
		}
		return false
	}
	manager := newTestManager(t, store, clock, provider)
	_, err := manager.Login(context.Background(), true, func(DeviceCode) {
		t.Error("already-expired user code was sent to the UI")
	})
	assertError(t, err, ErrDeviceCodeExpired)
}

func TestOAuthDenialAndConfigurationErrorsAreActionable(t *testing.T) {
	tests := []struct {
		code     string
		expected error
	}{
		{"access_denied", ErrAuthorizationDenied},
		{"device_flow_disabled", ErrOAuthClientRejected},
		{"incorrect_client_credentials", ErrOAuthClientRejected},
		{"invalid_scope", ErrOAuthClientRejected},
		{"incorrect_device_code", ErrReauthenticationRequired},
		{"sensitive-error-marker", ErrOAuthFailed},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			provider := newFakeProvider(t, clock)
			provider.tokens = []providerReply{{
				status: 400,
				body:   `{"error":"` + test.code + `","error_description":"sensitive-error-marker","error_uri":"https://example.invalid/` + testAccessToken + `"}`,
			}}
			manager := newTestManager(t, store, clock, provider)
			account, err := manager.Login(context.Background(), false, nil)
			assertError(t, err, test.expected)
			if account != (Account{}) || provider.count("/user") != 0 {
				t.Error("failed authorization proceeded to authenticated identity")
			}
			store.assertCalls(t, "get")
		})
	}
}

func TestMalformedOrUnsafeDeviceResponseNeverReachesUI(t *testing.T) {
	tests := map[string]string{
		"missing-device-code":   strings.Replace(testDeviceJSON, testDeviceCode, "", 1),
		"user-code-is-secret":   strings.Replace(testDeviceJSON, "ABCD-EFGH", testDeviceCode, 1),
		"terminal-escape":       strings.Replace(testDeviceJSON, "ABCD-EFGH", `ABCD-\u001bFGH`, 1),
		"unsafe-url":            strings.Replace(testDeviceJSON, githubVerificationURL, "https://example.invalid/login/device", 1),
		"secret-in-url":         strings.Replace(testDeviceJSON, githubVerificationURL, githubVerificationURL+"?device_code="+testDeviceCode, 1),
		"url-with-fragment":     strings.Replace(testDeviceJSON, githubVerificationURL, githubVerificationURL+"#"+testAccessToken, 1),
		"url-with-user-info":    strings.Replace(testDeviceJSON, githubVerificationURL, "https://sodapop@github.com/login/device", 1),
		"no-expiry":             strings.Replace(testDeviceJSON, `"expires_in":900`, `"expires_in":0`, 1),
		"negative-expiry":       strings.Replace(testDeviceJSON, `"expires_in":900`, `"expires_in":-1`, 1),
		"overflow-expiry":       strings.Replace(testDeviceJSON, `"expires_in":900`, `"expires_in":9223372036854775807`, 1),
		"negative-interval":     strings.Replace(testDeviceJSON, `"interval":5`, `"interval":-1`, 1),
		"overflow-interval":     strings.Replace(testDeviceJSON, `"interval":5`, `"interval":9223372036854775807`, 1),
		"trailing-json":         testDeviceJSON + "{}",
		"missing-json":          "",
		"json-null":             "null",
		"oversized-json":        strings.Repeat(" ", maxResponseBytes) + testDeviceJSON,
		"private-and-user-same": strings.Replace(testDeviceJSON, testDeviceCode, "ABCD-EFGH", 1),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			provider := newFakeProvider(t, clock)
			provider.device.body = body
			manager := newTestManager(t, store, clock, provider)
			_, err := manager.Login(context.Background(), true, func(DeviceCode) {
				t.Error("unsafe device response reached the UI")
			})
			assertError(t, err, ErrInvalidResponse)
			if provider.count("/login/oauth/access_token") != 0 {
				t.Error("unsafe device response was polled")
			}
		})
	}
}

func TestDefaultPollingIntervalAndLegacyVerificationURL(t *testing.T) {
	store, clock := newFakeStore(), newFakeClock()
	provider := newFakeProvider(t, clock)
	provider.device.body = strings.ReplaceAll(
		strings.Replace(testDeviceJSON, `,"interval":5`, "", 1),
		"verification_uri", "verification_url",
	)
	manager := newTestManager(t, store, clock, provider)
	if _, err := manager.Login(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	clock.assertWaits(t, 5*time.Second)
}

func TestMalformedTokensAndIdentitiesAreNotPersisted(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		user     string
		expected error
	}{
		{"missing-token", `{"token_type":"bearer"}`, testUserJSON, ErrInvalidResponse},
		{"wrong-token-type", strings.Replace(testTokenJSON, "bearer", "mac", 1), testUserJSON, ErrInvalidResponse},
		{"header-injection", strings.Replace(testTokenJSON, testAccessToken, `value\r\nInjected: invalid`, 1), testUserJSON, ErrInvalidResponse},
		{"expired-token", strings.TrimSuffix(testTokenJSON, "}") + `,"expires_in":0}`, testUserJSON, ErrReauthenticationRequired},
		{"negative-expiry", strings.TrimSuffix(testTokenJSON, "}") + `,"expires_in":-1}`, testUserJSON, ErrInvalidResponse},
		{"overflow-expiry", strings.TrimSuffix(testTokenJSON, "}") + `,"expires_in":9223372036854775807}`, testUserJSON, ErrInvalidResponse},
		{"orphan-refresh-expiry", strings.TrimSuffix(testTokenJSON, "}") + `,"refresh_token_expires_in":3600}`, testUserJSON, ErrInvalidResponse},
		{"negative-user-id", testTokenJSON, `{"id":-1,"login":"sodapop-user"}`, ErrInvalidResponse},
		{"floating-user-id", testTokenJSON, `{"id":123.4,"login":"sodapop-user"}`, ErrInvalidResponse},
		{"string-user-id", testTokenJSON, `{"id":"123","login":"sodapop-user"}`, ErrInvalidResponse},
		{"unsafe-login", testTokenJSON, `{"id":123,"login":"sodapop\u001b[31m"}`, ErrInvalidResponse},
		{"missing-login", testTokenJSON, `{"id":123}`, ErrInvalidResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			provider := newFakeProvider(t, clock)
			provider.tokens, provider.user.body = []providerReply{{body: test.token}}, test.user
			manager := newTestManager(t, store, clock, provider)
			_, err := manager.Login(context.Background(), false, nil)
			assertError(t, err, test.expected)
			store.assertCalls(t, "get")
			if test.name == "expired-token" && provider.count("/user") != 0 {
				t.Error("a known expired token was sent to GitHub")
			}
		})
	}
}

func TestHTTPNetworkFailuresAreRedacted(t *testing.T) {
	for _, endpoint := range []string{githubDeviceURL, githubTokenURL, githubUserURL} {
		t.Run(strings.TrimPrefix(endpoint, "https://"), func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			manager := newTestManager(t, store, clock, newFakeProvider(t, clock))
			transport := manager.state.client.Transport
			manager.state.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() == endpoint {
					return nil, errors.New("sensitive-error-marker " + testDeviceCode + " " + testAccessToken)
				}
				return transport.RoundTrip(request)
			})
			_, err := manager.Login(context.Background(), false, nil)
			assertError(t, err, ErrNetworkUnavailable)
			for cause := err; cause != nil; cause = errors.Unwrap(cause) {
				assertRedacted(t, cause.Error())
			}
			store.assertCalls(t, "get")
		})
	}
}

func TestHTTPRedirectsCannotForwardDeviceOrAccessTokens(t *testing.T) {
	for _, path := range []string{"/login/device/code", "/login/oauth/access_token", "/user"} {
		t.Run(path, func(t *testing.T) {
			store, clock := newFakeStore(), newFakeClock()
			provider := newFakeProvider(t, clock)
			provider.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == path {
					w.Header().Set("Location", "https://github.com/steal?secret="+testAccessToken)
					w.WriteHeader(http.StatusTemporaryRedirect)
					return true
				}
				return false
			}
			manager := newTestManager(t, store, clock, provider)
			_, err := manager.Login(context.Background(), false, nil)
			if err == nil {
				t.Fatal("redirect was accepted as successful authorization")
			}
			assertRedacted(t, err.Error())
			if provider.count("/steal") != 0 {
				t.Error("credential-bearing request followed a redirect")
			}
			store.assertCalls(t, "get")
		})
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("sensitive-error-marker " + testAccessToken)
}

func TestHTTPBodiesAreClosedAndReadErrorsAreRedacted(t *testing.T) {
	for _, reader := range []io.Reader{strings.NewReader(testDeviceJSON), failingReader{}} {
		body := &trackedBody{Reader: reader}
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: request}, nil
		})}
		manager := NewWithOptions(testClientID, Options{Store: newFakeStore(), Clock: newFakeClock(), HTTPClient: client})
		_, err := manager.state.authorizeDevice(context.Background())
		if err != nil {
			assertError(t, err, ErrNetworkUnavailable)
		}
		if !body.closed {
			t.Error("OAuth response body was not closed")
		}
	}
}

func TestScopeIsolationAndPublicClientConfiguration(t *testing.T) {
	for _, scopes := range [][]string{nil, {}, {"read:user", "read:org"}} {
		store, clock := newFakeStore(), newFakeClock()
		provider := newFakeProvider(t, clock)
		manager := newTestManager(t, store, clock, provider)
		client := manager.state.client
		input := append([]string{}, scopes...)
		if scopes == nil {
			input = nil
		}
		manager = NewWithOptions(testClientID, Options{Store: store, Clock: clock, HTTPClient: client, Scopes: input})
		if scopes != nil {
			provider.scope = strings.Join(scopes, " ")
		}
		if len(input) > 0 {
			input[0] = "repo"
		}
		if _, err := manager.Login(context.Background(), true, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, options := range []Options{{Scopes: []string{"read:user repo"}}, {Scopes: []string{""}}} {
		options.Store, options.Clock, options.HTTPClient = newFakeStore(), newFakeClock(), offlineClient(t)
		_, err := NewWithOptions(testClientID, options).Login(context.Background(), true, nil)
		assertError(t, err, ErrOAuthClientRejected)
	}
	manager := NewWithOptions("invalid client ID", Options{Store: newFakeStore(), HTTPClient: offlineClient(t)})
	_, err := manager.Login(context.Background(), true, nil)
	assertError(t, err, ErrOAuthClientRejected)
}

func TestHTTPClientTimeoutIsBoundedWithoutMutatingCaller(t *testing.T) {
	for _, timeout := range []time.Duration{0, time.Hour, time.Second} {
		client := &http.Client{Timeout: timeout, Transport: offlineClient(t).Transport}
		manager := NewWithOptions(testClientID, Options{Store: newFakeStore(), HTTPClient: client})
		expected := timeout
		if expected <= 0 || expected > requestTimeout {
			expected = requestTimeout
		}
		if manager.state.client.Timeout != expected || client.Timeout != timeout {
			t.Error("HTTP timeout was unbounded or the caller's client was mutated")
		}
	}
}

func TestRetryAfterAndSaturatingIntervals(t *testing.T) {
	now := newFakeClock().Now()
	tests := map[string]time.Duration{
		"15": 15 * time.Second,
		"-1": 0, "invalid": 0, "9223372036854775807": 0,
		now.Add(30 * time.Second).Format(http.TimeFormat): 30 * time.Second,
		now.Add(-time.Second).Format(http.TimeFormat):     0,
	}
	for input, expected := range tests {
		if got := retryAfter(input, now); got != expected {
			t.Errorf("retry delay = %v, want %v", got, expected)
		}
	}
	maxDuration := time.Duration(1<<63 - 1)
	if backoff(maxDuration) != maxDuration || slowDown(maxDuration) != maxDuration {
		t.Error("poll interval overflowed")
	}
}

func TestSystemClockWaitHonorsCancellation(t *testing.T) {
	clock := systemClock{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertError(t, clock.Wait(ctx, time.Hour), context.Canceled)
	if err := clock.Wait(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if clock.Now().IsZero() {
		t.Error("system clock returned an unset time")
	}
}

func TestDeviceRequestHonorsClientAndCallerTimeouts(t *testing.T) {
	for _, callerDeadline := range []bool{false, true} {
		store, clock := newFakeStore(), newFakeClock()
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})}
		manager := NewWithOptions(testClientID, Options{Store: store, Clock: clock, HTTPClient: client})
		ctx := context.Background()
		expected := ErrNetworkUnavailable
		if callerDeadline {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 25*time.Millisecond)
			defer cancel()
			expected = context.DeadlineExceeded
		} else {
			manager.state.client.Timeout = 25 * time.Millisecond
		}
		_, err := manager.Login(ctx, true, nil)
		assertError(t, err, expected)
		store.assertCalls(t)
	}
}
