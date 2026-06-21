package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/symphony/go/internal/domain"
)

func TestResolveValidate(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "secret")
	wf := domain.WorkflowDefinition{Config: map[string]any{
		"tracker":   map[string]any{"kind": "linear", "api_key": "$LINEAR_API_KEY", "project_slug": "p"},
		"workspace": map[string]any{"root": "work"},
		"agent":     map[string]any{"per_state_concurrency": map[string]any{"Todo": 2, "Bad": 0}},
	}}
	cfg, err := Resolve(wf, filepath.Join(t.TempDir(), "WORKFLOW.md"))
	if err != nil || cfg.TrackerAPIKey != "secret" || !filepath.IsAbs(cfg.WorkspaceRoot) || cfg.PerStateConcurrency["todo"] != 2 || cfg.PerStateConcurrency["bad"] != 0 {
		t.Fatalf("%+v %v", cfg, err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestInvalid(t *testing.T) {
	cfg, err := Resolve(domain.WorkflowDefinition{Config: map[string]any{"tracker": map[string]any{"kind": "linear"}, "agent": map[string]any{"max_turns": 0}}}, "WORKFLOW.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error")
	}
	if home, err := os.UserHomeDir(); err == nil {
		cfg, err := Resolve(domain.WorkflowDefinition{Config: map[string]any{"workspace": map[string]any{"root": "~/x"}}}, "WORKFLOW.md")
		if err != nil || cfg.WorkspaceRoot != filepath.Join(home, "x") {
			t.Fatalf("%q %v", cfg.WorkspaceRoot, err)
		}
	}
}
