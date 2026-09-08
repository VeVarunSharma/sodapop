package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	testClientID       = "sodapop-owned-test-client"
	testDeviceCode     = "sodapop-test-private-device-code"
	testAccessToken    = "sodapop-test-user-access-token"
	testRefreshToken   = "sodapop-test-user-refresh-token"
	testRenewedToken   = "sodapop-test-renewed-access-token"
	testRenewedRefresh = "sodapop-test-renewed-refresh-token"
	testAccountID      = "9007199254740993"
	testDeviceJSON     = `{"device_code":"sodapop-test-private-device-code","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`
	testTokenJSON      = `{"access_token":"sodapop-test-user-access-token","token_type":"bearer","scope":"read:user"}`
	testRenewedJSON    = `{"access_token":"sodapop-test-renewed-access-token","token_type":"bearer","expires_in":3600,"refresh_token":"sodapop-test-renewed-refresh-token","refresh_token_expires_in":86400}`
	testUserJSON       = `{"id":9007199254740993,"login":"sodapop-user"}`
)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	waits  []time.Duration
	onWait func(context.Context, time.Duration) error
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func (c *fakeClock) Wait(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.waits = append(c.waits, duration)
	hook := c.onWait
	c.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, duration); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.Advance(duration)
	return nil
}

func (c *fakeClock) assertWaits(t *testing.T, expected ...time.Duration) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !reflect.DeepEqual(c.waits, expected) {
		t.Errorf("poll waits = %v, want %v", c.waits, expected)
	}
}

type storeCall struct {
	operation string
	service   string
	account   string
}

type fakeStore struct {
	mu           sync.Mutex
	values       map[string]string
	calls        []storeCall
	getErr       error
	setErr       error
	deleteErr    error
	writeOnError bool
	onGet        func()
	onSet        func()
}

func newFakeStore() *fakeStore {
	return &fakeStore{values: make(map[string]string)}
}

func storeKey(service, account string) string { return service + "\x00" + account }

func (s *fakeStore) Get(service, account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, storeCall{"get", service, account})
	if s.onGet != nil {
		s.onGet()
	}
	if s.getErr != nil {
		return "", s.getErr
	}
	value, ok := s.values[storeKey(service, account)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (s *fakeStore) Set(service, account, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, storeCall{"set", service, account})
	if s.setErr == nil || s.writeOnError {
		s.values[storeKey(service, account)] = value
	}
	if s.onSet != nil {
		s.onSet()
	}
	return s.setErr
}

func (s *fakeStore) Delete(service, account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, storeCall{"delete", service, account})
	if s.deleteErr != nil {
		return s.deleteErr
	}
	key := storeKey(service, account)
	if _, ok := s.values[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(s.values, key)
	return nil
}

func (s *fakeStore) put(t *testing.T, c credential) {
	t.Helper()
	value, err := encodeCredential(c, testClientID)
	if err != nil {
		t.Fatal("could not create the synthetic secure-store fixture")
	}
	s.putRaw(value)
}

func (s *fakeStore) putRaw(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[storeKey(credentialService, credentialAccount)] = value
}

func (s *fakeStore) saved() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[storeKey(credentialService, credentialAccount)]
	return value, ok
}

func (s *fakeStore) assertCalls(t *testing.T, operations ...string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) != len(operations) {
		t.Errorf("store call count = %d, want %d", len(s.calls), len(operations))
		return
	}
	for i, call := range s.calls {
		if call.operation != operations[i] || call.service != credentialService || call.account != credentialAccount {
			t.Errorf("unexpected credential-store operation at index %d", i)
		}
	}
}

func testCredential() credential {
	return credential{
		account:     Account{ID: testAccountID, Login: "sodapop-user"},
		accessToken: secret(testAccessToken),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func offlineClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected HTTP request")
		return nil, errors.New("unexpected test HTTP request")
	})}
}

func newTestManager(t *testing.T, store Store, clock Clock, handler http.Handler) *Manager {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := server.Client().Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" ||
			(request.URL.Host != "github.com" && request.URL.Host != "api.github.com") {
			t.Error("authentication attempted an unexpected endpoint")
			return nil, errors.New("unexpected test endpoint")
		}
		copy := request.Clone(request.Context())
		copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
		return transport.RoundTrip(copy)
	})}
	return NewWithOptions(testClientID, Options{Store: store, Clock: clock, HTTPClient: client})
}

type providerReply struct {
	status     int
	body       string
	retryAfter string
}

