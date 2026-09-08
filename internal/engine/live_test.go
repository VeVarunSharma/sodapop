package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestPermissionEventActuallyGatesAndResolvesOnce(t *testing.T) {
	engine, client, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "Run a command"}); err != nil {
		t.Fatal(err)
	}
	request := &copilot.PermissionRequestShell{FullCommandText: "echo hello", Intention: "Test shell", ToolCallID: copilot.String("tool")}
	decision, err := client.created[0].OnPermissionRequest(request, copilot.PermissionInvocation{SessionID: session.id})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decision.(*rpc.PermissionDecisionNoResult); !ok {
		t.Fatal("legacy callback answered without preserving request identity")
	}
	raw := copilot.SessionEvent{ID: "permission-event", Data: &copilot.PermissionRequestedData{
		RequestID: "request-id", PermissionRequest: request,
	}}
	session.emit(raw)
	event := nextKind(t, engine, EventPermission)
	if event.ID != raw.ID || event.SessionID != session.id || event.ToolID != "tool" ||
		event.Permission.ID != "request-id" || event.Permission.Command != "echo hello" {
		t.Fatalf("wrong permission identity/details: %+v", event)
	}
	select {
	case <-session.decisions:
		t.Fatal("runtime gate opened before the user responded")
	default:
	}
	// The SDK can deliver duplicate notifications; a duplicate must not create
	// another dialog or another permission RPC.
	session.emit(raw)
	if err := event.Permission.Respond(true); err != nil {
		t.Fatal(err)
	}
	if err := event.Permission.Respond(true); !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("second response = %v", err)
	}
	call := receive(t, session.decisions)
	if _, ok := call.decision.(*rpc.PermissionDecisionApproveOnce); !ok || call.id != "request-id" {
		t.Fatalf("wrong actual runtime decision: %+v", call)
	}
	if err := engine.active.waitCallbacks(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case extra := <-session.decisions:
		t.Fatalf("duplicate runtime response: %+v", extra)
	default:
	}
}

func TestPermissionCancellationDeniesAndUnblocksWorkers(t *testing.T) {
	for _, closeEngine := range []bool{false, true} {
		engine, _, session := startedEngine(t)
		if err := engine.Send(t.Context(), Message{Text: "edit"}); err != nil {
			t.Fatal(err)
		}
		session.emit(copilot.SessionEvent{ID: "permission", Data: &copilot.PermissionRequestedData{
			RequestID: "write", PermissionRequest: &copilot.PermissionRequestWrite{FileName: "file.txt", Intention: "edit"},
		}})
		event := nextKind(t, engine, EventPermission)
		if closeEngine {
			if err := engine.Close(); err != nil {
				t.Fatal(err)
			}
		} else if err := engine.Abort(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := event.Permission.Respond(true); !errors.Is(err, context.Canceled) {
			t.Fatalf("late approval after cancellation: %v", err)
		}
		if err := engine.active.waitCallbacks(t.Context()); err != nil {
			t.Fatal(err)
		}
		select {
		case call := <-session.decisions:
			if _, approved := call.decision.(*rpc.PermissionDecisionApproveOnce); approved {
				t.Fatal("approved after cancellation")
			}
		default:
			// Closing the session can cancel the denial RPC too. The owned
			// runtime is stopped; an approval must never be issued.
		}
	}
}

func TestEnterpriseReadRequiresHumanDecision(t *testing.T) {
	engine, _, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "read"}); err != nil {
		t.Fatal(err)
	}
	session.emit(copilot.SessionEvent{ID: "permission", Data: &copilot.PermissionRequestedData{
		RequestID: "read", PermissionRequest: &copilot.PermissionRequestRead{
			Path: engine.cfg.Project, ManagedApprovalRequired: copilot.Bool(true),
		},
	}})
	event := nextKind(t, engine, EventPermission)
	if err := event.Permission.Respond(false); err != nil {
		t.Fatal(err)
	}
	if call := receive(t, session.decisions); call.id != "read" {
		t.Fatal("lost enterprise read request ID")
	} else if _, rejected := call.decision.(*rpc.PermissionDecisionReject); !rejected {
		t.Fatal("did not apply the human denial")
	}
}

func TestCanonicalReadAutoApprovalUsesRealRequestID(t *testing.T) {
	engine, _, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "read project"}); err != nil {
		t.Fatal(err)
	}
	session.emit(copilot.SessionEvent{ID: "permission", Data: &copilot.PermissionRequestedData{
		RequestID: "safe-read", PermissionRequest: &copilot.PermissionRequestRead{Path: engine.cfg.Project},
	}})
	call := receive(t, session.decisions)
	if call.id != "safe-read" {
		t.Fatal("lost read permission identity")
	}
	if _, allowed := call.decision.(*rpc.PermissionDecisionApproveOnce); !allowed {
		t.Fatal("safe project read was not approved once")
	}
}

