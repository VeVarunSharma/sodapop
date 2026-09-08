package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

var attachmentSequence atomic.Uint64

type liveSession struct {
	mu            sync.Mutex
	id            string
	project       string
	attachment    uint64
	errorSequence atomic.Uint64
	meta          Session
	runtime       runtimeSession
	ctx           context.Context
	cancel        context.CancelFunc
	turnCtx       context.Context
	cancelTurn    context.CancelFunc
	idle          chan struct{}
	abortIdle     Event
	callbackIdle  chan struct{}
	callbackCount int
	ready         bool
	resuming      bool
	bootstrapErr  error
	closed        bool
	running       bool
	aborting      bool
	needsAbort    bool
	token         string
	staged        []copilot.SessionEvent
	mapper        *eventMapper
	stream        *eventStream
	redact        *redactor
	permissionIDs map[string]bool
	pending       map[string]cancelableRequest
	questionID    uint64
}

func newLiveSession(ctx context.Context, meta Session, stream *eventStream, redact *redactor) *liveSession {
	ctx, cancel := context.WithCancel(ctx)
	idle := make(chan struct{})
	close(idle)
	return &liveSession{
		id: meta.ID, project: meta.Project, attachment: attachmentSequence.Add(1),
		meta: meta, ctx: ctx, cancel: cancel, idle: idle, callbackIdle: idle, stream: stream, redact: redact,
		mapper: newEventMapper(meta.ID, redact), permissionIDs: make(map[string]bool),
		pending: make(map[string]cancelableRequest),
	}
}

func (s *liveSession) snapshot() Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta
}

func (s *liveSession) isBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running || s.aborting || s.needsAbort
}

func (s *liveSession) setRuntime(runtime runtimeSession, token string) {
	s.mu.Lock()
	s.runtime, s.token = runtime, token
	s.mu.Unlock()
}

func (s *liveSession) activate(history []copilot.SessionEvent, resumed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return errors.Join(context.Canceled, s.bootstrapErr)
	}
	var events []Event
	for _, raw := range history {
		if err := s.validateHistoryIdentity(raw); err != nil {
			return err
		}
		if request, ok := raw.Data.(*copilot.PermissionRequestedData); ok && request != nil {
			s.permissionIDs[request.RequestID] = true
		}
		events = append(events, s.mapper.mapEvent(raw, true)...)
	}
	for _, raw := range s.staged {
		switch data := raw.Data.(type) {
		case *copilot.SessionStartData:
			if data != nil && boolValue(data.AlreadyInUse) {
				return errors.New("Copilot session is already attached to another client")
			}
		case *copilot.SessionResumeData:
			if data != nil && (boolValue(data.SessionWasActive) || boolValue(data.ContinuePendingWork)) {
				return errors.New("Copilot session is already active in another client; no pending work was adopted")
			}
		}
		if err := s.validateHistoryIdentity(raw); err != nil {
			return err
		}
		if resumed {
			if request, ok := raw.Data.(*copilot.PermissionRequestedData); ok && request != nil {
				s.permissionIDs[request.RequestID] = true
			}
		}
		events = append(events, s.mapper.mapEvent(raw, resumed)...)
	}
	s.staged = nil
	if resumed {
		events = append(events, s.mapper.finishHistory()...)
	}
	// Publish history before exposing ready=true to the SDK event consumer.
	s.stream.push(s, events...)
	s.ready = true
	return nil
}

func (s *liveSession) validateHistoryIdentity(raw copilot.SessionEvent) error {
	var context *copilot.WorkingDirectoryContext
	switch data := raw.Data.(type) {
	case *copilot.SessionStartData:
		if data != nil {
			if data.SessionID != s.id || boolValue(data.RemoteSteerable) {
				return errors.New("Copilot history has a mismatched or remote session identity")
			}
			context = data.Context
		}
	case *copilot.SessionResumeData:
		if data != nil {
			if boolValue(data.RemoteSteerable) {
				return errors.New("Copilot history contains a remote session")
			}
			context = data.Context
		}
	}
	if context != nil {
		project, err := canonicalDirectory(context.Cwd)
		if err != nil || project != s.project {
			return errors.New("Copilot history contains a different or missing project")
		}
	}
	return nil
}

func (s *liveSession) startTurn() (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return nil, ErrClosed
	}
	if !s.ready {
		return nil, errors.New("Copilot session is not ready")
	}
	if s.running || s.aborting || s.needsAbort {
		return nil, ErrBusy
	}
	s.running = true
	s.turnCtx, s.cancelTurn = context.WithCancel(s.ctx)
	s.idle = make(chan struct{})
	return s.turnCtx, nil
}

func (s *liveSession) finishTurnLocked() {
	if s.cancelTurn != nil {
		s.cancelTurn()
	}
	for _, pending := range s.pending {
		pending.cancel(context.Canceled)
	}
	if s.running {
		close(s.idle)
	}
	s.running, s.needsAbort = false, false
}

