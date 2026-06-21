package codex

import (
	"context"
	"testing"

	"github.com/openai/symphony/go/internal/agent"
)

func TestRunner(t *testing.T) {
	r := New("cat")
	events := make(chan agent.Event, 2)
	res := r.Run(context.Background(), agent.RunRequest{Workspace: t.TempDir(), Prompt: "hello", SessionID: "s"}, events)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if len(events) == 0 {
		t.Fatal("expected event")
	}
}
