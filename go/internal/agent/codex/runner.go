package codex

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/openai/symphony/go/internal/agent"
)

type Runner struct {
	Command     string
	TurnTimeout time.Duration
}

func New(command string) *Runner { return &Runner{Command: command} }

func (r *Runner) Run(ctx context.Context, req agent.RunRequest, events chan<- agent.Event) agent.Result {
	command := req.Command
	if command == "" {
		command = r.Command
	}
	if command == "" {
		command = "codex app-server"
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
			select {
			case events <- agent.Event{SessionID: req.SessionID, IssueID: req.Issue.ID, Type: "codex", Message: line, Usage: u, At: time.Now()}:
			case <-ctx.Done():
				return
			}
		}
	}()
	err = cmd.Wait()
	<-scanDone
	if ctx.Err() != nil {
		return agent.Result{SessionID: req.SessionID, Usage: usage, Err: ctx.Err()}
	}
	if err != nil {
		return agent.Result{SessionID: req.SessionID, Usage: usage, Err: fmt.Errorf("codex exited: %w: %s", err, stderr.String())}
	}
	return agent.Result{SessionID: req.SessionID, Usage: usage, Completed: completed}
}
