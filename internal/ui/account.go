package ui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/engine"
)

func authExplanation(err error) string {
	switch {
	case errors.Is(err, auth.ErrNoClientID):
		return "Sodapop OAuth is not configured.\n\nThe owner must register a GitHub OAuth application with device flow enabled and supply its public client ID through SODAPOP_GITHUB_CLIENT_ID. No client secret is needed.\n\nNo sign-in was completed. Sodapop does not use ambient Copilot or gh credentials. /help and /theme remain available."
	case errors.Is(err, auth.ErrSecureStorageUnavailable):
		return "Secure credential storage is unavailable.\n\nChoose Session-only sign-in to keep credentials in memory until Sodapop closes, or cancel. Sodapop will not save a token in a plaintext file."
	case errors.Is(err, auth.ErrNotSignedIn):
		return "Connect your GitHub account to use Copilot. Authorization happens in your browser while this screen stays in Sodapop.\n\nGitHub sign-in and Copilot entitlement / organization policy are separate requirements."
	case err != nil:
		return "GitHub sign-in failed: " + safeText(err.Error()) + "\n\nUse /login to retry. Your draft and conversation history are kept."
	default:
		return "Sodapop uses its own GitHub device authorization. Browser opening and clipboard copying always require your action."
	}
}

func (m *Model) showAccount() {
	if m.loggingIn {
		m.showLogin()
		return
	}
	d := m.newDialog(dialogAccount, "ACCOUNT / GitHub and Copilot", m.accountBody())
	if m.account.ID == "" {
		d.items = []menuItem{
			{id: "login", label: "Sign in securely", detail: "Keep credentials in the OS keychain"},
			{id: "session-login", label: "Session-only sign-in", detail: "Memory only; expires when Sodapop closes"},
			{id: "close", label: "Not now", detail: "Keep using local commands"},
		}
		if errors.Is(m.authError, auth.ErrSecureStorageUnavailable) {
			d.selected = 1
		}
		if m.signOutWarning != "" {
			d.items = append(d.items, menuItem{id: "signout", label: "Retry removing saved credentials", detail: "Only Sodapop's keyring entry"})
		}
	} else {
		d.items = m.accountAccessItems()
	}
}

func (m *Model) startLogin(sessionOnly bool) tea.Cmd {
	if m.opts.Auth == nil {
		m.authError = errors.New("GitHub authentication is unavailable in this build")
		m.showAccount()
		return nil
	}
	if m.busy() {
		m.report("Finish or cancel the current operation before signing in. Your draft is kept.", true)
		return nil
	}
	m.cancelAccountAction()
	m.clearAccessFailure()
	m.accessRecovery.pending = false
	if m.identityCancel != nil {
		m.identityCancel()
		m.identityCancel = nil
	}
	m.identityLoading = false
	m.cancelContextRequest()
	m.life.cancelEngineWork()
	m.engineGeneration++
	m.lease = nil
	if m.session.ID != "" {
		m.reconnectID = m.session.ID
		m.needsResume = true
	}
	m.authGeneration++
	generation := m.authGeneration
	ctx, cancel := context.WithCancel(m.life.ctx)
	m.loginCancel = cancel
	m.loginContext = ctx
	m.loggingIn = true
	m.authError = nil
	m.deviceCode = auth.DeviceCode{}
	m.showLogin()
	codes := make(chan auth.DeviceCode, 1)
	service, life := m.opts.Auth, m.life
	login := func() tea.Msg {
		var account auth.Account
		_, err := life.mutateAccount(ctx, func() error {
			var err error
			account, err = service.Login(ctx, sessionOnly, func(code auth.DeviceCode) {
				select {
				case codes <- code:
				case <-ctx.Done():
				}
			})
			return err
		})
		if err == nil && (account.ID == "" || account.Login == "") {
			err = errors.New("GitHub returned an incomplete account identity; sign in again")
		}
		return identityMsg{generation: generation, account: account, err: err, login: true}
	}
	code := func() tea.Msg {
		select {
		case value := <-codes:
			return deviceCodeMsg{generation: generation, code: value}
		case <-ctx.Done():
			return nil
		}
	}
	return tea.Batch(login, code)
}

