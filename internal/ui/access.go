package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/charmbracelet/x/ansi"
)

const (
	copilotPlansURL    = "https://github.com/features/copilot/plans"
	copilotSettingsURL = "https://github.com/settings/copilot"
	accessCheckTimeout = 30 * time.Second
)

type accessFailure struct {
	account    string
	generation uint64
	issue      engine.AccessIssue
}

type accessRecovery struct {
	generation, dialog uint64
	pending            bool
}

type connectionProbe struct {
	ctx    context.Context
	detach func() bool
	cancel context.CancelFunc
}

func newConnectionProbe(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc, *connectionProbe) {
	lifetime, closeLifetime := context.WithCancel(parent)
	deadline, cancel := context.WithTimeout(parent, timeout)
	p := &connectionProbe{ctx: deadline, cancel: cancel}
	p.detach = context.AfterFunc(deadline, closeLifetime)
	closeAll := func() {
		_ = p.finish(nil)
		closeLifetime()
	}
	return lifetime, closeAll, p
}

func (p *connectionProbe) finish(err error) error {
	// A completed check must detach its deadline before canceling the probe,
	// otherwise a healthy account-bound engine would expire with the check.
	p.detach()
	if errors.Is(p.ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("Copilot access check timed out: %w", context.DeadlineExceeded)
	}
	p.cancel()
	return err
}

func (m *Model) finishConnectionProbe(err error) error {
	if m.connectProbe != nil {
		err = m.connectProbe.finish(err)
		m.connectProbe = nil
	}
	m.connectCancel = nil
	return err
}

func (m *Model) retireConnection() tea.Cmd {
	l := m.lease
	m.lease = nil
	if l == nil {
		return nil
	}
	return func() tea.Msg { return decisionMsg{err: l.close()} }
}

func (m *Model) clearAccessFailure() {
	m.access = accessFailure{}
	m.connectionError = nil
}

func (m *Model) currentAccessIssue() (engine.AccessIssue, bool) {
	if m.account.ID == "" {
		return engine.AccessIssue{}, false
	}
	if m.access.account == m.account.ID && m.access.generation == m.engineGeneration &&
		m.access.issue.Reason != "" {
		return m.access.issue, true
	}
	if m.connectionError != nil {
		if issue, ok := engine.AccessIssueFor(m.connectionError); ok {
			return issue, true
		}
		return engine.AccessIssue{Reason: engine.AccessUnknown}, true
	}
	return engine.AccessIssue{}, false
}

func (m *Model) accessBlocked() bool {
	_, blocked := m.currentAccessIssue()
	return blocked
}

func (m *Model) recordAccessFailure(err error, supplied *engine.AccessIssue, fallback bool) bool {
	if m.account.ID == "" || m.quitting || errors.Is(err, context.Canceled) {
		return false
	}
	issue, known := engine.AccessIssueFor(err)
	if supplied != nil {
		issue, known = *supplied, true
	}
	if errors.Is(err, auth.ErrReauthenticationRequired) || errors.Is(err, auth.ErrInvalidCredential) {
		issue, known = engine.AccessIssue{Reason: engine.AccessAuthentication, Remediation: "sign_in"}, true
	} else if errors.Is(err, auth.ErrNetworkUnavailable) {
		issue, known = engine.AccessIssue{Reason: engine.AccessNetwork}, true
	}
	if !known {
		if !fallback {
			return false
		}
		issue = engine.AccessIssue{Reason: engine.AccessUnknown}
	}
	m.access = accessFailure{account: m.account.ID, generation: m.engineGeneration, issue: issue}
	m.connectionError = err
	m.finishStartup()
	m.clearMoment()
	return true
}

func accessTitle(issue engine.AccessIssue) string {
	switch issue.Reason {
	case engine.AccessAuthentication:
		return "Copilot needs reauthorization"
	case engine.AccessAuthorization:
		return "Copilot access denied"
	case engine.AccessPolicy:
		return "Copilot models disabled by policy"
	case engine.AccessQuota:
		return "Copilot usage limit reached"
	case engine.AccessBilling:
		return "Copilot billing needs attention"
	case engine.AccessRateLimit:
		return "Copilot rate limit reached"
	case engine.AccessNetwork:
		return "Copilot connection unavailable"
	default:
		return "Copilot access unavailable"
	}
}

