package codex

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

func TestRunnerCompletesFakeAppServerTurnAndPassesProtocolConfig(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "protocol.jsonl")
	r := New(fakeCodexCommand(t, "success", logPath))
	events := make(chan agent.Event, 32)
	res := r.Run(context.Background(), agent.RunRequest{
		Issue:       domain.Issue{ID: "1", Identifier: "ABC-1", Title: "Test issue"},
		Workspace:   workspace,
		Prompt:      "Do work",
		SessionID:   "s",
		ReadTimeout: time.Second,
		TurnTimeout: time.Second,
		Policy: map[string]any{
			"approval_policy":     "never",
			"thread_sandbox":      "workspace-write",
			"turn_sandbox_policy": map[string]any{"type": "workspaceWrite", "writableRoots": []any{workspace}, "networkAccess": true},
		},
	}, events)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.Completed {
		t.Fatal("expected completed result")
	}
	if res.SessionID != "thr_1-turn_1" {
		t.Fatalf("session id = %q", res.SessionID)
	}
	if res.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", res.Usage)
	}

	messages := readProtocolLog(t, logPath)
	if got := methods(messages); strings.Join(got[:4], ",") != "initialize,initialized,thread/start,turn/start" {
		t.Fatalf("unexpected protocol methods: %v", got)
	}
	threadStart := findMethod(t, messages, "thread/start")
	if got := valueAt(threadStart, "params", "cwd"); got != workspace {
		t.Fatalf("thread cwd = %#v", got)
	}
	if got := valueAt(threadStart, "params", "approvalPolicy"); got != "never" {
		t.Fatalf("approvalPolicy = %#v", got)
	}
	if got := valueAt(threadStart, "params", "sandbox"); got != "workspace-write" {
		t.Fatalf("sandbox = %#v", got)
	}
	turnStart := findMethod(t, messages, "turn/start")
	if got := valueAt(turnStart, "params", "input", 0, "text"); got != "Do work" {
		t.Fatalf("turn input = %#v", got)
	}
	if got := valueAt(turnStart, "params", "sandboxPolicy", "networkAccess"); got != true {
		t.Fatalf("turn sandboxPolicy = %#v", got)
	}
	if !hasEvent(events, "session_started") || !hasEvent(events, "turn_completed") {
		t.Fatalf("expected session_started and turn_completed events")
	}
}