func TestQuestionsGateLegacySDKCallback(t *testing.T) {
	engine, client, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "ask"}); err != nil {
		t.Fatal(err)
	}
	type result struct {
		response copilot.UserInputResponse
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, err := client.created[0].OnUserInputRequest(copilot.UserInputRequest{
			Question: "Which approach?", Choices: []string{"A", "B"}, AllowFreeform: copilot.Bool(false),
		}, copilot.UserInputInvocation{SessionID: session.id})
		done <- result{response, err}
	}()
	event := nextKind(t, engine, EventQuestion)
	if event.Question.ID == "" || event.Question.Prompt != "Which approach?" || event.Question.AllowFreeform {
		t.Fatalf("bad question: %+v", event.Question)
	}
	select {
	case <-done:
		t.Fatal("SDK callback completed before an answer")
	default:
	}
	for _, invalid := range []string{"", "other"} {
		if err := event.Question.Respond(invalid); err == nil {
			t.Fatalf("accepted invalid answer %q", invalid)
		}
	}
	if err := event.Question.Respond("B"); err != nil {
		t.Fatal(err)
	}
	answer := receive(t, done)
	if answer.err != nil || answer.response.Answer != "B" || answer.response.WasFreeform {
		t.Fatalf("wrong SDK answer: %+v", answer)
	}
	if err := event.Question.Cancel(); !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("question resolved twice: %v", err)
	}
}

func TestQuestionCancellationAndFreeform(t *testing.T) {
	for _, action := range []string{"cancel", "abort", "close", "freeform"} {
		t.Run(action, func(t *testing.T) {
			engine, client, session := startedEngine(t)
			if err := engine.Send(t.Context(), Message{Text: "ask"}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				response, err := client.created[0].OnUserInputRequest(
					copilot.UserInputRequest{Question: "Explain?", Choices: []string{"A"}},
					copilot.UserInputInvocation{SessionID: session.id},
				)
				if action == "freeform" && (response.Answer != "my answer" || !response.WasFreeform) {
					t.Error("freeform response was lost")
				}
				done <- err
			}()
			event := nextKind(t, engine, EventQuestion)
			var err error
			switch action {
			case "cancel":
				err = event.Question.Cancel()
			case "abort":
				err = engine.Abort(t.Context())
			case "close":
				err = engine.Close()
			case "freeform":
				err = event.Question.Respond("my answer")
			}
			if err != nil {
				t.Fatal(err)
			}
			result := receive(t, done)
			if action == "freeform" && result != nil {
				t.Fatal(result)
			}
			if action != "freeform" && result == nil {
				t.Fatal("canceled question returned a success-shaped SDK answer")
			}
		})
	}
}

func TestOldSessionEventsCannotReachNewConversation(t *testing.T) {
	engine, client, old := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	meta, err := engine.NewSession(t.Context(), ModelSelection{ModelID: "model-a", ContextTier: "default"})
	if err != nil {
		t.Fatal(err)
	}
	old.emit(copilot.SessionEvent{ID: "late", Data: &copilot.AssistantMessageDeltaData{MessageID: "old", DeltaContent: "must not leak"}})
	if err := engine.Send(t.Context(), Message{Text: "new"}); err != nil {
		t.Fatal(err)
	}
	current := client.session(t, meta.ID)
	current.emit(copilot.SessionEvent{ID: "new-delta", Data: &copilot.AssistantMessageDeltaData{MessageID: "new", DeltaContent: "new text"}})
	for {
		event := receive(t, engine.Events())
		if event.SessionID != meta.ID {
			t.Fatalf("cross-session delivery: %+v", event)
		}
		if event.Kind == EventDelta {
			if event.Text != "new text" {
				t.Fatalf("old text delivered: %+v", event)
			}
			break
		}
	}
}

func TestHistoryRejectsMismatchedProjectAndSession(t *testing.T) {
	engine, _, session := startedEngine(t)
	for _, raw := range []copilot.SessionEvent{
		{Data: &copilot.SessionStartData{SessionID: "foreign", Context: &copilot.WorkingDirectoryContext{Cwd: engine.cfg.Project}}},
		{Data: &copilot.SessionStartData{SessionID: session.id, Context: &copilot.WorkingDirectoryContext{Cwd: t.TempDir()}}},
		{Data: &copilot.SessionStartData{SessionID: session.id, RemoteSteerable: copilot.Bool(true)}},
	} {
		if err := engine.active.validateHistoryIdentity(raw); err == nil {
			t.Fatal("accepted foreign SDK history")
		}
	}
}

