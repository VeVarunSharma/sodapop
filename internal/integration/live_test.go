package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/VeVarunSharma/sodapop/internal/runtimebundle"
)

const (
	operationTimeout = 45 * time.Second
	turnTimeout      = 2 * time.Minute
)

// This is the only test that can access credentials or start a real runtime.
func TestLiveQualification(t *testing.T) {
	options, enabled, err := qualificationOptions(os.Getenv)
	if !enabled {
		t.Skip("not live-qualified: set SODAPOP_LIVE_QUALIFY=1 deliberately; see docs/live-qualification.md")
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Minute)
	defer cancel()

	manager := auth.New(options.clientID)
	authCtx, authCancel := context.WithTimeout(ctx, operationTimeout)
	account, err := manager.Current(authCtx)
	authCancel()
	requireStep(t, "restore existing Sodapop sign-in", err)
	if account.ID == "" || account.SessionOnly {
		t.Fatal("no usable saved Sodapop credential; start sodapop and run /login with secure storage first")
	}

	f := newFixture(t)
	config := engine.Config{
		Project: f.project, Home: f.home, AccountID: account.ID,
		TokenSource: func(ctx context.Context) (string, error) {
			return manager.TokenForAccount(ctx, account.ID)
		},
	}
	first := newLiveRuntime(t, config)
	opCtx, opCancel := context.WithTimeout(ctx, operationTimeout)
	err = first.client.Start(opCtx)
	opCancel()
	requireStep(t, "start the bundled runtime", err)

	opCtx, opCancel = context.WithTimeout(ctx, operationTimeout)
	models, err := first.client.Models(opCtx)
	opCancel()
	requireStep(t, "list eligible Copilot models", err)
	model, err := selectedModel(models, options.model)
	if err != nil {
		t.Fatal(err)
	}
	opCtx, opCancel = context.WithTimeout(ctx, operationTimeout)
	session, err := first.client.NewSession(opCtx, engine.ModelSelection{ModelID: model, ContextTier: "default"})
	opCancel()
	requireStep(t, "create the controlled session", err)
	checkSession(t, session, "", f.project, model)
	requireLocal(t, first.barrier(ctx, session.ID, nil))

	trace := newTurnTrace(f, session.ID)
	requireLocal(t, first.turn(ctx, trace))
	requireLocal(t, fixtureContents(f, fixtureAfter))
	requireLocal(t, first.barrier(ctx, session.ID, nil))
	requireLocal(t, first.close())

	// A genuinely new engine/process must restore the SDK's history, not a
	// test-owned transcript or the first engine's in-memory session.
	second := newLiveRuntime(t, config)
	opCtx, opCancel = context.WithTimeout(ctx, operationTimeout)
	err = second.client.Start(opCtx)
	opCancel()
	requireStep(t, "restart the bundled runtime", err)
	opCtx, opCancel = context.WithTimeout(ctx, operationTimeout)
	sessions, err := second.client.Sessions(opCtx)
	opCancel()
	requireStep(t, "list the same account/project's sessions", err)
	if len(sessions) != 1 {
		t.Fatal("the isolated state did not list exactly the created session")
	}
	checkSession(t, sessions[0], session.ID, f.project, model)
	opCtx, opCancel = context.WithTimeout(ctx, operationTimeout)
	resumed, err := second.client.ResumeSession(opCtx, session.ID)
	opCancel()
	requireStep(t, "cold-resume the same account/project's session", err)
	checkSession(t, resumed, session.ID, f.project, model)

	seen := make(map[string]bool)
	requireLocal(t, second.barrier(ctx, session.ID, func(event engine.Event) error {
		if event.Kind != engine.EventMessage || !event.History || event.ID == "" || seen[event.ID] {
			return errors.New("resume did not produce distinct historical message source IDs")
		}
		seen[event.ID] = true
		if expected, ok := trace.history[event.ID]; ok {
			if historyRecord(event) != expected {
				return errors.New("resumed history changed a known source ID, message ID, role, tool, or content")
			}
			delete(trace.history, event.ID)
		}
		return nil
	}))
	if len(trace.history) != 0 {
		t.Fatal("resumed history is missing known live message or tool-completion source IDs")
	}
	requireLocal(t, second.close())
	requireLocal(t, fixtureContents(f, fixtureAfter))
	t.Log("Live fixture edit, explicit approval, selected model, idle abort barriers, and cold-resumed history qualified; one prompt sent.")
}

