package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agentfake "github.com/openai/symphony/go/internal/agent/fake"
	trackerfake "github.com/openai/symphony/go/internal/tracker/fake"
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
