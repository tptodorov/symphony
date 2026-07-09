package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tptodorov/symphony/go/internal/agent"
	"github.com/tptodorov/symphony/go/internal/domain"
	"github.com/tptodorov/symphony/go/internal/tools"
)

const continuationPrompt = "Continue working on the same issue. Re-check the tracker state and move the issue toward the workflow-defined handoff state. Do not repeat context already present in this thread."

type Runner struct{ Command string }

func New(command string) *Runner { return &Runner{Command: command} }

func (r *Runner) Run(ctx context.Context, req agent.RunRequest, events chan<- agent.Event) agent.Result {
	command := req.Command
	if command == "" {
		command = r.Command
	}
	if command == "" {
		command = "pi --mode rpc --no-session"
	}
	if req.Workspace == "" {
		return agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("workspace path is required")}
	}
	runLogs, err := agent.OpenRunLogs(req.Logs)
	if err != nil {
		return agent.Result{SessionID: req.SessionID, Logs: req.Logs, Err: fmt.Errorf("open agent logs: %w", err)}
	}
	defer runLogs.Close()
	finish := func(res agent.Result) agent.Result {
		if res.Logs.Protocol == "" && res.Logs.Stderr == "" && res.Logs.Result == "" {
			res.Logs = req.Logs
		}
		runLogs.WriteResult(res)
		return res
	}
	if req.TurnTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.TurnTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = req.Workspace
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return finish(agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("open pi stdin: %w", err)})
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return finish(agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("open pi stdout: %w", err)})
	}
	var stderr bytes.Buffer
	cmd.Stderr = runLogs.StderrWriter(&stderr)
	if err := cmd.Start(); err != nil {
		return finish(agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("start pi: %w", err)})
	}
	sessionID := derivePISessionID(req.SessionID, cmd)
	if err := writeLoggedJSON(stdin, runLogs, map[string]any{"id": req.SessionID, "type": "prompt", "message": req.Prompt}); err != nil {
		_ = cmd.Process.Kill()
		return finish(agent.Result{SessionID: sessionID, Err: fmt.Errorf("send pi prompt: %w", err)})
	}

	maxTurns := req.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 1
	}
	completed, err := r.read(stdout, events, req, stdin, sessionID, runLogs, maxTurns)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return finish(agent.Result{SessionID: sessionID, Err: ctx.Err()})
	}
	if err != nil {
		return finish(agent.Result{SessionID: sessionID, Err: err})
	}
	if waitErr != nil {
		return finish(agent.Result{SessionID: sessionID, Err: fmt.Errorf("pi exited: %w: %s", waitErr, stderr.String())})
	}
	if !completed {
		return finish(agent.Result{SessionID: sessionID, Err: fmt.Errorf("subprocess_exit: pi exited before agent_end")})
	}
	return finish(agent.Result{SessionID: sessionID, Completed: completed})
}

func derivePISessionID(reqSessionID string, cmd *exec.Cmd) string {
	if cmd != nil && cmd.Process != nil && cmd.Process.Pid > 0 {
		return "pi-" + strconv.Itoa(cmd.Process.Pid)
	}
	return reqSessionID
}