func checkSession(t *testing.T, session engine.Session, id, project, model string) {
	t.Helper()
	if session.ID == "" || (id != "" && session.ID != id) || session.Project != project || session.Model != model {
		t.Fatal("session identity, canonical project, or selected model changed")
	}
}

// Never format backend errors, events, accounts, or environment values: even an
// upstream error with a redacted token could still include an account identity.
func safeFailure(err error) string {
	switch {
	case errors.Is(err, auth.ErrNoClientID):
		return "SODAPOP_GITHUB_CLIENT_ID is required"
	case errors.Is(err, auth.ErrSecureStorageUnavailable):
		return "Sodapop secure storage is unavailable; unlock the keyring, then start sodapop and run /login with secure storage first"
	case errors.Is(err, auth.ErrNotSignedIn), errors.Is(err, auth.ErrReauthenticationRequired), errors.Is(err, engine.ErrNoToken):
		return "no usable saved Sodapop credential; start sodapop and run /login with secure storage first"
	case errors.Is(err, runtimebundle.ErrUnavailable):
		return "bundled runtime unavailable; build the existing pinned bundle before qualification"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline exceeded; no automatic retry"
	case errors.Is(err, context.Canceled):
		return "operation canceled; no automatic retry"
	default:
		return "operation failed (backend details suppressed); check the pinned runtime, existing Sodapop /login, Copilot entitlement, network, and organization policy"
	}
}

func requireStep(t *testing.T, stage string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %s", stage, safeFailure(err))
	}
}

func requireLocal(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type liveRuntime struct {
	client   *engine.Copilot
	events   <-chan engine.Event
	closed   bool
	closeErr error
}

func newLiveRuntime(t *testing.T, config engine.Config) *liveRuntime {
	t.Helper()
	client, err := engine.New(config)
	requireStep(t, "construct the isolated Sodapop engine", err)
	r := &liveRuntime{client: client, events: client.Events()}
	t.Cleanup(func() {
		if !r.closed {
			if err := r.close(); err != nil {
				t.Error(err)
			}
		}
	})
	return r
}

func (r *liveRuntime) close() error {
	if r.closed {
		return r.closeErr
	}
	r.closed = true
	done := make(chan error, 1)
	go func() { done <- r.client.Close() }()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	events := r.events
	// Close cancels unresolved callbacks itself. Drain concurrently, without
	// approving or re-resolving requests that Close may already have canceled.
	for events != nil || done != nil {
		select {
		case err := <-done:
			if err != nil {
				r.closeErr = errors.Join(r.closeErr, errors.New("owned runtime cleanup failed: "+safeFailure(err)))
			}
			done = nil
		case event, ok := <-events:
			if !ok {
				events = nil
			} else if event.Kind == engine.EventError {
				r.closeErr = errors.Join(r.closeErr, errors.New("runtime emitted an error during shutdown; backend details suppressed"))
			}
		case <-timer.C:
			r.closeErr = errors.Join(r.closeErr, errors.New("owned runtime did not close and drain within 30 seconds"))
			return r.closeErr
		}
	}
	return r.closeErr
}

func (r *liveRuntime) barrier(parent context.Context, sessionID string, history func(engine.Event) error) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	if err := r.client.Abort(ctx); err != nil {
		return errors.New("idle Abort failed: " + safeFailure(err))
	}
	for {
		select {
		case <-ctx.Done():
			return contextFailure(ctx)
		case event, ok := <-r.events:
			if !ok {
				return errors.New("event stream closed before the tagged idle barrier")
			}
			if event.SessionID != sessionID {
				return errors.New("idle barrier received an event for a different session")
			}
			switch event.Kind {
			case engine.EventIdle:
				if event.Name == "aborted" && event.ID != "" {
					return nil
				}
			case engine.EventUsage:
			case engine.EventError:
				return errors.New("runtime emitted an error while awaiting the idle barrier; backend details suppressed")
			default:
				if history == nil || event.Kind == engine.EventPermission || event.Kind == engine.EventQuestion {
					return errors.New("unexpected activity outside the single coding turn; no approval granted")
				}
				if err := history(event); err != nil {
					return err
				}
			}
		}
	}
}

type savedMessage struct {
	id, sessionID, messageID, role, toolID, name, text, arguments string
	failed                                                        bool
}

func historyRecord(event engine.Event) savedMessage {
	messageID := event.MessageID
	if event.Kind == engine.EventToolEnd {
		messageID = "tool:" + event.ToolID
	}
	return savedMessage{event.ID, event.SessionID, messageID, event.Role, event.ToolID, event.Name, event.Text, event.Arguments, event.Failed}
}