func (m *Model) showLogin() {
	body := "Requesting a GitHub device code...\n\nNo browser or clipboard action happens automatically."
	items := []menuItem{{id: "cancel-login", label: "Cancel sign-in", detail: "Esc or Ctrl+C"}}
	if m.deviceCode.UserCode != "" {
		body = "Open this GitHub address and enter the code:\n\n" +
			safeText(m.deviceCode.VerificationURI) + "\n\n  " + singleLine(m.deviceCode.UserCode) +
			"\n\nWaiting for authorization in your browser."
		if !m.deviceCode.ExpiresAt.IsZero() {
			body += "\nCode expires at " + m.deviceCode.ExpiresAt.Local().Format("15:04") + "."
		}
		items = []menuItem{
			{id: "open-browser", label: "Open browser", detail: "O / explicit action"},
			{id: "copy-code", label: "Copy device code", detail: "C / terminal clipboard (OSC 52)"},
			{id: "cancel-login", label: "Cancel sign-in", detail: "Esc or Ctrl+C"},
		}
	}
	d := m.newDialog(dialogLogin, "CONNECT / GitHub device authorization", body)
	d.items = items
}

func (m *Model) cancelLogin() tea.Cmd {
	if m.loginCancel != nil {
		m.loginCancel()
		m.loginCancel = nil
	}
	m.authGeneration++
	m.loggingIn = false
	m.identityLoading = false
	if m.identityCancel != nil {
		m.identityCancel()
		m.identityCancel = nil
	}
	m.deviceCode = auth.DeviceCode{}
	m.authError = nil
	m.report("Sign-in cancelled. No prompt was sent.", false)
	m.showAccount()
	return nil
}

func (m *Model) identityResult(msg identityMsg) tea.Cmd {
	if msg.generation != m.authGeneration {
		return nil
	}
	m.identityLoading = false
	if m.identityCancel != nil {
		m.identityCancel()
		m.identityCancel = nil
	}
	m.loggingIn = false
	if m.loginCancel != nil {
		m.loginCancel()
		m.loginCancel = nil
	}
	m.deviceCode = auth.DeviceCode{}
	if msg.err != nil {
		m.authError = msg.err
		if msg.login {
			m.showAccount()
		}
		if !errors.Is(msg.err, auth.ErrNotSignedIn) {
			m.report(authExplanation(msg.err), true)
		}
		return nil
	}
	if m.accountOwner != "" && m.accountOwner != msg.account.ID {
		m.resetConversation()
		m.reconnectID = ""
	}
	m.account = msg.account
	m.accountOwner = msg.account.ID
	m.authError = nil
	if !msg.account.SessionOnly {
		m.signOutWarning = ""
	}
	if m.overlay != nil && (m.overlay.kind == dialogLogin || m.overlay.kind == dialogAccount) {
		m.cancelAccountAction()
		m.overlay = nil
	}
	return tea.Batch(m.loadMCP(), m.connectWithRecovery(msg.login))
}

func (m *Model) connect() tea.Cmd {
	return m.connectWithRecovery(m.overlay != nil && m.overlay.kind == dialogAccount)
}

func (m *Model) connectWithRecovery(showRecovery bool) tea.Cmd {
	if m.opts.NewEngine == nil {
		m.connectionError = errors.New("the Copilot engine is unavailable in this build")
		m.report(m.connectionError.Error(), true)
		return nil
	}
	if m.account.ID == "" {
		m.showAccount()
		return nil
	}
	if m.busy() {
		m.report("Finish or cancel the current operation before reconnecting.", true)
		return nil
	}
	if m.session.ID != "" {
		m.reconnectID = m.session.ID
	}
	showRecovery = showRecovery && (m.overlay == nil || m.overlay.kind == dialogAccount || m.overlay.kind == dialogLogin)
	m.cancelAccountAction()
	_ = m.finishConnectionProbe(nil)
	m.cancelContextRequest()
	m.engineGeneration++
	generation := m.engineGeneration
	ctx, cancel, probe := newConnectionProbe(m.life.ctx, m.connectTimeout)
	m.connectProbe = probe
	m.connectCancel = cancel
	m.connecting = true
	m.clearAccessFailure()
	m.accessRecovery = accessRecovery{generation: generation, dialog: m.dialogSequence, pending: showRecovery}
	m.report("Checking Copilot access. Your draft is kept; nothing is sent.", false)
	previous := m.lease
	m.lease = nil
	if m.overlay != nil && (m.overlay.kind == dialogAccount || m.overlay.kind == dialogLogin) {
		m.overlay = nil
	}
	account, create, life := m.account, m.opts.NewEngine, m.life
	work := life.registerFactory(cancel)
	return func() tea.Msg {
		defer life.finishFactory(work)
		if previous != nil {
			if err := previous.close(); err != nil {
				cancel()
				return connectedMsg{generation: generation, err: fmt.Errorf("close previous connection: %w", err)}
			}
		}
		lease, err := accountRead(life, ctx, func() (*engineLease, error) {
			if err := life.closeEngines(); err != nil {
				return nil, fmt.Errorf("close previous connection: %w", err)
			}
			backend, err := create(ctx, account)
			if err != nil {
				if backend != nil {
					err = errors.Join(err, backend.Close())
				}
				return nil, err
			}
			if backend == nil {
				return nil, errors.New("engine factory returned no connection")
			}
			lease := &engineLease{ctx: ctx, engine: backend, cancel: cancel}
			if !life.adopt(lease) || ctx.Err() != nil {
				return nil, errors.Join(ctx.Err(), lease.close())
			}
			return lease, nil
		})
		if err != nil {
			cancel()
		}
		return connectedMsg{generation: generation, lease: lease, err: err}
	}
}

