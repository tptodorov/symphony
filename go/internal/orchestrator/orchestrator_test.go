package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	o.mu.Lock()
	hist := append([]domain.RunAttempt(nil), o.runHistory["1"]...)
	attempt := o.attempts["1"]
	o.mu.Unlock()
	if attempt != 1 || len(hist) != 1 || hist[0].Attempt != 0 || hist[0].Status != domain.RunAttemptFailed || hist[0].Error == nil {
		t.Fatalf("attempt state mismatch: attempt=%d hist=%+v", attempt, hist)
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