func TestLateEventsAndHooksAreSuppressedDuringAbort(t *testing.T) {
	engine, client, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "go"}); err != nil {
		t.Fatal(err)
	}
	_, _, _ = engine.active.requestAbort()
	output, err := client.created[0].Hooks.OnPreToolUse(copilot.PreToolUseHookInput{
		SessionID: session.id, ToolName: "bash", ToolArgs: map[string]any{"command": "echo hi"},
	}, copilot.HookInvocation{SessionID: session.id})
	if err != nil || output.PermissionDecision != "deny" {
		t.Fatal("hook did not deny after cancellation")
	}
	session.emit(copilot.SessionEvent{ID: "late", Data: &copilot.AssistantMessageDeltaData{MessageID: "late", DeltaContent: "late"}})
	if err := engine.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	for {
		event := receive(t, engine.Events())
		if event.Kind == EventDelta {
			t.Fatal("delivered a late canceled delta")
		}
		if event.Kind == EventIdle {
			break
		}
	}
}

func TestRespondRacingAbortNeverResolvesTwice(t *testing.T) {
	for range 20 {
		ctx, cancel := context.WithCancel(t.Context())
		pending := newPendingResponse[bool](ctx)
		var workers sync.WaitGroup
		workers.Go(func() { _ = pending.resolve(true, nil) })
		workers.Go(cancel)
		workers.Wait()
		if approved, err := pending.wait(); approved || !errors.Is(err, context.Canceled) {
			t.Fatalf("approved canceled pending decision: %v, %v", approved, err)
		}
	}
}

func TestDuplicateIdleCannotCancelTheNextTurn(t *testing.T) {
	engine, _, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "first"}); err != nil {
		t.Fatal(err)
	}
	idle := copilot.SessionEvent{ID: "first-idle", Data: &copilot.SessionIdleData{}}
	session.emit(idle)
	if err := engine.Send(t.Context(), Message{Text: "second"}); err != nil {
		t.Fatal(err)
	}
	session.emit(idle)
	if !engine.active.isBusy() {
		t.Fatal("duplicate idle canceled a new turn")
	}
	if err := engine.Send(t.Context(), Message{Text: "third"}); !errors.Is(err, ErrBusy) {
		t.Fatal("duplicate event allowed overlapping sends")
	}
}

func TestInvalidPermissionEventFailsClosedAndEmitsError(t *testing.T) {
	engine, _, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "change files"}); err != nil {
		t.Fatal(err)
	}
	session.emit(copilot.SessionEvent{
		ID:   "invalid-permission",
		Data: &copilot.PermissionRequestedData{PermissionRequest: &copilot.PermissionRequestWrite{FileName: "file.txt"}},
	})
	event := nextKind(t, engine, EventError)
	if !event.Failed || event.SessionID != session.id || event.Err == nil ||
		!strings.Contains(event.Text, "no permission request ID") {
		t.Fatalf("invalid permission was not surfaced safely: %+v", event)
	}
	if !engine.active.isBusy() {
		t.Fatal("invalid permission did not gate the session pending abort")
	}
	if err := engine.Send(t.Context(), Message{Text: "retry"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("invalid permission allowed another turn: %v", err)
	}
}

func TestPermissionCompletionRevokesUnreadResponse(t *testing.T) {
	engine, _, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "write"}); err != nil {
		t.Fatal(err)
	}
	session.emit(copilot.SessionEvent{ID: "permission", Data: &copilot.PermissionRequestedData{
		RequestID: "write-request", PermissionRequest: &copilot.PermissionRequestWrite{FileName: "file.txt"},
	}})
	event := nextKind(t, engine, EventPermission)
	session.emit(copilot.SessionEvent{ID: "permission-complete", Data: &copilot.PermissionCompletedData{
		RequestID: "write-request",
	}})
	if err := event.Permission.Respond(true); !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("completed permission remained actionable: %v", err)
	}
	if err := engine.active.waitCallbacks(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case decision := <-session.decisions:
		t.Fatalf("responded after runtime completed permission: %+v", decision)
	default:
	}
}

