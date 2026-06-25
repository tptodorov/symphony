package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tptodorov/symphony/go/internal/agent"
	agentfake "github.com/tptodorov/symphony/go/internal/agent/fake"
	"github.com/tptodorov/symphony/go/internal/config"
	"github.com/tptodorov/symphony/go/internal/domain"
	"github.com/tptodorov/symphony/go/internal/observability"
	trackerfake "github.com/tptodorov/symphony/go/internal/tracker/fake"
	"github.com/tptodorov/symphony/go/internal/workspace"
)

func TestDispatch(t *testing.T) {
	cfg := config.Defaults()
	cfg.TrackerKind = "linear"
	cfg.ActiveStates = []string{"Todo"}
	cfg.WorkspaceRoot = t.TempDir()
	cfg.Agent.MaxConcurrentAgents = 1
	tr := &trackerfake.Tracker{Issues: []domain.Issue{{ID: "1", Identifier: "A-1", Title: "T", State: "Todo"}, {ID: "2", Identifier: "A-2", Title: "T", State: "Todo"}}}
	r := &agentfake.Runner{}
	o := New(cfg, tr, r, workspace.NewManager(cfg.WorkspaceRoot))
	if err := o.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if r.Count() != 1 {
		t.Fatalf("runs=%d", r.Count())
	}
}

func TestReadyQueueSnapshotUsesDispatchOrder(t *testing.T) {
	cfg := config.Defaults()
	cfg.TrackerKind = "linear"
	cfg.ActiveStates = []string{"Todo"}
	cfg.WorkspaceRoot = t.TempDir()
	cfg.Agent.MaxConcurrentAgents = 1
	p1, p2, p3 := 1, 2, 3
	u2 := "https://tracker.example/A-2"
	issues := []domain.Issue{
		{ID: "3", Identifier: "A-3", Title: "Third", State: "Todo", Priority: &p3},
		{ID: "1", Identifier: "A-1", Title: "First", State: "Todo", Priority: &p1},
		{ID: "2", Identifier: "A-2", Title: "Second", State: "Todo", Priority: &p2, URL: &u2},
	}
	tr := &trackerfake.Tracker{Issues: issues}
	runner := &queueBlockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	o := New(cfg, tr, runner, workspace.NewManager(cfg.WorkspaceRoot))
	if err := o.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	defer close(runner.release)

	sn := o.Snapshot()
	if sn.Counts["running"] != 1 || len(sn.Running) != 1 || sn.Running[0].IssueIdentifier != "A-1" {
		t.Fatalf("running snapshot = %+v counts=%+v", sn.Running, sn.Counts)
	}
	if sn.Counts["ready"] != 2 || len(sn.Ready) != 2 {
		t.Fatalf("ready snapshot = %+v counts=%+v", sn.Ready, sn.Counts)
	}
	if sn.Ready[0].IssueIdentifier != "A-2" || sn.Ready[1].IssueIdentifier != "A-3" {
		t.Fatalf("ready queue order = %+v", sn.Ready)
	}
	if sn.Ready[0].IssueURL == nil || *sn.Ready[0].IssueURL != u2 || sn.Ready[0].Title != "Second" {
		t.Fatalf("ready row details = %+v", sn.Ready[0])
	}
}