func accessExplanation(issue engine.AccessIssue) string {
	switch issue.Reason {
	case engine.AccessAuthentication:
		return "Copilot could not use this GitHub credential. Sign in again through Sodapop to renew authorization. This does not establish that your Copilot plan is missing."
	case engine.AccessPolicy:
		return "The available Copilot models are disabled by policy. Ask your organization administrator to review your seat, CLI access, and model policies. Buying another plan may not resolve a policy restriction."
	case engine.AccessQuota:
		return "Copilot reported a usage quota limit, not a missing subscription. Review usage and the reset information in your GitHub Copilot settings, or contact your organization administrator."
	case engine.AccessBilling:
		return "Copilot reported that billing is not configured. Review the account's Copilot billing/spending settings, or contact your organization administrator. This does not mean your GitHub sign-in failed."
	case engine.AccessRateLimit:
		return "Copilot reported a rate limit, not a missing subscription. Wait for the limit to reset before checking access again. Review your GitHub Copilot usage if the limit persists."
	case engine.AccessNetwork:
		return "Copilot could not be reached or the access check timed out. Check your connection and try Check access again. A connectivity failure is not evidence that you need to buy a plan."
	default:
		return "Your GitHub sign-in worked, but Copilot access could not be confirmed. If you have not activated Copilot for this account, use Get Copilot to review available plans, including Free where eligible. If your organization provides Copilot, ask your administrator to check your seat and CLI policy."
	}
}

func accessShowsPlans(issue engine.AccessIssue) bool {
	switch issue.Reason {
	case engine.AccessAuthentication, engine.AccessPolicy, engine.AccessQuota,
		engine.AccessBilling, engine.AccessRateLimit, engine.AccessNetwork:
		return false
	default:
		return true
	}
}

func accessShowsSettings(issue engine.AccessIssue) bool {
	return issue.Reason == engine.AccessQuota || issue.Reason == engine.AccessBilling || issue.Reason == engine.AccessRateLimit
}

func (m *Model) accessHint(issue engine.AccessIssue) string {
	if m.needsAbort || m.canceling {
		return "Ctrl+C stops the unresolved turn; /login offers recovery afterward."
	}
	if issue.Reason == engine.AccessAuthentication || issue.Remediation == "sign_in" {
		return "/login: Sign in again. Your draft is kept."
	}
	if accessShowsPlans(issue) {
		return "/login: Get Copilot / Check access again. Your draft is kept."
	}
	return "/login: Check access again. Your draft is kept."
}

func (m *Model) notifyAccessFailure() {
	if issue, ok := m.currentAccessIssue(); ok {
		// This is durable status, not a new transcript entry on every retry.
		m.notice = notice{text: accessTitle(issue) + ". " + m.accessHint(issue), error: true}
	}
}

func (m *Model) accessErrorText(err error, supplied *engine.AccessIssue) string {
	if m.recordAccessFailure(err, supplied, false) {
		issue, _ := m.currentAccessIssue()
		return accessTitle(issue) + ". " + m.accessHint(issue)
	}
	if err != nil {
		return err.Error()
	}
	return "the Copilot engine reported an unspecified error"
}

func (m *Model) presentAccessRecovery() {
	prompt := m.accessRecovery
	m.accessRecovery.pending = false
	if prompt.pending && prompt.generation == m.engineGeneration &&
		prompt.dialog == m.dialogSequence && m.overlay == nil && !m.quitting {
		m.showAccount()
	}
}

func (m *Model) accessWelcomeView(issue engine.AccessIssue) string {
	c, g := m.color, m.layout
	lines := []string{
		c.paint(c.amber, accessTitle(issue)),
		c.paint(c.lime, clip("GitHub: @"+singleLine(m.account.Login)+" / account retained", g.innerWidth)),
		c.paint(c.cyan, m.accessHint(issue)),
		"",
		accessExplanation(issue),
		"",
		c.paint(c.muted, "Local commands: /help /theme /diff"),
		c.paint(c.muted, "No prompt is retried. Your draft and current history are kept."),
	}
	return fitBlock(ansi.Wrap(strings.Join(lines, "\n"), max(1, g.innerWidth), ""), g.innerWidth, g.timeline)
}

func (m *Model) accountBody() string {
	body := authExplanation(m.authError)
	if m.account.ID != "" {
		storage := "secure credential storage"
		if m.account.SessionOnly {
			storage = "session-only; not saved to disk"
		}
		body = fmt.Sprintf("GitHub: @%s\nCredentials: %s", singleLine(m.account.Login), storage)
		if m.connecting {
			body += "\n\nChecking Copilot access. Your draft is kept."
		} else if issue, blocked := m.currentAccessIssue(); blocked {
			body += "\n\nGitHub is signed in, but Copilot is unavailable.\n" +
				accessTitle(issue) + "\n\n" + accessExplanation(issue)
			if accessShowsPlans(issue) {
				body += "\n\nAvailable Copilot plans:\n" + copilotPlansURL
			} else if accessShowsSettings(issue) {
				body += "\n\nCopilot settings:\n" + copilotSettingsURL
			}
			if accessShowsPlans(issue) || accessShowsSettings(issue) {
				body += "\nUse @" + singleLine(m.account.Login) + " in the browser; it may be signed in to a different account."
			}
			if issue.Remediation == "switch_account" {
				body += "\n\nIf a different GitHub account has access, sign out here and sign in with that account."
			}
			body += "\n\nAfter resolving access, select Check access again. Your draft and history are kept; nothing is replayed."
			if m.needsAbort || m.canceling {
				body += "\nStop the unresolved turn with Ctrl+C before reconnecting."
			}
		} else if m.lease != nil && len(m.models) > 0 {
			body += "\n\nCopilot: connected."
		} else {
			body += "\n\nCopilot: not connected. Select Check access again."
		}
		body += "\n\nSigning out removes Sodapop credentials, not conversation history or completed edits."
	}
	if m.signOutWarning != "" {
		body = m.signOutWarning + "\n\n" + body
	}
	return body
}

