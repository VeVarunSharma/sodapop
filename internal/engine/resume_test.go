package engine

import (
	"errors"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

func TestQuestionIDsRemainUniqueAcrossSameSessionResume(t *testing.T) {
	engine, client, session := startedEngine(t)
	ask := func(handler copilot.UserInputHandler) string {
		t.Helper()
		done := make(chan error, 1)
		go func() {
			_, err := handler(copilot.UserInputRequest{Question: "Continue?"}, copilot.UserInputInvocation{SessionID: session.id})
			done <- err
		}()
		event := nextKind(t, engine, EventQuestion)
		if err := event.Question.Cancel(); err != nil {
			t.Fatal(err)
		}
		if err := receive(t, done); !errors.Is(err, ErrQuestionCanceled) {
			t.Fatalf("question cancel: %v", err)
		}
		return event.Question.ID
	}
	if err := engine.Send(t.Context(), Message{Text: "first"}); err != nil {
		t.Fatal(err)
	}
	firstID := ask(client.created[0].OnUserInputRequest)
	session.emit(copilot.SessionEvent{ID: "idle", Data: &copilot.SessionIdleData{}})
	if _, err := engine.ResumeSession(t.Context(), session.id); err != nil {
		t.Fatal(err)
	}
	if err := engine.Send(t.Context(), Message{Text: "second"}); err != nil {
		t.Fatal(err)
	}
	secondID := ask(client.resumed[0].OnUserInputRequest)
	if firstID == secondID {
		t.Fatal("resumed question reused an ID from an already-resolved UI dialog")
	}
}

func TestUnexpectedBootstrapActionFailsClosed(t *testing.T) {
	client := newFakeClient()
	client.createEvent = &copilot.SessionEvent{ID: "unexpected", Data: &copilot.PermissionRequestedData{
		RequestID: "bootstrap-permission", PermissionRequest: &copilot.PermissionRequestShell{FullCommandText: "echo unexpected"},
	}}
	engine := testEngine(t, testConfig(t), client, nil)
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.NewSession(t.Context(), ModelSelection{ModelID: "model-a", ContextTier: "default"}); err == nil {
		t.Fatal("ignored an executable request before session safeguards were installed")
	}
	if records, err := engine.Sessions(t.Context()); err != nil || len(records) != 0 {
		t.Fatalf("indexed an unsafe bootstrap: %+v, %v", records, err)
	}
}