func TestRunnerWritesAgentRunLogsAndExtractsText(t *testing.T) {
	workspace := t.TempDir()
	paths := domain.RunLogPaths{
		Protocol: filepath.Join(workspace, "agent", "protocol.jsonl"),
		Stderr:   filepath.Join(workspace, "agent", "stderr.log"),
		Result:   filepath.Join(workspace, "agent", "result.json"),
	}
	r := New(fakeCodexCommand(t, "success", filepath.Join(workspace, "fake-incoming.jsonl")))
	events := make(chan agent.Event, 64)
	res := r.Run(context.Background(), agent.RunRequest{
		Issue:       domain.Issue{ID: "1", Identifier: "ABC-1", Title: "Test issue"},
		Workspace:   workspace,
		Prompt:      "Do work",
		SessionID:   "s",
		ReadTimeout: time.Second,
		TurnTimeout: time.Second,
		Logs:        paths,
	}, events)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.Logs.Protocol != paths.Protocol || res.Logs.Result != paths.Result {
		t.Fatalf("logs = %+v", res.Logs)
	}
	protocol, err := os.ReadFile(paths.Protocol)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(protocol), `"direction":"send"`) || !strings.Contains(string(protocol), `"direction":"recv"`) || !strings.Contains(string(protocol), "item/agentMessage/delta") {
		t.Fatalf("protocol log missing expected entries: %s", protocol)
	}
	result, err := os.ReadFile(paths.Result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"completed": true`) {
		t.Fatalf("result log missing completion: %s", result)
	}
	if !hasTextEvent(events, "I am checking the tests.") {
		t.Fatalf("missing agent text event")
	}
}

func TestRunnerMapsAppServerFailures(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		want     string
	}{
		{"failed", "turn_failed: model failed"},
		{"interrupted", "turn_cancelled"},
		{"read-timeout", "response_timeout: initialize"},
		{"turn-timeout", "context deadline exceeded"},
		{"user-input", "turn_input_required"},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			workspace := t.TempDir()
			r := New(fakeCodexCommand(t, tc.scenario, filepath.Join(workspace, "protocol.jsonl")))
			res := r.Run(context.Background(), agent.RunRequest{Issue: domain.Issue{ID: "1"}, Workspace: workspace, Prompt: "Do work", SessionID: "s", ReadTimeout: 100 * time.Millisecond, TurnTimeout: 150 * time.Millisecond}, make(chan agent.Event, 32))
			if res.Err == nil || !strings.Contains(res.Err.Error(), tc.want) {
				t.Fatalf("expected %q, got %+v", tc.want, res)
			}
		})
	}
}

func TestRunnerAutoApprovesAppServerApprovalRequests(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "protocol.jsonl")
	r := New(fakeCodexCommand(t, "approval-request", logPath))
	events := make(chan agent.Event, 32)
	res := r.Run(context.Background(), agent.RunRequest{Issue: domain.Issue{ID: "1"}, Workspace: workspace, Prompt: "Do work", SessionID: "s", ReadTimeout: time.Second, TurnTimeout: time.Second}, events)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	messages := readProtocolLog(t, logPath)
	for _, id := range []float64{101, 102} {
		response := findID(t, messages, id)
		if got := valueAt(response, "result", "decision"); got != "accept" {
			t.Fatalf("approval response %v = %#v", id, got)
		}
	}
	if !hasEvent(events, "approval_auto_approved") {
		t.Fatalf("expected approval_auto_approved event")
	}
}

func TestRunnerHandlesFastTurnCompletion(t *testing.T) {
	workspace := t.TempDir()
	r := New(fakeCodexCommand(t, "fast-complete", filepath.Join(workspace, "protocol.jsonl")))
	res := r.Run(context.Background(), agent.RunRequest{Issue: domain.Issue{ID: "1"}, Workspace: workspace, Prompt: "Do work", SessionID: "s", ReadTimeout: time.Second, TurnTimeout: time.Second}, make(chan agent.Event, 32))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.Completed {
		t.Fatalf("expected completed result: %+v", res)
	}
	if res.SessionID != "thr_1-turn_1" {
		t.Fatalf("session id = %q", res.SessionID)
	}
}

func TestRunnerRunsContinuationTurnsOnSameThread(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "protocol.jsonl")
	r := New(fakeCodexCommand(t, "success", logPath))
	res := r.Run(context.Background(), agent.RunRequest{
		Issue:       domain.Issue{ID: "1"},
		Workspace:   workspace,
		Prompt:      "Do work",
		SessionID:   "s",
		ReadTimeout: time.Second,
		TurnTimeout: time.Second,
		MaxTurns:    2,
	}, make(chan agent.Event, 64))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.Completed {
		t.Fatalf("expected completed result: %+v", res)
	}
	if res.SessionID != "thr_1-turn_2" {
		t.Fatalf("session id = %q", res.SessionID)
	}

	turnStarts := findMethods(readProtocolLog(t, logPath), "turn/start")
	if len(turnStarts) != 2 {
		t.Fatalf("turn/start count = %d", len(turnStarts))
	}
	if got := valueAt(turnStarts[0], "params", "threadId"); got != "thr_1" {
		t.Fatalf("first turn thread = %#v", got)
	}
	if got := valueAt(turnStarts[1], "params", "threadId"); got != "thr_1" {
		t.Fatalf("second turn thread = %#v", got)
	}
	if got := valueAt(turnStarts[0], "params", "input", 0, "text"); got != "Do work" {
		t.Fatalf("first prompt = %#v", got)
	}
	if got := valueAt(turnStarts[1], "params", "input", 0, "text"); got != continuationPrompt {
		t.Fatalf("continuation prompt = %#v", got)
	}
}

func TestRunnerAdvertisesConfiguredDynamicTools(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "protocol.jsonl")

	r := New(fakeCodexCommand(t, "success", logPath))
	res := r.Run(context.Background(), agent.RunRequest{
		Issue:               domain.Issue{ID: "1"},
		Workspace:           workspace,
		Prompt:              "Do work",
		SessionID:           "s",
		ReadTimeout:         time.Second,
		TurnTimeout:         time.Second,
		EnableBeadsCLI:      true,
		EnableLinearGraphQL: true,
	}, make(chan agent.Event, 32))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	messages := readProtocolLog(t, logPath)
	threadStart := findMethod(t, messages, "thread/start")
	if got := valueAt(threadStart, "params", "dynamicTools", 0, "name"); got != "beads_cli" {
		t.Fatalf("first dynamic tool = %#v", got)
	}
	if got := valueAt(threadStart, "params", "dynamicTools", 1, "name"); got != "linear_graphql" {
		t.Fatalf("second dynamic tool = %#v", got)
	}
}

func TestRunnerHandlesBeadsDynamicTool(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "protocol.jsonl")

	r := New(fakeCodexCommand(t, "beads-tool", logPath))
	res := r.Run(context.Background(), agent.RunRequest{
		Issue:            domain.Issue{ID: "1"},
		Workspace:        workspace,
		Prompt:           "Do work",
		SessionID:        "s",
		ReadTimeout:      time.Second,
		TurnTimeout:      time.Second,
		EnableBeadsCLI:   true,
		TrackerBDCommand: "printf",
	}, make(chan agent.Event, 32))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	messages := readProtocolLog(t, logPath)
	response := findID(t, messages, 103)
	if got := valueAt(response, "result", "success"); got != true {
		t.Fatalf("tool response success = %#v response=%#v", got, response)
	}
	if text, _ := valueAt(response, "result", "contentItems", 0, "text").(string); !strings.Contains(text, `"Stdout":"ok"`) {
		t.Fatalf("tool response text = %q", text)
	}
}

func TestFakeCodexAppServer(t *testing.T) {
	if os.Getenv("GO_FAKE_CODEX_APP_SERVER") != "1" {
		return
	}
	runFakeCodexAppServer()
	os.Exit(0)
}

func runFakeCodexAppServer() {
	scenario := os.Getenv("FAKE_CODEX_SCENARIO")
	logPath := os.Getenv("FAKE_CODEX_LOG")
	scanner := bufio.NewScanner(os.Stdin)
	approvalResponses := map[float64]bool{}
	turnCount := 0
	turnID := "turn_1"
	send := func(message map[string]any) {
		b, _ := json.Marshal(message)
		fmt.Println(string(b))
	}
	logIncoming := func(line string) {
		if logPath == "" {
			return
		}
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.WriteString(line + "\n")
			_ = f.Close()
		}
	}
	complete := func(status string, message string) {
		var err any
		if message != "" {
			err = map[string]any{"message": message}
		}
		send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thr_1", "turn": map[string]any{"id": turnID, "status": status, "error": err}}})
	}
	for scanner.Scan() {
		line := scanner.Text()
		logIncoming(line)
		var msg map[string]any
		_ = json.Unmarshal([]byte(line), &msg)
		method, _ := msg["method"].(string)
		id, _ := msg["id"].(float64)
		if method == "" {
			switch id {
			case 100, 103:
				complete("completed", "")
			case 101, 102:
				approvalResponses[id] = true
				if approvalResponses[101] && approvalResponses[102] {
					complete("completed", "")
				}
			}
			continue
		}
		switch method {
		case "initialize":
			if scenario != "read-timeout" {
				send(map[string]any{"id": id, "result": map[string]any{"userAgent": "fake"}})
			}
		case "initialized":
		case "thread/start":
			send(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "thr_1"}}})
		case "turn/start":
			turnCount++
			turnID = fmt.Sprintf("turn_%d", turnCount)
			send(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": turnID, "status": "inProgress", "items": []any{}}}})
			if scenario == "turn-timeout" {
				continue
			}
			if scenario == "fast-complete" {
				complete("completed", "")
				continue
			}
			send(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": "thr_1", "turn": map[string]any{"id": turnID, "status": "inProgress"}}})
			send(map[string]any{"method": "thread/tokenUsage/updated", "params": map[string]any{"threadId": "thr_1", "total_token_usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}, "rate_limits": map[string]any{"primary": "ok"}}})
			itemID := "msg_" + turnID
			send(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thr_1", "turnId": turnID, "item": map[string]any{"type": "agentMessage", "id": itemID, "text": "", "phase": "commentary"}}})
			send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": "thr_1", "turnId": turnID, "itemId": itemID, "delta": "I am "}})
			send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": "thr_1", "turnId": turnID, "itemId": itemID, "delta": "checking the tests."}})
			send(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thr_1", "turnId": turnID, "item": map[string]any{"type": "agentMessage", "id": itemID, "text": "I am checking the tests.", "phase": "commentary"}}})
			switch scenario {
			case "failed":
				complete("failed", "model failed")
			case "interrupted":
				complete("interrupted", "")
			case "user-input":
				send(map[string]any{"id": 99, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "thr_1", "turnId": turnID}})
			case "approval-request":
				send(map[string]any{"id": 101, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "thr_1", "turnId": turnID}})
				send(map[string]any{"id": 102, "method": "item/fileChange/requestApproval", "params": map[string]any{"threadId": "thr_1", "turnId": turnID}})
			case "beads-tool":
				send(map[string]any{"id": 103, "method": "item/tool/call", "params": map[string]any{"threadId": "thr_1", "turnId": turnID, "callId": "call_1", "tool": "beads_cli", "arguments": map[string]any{"args": []any{"ok"}}}})
			default:
				complete("completed", "")
			}
		}
	}
}

func fakeCodexCommand(t *testing.T, scenario, logPath string) string {
	t.Helper()
	return fmt.Sprintf("GO_FAKE_CODEX_APP_SERVER=1 FAKE_CODEX_SCENARIO=%s FAKE_CODEX_LOG=%s %s -test.run=TestFakeCodexAppServer --", shellQuote(scenario), shellQuote(logPath), shellQuote(os.Args[0]))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func readProtocolLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	out := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func methods(messages []map[string]any) []string {
	out := []string{}
	for _, message := range messages {
		if method, _ := message["method"].(string); method != "" {
			out = append(out, method)
		}
	}
	return out
}

func findMethod(t *testing.T, messages []map[string]any, method string) map[string]any {
	t.Helper()
	for _, message := range messages {
		if message["method"] == method {
			return message
		}
	}
	t.Fatalf("method %q not found in %#v", method, messages)
	return nil
}

func findMethods(messages []map[string]any, method string) []map[string]any {
	out := []map[string]any{}
	for _, message := range messages {
		if message["method"] == method {
			out = append(out, message)
		}
	}
	return out
}

func findID(t *testing.T, messages []map[string]any, id float64) map[string]any {
	t.Helper()
	for _, message := range messages {
		if message["id"] == id {
			return message
		}
	}
	t.Fatalf("id %v not found in %#v", id, messages)
	return nil
}

func valueAt(root any, path ...any) any {
	current := root
	for _, key := range path {
		switch typed := key.(type) {
		case string:
			m, _ := current.(map[string]any)
			current = m[typed]
		case int:
			a, _ := current.([]any)
			current = a[typed]
		}
	}
	return current
}

func hasEvent(events <-chan agent.Event, typeName string) bool {
	for {
		select {
		case event := <-events:
			if event.Type == typeName {
				return true
			}
		default:
			return false
		}
	}
}

func hasTextEvent(events <-chan agent.Event, text string) bool {
	for {
		select {
		case event := <-events:
			if event.Text == text {
				return true
			}
		default:
			return false
		}
	}
}