func (s *liveSession) sendFailed(submitted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !submitted {
		s.finishTurnLocked()
		return
	}
	// A failed RPC does not establish whether the runtime accepted the prompt.
	// Keep the session gated until an explicit Abort or authoritative idle.
	if s.running {
		s.needsAbort = true
		if s.cancelTurn != nil {
			s.cancelTurn()
		}
	}
}

func (s *liveSession) requestAbort() (runtimeSession, <-chan struct{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aborting = true
	if s.cancelTurn != nil {
		s.cancelTurn()
	}
	for _, pending := range s.pending {
		pending.cancel(context.Canceled)
	}
	return s.runtime, s.idle, s.running
}

func (s *liveSession) finishIdleAbort() error {
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return context.Canceled
	}
	if s.running || s.callbackCount != 0 {
		s.mu.Unlock()
		return errors.New("Copilot cancellation has not quiesced the turn and its callbacks")
	}
	idle := s.abortIdle
	if idle.Kind == "" {
		idle = Event{
			Kind: EventIdle, ID: fmt.Sprintf("%s:attachment:%d:idle:%d", s.id, s.attachment, s.errorSequence.Add(1)),
			SessionID: s.id, Text: "Canceled. Completed filesystem changes were not reverted.",
		}
	}
	idle.Name = "aborted"
	s.abortIdle = Event{}
	s.aborting, s.needsAbort = false, false
	s.mu.Unlock()
	s.stream.push(s, idle)
	return nil
}

func (s *liveSession) stop() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.finishTurnLocked()
	drained := s.callbackIdle
	s.mu.Unlock()
	<-drained
}

func (s *liveSession) onPreToolUse(input copilot.PreToolUseHookInput, invocation copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
	s.mu.Lock()
	allowed := s.ready && s.running && !s.aborting && !s.needsAbort && !s.closed &&
		s.turnCtx.Err() == nil && invocation.SessionID == s.id &&
		(input.SessionID == "" || input.SessionID == s.id)
	project := s.project
	turn := s.turnCtx
	if allowed {
		s.beginCallbackLocked("", nil)
	}
	s.mu.Unlock()
	if !allowed {
		return &copilot.PreToolUseHookOutput{
			PermissionDecision: "deny", PermissionDecisionReason: "The Sodapop turn is not active or was canceled.",
		}, nil
	}
	defer s.workerDone("")
	decision := preToolDecision(project, input)
	if turn.Err() != nil {
		return &copilot.PreToolUseHookOutput{
			PermissionDecision: "deny", PermissionDecisionReason: "The Sodapop turn was canceled.",
		}, nil
	}
	return decision, nil
}

func (s *liveSession) onEvent(raw copilot.SessionEvent) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if !s.ready {
		s.staged = append(s.staged, raw)
		if request, ok := raw.Data.(*copilot.PermissionRequestedData); ok && request != nil &&
			!boolValue(request.ResolvedByHook) && (!s.resuming || boolValue(raw.Ephemeral)) {
			s.bootstrapErr = errors.New("runtime requested an action before Sodapop finished installing the session's permission boundaries")
			s.cancel()
		}
		s.mu.Unlock()
		return
	}
	if raw.ID != "" && s.mapper.seen[raw.ID] {
		s.mu.Unlock()
		return
	}
	switch data := raw.Data.(type) {
	case *copilot.PermissionRequestedData:
		if raw.ID != "" {
			s.mapper.seen[raw.ID] = true
		}
		if data == nil || data.RequestID == "" {
			if s.running {
				s.needsAbort = true
				s.cancelTurn()
			}
			s.mu.Unlock()
			s.emitError("handle permission", errors.New("runtime supplied no permission request ID; no action was approved"))
			return
		}
		if boolValue(data.ResolvedByHook) || s.permissionIDs[data.RequestID] {
			s.mu.Unlock()
			return
		}
		s.permissionIDs[data.RequestID] = true
		if !s.running || s.aborting || s.needsAbort {
			s.mu.Unlock()
			return
		}
		pending := newPendingResponse[bool](s.turnCtx)
		s.beginCallbackLocked(data.RequestID, pending)
		runtime, project := s.runtime, s.project
		s.mu.Unlock()
		go s.runPermission(raw.ID, data.RequestID, data.PermissionRequest, project, runtime, pending)
		return
	case *copilot.PermissionCompletedData:
		if raw.ID != "" {
			s.mapper.seen[raw.ID] = true
		}
		if data != nil {
			if pending := s.pending[data.RequestID]; pending != nil {
				pending.cancel(ErrAlreadyResolved)
			}
		}
		s.mu.Unlock()
		return
	case *copilot.SessionIdleData:
		if data != nil && s.aborting && !boolValue(data.Aborted) {
			copy := *data
			copy.Aborted = copilot.Bool(true)
			raw.Data = &copy
		}
		s.finishTurnLocked()
		if s.aborting {
			events := s.mapper.mapEvent(raw, false)
			if len(events) != 0 {
				s.abortIdle = events[0]
			}
			s.mu.Unlock()
			return
		}
	case *copilot.SessionErrorData:
		if s.running {
			s.needsAbort = true
			s.cancelTurn()
		}
	default:
		if !s.running || s.aborting || s.needsAbort {
			s.mu.Unlock()
			return
		}
	}
	events := s.mapper.mapEvent(raw, false)
	s.mu.Unlock()
	s.stream.push(s, events...)
}

