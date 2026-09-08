package engine

import (
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

func TestStreamingFinalReconciliation(t *testing.T) {
	mapper := newEventMapper("session", &redactor{})
	delta := copilot.SessionEvent{ID: "event-delta", Data: &copilot.AssistantMessageDeltaData{MessageID: "message", DeltaContent: "Hello"}}
	events := mapper.mapEvent(delta, false)
	if len(events) != 1 || events[0].Kind != EventDelta || events[0].MessageID != "message" ||
		events[0].ID != "event-delta" || events[0].SessionID != "session" {
		t.Fatalf("bad delta mapping: %+v", events)
	}
	if len(mapper.mapEvent(delta, false)) != 0 {
		t.Fatal("replayed duplicate delta")
	}
	final := copilot.SessionEvent{ID: "event-final", Data: &copilot.AssistantMessageData{MessageID: "message", Content: "Hello world"}}
	events = mapper.mapEvent(final, false)
	if len(events) != 1 || events[0].Text != "Hello world" || events[0].MessageID != "message" || events[0].Kind != EventMessage {
		t.Fatalf("bad final mapping: %+v", events)
	}
	final.ID = "duplicate-logical-message"
	if len(mapper.mapEvent(final, false)) != 0 {
		t.Fatal("duplicated finalized message under a different event ID")
	}
	delta.ID = "late-delta"
	if len(mapper.mapEvent(delta, false)) != 0 {
		t.Fatal("applied delta after final")
	}
}

func TestTypedToolEventMapping(t *testing.T) {
	mapper := newEventMapper("session", &redactor{})
	start := copilot.SessionEvent{ID: "start", Data: &copilot.ToolExecutionStartData{
		ToolCallID: "tool", ToolName: "bash", Arguments: map[string]any{"command": "echo hi"},
	}}
	events := mapper.mapEvent(start, false)
	if len(events) != 1 || events[0].ToolID != "tool" || events[0].Name != "bash" || !strings.Contains(events[0].Arguments, "echo hi") {
		t.Fatalf("bad tool start: %+v", events)
	}
	start.ID = "duplicate-start"
	if len(mapper.mapEvent(start, false)) != 0 {
		t.Fatal("duplicated tool start")
	}
	partial := copilot.SessionEvent{ID: "partial", Data: &copilot.ToolExecutionPartialResultData{ToolCallID: "tool", PartialOutput: "hi"}}
	if events = mapper.mapEvent(partial, false); len(events) != 1 || events[0].Kind != EventToolOutput || events[0].Text != "hi" {
		t.Fatalf("bad tool partial: %+v", events)
	}
	detail := "complete, detailed output"
	finish := copilot.SessionEvent{ID: "finish", Data: &copilot.ToolExecutionCompleteData{
		ToolCallID: "tool", Success: false, Result: &copilot.ToolExecutionCompleteResult{Content: "brief", DetailedContent: &detail},
		Error: &copilot.ToolExecutionCompleteError{Message: "command failed"},
	}}
	events = mapper.mapEvent(finish, false)
	if len(events) != 1 || events[0].Kind != EventToolEnd || events[0].Name != "bash" || !events[0].Failed ||
		events[0].Text != detail+"\ncommand failed" {
		t.Fatalf("bad tool completion: %+v", events)
	}
	partial.ID = "late-partial"
	if len(mapper.mapEvent(partial, false)) != 0 {
		t.Fatal("applied output after tool completion")
	}
}

func TestHistoryContainsOnlyFinalRecords(t *testing.T) {
	mapper := newEventMapper("session", &redactor{})
	userID := "user-id"
	var history []Event
	for _, raw := range []copilot.SessionEvent{
		{ID: "user", Data: &copilot.UserMessageData{Content: "request", MessageID: &userID}},
		{ID: "delta", Data: &copilot.AssistantMessageDeltaData{MessageID: "assistant", DeltaContent: "answer"}},
		{ID: "assistant", Data: &copilot.AssistantMessageData{MessageID: "assistant", Content: "answer"}},
		{ID: "start", Data: &copilot.ToolExecutionStartData{ToolCallID: "tool", ToolName: "view", Arguments: map[string]any{"path": "file"}}},
		{ID: "partial", Data: &copilot.ToolExecutionPartialResultData{ToolCallID: "tool", PartialOutput: "chunk"}},
		{ID: "permission", Data: &copilot.PermissionRequestedData{RequestID: "permission"}},
		{ID: "end", Data: &copilot.ToolExecutionCompleteData{ToolCallID: "tool", Success: true, Result: &copilot.ToolExecutionCompleteResult{Content: "file contents"}}},
		{ID: "idle", Data: &copilot.SessionIdleData{}},
		{ID: "error", Data: &copilot.SessionErrorData{Message: "old error"}},
	} {
		history = append(history, mapper.mapEvent(raw, true)...)
	}
	if len(history) != 3 {
		t.Fatalf("history contains %d records: %+v", len(history), history)
	}
	for i, role := range []string{"user", "assistant", "tool"} {
		if history[i].Kind != EventMessage || !history[i].History || history[i].Role != role || history[i].SessionID != "session" {
			t.Fatalf("unexpected history record: %+v", history[i])
		}
	}
	if history[0].MessageID != userID || history[2].ToolID != "tool" || history[2].Name != "view" {
		t.Fatal("lost history identity or tool metadata")
	}
	if len(mapper.finishHistory()) != 0 {
		t.Fatal("completed tools appeared as interrupted")
	}
}

func TestUnfinishedHistoryToolIsNotAnAction(t *testing.T) {
	mapper := newEventMapper("session", &redactor{})
	mapper.mapEvent(copilot.SessionEvent{ID: "start", Data: &copilot.ToolExecutionStartData{ToolCallID: "unfinished", ToolName: "edit"}}, true)
	events := mapper.finishHistory()
	if len(events) != 1 || events[0].Kind != EventMessage || !events[0].History || events[0].Role != "tool" {
		t.Fatalf("bad unfinished history: %+v", events)
	}
	if len(mapper.finishHistory()) != 0 {
		t.Fatal("unfinished history record was duplicated")
	}
}

func TestUsageIdleErrorsAndRedaction(t *testing.T) {
	redact := &redactor{}
	redact.remember("private-test-token")
	mapper := newEventMapper("session", redact)
	input, output := int64(100), int64(25)
	usage := mapper.mapEvent(copilot.SessionEvent{ID: "usage", Data: &copilot.AssistantUsageData{Model: "model-a", InputTokens: &input, OutputTokens: &output}}, false)
	if len(usage) != 1 || usage[0].Kind != EventUsage || !strings.Contains(usage[0].Text, "100 input") || usage[0].Name != "model-a" {
		t.Fatalf("bad usage: %+v", usage)
	}

	idle := mapper.mapEvent(copilot.SessionEvent{ID: "idle", Data: &copilot.SessionIdleData{Aborted: copilot.Bool(true)}}, false)
	if len(idle) != 1 || idle[0].Kind != EventIdle || !strings.Contains(idle[0].Text, "not reverted") {
		t.Fatalf("bad canceled idle: %+v", idle)
	}
	failure := mapper.mapEvent(copilot.SessionEvent{ID: "error", Data: &copilot.SessionErrorData{
		ErrorType: "authentication", Message: "private-test-token ghp_fakecredential Bearer other-token",
	}}, false)
	if len(failure) != 1 || failure[0].Err == nil || !failure[0].Failed {
		t.Fatalf("bad error: %+v", failure)
	}
	for _, secret := range []string{"private-test-token", "ghp_fakecredential", "other-token"} {
		if strings.Contains(failure[0].Text+failure[0].Err.Error(), secret) {
			t.Fatalf("error leaked %s", secret)
		}
	}
}

func TestCompactionLifecycleEventsStayOutOfTranscript(t *testing.T) {
	mapper := newEventMapper("session", &redactor{})
	for _, event := range []copilot.SessionEvent{
		{ID: "compact-start", Data: &copilot.SessionCompactionStartData{}},
		{ID: "compact-complete", Data: &copilot.SessionCompactionCompleteData{Success: true}},
	} {
		if mapped := mapper.mapEvent(event, false); len(mapped) != 0 {
			t.Fatalf("compaction event became transcript content: %+v", mapped)
		}
	}
}
