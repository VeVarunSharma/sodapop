package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/engine"
)

type classifiedAccessError struct {
	issue engine.AccessIssue
}

func (e classifiedAccessError) Error() string                   { return "classified access failure" }
func (e classifiedAccessError) AccessIssue() engine.AccessIssue { return e.issue }

func TestAccessPresentationCoversRecoveryCategories(t *testing.T) {
	m, _ := readyModel(t)
	tests := []struct {
		reason   engine.AccessReason
		title    string
		plans    bool
		settings bool
	}{
		{engine.AccessAuthentication, "reauthorization", false, false},
		{engine.AccessAuthorization, "access denied", true, false},
		{engine.AccessPolicy, "disabled by policy", false, false},
		{engine.AccessQuota, "usage limit", false, true},
		{engine.AccessBilling, "billing", false, true},
		{engine.AccessRateLimit, "rate limit", false, true},
		{engine.AccessNetwork, "connection", false, false},
		{engine.AccessUnknown, "unavailable", true, false},
	}
	for _, test := range tests {
		t.Run(string(test.reason), func(t *testing.T) {
			issue := engine.AccessIssue{Reason: test.reason}
			if title := accessTitle(issue); !strings.Contains(strings.ToLower(title), test.title) {
				t.Fatalf("title = %q", title)
			}
			if explanation := accessExplanation(issue); strings.TrimSpace(explanation) == "" {
				t.Fatal("missing explanation")
			}
			if accessShowsPlans(issue) != test.plans || accessShowsSettings(issue) != test.settings {
				t.Fatalf("wrong actions for %+v", issue)
			}
			m.access = accessFailure{account: m.account.ID, generation: m.engineGeneration, issue: issue}
			m.connectionError = errors.New("access failed")
			if hint := m.accessHint(issue); !strings.Contains(hint, "/login") {
				t.Fatalf("hint = %q", hint)
			}
			if body := m.accessWelcomeView(issue); !strings.Contains(body, accessTitle(issue)) {
				t.Fatalf("welcome omitted access title: %q", body)
			}
			if body := m.accountBody(); !strings.Contains(body, "GitHub is signed in") {
				t.Fatalf("account body omitted retained identity: %q", body)
			}
			if items := m.accountAccessItems(); len(items) < 3 {
				t.Fatalf("account actions = %#v", items)
			}
		})
	}
}

func TestAccessFailureClassificationAndHints(t *testing.T) {
	m, _ := readyModel(t)
	if m.recordAccessFailure(context.Canceled, nil, true) {
		t.Fatal("cancellation became an access failure")
	}
	if m.recordAccessFailure(errors.New("ordinary failure"), nil, false) {
		t.Fatal("unclassified failure became an access failure without fallback")
	}
	if !m.recordAccessFailure(errors.New("ordinary failure"), nil, true) {
		t.Fatal("fallback access failure was discarded")
	}
	if issue, ok := m.currentAccessIssue(); !ok || issue.Reason != engine.AccessUnknown || !m.accessBlocked() {
		t.Fatalf("fallback issue = %+v, %t", issue, ok)
	}
	m.notifyAccessFailure()
	if !m.notice.error || !strings.Contains(m.notice.text, "unavailable") {
		t.Fatalf("access notice = %#v", m.notice)
	}
	m.clearAccessFailure()
	if _, ok := m.currentAccessIssue(); ok || m.connectionError != nil {
		t.Fatal("access failure was not cleared")
	}
	if !m.recordAccessFailure(auth.ErrReauthenticationRequired, nil, false) {
		t.Fatal("reauthorization error was not classified")
	}
	if issue, _ := m.currentAccessIssue(); issue.Reason != engine.AccessAuthentication {
		t.Fatalf("authentication issue = %+v", issue)
	}
	m.clearAccessFailure()
	if !m.recordAccessFailure(auth.ErrNetworkUnavailable, nil, false) {
		t.Fatal("network error was not classified")
	}
	if issue, _ := m.currentAccessIssue(); issue.Reason != engine.AccessNetwork {
		t.Fatalf("network issue = %+v", issue)
	}
	m.needsAbort = true
	if hint := m.accessHint(engine.AccessIssue{Reason: engine.AccessNetwork}); !strings.Contains(hint, "Ctrl+C") {
		t.Fatalf("unresolved-turn hint = %q", hint)
	}
}

func TestConnectionProbeAndAccountLinksAreGenerationBound(t *testing.T) {
	m, f := readyModel(t)
	lifetime, closeLifetime, probe := newConnectionProbe(m.life.ctx, time.Minute)
	if lifetime.Err() != nil || probe.ctx.Err() != nil {
		t.Fatal("new connection probe was already canceled")
	}
	closeLifetime()
	if lifetime.Err() == nil || probe.ctx.Err() == nil {
		t.Fatal("connection probe did not close")
	}

	if m.lease == nil {
		t.Fatal("fixture has no engine lease")
	}
	cmd := m.retireConnection()
	if m.lease != nil || cmd == nil {
		t.Fatal("engine lease was not retired")
	}
	cmd()
	if f.closes != 1 {
		t.Fatalf("retired engine closes = %d", f.closes)
	}

	m, _ = readyModel(t)
	m.access = accessFailure{
		account: m.account.ID, generation: m.engineGeneration,
		issue: engine.AccessIssue{Reason: engine.AccessAuthorization},
	}
	m.showAccount()
	if copilotPlans.url() != copilotPlansURL || copilotSettings.url() != copilotSettingsURL ||
		copilotPage(99).url() != "" {
		t.Fatal("Copilot account URLs are inconsistent")
	}
	opened := ""
	m.openBrowser = func(_ context.Context, uri string) error {
		opened = uri
		return nil
	}
	result := m.copilotLinkCommand(copilotPlans, false)().(accountLinkMsg)
	m.accountLinkResult(result)
	if opened != copilotPlansURL || !strings.Contains(m.notice.text, "opened") {
		t.Fatalf("account link result = %q, %#v", opened, m.notice)
	}

	m.showAccount()
	m.openBrowser = func(context.Context, string) error { return errors.New("browser unavailable") }
	result = m.copilotLinkCommand(copilotPlans, false)().(accountLinkMsg)
	m.accountLinkResult(result)
	if !m.notice.error || !strings.Contains(m.overlay.body, copilotPlansURL) {
		t.Fatalf("browser failure was not recoverable: %#v", m.notice)
	}
	m.cancelAccountAction()
	if m.overlay.cancel != nil {
		t.Fatal("account action cancellation was not cleared")
	}
}