func TestPromptIncludesHookWrittenFiles(t *testing.T) {
	cfg := config.Defaults()
	cfg.TrackerKind = "linear"
	cfg.ActiveStates = []string{"Todo"}
	cfg.WorkspaceRoot = t.TempDir()
	cfg.PromptTemplate = "base prompt"
	cfg.PromptIncludeFiles = []string{".symphony/setup-packet.md", "missing.md"}
	cfg.Hooks.BeforeRun = `mkdir -p .symphony; printf 'packet context\n' > .symphony/setup-packet.md`
	tr := &trackerfake.Tracker{Issues: []domain.Issue{{ID: "1", Identifier: "A-1", Title: "T", State: "Todo"}}}
	runner := &promptCapturingRunner{started: make(chan agent.RunRequest, 1), release: make(chan struct{})}
	o := New(cfg, tr, runner, workspace.NewManager(cfg.WorkspaceRoot))
	if err := o.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer close(runner.release)

	var req agent.RunRequest
	select {
	case req = <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	if !strings.Contains(req.Prompt, "base prompt") || !strings.Contains(req.Prompt, "## Included Context: .symphony/setup-packet.md") || !strings.Contains(req.Prompt, "packet context") {
		t.Fatalf("prompt missing include context:\n%s", req.Prompt)
	}
	if strings.Contains(req.Prompt, "missing.md") {
		t.Fatalf("missing include should be skipped:\n%s", req.Prompt)
	}
}

func TestForwardEventsAdoptsAgentSessionIdentity(t *testing.T) {
	cfg := config.Defaults()
	o := New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	started := time.Now()
	o.mu.Lock()
	o.running["1"] = running{
		issue:         domain.Issue{ID: "1", Identifier: "A-1", Title: "T", State: "Todo"},
		sessionID:     "A-1-dispatch",
		workspace:     filepath.Join(t.TempDir(), "A-1"),
		started:       started,
		lastEvent:     started,
		status:        "running",
		lastEventType: "session_started",
	}
	o.mu.Unlock()

	events := make(chan agent.Event, 1)
	events <- agent.Event{
		IssueID:   "1",
		SessionID: "thread-1-turn-1",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		Type:      "session_started",
		Message:   "turn started",
		At:        time.Now(),
	}
	close(events)

	o.forwardEvents(events)

	sn := o.Snapshot()
	if len(sn.Running) != 1 {
		t.Fatalf("running count = %d", len(sn.Running))
	}
	running := sn.Running[0]
	if running.SessionID != "thread-1-turn-1" || running.ThreadID != "thread-1" || running.TurnID != "turn-1" {
		t.Fatalf("unexpected identity: %+v", running)
	}
}

func TestForwardEventsDoesNotDoubleCountAcrossNonUsageEvents(t *testing.T) {
	cfg := config.Defaults()
	o := New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	started := time.Now()
	o.mu.Lock()
	o.running["1"] = running{
		issue:         domain.Issue{ID: "1", Identifier: "A-1", Title: "T", State: "Todo"},
		sessionID:     "A-1-dispatch",
		workspace:     filepath.Join(t.TempDir(), "A-1"),
		started:       started,
		lastEvent:     started,
		status:        "running",
		lastEventType: "session_started",
	}
	o.mu.Unlock()

	events := make(chan agent.Event, 3)
	events <- agent.Event{IssueID: "1", Type: "thread_tokenUsage_updated", Usage: domain.TokenUsage{InputTokens: 70, OutputTokens: 30, TotalTokens: 100}, At: time.Now()}
	events <- agent.Event{IssueID: "1", Type: "item_agentMessage_delta", Message: "working", At: time.Now()}
	events <- agent.Event{IssueID: "1", Type: "thread_tokenUsage_updated", Usage: domain.TokenUsage{InputTokens: 105, OutputTokens: 45, TotalTokens: 150}, At: time.Now()}
	close(events)

	o.forwardEvents(events)

	sn := o.Snapshot()
	if sn.AgentTotals.TotalTokens != 150 || sn.AgentTotals.InputTokens != 105 || sn.AgentTotals.OutputTokens != 45 {
		t.Fatalf("agent totals = %+v", sn.AgentTotals)
	}
	if len(sn.Running) != 1 || sn.Running[0].Tokens == nil || sn.Running[0].Tokens.TotalTokens != 150 {
		t.Fatalf("running tokens = %+v", sn.Running)
	}
}

func TestRunningSnapshotIncludesLogsAndAgentTextTail(t *testing.T) {
	cfg := config.Defaults()
	o := New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	started := time.Now()
	logs := domain.RunLogPaths{Protocol: filepath.Join(t.TempDir(), "protocol.jsonl"), Stderr: filepath.Join(t.TempDir(), "stderr.log"), Result: filepath.Join(t.TempDir(), "result.json")}
	o.mu.Lock()
	o.running["1"] = running{
		issue:         domain.Issue{ID: "1", Identifier: "A-1", Title: "T", State: "Todo"},
		sessionID:     "A-1-dispatch",
		workspace:     filepath.Join(t.TempDir(), "A-1"),
		started:       started,
		lastEvent:     started,
		status:        "running",
		lastEventType: "session_started",
		logs:          logs,
	}
	o.mu.Unlock()

	events := make(chan agent.Event, 105)
	for i := 0; i < 105; i++ {
		events <- agent.Event{IssueID: "1", Type: "item_agentMessage_updated", Text: "x", At: time.Now()}
	}
	close(events)

	o.forwardEvents(events)

	sn := o.Snapshot()
	if len(sn.Running) != 1 {
		t.Fatalf("running count = %d", len(sn.Running))
	}
	running := sn.Running[0]
	if running.LogPath != logs.Protocol || running.Logs == nil || len(running.Logs.CodexSessionLogs) != 3 {
		t.Fatalf("logs missing from running snapshot: %+v", running)
	}
	if len(running.RecentAgentMessages) != 100 {
		t.Fatalf("tail length = %d", len(running.RecentAgentMessages))
	}
}

func TestAgentTextTailUsesCodexItemBoundaries(t *testing.T) {
	cfg := config.Defaults()
	o := New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	started := time.Now()
	o.mu.Lock()
	o.running["1"] = running{
		issue:         domain.Issue{ID: "1", Identifier: "A-1", Title: "T", State: "Todo"},
		sessionID:     "A-1-dispatch",
		workspace:     filepath.Join(t.TempDir(), "A-1"),
		started:       started,
		lastEvent:     started,
		status:        "running",
		lastEventType: "session_started",
	}
	o.mu.Unlock()

	events := make(chan agent.Event, 6)
	events <- agent.Event{IssueID: "1", ItemID: "msg-1", Type: "item_agentMessage_delta", Text: "First ", At: time.Now()}
	events <- agent.Event{IssueID: "1", ItemID: "msg-1", Type: "item_agentMessage_delta", Text: "message", At: time.Now()}
	events <- agent.Event{IssueID: "1", ItemID: "msg-1", Type: "item_completed", Text: "First message", At: time.Now()}
	events <- agent.Event{IssueID: "1", Type: "thread_tokenUsage_updated", Usage: domain.TokenUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, At: time.Now()}
	events <- agent.Event{IssueID: "1", ItemID: "msg-2", Type: "item_agentMessage_delta", Text: "Second", At: time.Now()}
	events <- agent.Event{IssueID: "1", ItemID: "msg-2", Type: "item_completed", Text: "Second message", At: time.Now()}
	close(events)

	o.forwardEvents(events)

	sn := o.Snapshot()
	if len(sn.Running) != 1 {
		t.Fatalf("running count = %d", len(sn.Running))
	}
	got := sn.Running[0].RecentAgentMessages
	if len(got) != 2 {
		t.Fatalf("tail length = %d, tail=%+v", len(got), got)
	}
	if got[0].Text != "First message" || got[1].Text != "Second message" {
		t.Fatalf("tail text = %+v", got)
	}
}

type queueBlockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *queueBlockingRunner) Run(ctx context.Context, req agent.RunRequest, events chan<- agent.Event) agent.Result {
	close(r.started)
	select {
	case <-r.release:
		return agent.Result{SessionID: req.SessionID, Completed: false, Err: errors.New("released")}
	case <-ctx.Done():
		return agent.Result{SessionID: req.SessionID, Completed: false, Err: ctx.Err()}
	}
}