func TestActivePreToolHookNeverGrantsAndBalancesCallback(t *testing.T) {
	engine, client, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "inspect and edit"}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		in   copilot.PreToolUseHookInput
		want string
	}{
		{name: "project read", in: copilot.PreToolUseHookInput{
			SessionID: session.id, ToolName: "view", ToolArgs: map[string]any{"path": engine.cfg.Project},
		}},
		{name: "shell", in: copilot.PreToolUseHookInput{
			SessionID: session.id, ToolName: "bash", ToolArgs: map[string]any{"command": "echo safe"},
		}, want: "ask"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := client.created[0].Hooks.OnPreToolUse(test.in, copilot.HookInvocation{SessionID: session.id})
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if output != nil {
					t.Fatalf("safe read was changed by the pre-tool hook: %+v", output)
				}
			} else if output == nil || output.PermissionDecision != test.want {
				t.Fatalf("tool did not pass through the runtime ask gate: %+v", output)
			}
		})
	}
	if err := engine.active.waitCallbacks(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSessionErrorRequiresAbortBeforeFurtherWork(t *testing.T) {
	engine, _, session := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "work"}); err != nil {
		t.Fatal(err)
	}
	session.emit(copilot.SessionEvent{ID: "runtime-error", Data: &copilot.SessionErrorData{
		ErrorType: "query", Message: "provider failed",
	}})
	event := nextKind(t, engine, EventError)
	if event.Name != "query" || !event.Failed || event.Err == nil {
		t.Fatalf("runtime error was not emitted: %+v", event)
	}
	if err := engine.Send(t.Context(), Message{Text: "retry"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("runtime error did not require an abort: %v", err)
	}
}

func TestHistoryActivationRejectsUnsafeStagedAndResumedIdentity(t *testing.T) {
	project := t.TempDir()
	newSession := func(t *testing.T) *liveSession {
		t.Helper()
		stream := newEventStream()
		t.Cleanup(stream.close)
		meta := Session{ID: "session-a", Project: project}
		session := newLiveSession(t.Context(), meta, stream, &redactor{})
		stream.activate(session)
		return session
	}

	session := newSession(t)
	if err := session.activate([]copilot.SessionEvent{{Data: &copilot.SessionStartData{
		SessionID: "other", Context: &copilot.WorkingDirectoryContext{Cwd: project},
	}}}, true); err == nil {
		t.Fatal("activated mismatched history")
	}

	session = newSession(t)
	session.onEvent(copilot.SessionEvent{Data: &copilot.SessionStartData{SessionID: "session-a", AlreadyInUse: copilot.Bool(true)}})
	if err := session.activate(nil, false); err == nil || !strings.Contains(err.Error(), "already attached") {
		t.Fatalf("accepted an already attached session: %v", err)
	}

	session = newSession(t)
	session.onEvent(copilot.SessionEvent{Data: &copilot.SessionResumeData{
		SessionWasActive: copilot.Bool(true), Context: &copilot.WorkingDirectoryContext{Cwd: project},
	}})
	if err := session.activate(nil, true); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("adopted pending remote work: %v", err)
	}

	for _, raw := range []copilot.SessionEvent{
		{Data: &copilot.SessionResumeData{RemoteSteerable: copilot.Bool(true)}},
		{Data: &copilot.SessionResumeData{Context: &copilot.WorkingDirectoryContext{Cwd: t.TempDir()}}},
	} {
		session = newSession(t)
		if err := session.validateHistoryIdentity(raw); err == nil {
			t.Fatalf("accepted unsafe resume identity: %#v", raw.Data)
		}
	}

	session = newSession(t)
	session.onEvent(copilot.SessionEvent{Data: &copilot.PermissionRequestedData{
		RequestID: "historical", ResolvedByHook: copilot.Bool(true),
	}})
	if err := session.activate([]copilot.SessionEvent{{Data: &copilot.PermissionRequestedData{
		RequestID: "saved",
	}}}, true); err != nil {
		t.Fatal(err)
	}
	if !session.permissionIDs["historical"] || !session.permissionIDs["saved"] {
		t.Fatal("resume forgot already observed permission identities")
	}
}

func TestFinishIdleAbortRejectsActiveStateAndSynthesizesBarrier(t *testing.T) {
	stream := newEventStream()
	t.Cleanup(stream.close)
	session := newLiveSession(t.Context(), Session{ID: "session-a", Project: t.TempDir()}, stream, &redactor{})
	stream.activate(session)

	session.aborting = true
	if err := session.finishIdleAbort(); err != nil {
		t.Fatal(err)
	}
	event := receive(t, stream.output)
	if event.Kind != EventIdle || event.Name != "aborted" || event.SessionID != session.id ||
		!strings.Contains(event.Text, "filesystem changes were not reverted") {
		t.Fatalf("bad synthesized abort barrier: %+v", event)
	}

	session.running = true
	if err := session.finishIdleAbort(); err == nil {
		t.Fatal("finished abort while a turn was still active")
	}
	session.running = false
	session.closed = true
	if err := session.finishIdleAbort(); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed abort completion = %v", err)
	}
}
