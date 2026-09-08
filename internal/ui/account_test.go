package ui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/commands"
	"github.com/VeVarunSharma/sodapop/internal/engine"
)

func TestMissingAuthKeepsHelpThemeAndAccountAccessible(t *testing.T) {
	options := testOptions()
	a := options.Auth.(*fakeAuth)
	m := testModel(t, options)
	runFinite(t, m, m.loadIdentity())
	if m.account.ID != "" || m.lease != nil {
		t.Fatal("missing auth fabricated an identity or connection")
	}
	for _, text := range []string{"/help", "/theme midnight"} {
		m.composer.SetValue(text)
		runFinite(t, m, m.submit())
		m.closeDialog()
	}
	if m.prefs.Theme != "midnight" || a.loginCalls.Load() != 0 || a.tokenCalls.Load() != 0 {
		t.Fatal("local onboarding actions attempted credentials or failed")
	}
	m.handleKey(keyPress("f2"))
	if m.overlay != nil {
		t.Fatal("F2 still opened the account menu")
	}
	m.runLocal("login")
	if m.overlay == nil || m.overlay.kind != dialogAccount || len(m.overlay.items) != 3 {
		t.Fatal("native account menu is unreachable without auth")
	}
	m.composer.SetValue("my prompt")
	m.closeDialog()
	runFinite(t, m, m.submit())
	if m.composer.Value() != "my prompt" || !strings.Contains(m.notice.text, "/login") {
		t.Fatal("unauthenticated send did not retain draft and direct user to account")
	}
}

func TestLoginFailuresAreExplicitAndNeverReportedAsSuccess(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"missing client ID", auth.ErrNoClientID, "SODAPOP_GITHUB_CLIENT_ID"},
		{"unavailable keychain", auth.ErrSecureStorageUnavailable, "Session-only"},
		{"network", errors.New("network unavailable"), "network unavailable"},
		{"denied", errors.New("authorization denied"), "authorization denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := testOptions()
			a := &fakeAuth{currentErr: auth.ErrNotSignedIn, loginErr: test.err}
			options.Auth = a
			var factories atomic.Int32
			options.NewEngine = func(context.Context, auth.Account) (engine.Engine, error) {
				factories.Add(1)
				return newFakeEngine(), nil
			}
			m := testModel(t, options)
			m.identityLoading = false
			m.composer.SetValue("draft stays")
			cmd := m.startLogin(false)
			if a.loginCalls.Load() != 0 {
				t.Fatal("Login ran in UI update rather than a command")
			}
			batch := cmd().(tea.BatchMsg)
			runFinite(t, m, batch[0])
			if batch[1]() != nil {
				t.Fatal("device-code subscription not cancelled after failed login")
			}
			if m.overlay == nil || m.overlay.kind != dialogAccount || !strings.Contains(m.overlay.body, test.want) {
				t.Fatalf("actionable login error missing: %#v", m.overlay)
			}
			if m.account.ID != "" || m.loggingIn || factories.Load() != 0 || a.tokenCalls.Load() != 0 {
				t.Fatal("failed login was represented as authenticated")
			}
			if m.composer.Value() != "draft stays" {
				t.Fatal("failed login discarded draft")
			}
			m.runLocal("login")
			if m.overlay.kind != dialogAccount {
				t.Fatal("/login inaccessible after auth error")
			}
		})
	}
}

func TestDeviceFlowExplicitBrowserCopyAndCancellation(t *testing.T) {
	options := testOptions()
	var requestedSessionOnly atomic.Bool
	a := &fakeAuth{loginFn: func(ctx context.Context, sessionOnly bool, onCode func(auth.DeviceCode)) (auth.Account, error) {
		requestedSessionOnly.Store(sessionOnly)
		onCode(auth.DeviceCode{UserCode: "TEST-CODE", VerificationURI: "https://github.com/login/device"})
		<-ctx.Done()
		return auth.Account{}, ctx.Err()
	}}
	options.Auth = a
	m := testModel(t, options)
	m.identityLoading = false
	var opens atomic.Int32
	m.openBrowser = func(context.Context, string) error { opens.Add(1); return nil }
	commands := m.startLogin(true)().(tea.BatchMsg)
	done := make(chan tea.Msg, 1)
	go func() { done <- commands[0]() }()
	code := commands[1]().(deviceCodeMsg)
	m.Update(code)
	if !requestedSessionOnly.Load() || opens.Load() != 0 || m.overlay == nil || m.overlay.kind != dialogLogin {
		t.Fatal("device flow did not stay native and explicit")
	}
	open := m.handleKey(keyPress("o"))
	if opens.Load() != 0 {
		t.Fatal("browser was opened synchronously")
	}
	runFinite(t, m, open)
	if opens.Load() != 1 || m.copyDeviceCode() == nil {
		t.Fatal("explicit browser / copy action unavailable")
	}
	m.composer.SetValue("preserved draft")
	m.cancelLogin()
	select {
	case msg := <-done:
		m.Update(msg)
	case <-time.After(time.Second):
		t.Fatal("login was not cancelled")
	}
	if m.loggingIn || m.account.ID != "" || m.deviceCode.UserCode != "" || m.composer.Value() != "preserved draft" {
		t.Fatal("login cancellation failed or retained the device code")
	}
}