type promptCapturingRunner struct {
	started chan agent.RunRequest
	release chan struct{}
}

func (r *promptCapturingRunner) Run(ctx context.Context, req agent.RunRequest, events chan<- agent.Event) agent.Result {
	r.started <- req
	select {
	case <-r.release:
		return agent.Result{SessionID: req.SessionID, Completed: false, Err: errors.New("released")}
	case <-ctx.Done():
		return agent.Result{SessionID: req.SessionID, Completed: false, Err: ctx.Err()}
	}
}

func TestBackoff(t *testing.T) {
	if got := backoff(3, 30*time.Second); got != 30*time.Second {
		t.Fatal(got)
	}
}

func TestSetupSnapshotVisibleDuringWorkspacePreparation(t *testing.T) {
	cfg := config.Defaults()
	cfg.TrackerKind = "linear"
	cfg.ActiveStates = []string{"Todo"}
	cfg.WorkspaceRoot = t.TempDir()
	cfg.Agent.MaxConcurrentAgents = 1
	tmp := t.TempDir()
	started := filepath.Join(tmp, "started")
	release := filepath.Join(tmp, "release")
	cfg.Hooks.AfterCreate = fmt.Sprintf("touch %q; while [ ! -f %q ]; do sleep 0.01; done", started, release)
	tr := &trackerfake.Tracker{Issues: []domain.Issue{{ID: "1", Identifier: "A-1", Title: "Prepare workspace", State: "Todo"}}}
	runner := &queueBlockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	o := New(cfg, tr, runner, workspace.NewManager(cfg.WorkspaceRoot))

	done := make(chan error, 1)
	go func() { done <- o.Tick(context.Background()) }()
	waitFor(t, time.Second, func() bool {
		_, err := os.Stat(started)
		return err == nil
	})

	sn := o.Snapshot()
	if sn.Counts["setup"] != 1 || len(sn.Setup) != 1 {
		t.Fatalf("setup snapshot missing while hook runs: counts=%+v setup=%+v", sn.Counts, sn.Setup)
	}
	setup := sn.Setup[0]
	if setup.IssueIdentifier != "A-1" || setup.Title != "Prepare workspace" || setup.Stage != "after_create" || setup.Status != "running" || setup.Hook != "after_create" {
		t.Fatalf("unexpected setup snapshot: %+v", setup)
	}
	if setup.Workspace != "" {
		t.Fatalf("setup workspace should not be guessed before preparation returns: %+v", setup)
	}
	if len(sn.Running) != 0 {
		t.Fatalf("agent should not be running while workspace hook is blocked: %+v", sn.Running)
	}

	if err := os.WriteFile(release, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start after setup released")
	}
	close(runner.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("tick did not finish")
	}
}