func (r *Runner) read(stdout io.Reader, events chan<- agent.Event, req agent.RunRequest, stdin io.WriteCloser, sessionID string, runLogs *agent.RunLogs, maxTurns int) (bool, error) {
	s := bufio.NewScanner(stdout)
	s.Buffer(make([]byte, 64*1024), 10*1024*1024)
	completed := false
	accepted := false
	promptID := req.SessionID
	turnsCompleted := 0
	for s.Scan() {
		line := s.Text()
		runLogs.WriteProtocol("recv", []byte(line))
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			events <- agent.Event{SessionID: sessionID, IssueID: req.Issue.ID, Type: "pi_output", Message: line, Text: piText(map[string]any{"message": line}), At: time.Now()}
			continue
		}
		typeName, _ := msg["type"].(string)
		if typeName == "response" && msg["id"] == promptID {
			if ok, _ := msg["success"].(bool); !ok {
				return false, fmt.Errorf("pi prompt rejected: %v", msg["error"])
			}
			accepted = true
		}
		if typeName == "extension_ui_request" {
			_ = respondExtensionUI(stdin, runLogs, msg, req.Policy)
		}
		if typeName == "tool_request" {
			_ = handleToolRequest(stdin, runLogs, msg, req)
		}
		if typeName == "agent_end" {
			turnsCompleted++
			completed = true
			if !shouldContinueAfterAgentEnd(msg, turnsCompleted, maxTurns) {
				_ = stdin.Close()
			} else {
				promptID = fmt.Sprintf("%s-turn-%d", req.SessionID, turnsCompleted+1)
				accepted = false
				completed = false
				if err := writeLoggedJSON(stdin, runLogs, map[string]any{"id": promptID, "type": "prompt", "message": continuationPrompt}); err != nil {
					return false, fmt.Errorf("send pi continuation prompt: %w", err)
				}
			}
		}
		events <- agent.Event{SessionID: sessionID, IssueID: req.Issue.ID, Type: typeName, Message: line, Text: piText(msg), Usage: extractPIUsage(msg), RateLimits: extractPIRateLimits(msg), At: time.Now()}
	}
	if err := s.Err(); err != nil {
		return completed, fmt.Errorf("read pi output: %w", err)
	}
	if !accepted {
		return completed, fmt.Errorf("pi prompt was not accepted")
	}
	return completed, nil
}

func handleToolRequest(w io.Writer, logs *agent.RunLogs, msg map[string]any, req agent.RunRequest) error {
	if req.Policy != nil && isStrictPolicy(req.Policy) {
		return writeLoggedJSON(w, logs, map[string]any{"type": "tool_result", "id": msg["id"], "success": false, "error": "tool calls disabled by policy"})
	}
	id, _ := msg["id"].(string)
	method, _ := msg["method"].(string)
	if id == "" || method == "" {
		return nil
	}
	var result tools.ToolResult
	switch method {
	case "beads_cli":
		args := parseStringArray(msg["args"])
		result = tools.ExecuteBeadsCLI(context.Background(), firstNonEmpty(req.TrackerWorkDir, req.Workspace), req.TrackerBDCommand, args)
	case "linear_graphql":
		query, _ := msg["query"].(string)
		vars, _ := msg["variables"].(map[string]any)
		result = tools.ExecuteLinearGraphQL(context.Background(), req.TrackerEndpoint, req.TrackerAPIKey, query, vars)
	case "jira_rest":
		method, _ := msg["method"].(string)
		path, _ := msg["path"].(string)
		query, _ := msg["query"].(map[string]any)
		body := msg["body"]
		result = tools.ExecuteJiraREST(context.Background(), req.TrackerEndpoint, req.TrackerEmail, req.TrackerAPIKey, method, path, query, body)
	default:
		result = tools.ToolResult{Success: false, Error: "unknown tool: " + method}
	}
	resp := map[string]any{"type": "tool_result", "id": id, "success": result.Success}
	if !result.Success {
		resp["error"] = result.Error
	} else {
		resp["result"] = result.ParsedJSON
	}
	return writeLoggedJSON(w, logs, resp)
}

func shouldContinueAfterAgentEnd(msg map[string]any, turnsCompleted, maxTurns int) bool {
	if turnsCompleted >= maxTurns {
		return false
	}
	if willRetry, ok := msg["willRetry"].(bool); ok {
		return willRetry
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseStringArray(v any) []string {
	var out []string
	if a, ok := v.([]any); ok {
		for _, s := range a {
			if str, ok := s.(string); ok {
				out = append(out, str)
			}
		}
	}
	return out
}

func asInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		if n, err := strconv.Atoi(x); err == nil {
			return n
		}
	}
	return 0
}