func (m *Model) connectedResult(msg connectedMsg) tea.Cmd {
	if msg.generation != m.engineGeneration || m.quitting || m.life.ctx.Err() != nil ||
		!m.connecting || m.lease != nil {
		if msg.lease != nil && msg.lease != m.lease {
			l := msg.lease
			return func() tea.Msg { return decisionMsg{err: l.close()} }
		}
		return nil
	}
	if msg.err != nil {
		m.connecting = false
		err := m.finishConnectionProbe(msg.err)
		if !m.recordAccessFailure(err, nil, true) {
			m.connectionError = err
		}
		m.notifyAccessFailure()
		m.presentAccessRecovery()
		return nil
	}
	if msg.lease == nil || msg.lease.ctx.Err() != nil {
		err := errors.New("Copilot returned no usable connection")
		if msg.lease != nil {
			err = msg.lease.ctx.Err()
		}
		m.connecting = false
		err = m.finishConnectionProbe(err)
		if !m.recordAccessFailure(err, nil, true) {
			m.connectionError = err
		}
		m.notifyAccessFailure()
		m.presentAccessRecovery()
		if msg.lease != nil {
			l := msg.lease
			return func() tea.Msg { return decisionMsg{err: l.close()} }
		}
		return nil
	}
	m.lease = msg.lease
	return tea.Batch(m.waitEvents(), m.loadModels())
}

func (m *Model) loadModels() tea.Cmd {
	l, generation, life := m.lease, m.engineGeneration, m.life
	return func() tea.Msg {
		models, err := accountRead(life, l.ctx, func() ([]engine.Model, error) {
			return l.engine.Models(l.ctx)
		})
		return modelsMsg{generation: generation, models: models, err: err}
	}
}

func (m *Model) modelsResult(msg modelsMsg) tea.Cmd {
	if msg.generation != m.engineGeneration || m.quitting || m.life.ctx.Err() != nil || m.lease == nil {
		return nil
	}
	m.connecting = false
	err := m.finishConnectionProbe(msg.err)
	if err == nil && m.lease.ctx.Err() != nil {
		err = m.lease.ctx.Err()
	}
	if err != nil || len(msg.models) == 0 {
		if err == nil {
			err = engine.ErrNoModels
		}
		if !m.recordAccessFailure(err, nil, true) {
			m.connectionError = err
		}
		m.models = nil
		m.model = ""
		m.contextTier, m.reasoningEffort = "", ""
		m.notifyAccessFailure()
		if m.overlay != nil && m.overlay.kind == dialogModels {
			m.overlay.body = "Copilot access is unavailable. Close this picker and use /login for recovery."
			m.overlay.items = nil
		}
		m.presentAccessRecovery()
		return m.retireConnection()
	}
	m.accessRecovery.pending = false
	m.clearAccessFailure()
	m.report("Copilot connected. Your draft is kept; no prompt was sent.", false)
	m.models = msg.models
	m.model = ""
	for _, model := range m.models {
		if model.ID == m.prefs.Model {
			selection, _ := m.selectionForModel(model.ID)
			m.model = selection.ModelID
			m.contextTier = selection.ContextTier
			m.reasoningEffort = selection.ReasoningEffort
			break
		}
	}
	if m.reconnectID != "" {
		id := m.reconnectID
		m.reconnectID = ""
		return m.resumeSession(id)
	}
	if m.model == "" {
		if m.overlay == nil || m.overlay.kind == dialogModels {
			m.showModels()
		} else {
			m.report("Choose a Copilot model with /model before your first prompt.", false)
		}
	} else if m.overlay != nil && m.overlay.kind == dialogModels {
		m.showModels()
	}
	return nil
}