func TestDispatchPreparationFailureSchedulesRetry(t *testing.T) {
	cfg := config.Defaults()
	cfg.TrackerKind = "linear"
	cfg.ActiveStates = []string{"Todo"}
	cfg.WorkspaceRoot = t.TempDir()
	cfg.Agent.MaxConcurrentAgents = 1
	cfg.Hooks.AfterCreate = "exit 2"
	tr := &trackerfake.Tracker{Issues: []domain.Issue{{ID: "1", Identifier: "A-1", Title: "T", State: "Todo"}}}
	r := &agentfake.Runner{}
	var logs bytes.Buffer
	o := NewWithLogger(cfg, tr, r, workspace.NewManager(cfg.WorkspaceRoot), observability.NewLogger(&logs))

	if err := o.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Count() != 0 {
		t.Fatalf("agent should not run after workspace prep failure, runs=%d", r.Count())
	}
	sn := o.Snapshot()
	if sn.Counts["retrying"] != 1 || len(sn.Retrying) != 1 {
		t.Fatalf("retry not queued: %+v", sn)
	}
	if sn.Retrying[0].Attempt != 1 || !strings.Contains(sn.Retrying[0].Error, "hook failed") {
		t.Fatalf("bad retry row: %+v", sn.Retrying[0])
	}
	if _, err := os.Stat(filepath.Join(cfg.WorkspaceRoot, "A-1")); !os.IsNotExist(err) {
		t.Fatalf("failed after_create workspace should be removed, stat err=%v", err)
	}
	failed, err := filepath.Glob(filepath.Join(cfg.WorkspaceRoot, workspace.FailedDirName, "A-1-*"))
	if err != nil || len(failed) != 1 {
		t.Fatalf("failed preparation workspace not retained: %v %#v", err, failed)
	}
	if sn.Counts["setup"] != 1 || len(sn.Setup) != 1 {
		t.Fatalf("failed setup snapshot missing: counts=%+v setup=%+v", sn.Counts, sn.Setup)
	}
	setup := sn.Setup[0]
	prepareErrorPath := filepath.Join(failed[0], "prepare-error.txt")
	if setup.Stage != "after_create" || setup.Status != "failed" || setup.Hook != "after_create" || setup.FailedWorkspace != failed[0] || setup.LogPath != prepareErrorPath || !strings.Contains(setup.Error, "hook failed") {
		t.Fatalf("unexpected failed setup snapshot: %+v", setup)
	}
	if sn.Retrying[0].Setup == nil || sn.Retrying[0].Setup.FailedWorkspace != failed[0] || sn.Retrying[0].Setup.LogPath != prepareErrorPath {
		t.Fatalf("retry row missing setup details: %+v", sn.Retrying[0])
	}
	o.mu.Lock()
	hist := append([]domain.RunAttempt(nil), o.runHistory["1"]...)
	attempt := o.attempts["1"]
	o.mu.Unlock()
	if attempt != 1 || len(hist) != 1 || hist[0].Attempt != 0 || hist[0].Status != domain.RunAttemptFailed || hist[0].Error == nil {
		t.Fatalf("attempt state mismatch: attempt=%d hist=%+v", attempt, hist)
	}
	if hist[0].WorkspacePath != failed[0] {
		t.Fatalf("history should point at retained failed workspace: %+v failed=%s", hist[0], failed[0])
	}
	logText := logs.String()
	for _, want := range []string{
		`"msg":"workspace preparation started"`,
		`"msg":"workflow hook failed"`,
		`"hook":"after_create"`,
		`"msg":"workspace preparation retained failed workspace"`,
		`"failed_workspace":"` + failed[0] + `"`,
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("setup log missing %q in logs:\n%s", want, logText)
		}
	}

	if err := o.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Count() != 0 {
		t.Fatalf("agent should not run before retry is due, runs=%d", r.Count())
	}
	if got := o.Snapshot().Counts["retrying"]; got != 1 {
		t.Fatalf("retry should remain queued, got %d", got)
	}
}