func (m *Model) accountAccessItems() []menuItem {
	items := []menuItem{{
		id: "reconnect", label: "Check access again", detail: "Refresh this account's connection; never replay a prompt",
		disabled: m.busy(),
	}}
	if issue, blocked := m.currentAccessIssue(); blocked {
		if issue.Reason == engine.AccessAuthentication || issue.Remediation == "sign_in" {
			id := "login"
			if m.account.SessionOnly {
				id = "session-login"
			}
			items = append([]menuItem{{id: id, label: "Sign in again", detail: "Renew Sodapop's GitHub authorization", disabled: m.busy()}}, items...)
		}
		if accessShowsPlans(issue) {
			items = append(items,
				menuItem{id: "copilot-plans", label: "Get Copilot", detail: "Review Free eligibility, personal plans, or organization access"},
				menuItem{id: "copy-copilot-plans", label: "Copy plan link", detail: "Use this account in your browser"},
			)
		} else if accessShowsSettings(issue) {
			items = append(items,
				menuItem{id: "copilot-settings", label: "Open Copilot settings", detail: "Review usage or billing; this does not change your plan"},
				menuItem{id: "copy-copilot-settings", label: "Copy settings link", detail: "Use this account in your browser"},
			)
		}
	}
	return append(items,
		menuItem{id: "signout", label: "Sign out", detail: "Keep history and working-tree changes"},
		menuItem{id: "close", label: "Not now / Back to conversation", detail: "Keep local commands and your draft available"},
	)
}

type copilotPage uint8

const (
	copilotPlans copilotPage = iota
	copilotSettings
)

func (p copilotPage) url() string {
	switch p {
	case copilotPlans:
		return copilotPlansURL
	case copilotSettings:
		return copilotSettingsURL
	default:
		return ""
	}
}

type accountLinkMsg struct {
	auth, engine, dialog, request uint64
	account                       string
	page                          copilotPage
	err                           error
}

func (m *Model) cancelAccountAction() {
	if m.overlay != nil && m.overlay.kind == dialogAccount && m.overlay.cancel != nil {
		m.overlay.cancel()
		m.overlay.cancel = nil
	}
}

func (m *Model) copilotLinkCommand(page copilotPage, copyLink bool) tea.Cmd {
	uri := page.url()
	if uri == "" || m.account.ID == "" || m.overlay == nil || m.overlay.kind != dialogAccount {
		m.report("Open /login and choose an available Copilot link; no action was taken.", true)
		return nil
	}
	m.cancelAccountAction()
	ctx, cancel := context.WithCancel(m.life.ctx)
	m.overlay.cancel = cancel
	m.accountLinkSequence++
	reply := accountLinkMsg{
		auth: m.authGeneration, engine: m.engineGeneration, dialog: m.overlay.id,
		request: m.accountLinkSequence, account: m.account.ID, page: page,
	}
	if copyLink {
		m.notice = notice{text: "Copilot link copied if your terminal supports clipboard access."}
		command := tea.SetClipboard(uri)
		return func() tea.Msg {
			defer cancel()
			if ctx.Err() != nil {
				return nil
			}
			return command()
		}
	}
	open := m.openBrowser
	return func() tea.Msg {
		defer cancel()
		if err := ctx.Err(); err != nil {
			reply.err = err
			return reply
		}
		openCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		reply.err = open(openCtx, uri)
		return reply
	}
}

func (m *Model) accountLinkResult(msg accountLinkMsg) {
	if m.quitting || msg.auth != m.authGeneration || msg.engine != m.engineGeneration ||
		msg.account != m.account.ID || msg.request != m.accountLinkSequence ||
		m.overlay == nil || m.overlay.kind != dialogAccount || msg.dialog != m.overlay.id {
		return
	}
	if errors.Is(msg.err, context.Canceled) {
		return
	}
	if msg.err != nil {
		m.overlay.body = m.accountBody() + "\n\nThe browser could not be opened. Open this address yourself, or use the copy-link action:\n" + msg.page.url()
		m.notice = notice{text: "Browser opening failed. The link is available in /login.", error: true}
	} else {
		m.notice = notice{text: "GitHub page opened. Return here and choose Check access again when ready."}
	}
}