func extractPIUsage(msg map[string]any) domain.TokenUsage {
	if usage, ok := msg["usage"].(map[string]any); ok {
		return domain.TokenUsage{
			InputTokens:  asInt(usage["input_tokens"]),
			OutputTokens: asInt(usage["output_tokens"]),
			TotalTokens:  asInt(usage["total_tokens"]),
		}
	}
	if session, ok := msg["session"].(map[string]any); ok {
		if usage, ok := session["usage"].(map[string]any); ok {
			return domain.TokenUsage{
				InputTokens:  asInt(usage["input_tokens"]),
				OutputTokens: asInt(usage["output_tokens"]),
				TotalTokens:  asInt(usage["total_tokens"]),
			}
		}
	}
	return domain.TokenUsage{}
}

func extractPIRateLimits(msg map[string]any) map[string]any {
	if rl, ok := msg["rate_limits"].(map[string]any); ok {
		return rl
	}
	if rl, ok := msg["rateLimit"].(map[string]any); ok {
		return rl
	}
	if session, ok := msg["session"].(map[string]any); ok {
		if rl, ok := session["rate_limits"].(map[string]any); ok {
			return rl
		}
		if stats, ok := session["stats"].(map[string]any); ok {
			if rl, ok := stats["rate_limits"].(map[string]any); ok {
				return rl
			}
		}
	}
	return nil
}

func piText(msg map[string]any) string {
	for _, key := range []string{"text", "message", "delta"} {
		if text, ok := msg[key].(string); ok {
			return text
		}
	}
	if text := piAssistantText(msg["assistantMessageEvent"]); text != "" {
		return text
	}
	if text := piAssistantText(msg["message"]); text != "" {
		return text
	}
	if item, ok := msg["item"].(map[string]any); ok {
		if text, ok := item["text"].(string); ok {
			return text
		}
	}
	return ""
}

func piAssistantText(v any) string {
	msg, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if partial, ok := msg["partial"].(map[string]any); ok {
		if text := piContentText(partial["content"]); text != "" {
			return text
		}
	}
	if text := piContentText(msg["content"]); text != "" {
		return text
	}
	if typ, _ := msg["type"].(string); typ == "text_delta" {
		if text, ok := msg["delta"].(string); ok {
			return text
		}
	}
	if text, ok := msg["text"].(string); ok {
		return text
	}
	return ""
}

func piContentText(v any) string {
	switch content := v.(type) {
	case string:
		return content
	case []any:
		parts := []string{}
		for _, item := range content {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := m["type"].(string)
			if typ != "" && typ != "text" && typ != "output_text" {
				continue
			}
			if text, ok := m["text"].(string); ok && text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func respondExtensionUI(w io.Writer, logs *agent.RunLogs, msg map[string]any, policy any) error {
	method, _ := msg["method"].(string)
	id, _ := msg["id"].(string)
	if id == "" {
		return nil
	}
	if isStrictPolicy(policy) {
		return writeLoggedJSON(w, logs, map[string]any{"type": "extension_ui_response", "id": id, "cancelled": true, "error": "extension UI dialogs are disabled by approval_policy"})
	}
	switch method {
	case "confirm", "select", "input", "editor":
		return writeLoggedJSON(w, logs, map[string]any{"type": "extension_ui_response", "id": id, "cancelled": true})
	}
	return nil
}

func isStrictPolicy(policy any) bool {
	if policy == nil {
		return false
	}
	if s, ok := policy.(string); ok {
		return strings.EqualFold(s, "strict")
	}
	if m, ok := policy.(map[string]any); ok {
		if mode, ok := m["mode"].(string); ok {
			return strings.EqualFold(mode, "strict")
		}
	}
	return false
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func writeLoggedJSON(w io.Writer, logs *agent.RunLogs, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	logs.WriteProtocol("send", b)
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}
