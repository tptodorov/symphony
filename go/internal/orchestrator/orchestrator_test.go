package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/symphony/go/internal/agent"
	agentfake "github.com/openai/symphony/go/internal/agent/fake"
	"github.com/openai/symphony/go/internal/config"
	"github.com/openai/symphony/go/internal/domain"
	trackerfake "github.com/openai/symphony/go/internal/tracker/fake"
	"github.com/openai/symphony/go/internal/workspace"
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

func TestBackoff(t *testing.T) {
	if got := backoff(3, 30*time.Second); got != 30*time.Second {
		t.Fatal(got)
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
	o := New(cfg, tr, r, workspace.NewManager(cfg.WorkspaceRoot))

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
