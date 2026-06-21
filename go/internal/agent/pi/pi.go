package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/openai/symphony/go/internal/agent"
)

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
	if req.Workspace != "" && !strings.Contains(command, "--cwd") {
		command += " --cwd " + req.Workspace
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
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("open pi stdin: %w", err)}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("open pi stdout: %w", err)}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("start pi: %w", err)}
	}
	if err := writeJSON(stdin, map[string]any{"id": req.SessionID, "type": "prompt", "message": req.Prompt}); err != nil {
		_ = cmd.Process.Kill()
		return agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("send pi prompt: %w", err)}
	}

	completed, err := r.read(stdout, events, req, stdin)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return agent.Result{SessionID: req.SessionID, Err: ctx.Err()}
	}
	if err != nil {
		return agent.Result{SessionID: req.SessionID, Err: err}
	}
	if waitErr != nil {
		return agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("pi exited: %w: %s", waitErr, stderr.String())}
	}
	return agent.Result{SessionID: req.SessionID, Completed: completed}
}

func (r *Runner) read(stdout io.Reader, events chan<- agent.Event, req agent.RunRequest, stdin io.WriteCloser) (bool, error) {
	s := bufio.NewScanner(stdout)
	s.Buffer(make([]byte, 64*1024), 10*1024*1024)
	completed := false
	accepted := false
	for s.Scan() {
		line := s.Text()
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			events <- agent.Event{SessionID: req.SessionID, IssueID: req.Issue.ID, Type: "pi_output", Message: line, At: time.Now()}
			continue
		}
		typeName, _ := msg["type"].(string)
		if typeName == "response" && msg["id"] == req.SessionID {
			if ok, _ := msg["success"].(bool); !ok {
				return false, fmt.Errorf("pi prompt rejected: %v", msg["error"])
			}
			accepted = true
		}
		if typeName == "extension_ui_request" {
			_ = respondExtensionUI(stdin, msg)
		}
		if typeName == "agent_end" {
			completed = true
			_ = stdin.Close()
		}
		events <- agent.Event{SessionID: req.SessionID, IssueID: req.Issue.ID, Type: typeName, Message: line, At: time.Now()}
	}
	if err := s.Err(); err != nil {
		return completed, fmt.Errorf("read pi output: %w", err)
	}
	if !accepted {
		return completed, fmt.Errorf("pi prompt was not accepted")
	}
	return completed, nil
}

func respondExtensionUI(w io.Writer, msg map[string]any) error {
	method, _ := msg["method"].(string)
	id, _ := msg["id"].(string)
	if id == "" {
		return nil
	}
	switch method {
	case "confirm", "select", "input", "editor":
		return writeJSON(w, map[string]any{"type": "extension_ui_response", "id": id, "cancelled": true})
	}
	return nil
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
