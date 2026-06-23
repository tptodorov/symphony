package codex

import (
	"context"
	"strings"
	"testing"

	"github.com/openai/symphony/go/internal/agent"
)

func TestRunnerCompletesOnTerminalEvent(t *testing.T) {
	r := New(`printf '{"type":"turn.completed"}\n'`)
	events := make(chan agent.Event, 2)
	res := r.Run(context.Background(), agent.RunRequest{Workspace: t.TempDir(), Prompt: "hello", SessionID: "s"}, events)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.Completed {
		t.Fatal("expected completed result")
	}
	if len(events) == 0 {
		t.Fatal("expected event")
	}
}

func TestRunnerFailsWithoutTerminalEvent(t *testing.T) {
	r := New("cat")
	events := make(chan agent.Event, 2)
	res := r.Run(context.Background(), agent.RunRequest{Workspace: t.TempDir(), Prompt: "hello", SessionID: "s"}, events)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "without terminal event") {
		t.Fatalf("expected terminal event error, got %+v", res)
	}
	if len(events) == 0 {
		t.Fatal("expected event")
	}
}
