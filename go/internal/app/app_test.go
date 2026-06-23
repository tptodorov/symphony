package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentfake "github.com/openai/symphony/go/internal/agent/fake"
	"github.com/openai/symphony/go/internal/config"
	"github.com/openai/symphony/go/internal/orchestrator"
	trackerfake "github.com/openai/symphony/go/internal/tracker/fake"
	"github.com/openai/symphony/go/internal/workspace"
)

func TestNew(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, "WORKFLOW.md")
	if err := os.WriteFile(wf, []byte("---\ntracker:\n  kind: linear\n  api_key: k\n  project_slug: p\nworkspace:\n  root: work\n---\nPrompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), Options{WorkflowPath: wf, Tracker: &trackerfake.Tracker{}, Runner: &agentfake.Runner{}})
	if err != nil || app.Orch == nil {
		t.Fatal(err)
	}
}

func TestRunStartsEphemeralStatusServer(t *testing.T) {
	cfg := config.Defaults()
	cfg.WorkspaceRoot = t.TempDir()
	o := orchestrator.New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(cfg.WorkspaceRoot))
	a := &App{Opt: Options{Port: 0, PortSet: true}, Orch: o, cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not stop")
	}
}

func TestRunRejectsNegativePort(t *testing.T) {
	cfg := config.Defaults()
	o := orchestrator.New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	a := &App{Opt: Options{Port: -1, PortSet: true}, Orch: o, cfg: cfg}
	err := a.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "server.port") {
		t.Fatalf("expected server.port error, got %v", err)
	}
}
