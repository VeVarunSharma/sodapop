package engine

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
)

type toolRecord struct {
	name      string
	arguments string
	eventID   string
	started   bool
	done      bool
}

type eventMapper struct {
	sessionID string
	seen      map[string]bool
	final     map[string]bool
	tools     map[string]toolRecord
	toolOrder []string
	redact    *redactor
}

func newEventMapper(sessionID string, redact *redactor) *eventMapper {
	return &eventMapper{
		sessionID: sessionID, seen: make(map[string]bool), final: make(map[string]bool),
		tools: make(map[string]toolRecord), redact: redact,
	}
}

func (m *eventMapper) mapEvent(raw copilot.SessionEvent, history bool) []Event {
	if raw.ID != "" && m.seen[raw.ID] {
		return nil
	}
	if raw.ID != "" {
		m.seen[raw.ID] = true
	}
	e := Event{ID: raw.ID, SessionID: m.sessionID, History: history}
	if raw.Data == nil || (reflect.ValueOf(raw.Data).Kind() == reflect.Pointer && reflect.ValueOf(raw.Data).IsNil()) {
		if history {
			return nil
		}
		e.Kind, e.Err, e.Failed = EventError, errors.New("received a malformed Copilot event"), true
		return []Event{e}
	}
	switch data := raw.Data.(type) {
	case *copilot.UserMessageData:
		e.Kind, e.Role, e.Text = EventMessage, "user", data.Content
		e.MessageID = stringValue(data.MessageID)
		if e.MessageID == "" {
			e.MessageID = raw.ID
		}
		if m.final["user:"+e.MessageID] {
			return nil
		}
		m.final["user:"+e.MessageID] = true
	case *copilot.AssistantMessageDeltaData:
		if history || m.final["assistant:"+data.MessageID] {
			return nil
		}
		e.Kind, e.Role, e.MessageID, e.Text = EventDelta, "assistant", data.MessageID, data.DeltaContent
	case *copilot.AssistantMessageData:
		for _, tool := range data.ToolRequests {
			if _, exists := m.tools[tool.ToolCallID]; !exists {
				m.tools[tool.ToolCallID] = toolRecord{name: tool.Name, arguments: describeJSON(tool.Arguments), eventID: raw.ID}
				m.toolOrder = append(m.toolOrder, tool.ToolCallID)
			}
		}
		e.Kind, e.Role, e.MessageID, e.Text = EventMessage, "assistant", data.MessageID, data.Content
		if e.MessageID == "" {
			e.MessageID = raw.ID
		}
		if m.final["assistant:"+e.MessageID] {
			return nil
		}
		m.final["assistant:"+e.MessageID] = true
	case *copilot.ToolExecutionStartData:
		tool, exists := m.tools[data.ToolCallID]
		if tool.done || tool.started {
			return nil
		}
		if !exists {
			m.toolOrder = append(m.toolOrder, data.ToolCallID)
		}
		tool.name, tool.arguments = data.ToolName, describeJSON(data.Arguments)
		tool.started, tool.eventID = true, raw.ID
		m.tools[data.ToolCallID] = tool
		if history {
			return nil
		}

		e.Kind, e.Role, e.ToolID, e.Name, e.Arguments = EventToolStart, "tool", data.ToolCallID, tool.name, tool.arguments
	case *copilot.ToolExecutionPartialResultData:
		if history || m.tools[data.ToolCallID].done {
			return nil
		}
		e.Kind, e.Role, e.ToolID, e.Text = EventToolOutput, "tool", data.ToolCallID, data.PartialOutput
		e.Name = m.tools[data.ToolCallID].name
	case *copilot.ToolExecutionProgressData:
		if history || m.tools[data.ToolCallID].done {
			return nil
		}
		e.Kind, e.Role, e.ToolID, e.Text = EventToolOutput, "tool", data.ToolCallID, data.ProgressMessage
		e.Name = m.tools[data.ToolCallID].name
	case *copilot.ToolExecutionCompleteData:
		tool := m.tools[data.ToolCallID]
		if tool.done {
			return nil
		}
		tool.done = true
		if tool.name == "" && data.ToolDescription != nil {
			tool.name = data.ToolDescription.Name
		}
		m.tools[data.ToolCallID] = tool
		e.Kind, e.Role, e.ToolID = EventToolEnd, "tool", data.ToolCallID
		e.Name, e.Arguments, e.Failed = tool.name, tool.arguments, !data.Success
		if data.Result != nil {
			e.Text = data.Result.Content
			if data.Result.DetailedContent != nil {
				e.Text = *data.Result.DetailedContent
			}
		}
		if data.Error != nil {
			if e.Text != "" {
				e.Text += "\n"
			}
			e.Text += data.Error.Message
		}
		if history {
			e.Kind, e.MessageID = EventMessage, "tool:"+data.ToolCallID
		}
	case *copilot.SessionIdleData:
		if history {
			return nil
		}
		e.Kind = EventIdle
		if boolValue(data.Aborted) {
			e.Text = "Canceled. Completed filesystem changes were not reverted."
		}
	case *copilot.SessionErrorData:
		if history {
			return nil
		}
		e.Kind, e.Name, e.Failed = EventError, data.ErrorType, true
		e.Text = data.Message
		if data.ErrorCode != nil {
			e.Name += "/" + *data.ErrorCode
		}
		e.Err = errors.New(m.redact.text(strings.TrimSpace(e.Name + ": " + data.Message)))
		if issue, ok := sessionAccessIssue(data); ok {
			e.Access = &issue
			e.Err = withAccessIssue(e.Err, issue)
		}
	case *copilot.AssistantUsageData:
		if history {
			return nil
		}
		e.Kind, e.Name = EventUsage, data.Model
		var parts []string
		if data.InputTokens != nil {
			parts = append(parts, fmt.Sprintf("%d input tokens", *data.InputTokens))
		}
		if data.OutputTokens != nil {
			parts = append(parts, fmt.Sprintf("%d output tokens", *data.OutputTokens))
		}
		if data.CacheReadTokens != nil {
			parts = append(parts, fmt.Sprintf("%d cache-read tokens", *data.CacheReadTokens))
		}
		e.Text = strings.Join(parts, ", ")
	case *copilot.SessionUsageInfoData:
		if history {
			return nil
		}
		e.Kind = EventUsage
		e.Text = fmt.Sprintf("%d / %d context tokens (%d messages)", data.CurrentTokens, data.TokenLimit, data.MessagesLength)
	case *copilot.SessionCompactionStartData, *copilot.SessionCompactionCompleteData:
		return nil
	default:
		// Permission and question requests are handled by the execution bridge,
		// never replayed as actions from history. Reasoning is not transcript text.
		return nil
	}
	e.Text, e.Arguments, e.Name = m.redact.text(e.Text), m.redact.text(e.Arguments), m.redact.text(e.Name)
	return []Event{e}
}

func (m *eventMapper) finishHistory() []Event {
	var events []Event
	for _, id := range m.toolOrder {
		tool := m.tools[id]
		if tool.done {
			continue
		}
		events = append(events, Event{
			Kind: EventMessage, ID: tool.eventID + ":tool:" + id, MessageID: "tool:" + id,
			SessionID: m.sessionID, ToolID: id, Name: m.redact.text(tool.name),
			Arguments: m.redact.text(tool.arguments), Role: "tool", History: true,
			Text: "Tool requested; no completion was recorded in the saved history.",
		})
		tool.done = true
		m.tools[id] = tool
	}
	return events
}