func waitFor(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func TestStallCancellationRecordsStalledRetry(t *testing.T) {
	cfg := config.Defaults()
	cfg.TrackerKind = "linear"
	cfg.ActiveStates = []string{"Todo"}
	cfg.Codex.StallTimeout = time.Millisecond
	issue := domain.Issue{ID: "1", Identifier: "A-1", Title: "T", State: "Todo"}
	o := New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now().Add(-time.Second)
	o.mu.Lock()
	o.running[issue.ID] = running{
		issue:         issue,
		sessionID:     "A-1-dispatch",
		workspace:     filepath.Join(t.TempDir(), "A-1"),
		started:       started,
		lastEvent:     started,
		status:        "running",
		lastEventType: "session_started",
		cancel:        cancel,
	}
	o.runHistory[issue.ID] = []domain.RunAttempt{{IssueID: issue.ID, IssueIdentifier: issue.Identifier, Attempt: 0, WorkspacePath: filepath.Join(t.TempDir(), "A-1"), StartedAt: started, Status: domain.RunAttemptStreaming}}
	o.mu.Unlock()

	if err := o.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("run context was not canceled: %v", ctx.Err())
	}

	o.workerExit(issue, agent.Result{Err: ctx.Err()}, time.Second)

	sn := o.Snapshot()
	if sn.Counts["retrying"] != 1 || len(sn.Retrying) != 1 {
		t.Fatalf("retry not queued: %+v", sn)
	}
	if !strings.Contains(sn.Retrying[0].Error, "stalled: no agent event") {
		t.Fatalf("retry error = %q", sn.Retrying[0].Error)
	}
	o.mu.Lock()
	hist := append([]domain.RunAttempt(nil), o.runHistory[issue.ID]...)
	o.mu.Unlock()
	if len(hist) != 1 || hist[0].Status != domain.RunAttemptStalled || hist[0].Error == nil || !strings.Contains(*hist[0].Error, "stalled: no agent event") {
		t.Fatalf("history = %+v", hist)
	}
}