func TestLateSuccessfulLoginCannotResurrectCancelledIdentity(t *testing.T) {
	m := testModel(t, testOptions())
	m.identityLoading = false
	old := m.authGeneration
	m.loggingIn = true
	m.loginCancel = func() {}
	m.cancelLogin()
	m.Update(identityMsg{generation: old, account: auth.Account{ID: "late", Login: "late"}, login: true})
	if m.account.ID != "" || m.connecting {
		t.Fatal("late login result replaced cancelled state")
	}
}

func TestCopilotAccessFailureIsDistinctFromGitHubIdentity(t *testing.T) {
	m, _ := readyModel(t)
	m.modelsResult(modelsMsg{generation: m.engineGeneration, err: errors.New("Copilot entitlement is missing")})
	m.showAccount()
	if m.account.Login != "octocat" || !strings.Contains(m.overlay.body, "GitHub is signed in, but Copilot is unavailable") {
		t.Fatal("Copilot entitlement failure was misreported as signed out")
	}
	if m.overlay.items[0].id != "reconnect" {
		t.Fatal("reconnect action not available after model access failure")
	}
}

func TestSignOutClosesEngineButKeepsDraftAndHistory(t *testing.T) {
	m, f := readyModel(t)
	a := &fakeAuth{}
	m.opts.Auth = a
	m.session = engine.Session{ID: "keep-session"}
	m.entries = append(m.entries, &entry{id: "keep", role: "assistant", raw: "kept history", final: true})
	m.composer.SetValue("kept draft")
	input, err := commands.Parse("/logout")
	if err != nil {
		t.Fatal(err)
	}
	cmd, accepted := m.execute(input, true)
	if !accepted {
		t.Fatal("/logout was not accepted")
	}
	if a.signOutCalls.Load() != 0 || f.closes != 0 {
		t.Fatal("sign-out blocked the UI loop")
	}
	runFinite(t, m, cmd)
	if a.signOutCalls.Load() != 1 || f.closes != 1 || m.account.ID != "" || m.lease != nil {
		t.Fatal("sign-out did not remove account and close engine")
	}
	if m.session.ID != "keep-session" || len(m.entries) != 1 || m.composer.Value() != "kept draft" {
		t.Fatal("sign-out erased conversation history or draft")
	}
}

func TestReconnectRestoresSessionWithoutReplayingPrompt(t *testing.T) {
	m, old := readyModel(t)
	f := newFakeEngine()
	m.session = engine.Session{ID: "existing"}
	m.composer.SetValue("next draft")
	m.opts.NewEngine = func(context.Context, auth.Account) (engine.Engine, error) { return f, nil }
	connected := m.connect()().(connectedMsg)
	discovery := m.connectedResult(connected)().(tea.BatchMsg)
	models := discovery[1]().(modelsMsg)
	runFinite(t, m, m.modelsResult(models))
	if old.closes != 1 || len(f.resumed) != 1 || f.resumed[0] != "existing" || len(f.sent) != 0 {
		t.Fatal("reconnect failed to resume, or replayed a prompt")
	}
	if m.composer.Value() != "next draft" {
		t.Fatal("reconnect discarded draft")
	}
}

func TestBrowserRejectsUntrustedVerificationURLs(t *testing.T) {
	for _, uri := range []string{
		"file:///etc/passwd", "javascript:alert(1)", "http://github.com/login/device",
		"https://evil.example/login/device", "https://github.com@evil.example/login/device",
		"https://github.com:8443/login/device", "\x1b]52;ignored",
	} {
		if err := openVerificationURL(context.Background(), uri); err == nil {
			t.Fatalf("accepted unsafe URI %q", uri)
		}
	}
}

func TestBackgroundIdentityDoesNotDismissLocalHelp(t *testing.T) {
	options := testOptions()
	options.Auth = &fakeAuth{account: auth.Account{ID: "account", Login: "octocat"}}
	options.NewEngine = func(context.Context, auth.Account) (engine.Engine, error) { return newFakeEngine(), nil }
	m := testModel(t, options)
	m.runLocal("help")
	id := m.overlay.id
	identity := m.loadIdentity()().(identityMsg)
	cmd := m.identityResult(identity)
	if cmd == nil || m.overlay == nil || m.overlay.id != id || m.overlay.kind != dialogHelp {
		t.Fatal("background identity discovery dismissed local help")
	}
}