func (s *liveSession) workerDone(id string) {
	s.mu.Lock()
	if id != "" {
		delete(s.pending, id)
	}
	s.callbackCount--
	if s.callbackCount == 0 {
		close(s.callbackIdle)
	}
	s.mu.Unlock()
}

func (s *liveSession) runPermission(eventID, id string, request copilot.PermissionRequest, project string, runtime runtimeSession, pending *pendingResponse[bool]) {
	defer s.workerDone(id)
	permission, toolID, auto := permissionDetails(project, request)
	permission.ID = id
	permission.Path, permission.Command, permission.URL = s.redact.text(permission.Path), s.redact.text(permission.Command), s.redact.text(permission.URL)
	permission.Description = s.redact.text(permission.Description)
	permission.Respond = func(allow bool) error { return pending.resolve(allow, nil) }
	if auto {
		_ = pending.resolve(true, nil)
	} else {
		pending.mu.Lock()
		open := !pending.resolved && pending.ctx.Err() == nil
		pending.mu.Unlock()
		if open {
			s.stream.push(s, Event{Kind: EventPermission, ID: eventID, SessionID: s.id, ToolID: toolID, Permission: &permission})
		}
	}
	allow, responseErr := pending.wait()
	if errors.Is(responseErr, ErrAlreadyResolved) {
		return
	}
	parent := pending.ctx
	var decision rpc.PermissionDecision = &rpc.PermissionDecisionReject{}
	if responseErr == nil && allow && pending.ctx.Err() == nil {
		decision = &rpc.PermissionDecisionApproveOnce{}
	} else if responseErr != nil {
		// Cancellation may still need to release the runtime waiter. Only a
		// denial may use the session lifetime after the turn was canceled.
		parent = s.ctx
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if ctx.Err() != nil || runtime == nil {
		return
	}
	capability := rpc.PermissionResponseCapabilityInteractive
	attribution := &rpc.PermissionDecisionContext{
		Outcome: rpc.PermissionDecisionOutcomePromptedUser, Source: rpc.PermissionDecisionSourceHumanResponse,
		Surface: rpc.PermissionDecisionSurfaceSDK, ResponseCapability: &capability,
	}
	if auto {
		attribution.Outcome, attribution.Source = rpc.PermissionDecisionOutcomeAutoApproved, rpc.PermissionDecisionSourceHostPolicy
	}
	if responseErr != nil {
		attribution = nil
	}
	if err := runtime.DecidePermission(ctx, id, decision, attribution); err != nil &&
		responseErr == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrAlreadyResolved) {
		s.emitError("deliver permission decision", err)
	}
}

func (s *liveSession) onQuestion(request copilot.UserInputRequest, invocation copilot.UserInputInvocation) (copilot.UserInputResponse, error) {
	if strings.TrimSpace(request.Question) == "" || (request.AllowFreeform != nil && !*request.AllowFreeform && len(request.Choices) == 0) {
		return copilot.UserInputResponse{}, errors.New("runtime supplied an empty or unanswerable question")
	}
	s.mu.Lock()
	if s.closed || !s.ready || !s.running || s.aborting || s.needsAbort || invocation.SessionID != s.id {
		s.mu.Unlock()
		return copilot.UserInputResponse{}, context.Canceled
	}
	s.questionID++
	id := fmt.Sprintf("%s:attachment:%d:question:%d", s.id, s.attachment, s.questionID)
	pending := newPendingResponse[copilot.UserInputResponse](s.turnCtx)
	s.beginCallbackLocked(id, pending)
	s.mu.Unlock()
	defer s.workerDone(id)
	choices := slices.Clone(request.Choices)
	freeform := request.AllowFreeform == nil || *request.AllowFreeform
	question := &Question{
		ID: id, Prompt: s.redact.text(request.Question), Choices: slices.Clone(choices), AllowFreeform: freeform,
	}
	question.Respond = func(answer string) error {
		if strings.TrimSpace(answer) == "" {
			return errors.New("answer must not be empty")
		}
		isChoice := slices.Contains(choices, answer)
		if !freeform && !isChoice {
			return errors.New("choose one of the offered answers")
		}
		return pending.resolve(copilot.UserInputResponse{Answer: answer, WasFreeform: !isChoice}, nil)
	}
	question.Cancel = func() error { return pending.resolve(copilot.UserInputResponse{}, ErrQuestionCanceled) }
	s.stream.push(s, Event{Kind: EventQuestion, ID: id, SessionID: s.id, Question: question})
	return pending.wait()
}

func (s *liveSession) emitError(operation string, err error) {
	err = s.redact.err(operation, err)
	id := fmt.Sprintf("%s:attachment:%d:error:%d", s.id, s.attachment, s.errorSequence.Add(1))
	s.stream.push(s, Event{Kind: EventError, ID: id, SessionID: s.id, Failed: true, Text: err.Error(), Err: err})
}
