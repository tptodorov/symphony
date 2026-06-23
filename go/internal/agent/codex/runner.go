package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/openai/symphony/go/internal/agent"
)

type Runner struct {
	Command     string
	TurnTimeout time.Duration
}

func New(command string) *Runner { return &Runner{Command: command} }

func eventType(line string) string {
	var msg map[string]any
	if err := json.Unmarshal([]byte(line), &msg); err == nil {
		if t, ok := msg["type"].(string); ok && t != "" {
			return t
		}
		if e, ok := msg["event"].(string); ok && e != "" {
			return e
		}
	}
	if strings.Contains(line, "turn.completed") || strings.Contains(line, "task_complete") {
		return "turn_completed"
	}
	if strings.Contains(line, "turn.failed") || strings.Contains(line, "turn_failed") {
		return "turn_failed"
	}
	if strings.Contains(line, "turn.cancelled") || strings.Contains(line, "turn_cancelled") {
		return "turn_cancelled"
	}
	if strings.Contains(line, "approval.auto_approved") || strings.Contains(line, "auto_approved") {
		return "approval_auto_approved"
	}
	if strings.Contains(line, "unsupported_tool") || strings.Contains(line, "tool_call") {
		return "unsupported_tool_call"
	}
	if strings.Contains(line, "input_required") || strings.Contains(line, "turn_input_required") {
		return "turn_input_required"
	}
	if strings.Contains(line, "error") {
		return "turn_ended_with_error"
	}
	return "other_message"
}

func (r *Runner) Run(ctx context.Context, req agent.RunRequest, events chan<- agent.Event) agent.Result {
	command := req.Command
	if command == "" {
		command = r.Command
	}
	if command == "" {
		command = "codex app-server"
	}
	if req.Workspace == "" {
		return agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("workspace path is required")}
	}
	if req.TurnTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.TurnTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = req.Workspace
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if req.Policy != nil {
		if b, err := json.Marshal(req.Policy); err == nil {
			cmd.Env = append(cmd.Environ(), "SYMPHONY_CODEX_POLICY="+string(b))
		}
	}
	if req.EnableBeadsCLI || req.EnableLinearGraphQL {
		tools := []map[string]any{}
		if req.EnableBeadsCLI {
			tools = append(tools, map[string]any{
				"name":        "beads_cli",
				"description": "Execute bd CLI commands using the configured tracker.bd_command in the repository working directory.",
				"input_schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
					"required": []string{"args"},
				},
			})
		}
		if req.EnableLinearGraphQL {
			tools = append(tools, map[string]any{
				"name":        "linear_graphql",
				"description": "Execute a raw GraphQL query or mutation against Linear using configured tracker auth.",
				"input_schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query":     map[string]any{"type": "string"},
						"variables": map[string]any{"type": "object"},
					},
					"required": []string{"query"},
				},
			})
		}
		if b, err := json.Marshal(map[string]any{"tools": tools}); err == nil {
			cmd.Env = append(cmd.Environ(), "SYMPHONY_TOOLS="+string(b))
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("open stdin: %w", err)}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("open stdout: %w", err)}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("start codex: %w", err)}
	}
	_, _ = stdin.Write([]byte(req.Prompt + "\n"))
	_ = stdin.Close()
	usage := ExtractUsage(nil)
	completed := false
	var threadID, turnID string
	sessionID := req.SessionID
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		s := bufio.NewScanner(stdout)
		s.Buffer(make([]byte, 64*1024), 10*1024*1024)
		for s.Scan() {
			line := s.Text()
			u := ExtractUsage([]byte(line))
			if u.TotalTokens != 0 {
				usage = u
			}
			if IsTerminalEvent(line) {
				completed = true
			}
			tid, turn := extractThreadTurnID(line)
			if tid != "" {
				threadID = tid
			}
			if turn != "" {
				turnID = turn
			}
			if threadID != "" && turnID != "" {
				sessionID = threadID + "-" + turnID
			}
			select {
			case events <- agent.Event{SessionID: sessionID, IssueID: req.Issue.ID, Type: eventType(line), Message: line, Usage: u, RateLimits: ExtractRateLimits([]byte(line)), At: time.Now()}:
			case <-ctx.Done():
				return
			}
		}
	}()
	err = cmd.Wait()
	<-scanDone
	if ctx.Err() != nil {
		return agent.Result{SessionID: sessionID, Usage: usage, Err: ctx.Err()}
	}
	if err != nil {
		return agent.Result{SessionID: sessionID, Usage: usage, Err: fmt.Errorf("codex exited: %w: %s", err, stderr.String())}
	}
	if !completed {
		return agent.Result{SessionID: sessionID, Usage: usage, Err: fmt.Errorf("codex exited without terminal event")}
	}
	return agent.Result{SessionID: sessionID, Usage: usage, Completed: completed}
}

func extractThreadTurnID(line string) (string, string) {
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return "", ""
	}
	tid := strField(m, "thread_id", "threadId", "thread.id")
	turn := strField(m, "turn_id", "turnId", "turn.id")
	if tid == "" {
		if inner, ok := m["identity"].(map[string]any); ok {
			tid = strField(inner, "thread_id", "threadId", "id")
		}
	}
	if turn == "" {
		if inner, ok := m["identity"].(map[string]any); ok {
			turn = strField(inner, "turn_id", "turnId", "id")
		}
	}
	return tid, turn
}

func strField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