func (m *Model) signOut() tea.Cmd {
	if m.opts.Auth == nil {
		m.report("GitHub authentication is unavailable in this build.", true)
		return nil
	}
	m.cancelAccountAction()
	m.accessRecovery.pending = false
	if m.identityCancel != nil {
		m.identityCancel()
		m.identityCancel = nil
	}
	if m.loginCancel != nil {
		m.loginCancel()
		m.loginCancel = nil
	}
	m.identityLoading, m.loggingIn = false, false
	m.deviceCode = auth.DeviceCode{}
	if m.turnCancel != nil {
		m.turnCancel()
		m.turnCancel = nil
	}
	if m.connectCancel != nil {
		m.connectCancel()
		m.connectCancel = nil
	}
	m.cancelContextRequest()
	m.finishOperation()
	m.life.cancelEngineWork()
	m.authGeneration++
	m.engineGeneration++
	generation := m.authGeneration
	life, service, ctx := m.life, m.opts.Auth, m.life.ctx
	m.lease = nil
	m.connecting, m.turn, m.sendPending, m.canceling = false, false, false, false
	m.setAutopilot(false)
	m.needsAbort = false
	m.abortAcknowledged, m.abortBarrierSeen = false, false
	m.needsResume = m.session.ID != ""
	m.reconnectID = m.session.ID
	m.operation = operation{kind: "signing out"}
	m.overlay = nil
	requests := append([]*decision(nil), m.requests...)
	for _, e := range m.sessionBuffer {
		if e.decision != nil {
			requests = append(requests, e.decision)
		}
	}
	m.requests, m.sessionBuffer, m.behindDecision = nil, nil, nil
	return func() tea.Msg {
		var closeErr error
		for _, request := range requests {
			closeErr = errors.Join(closeErr, request.resolve(answer{cancel: true}))
		}
		mutated, err := life.mutateAccount(ctx, func() error { return service.SignOut(ctx) })
		return signedOutMsg{generation: generation, mutated: mutated, err: err, closeErr: closeErr}
	}
}

func (m *Model) signOutResult(msg signedOutMsg) tea.Cmd {
	if msg.generation != m.authGeneration {
		return nil
	}
	m.finishOperation()
	if !msg.mutated {
		m.connectionError = msg.err
		m.report("Account was not changed because the previous connection could not be drained: "+fmt.Sprint(msg.err), true)
	} else {
		m.clearAccessFailure()
		m.account = auth.Account{}
		m.mcpGeneration++
		m.mcpSaving = false
		m.setMCPRegistry(config.DefaultMCPRegistry())
		m.models = nil
		m.model = ""
		m.contextTier = ""
		m.reasoningEffort = ""
		m.authError = auth.ErrNotSignedIn
		m.signOutWarning = ""
		m.report("Signed out. Conversation history, your draft, and completed edits are kept.", false)
	}
	if msg.mutated && msg.err != nil {
		m.authError = msg.err
		m.signOutWarning = "Signed out for this run, but saved Sodapop credentials could not be removed. They may remain in the keyring: " + safeText(msg.err.Error())
		m.report(m.signOutWarning, true)
	}
	if msg.closeErr != nil {
		m.report("Engine shutdown failed during sign-out: "+msg.closeErr.Error(), true)
	}
	m.showAccount()
	return nil
}

func (m *Model) openDeviceBrowser() tea.Cmd {
	if !m.loggingIn || m.deviceCode.VerificationURI == "" {
		m.report("A GitHub device code is not available yet.", true)
		return nil
	}
	uri, open, root, generation := m.deviceCode.VerificationURI, m.openBrowser, m.loginContext, m.authGeneration
	if root == nil {
		root = m.life.ctx
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(root, 10*time.Second)
		defer cancel()
		return browserMsg{generation: generation, err: open(ctx, uri)}
	}
}

func (m *Model) copyDeviceCode() tea.Cmd {
	code := singleLine(m.deviceCode.UserCode)
	if !m.loggingIn || code == "" {
		m.report("A GitHub device code is not available yet.", true)
		return nil
	}
	m.report("Device code sent to the terminal clipboard, if OSC 52 is supported.", false)
	return tea.SetClipboard(code)
}

func openVerificationURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "github.com") ||
		u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return errors.New("refusing to open an invalid GitHub HTTPS verification URL")
	}
	var command string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "linux":
		command = "xdg-open"
	default:
		return errors.New("automatic browser opening is unsupported on this platform")
	}
	return exec.CommandContext(ctx, command, u.String()).Run()
}