type fakeProvider struct {
	t         *testing.T
	clock     *fakeClock
	mu        sync.Mutex
	device    providerReply
	tokens    []providerReply
	user      providerReply
	scope     string
	counts    map[string]int
	grants    []string
	pollTimes []time.Time
	hook      func(http.ResponseWriter, *http.Request) bool
}

func newFakeProvider(t *testing.T, clock *fakeClock) *fakeProvider {
	return &fakeProvider{
		t: t, clock: clock, device: providerReply{body: testDeviceJSON},
		tokens: []providerReply{{body: testTokenJSON}},
		user:   providerReply{body: testUserJSON}, scope: "read:user",
		counts: make(map[string]int),
	}
}

func (p *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.counts[r.URL.Path]++
	call := p.counts[r.URL.Path]
	hook := p.hook
	p.mu.Unlock()
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			p.t.Error("OAuth form could not be parsed")
		}
	}
	if hook != nil && hook(w, r) {
		return
	}
	if r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" || r.Header.Get("User-Agent") != "sodapop" {
		p.t.Error("unexpected ambient credentials or request metadata")
	}
	var reply providerReply
	switch r.URL.Path {
	case "/login/device/code", "/login/oauth/access_token":
		if r.Method != http.MethodPost || r.Host != "github.com" ||
			r.Header.Get("Accept") != "application/json" || r.Header.Get("Authorization") != "" {
			p.t.Error("OAuth request is not a public-client POST")
		}
		if r.Form.Get("client_id") != testClientID || r.Form.Has("client_secret") || r.Form.Has("code") {
			p.t.Error("OAuth request did not use only the Sodapop-owned public client")
		}
		p.mu.Lock()
		if r.URL.Path == "/login/device/code" {
			if r.Form.Get("scope") != p.scope || len(r.Form) > 2 {
				p.t.Error("unexpected identity scopes or device request fields")
			}
			reply = p.device
		} else {
			grant := r.Form.Get("grant_type")
			p.grants = append(p.grants, grant)
			p.pollTimes = append(p.pollTimes, p.clock.Now())
			switch grant {
			case "urn:ietf:params:oauth:grant-type:device_code":
				if r.Form.Get("device_code") != testDeviceCode || len(r.Form) != 3 {
					p.t.Error("unexpected device grant fields")
				}
			case "refresh_token":
				token := r.Form.Get("refresh_token")
				if (token != testRefreshToken && token != testRenewedRefresh) || len(r.Form) != 3 {
					p.t.Error("unexpected public refresh grant fields")
				}
			default:
				p.t.Error("unexpected OAuth grant")
			}
			if call <= len(p.tokens) {
				reply = p.tokens[call-1]
			} else {
				p.t.Error("unexpected extra token request")
				reply = providerReply{body: `{"error":"access_denied"}`}
			}
		}
		p.mu.Unlock()
	case "/user":
		if r.Method != http.MethodGet || r.Host != "api.github.com" ||
			r.Header.Get("Accept") != "application/vnd.github+json" ||
			r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			p.t.Error("unexpected GitHub identity request")
		}
		token := r.Header.Get("Authorization")
		if token != "Bearer "+testAccessToken && token != "Bearer "+testRenewedToken {
			p.t.Error("identity request did not use the explicitly acquired token")
		}
		p.mu.Lock()
		reply = p.user
		p.mu.Unlock()
	default:
		p.t.Error("unexpected provider path")
		http.NotFound(w, r)
		return
	}
	writeReply(w, reply)
}

func writeReply(w http.ResponseWriter, reply providerReply) {
	w.Header().Set("Content-Type", "application/json")
	if reply.retryAfter != "" {
		w.Header().Set("Retry-After", reply.retryAfter)
	}
	status := reply.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, reply.body)
}

func (p *fakeProvider) count(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[path]
}

func (p *fakeProvider) setUser(reply providerReply) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.user = reply
}

func assertError(t *testing.T, err, expected error) {
	t.Helper()
	if !errors.Is(err, expected) {
		t.Fatalf("error category = %v, want %v", err, expected)
	}
	assertRedacted(t, err.Error())
}

func assertRedacted(t *testing.T, value string) {
	t.Helper()
	for _, sensitive := range []string{
		testDeviceCode, testAccessToken, testRefreshToken, testRenewedToken, testRenewedRefresh,
		"sensitive-error-marker",
	} {
		if strings.Contains(value, sensitive) {
			t.Error("sensitive authentication data escaped redaction")
		}
	}
}

func await[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an authentication operation")
		var zero T
		return zero
	}
}
