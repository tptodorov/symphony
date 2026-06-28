package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/symphony/go/internal/agent"
	"github.com/openai/symphony/go/internal/domain"
)

func TestRunnerStartsPiInWorkspaceWithoutUnsupportedCWDFlag(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "fake-pi.jsonl")
	r := New(fakePiCommand(t, logPath, "--mode rpc --no-session --approve"))

	res := r.Run(context.Background(), agent.RunRequest{
		Issue:       domain.Issue{ID: "1", Identifier: "API-1", Title: "Test"},
		Workspace:   workspace,
		Prompt:      "Do work",
		SessionID:   "s",
		TurnTimeout: time.Second,
	}, make(chan agent.Event, 16))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.Completed {
		t.Fatalf("expected completed result: %+v", res)
	}

	entries := readFakePiLog(t, logPath)
	if got := entries[0]["cwd"]; got != workspace {
		t.Fatalf("cwd = %#v", got)
	}
	args, _ := entries[0]["args"].([]any)
	for _, arg := range args {
		if arg == "--cwd" {
			t.Fatalf("unexpected --cwd arg in %#v", args)
		}
	}
	if got := entries[1]["message"]; got != "Do work" {
		t.Fatalf("prompt = %#v", got)
	}
}

func TestRunnerRunsContinuationPrompts(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "fake-pi.jsonl")
	r := New(fakePiCommand(t, logPath, "--mode rpc --no-session"))

	res := r.Run(context.Background(), agent.RunRequest{
		Issue:       domain.Issue{ID: "1"},
		Workspace:   workspace,
		Prompt:      "Do work",
		SessionID:   "s",
		MaxTurns:    2,
		TurnTimeout: time.Second,
	}, make(chan agent.Event, 16))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.Completed {
		t.Fatalf("expected completed result: %+v", res)
	}

	entries := readFakePiLog(t, logPath)
	if got := entries[1]["message"]; got != "Do work" {
		t.Fatalf("first prompt = %#v", got)
	}
	if got := entries[2]["message"]; got != "Continue working on the same issue. Re-check the tracker state and move the issue toward the workflow-defined handoff state. Do not repeat context already present in this thread." {
		t.Fatalf("continuation prompt = %#v", got)
	}
}

func TestRunnerStopsWhenPiAgentEndWillNotRetry(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "fake-pi.jsonl")
	command := fmt.Sprintf("GO_FAKE_PI_RPC=1 FAKE_PI_LOG=%s FAKE_PI_WILL_RETRY=false %s -test.run=TestFakePiRPC -- --mode rpc --no-session", shellQuote(logPath), shellQuote(os.Args[0]))
	r := New(command)

	res := r.Run(context.Background(), agent.RunRequest{
		Issue:       domain.Issue{ID: "1"},
		Workspace:   workspace,
		Prompt:      "Do work",
		SessionID:   "s",
		MaxTurns:    2,
		TurnTimeout: time.Second,
	}, make(chan agent.Event, 16))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.Completed {
		t.Fatalf("expected completed result: %+v", res)
	}

	entries := readFakePiLog(t, logPath)
	if len(entries) != 2 {
		t.Fatalf("expected process start plus one prompt, got %d entries: %#v", len(entries), entries)
	}
	if got := entries[1]["message"]; got != "Do work" {
		t.Fatalf("prompt = %#v", got)
	}
}

func TestPiTextExtractsAssistantMessagePartialText(t *testing.T) {
	msg := map[string]any{
		"type": "message_update",
		"assistantMessageEvent": map[string]any{
			"type":         "text_delta",
			"contentIndex": float64(1),
			"delta":        "done",
			"partial": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "thinking", "thinking": "internal"},
					map[string]any{"type": "text", "text": "Completed `api-22z`."},
				},
			},
		},
	}

	if got := piText(msg); got != "Completed `api-22z`." {
		t.Fatalf("piText = %q", got)
	}
}

func TestPiTextIgnoresThinkingDelta(t *testing.T) {
	msg := map[string]any{
		"type": "message_update",
		"assistantMessageEvent": map[string]any{
			"type":  "thinking_delta",
			"delta": "internal",
		},
	}

	if got := piText(msg); got != "" {
		t.Fatalf("piText = %q", got)
	}
}

func TestFakePiRPC(t *testing.T) {
	if os.Getenv("GO_FAKE_PI_RPC") != "1" {
		return
	}
	runFakePiRPC()
	os.Exit(0)
}

func fakePiCommand(t *testing.T, logPath, args string) string {
	t.Helper()
	return fmt.Sprintf("GO_FAKE_PI_RPC=1 FAKE_PI_LOG=%s %s -test.run=TestFakePiRPC -- %s", shellQuote(logPath), shellQuote(os.Args[0]), args)
}

func runFakePiRPC() {
	logPath := os.Getenv("FAKE_PI_LOG")
	logFakePi(map[string]any{"cwd": mustGetwd(), "args": os.Args}, logPath)
	s := bufio.NewScanner(os.Stdin)
	for s.Scan() {
		line := s.Text()
		var msg map[string]any
		_ = json.Unmarshal([]byte(line), &msg)
		logFakePi(msg, logPath)
		id := msg["id"]
		writeFakePi(map[string]any{"id": id, "type": "response", "command": "prompt", "success": true})
		writeFakePi(map[string]any{"type": "agent_start"})
		end := map[string]any{"type": "agent_end"}
		if raw := os.Getenv("FAKE_PI_WILL_RETRY"); raw != "" {
			end["willRetry"] = raw == "true"
		}
		writeFakePi(end)
	}
}

func writeFakePi(msg map[string]any) {
	b, _ := json.Marshal(msg)
	fmt.Println(string(b))
}

func logFakePi(msg map[string]any, path string) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(msg)
	_, _ = f.Write(append(b, '\n'))
}

func readFakePiLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return wd
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
