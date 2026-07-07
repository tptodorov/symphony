package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agentfake "github.com/tptodorov/symphony/go/internal/agent/fake"
	trackerfake "github.com/tptodorov/symphony/go/internal/tracker/fake"
)

func TestSmokeBeadsPiWorkflow(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, "WORKFLOW.md")
	content := `---
tracker:
  kind: beads
agent_kind: pi
agent:
  max_turns: 2
pi:
  command: pi --mode rpc --no-session
workspace:
  root: workspaces
---
Working on {{ issue.identifier }}: {{ issue.title }}`
	if err := os.WriteFile(wf, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), Options{WorkflowPath: wf, Tracker: &trackerfake.Tracker{}, Runner: &agentfake.Runner{}})
	if err != nil || app.Orch == nil {
		t.Fatal(err)
	}
}

func TestSmokeLinearCodexWorkflow(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, "WORKFLOW.md")
	content := `---
tracker:
  kind: linear
  api_key: test-key
  project_slug: TEST
agent_kind: codex
agent:
  max_turns: 2
codex:
  command: codex app-server
workspace:
  root: workspaces
---
Working on {{ issue.identifier }}`
	if err := os.WriteFile(wf, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), Options{WorkflowPath: wf, Tracker: &trackerfake.Tracker{}, Runner: &agentfake.Runner{}})
	if err != nil || app.Orch == nil {
		t.Fatal(err)
	}
}