type turnTrace struct {
	policy          *fixturePolicy
	sessionID       string
	history         map[string]savedMessage
	requests        map[string]bool
	user, assistant bool
}

func newTurnTrace(f fixture, sessionID string) *turnTrace {
	return &turnTrace{
		policy: newFixturePolicy(f), sessionID: sessionID,
		history: make(map[string]savedMessage), requests: make(map[string]bool),
	}
}

func (trace *turnTrace) accept(event engine.Event) error {
	if event.SessionID != trace.sessionID || event.History {
		return errors.New("live turn received a historical or differently bound event")
	}
	switch event.Kind {
	case engine.EventPermission:
		request := event.Permission
		if request == nil || request.ID == "" || request.Respond == nil || trace.requests["permission:"+request.ID] {
			return errors.New("permission callback is missing or duplicated; no approval granted")
		}
		trace.requests["permission:"+request.ID] = true
		allow := trace.policy.allow(event)
		if err := request.Respond(allow); err != nil {
			return errors.New("permission callback could not be resolved: " + safeFailure(err))
		}
		if !allow {
			return errors.New("permission denied: not an identified, exact fixture read/edit; qualification stopped without retry")
		}
		if trace.policy.tools[event.ToolID] == editFixture {
			trace.policy.approvedEditID = event.ToolID
		}
	case engine.EventQuestion:
		question := event.Question
		if question == nil || question.ID == "" || question.Cancel == nil || trace.requests["question:"+question.ID] {
			return errors.New("question callback is missing or duplicated")
		}
		trace.requests["question:"+question.ID] = true
		if err := question.Cancel(); err != nil {
			return errors.New("question cancellation failed: " + safeFailure(err))
		}
		return errors.New("unexpected agent question canceled; qualification requires no extra user input")
	case engine.EventError:
		return errors.New("Copilot emitted a turn error; backend details suppressed, no retry")
	case engine.EventToolStart, engine.EventToolEnd:
		if err := trace.policy.observeTool(event); err != nil {
			return err
		}
	case engine.EventMessage:
		switch event.Role {
		case "user":
			if trace.user || event.Text != livePrompt {
				return errors.New("live user message did not match the single synthetic prompt")
			}
			trace.user = true
		case "assistant":
			trace.assistant = trace.assistant || strings.TrimSpace(event.Text) != ""
		default:
			return errors.New("unexpected live message role")
		}
	case engine.EventDelta, engine.EventToolOutput, engine.EventUsage:
	default:
		return errors.New("unexpected live event kind")
	}
	if event.Kind == engine.EventMessage || event.Kind == engine.EventToolEnd {
		record := historyRecord(event)
		if record.id == "" || record.messageID == "" {
			return errors.New("live message or tool completion has no stable source/message ID")
		}
		if _, duplicate := trace.history[record.id]; duplicate {
			return errors.New("live source event ID was duplicated")
		}
		trace.history[record.id] = record
	}
	return nil
}

func (r *liveRuntime) turn(parent context.Context, trace *turnTrace) error {
	return awaitTurn(parent, r.events, trace, func(ctx context.Context) error {
		return r.client.Send(ctx, engine.Message{Text: livePrompt})
	})
}

func awaitTurn(parent context.Context, events <-chan engine.Event, trace *turnTrace, send func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(parent, turnTimeout)
	defer cancel()
	sent := make(chan error, 1)
	go func() { sent <- send(ctx) }()
	idle := false
	for sent != nil || !idle {
		select {
		case <-ctx.Done():
			return contextFailure(ctx)
		case err := <-sent:
			sent = nil
			if err != nil {
				return errors.New("send failed; delivery may have occurred, no retry: " + safeFailure(err))
			}
		case event, ok := <-events:
			if !ok {
				return errors.New("event stream closed before the turn completed")
			}
			if event.Kind == engine.EventIdle {
				if idle || event.ID == "" || event.History || event.SessionID != trace.sessionID || event.Name == "aborted" ||
					!trace.user || !trace.assistant || !trace.policy.readBefore || !trace.policy.edited || !trace.policy.readAfter {
					return errors.New("idle arrived without the required live read/approved-edit/read and conversation evidence")
				}
				idle = true
			} else if idle {
				if event.Kind != engine.EventUsage || event.History || event.SessionID != trace.sessionID {
					return errors.New("unexpected activity after turn idle")
				}
			} else if err := trace.accept(event); err != nil {
				return err
			}
		}
	}
	return nil
}