func TestAccessRecoveryHandlesStructuredErrorsTimeoutsAndDialogs(t *testing.T) {
	m, _ := readyModel(t)
	classified := classifiedAccessError{issue: engine.AccessIssue{
		Reason: engine.AccessQuota, Remediation: "show_account",
	}}
	m.connectionError = classified
	if issue, ok := m.currentAccessIssue(); !ok || issue != classified.issue {
		t.Fatalf("connection issue = %+v, %t", issue, ok)
	}
	m.connectionError = nil
	if text := m.accessErrorText(classified, nil); !strings.Contains(text, "usage limit") {
		t.Fatalf("classified error text = %q", text)
	}
	m.clearAccessFailure()
	supplied := engine.AccessIssue{Reason: engine.AccessPolicy}
	if !m.recordAccessFailure(errors.New("policy failure"), &supplied, false) {
		t.Fatal("supplied access issue was discarded")
	}
	if text := m.accessErrorText(errors.New("ordinary failure"), nil); text != "ordinary failure" {
		t.Fatalf("ordinary error text = %q", text)
	}
	if text := m.accessErrorText(nil, nil); !strings.Contains(text, "unspecified") {
		t.Fatalf("nil error text = %q", text)
	}

	m.overlay = nil
	m.dialogSequence++
	m.accessRecovery = accessRecovery{
		generation: m.engineGeneration, dialog: m.dialogSequence, pending: true,
	}
	m.presentAccessRecovery()
	if m.overlay == nil || m.overlay.kind != dialogAccount || m.accessRecovery.pending {
		t.Fatal("matching recovery prompt did not open the account dialog")
	}
	m.closeDialog()
	m.accessRecovery = accessRecovery{
		generation: m.engineGeneration - 1, dialog: m.dialogSequence, pending: true,
	}
	m.presentAccessRecovery()
	if m.overlay != nil {
		t.Fatal("stale recovery prompt opened a dialog")
	}

	_, closeLifetime, probe := newConnectionProbe(m.life.ctx, time.Nanosecond)
	<-probe.ctx.Done()
	if err := probe.finish(nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probe timeout = %v", err)
	}
	closeLifetime()

	m.lease = nil
	if cmd := m.retireConnection(); cmd != nil {
		t.Fatal("retiring an absent connection returned work")
	}
	m.overlay = nil
	if cmd := m.copilotLinkCommand(copilotPage(99), false); cmd != nil || !m.notice.error {
		t.Fatal("invalid account link action was accepted")
	}

	m, _ = readyModel(t)
	m.access = accessFailure{
		account: m.account.ID, generation: m.engineGeneration,
		issue: engine.AccessIssue{Reason: engine.AccessAuthorization},
	}
	m.showAccount()
	if cmd := m.copilotLinkCommand(copilotPlans, true); cmd == nil {
		t.Fatal("copy-link action was not scheduled")
	} else {
		cmd()
	}
	if !strings.Contains(m.notice.text, "copied") {
		t.Fatalf("copy-link notice = %#v", m.notice)
	}
	m.accountLinkResult(accountLinkMsg{
		auth: m.authGeneration, engine: m.engineGeneration, dialog: m.overlay.id,
		request: m.accountLinkSequence, account: m.account.ID, page: copilotPlans,
		err: context.Canceled,
	})

	account := m.account
	m.account = auth.Account{}
	if _, ok := m.currentAccessIssue(); ok {
		t.Fatal("signed-out state retained an access issue")
	}
	m.account = account
}

func TestAccountBodyDistinguishesConnectionStates(t *testing.T) {
	m, _ := readyModel(t)
	m.clearAccessFailure()
	m.connecting = true
	if body := m.accountBody(); !strings.Contains(body, "Checking Copilot access") {
		t.Fatalf("connecting body = %q", body)
	}
	m.connecting = false
	if body := m.accountBody(); !strings.Contains(body, "Copilot: connected") {
		t.Fatalf("connected body = %q", body)
	}
	m.lease = nil
	if body := m.accountBody(); !strings.Contains(body, "not connected") {
		t.Fatalf("disconnected body = %q", body)
	}
	m.account.SessionOnly = true
	m.signOutWarning = "Credential cleanup needs attention."
	body := m.accountBody()
	if !strings.Contains(body, "session-only") || !strings.HasPrefix(body, m.signOutWarning) {
		t.Fatalf("session-only body = %q", body)
	}
}
