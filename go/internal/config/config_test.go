package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tptodorov/symphony/go/internal/domain"
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

func TestResolveJira(t *testing.T) {
	t.Setenv("JIRA_TOKEN", "token")
	wf := domain.WorkflowDefinition{Config: map[string]any{
		"tracker": map[string]any{
			"kind":        "jira",
			"endpoint":    "https://example.atlassian.net",
			"email":       "user@example.com",
			"api_token":   "$JIRA_TOKEN",
			"project_key": "MOD",
			"jql":         "project = MOD",
			"page_size":   25,
		},
	}}
	cfg, err := Resolve(wf, filepath.Join(t.TempDir(), "WORKFLOW.md"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrackerAPIKey != "token" || cfg.TrackerEmail != "user@example.com" || cfg.TrackerProjectKey != "MOD" || cfg.TrackerJQL != "project = MOD" || cfg.TrackerPageSize != 25 {
		t.Fatalf("%+v", cfg)
	}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestResolveJiraAliases(t *testing.T) {
	wf := domain.WorkflowDefinition{Config: map[string]any{
		"tracker": map[string]any{
			"kind":         "jira",
			"endpoint":     "https://example.atlassian.net",
			"email":        "user@example.com",
			"api_key":      "token",
			"project_slug": "MOD",
		},
	}}
	cfg, err := Resolve(wf, filepath.Join(t.TempDir(), "WORKFLOW.md"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrackerAPIKey != "token" || cfg.TrackerProjectKey != "MOD" || cfg.TrackerProjectSlug != "MOD" {
		t.Fatalf("%+v", cfg)
	}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCodexSpecPolicyFields(t *testing.T) {
	wf := domain.WorkflowDefinition{Config: map[string]any{
		"codex": map[string]any{
			"policy": map[string]any{
				"legacy":          true,
				"approval_policy": "on-request",
			},
			"approval_policy": "never",
			"thread_sandbox":  "workspace-write",
			"turn_sandbox_policy": map[string]any{
				"type": "workspaceWrite",
			},
		},
	}}
	cfg, err := Resolve(wf, filepath.Join(t.TempDir(), "WORKFLOW.md"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Codex.Policy["legacy"] != true || cfg.Codex.Policy["approval_policy"] != "never" || cfg.Codex.Policy["thread_sandbox"] != "workspace-write" {
		t.Fatalf("%+v", cfg.Codex.Policy)
	}
	policy, ok := cfg.Codex.Policy["turn_sandbox_policy"].(map[string]any)
	if !ok || policy["type"] != "workspaceWrite" {
		t.Fatalf("%+v", cfg.Codex.Policy)
	}
}

func TestResolvePromptIncludeFiles(t *testing.T) {
	wf := domain.WorkflowDefinition{Config: map[string]any{
		"prompt": map[string]any{"include_files": []any{".symphony/setup-packet.md", "notes.md"}},
	}}
	cfg, err := Resolve(wf, filepath.Join(t.TempDir(), "WORKFLOW.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.PromptIncludeFiles) != 2 || cfg.PromptIncludeFiles[0] != ".symphony/setup-packet.md" || cfg.PromptIncludeFiles[1] != "notes.md" {
		t.Fatalf("include files = %#v", cfg.PromptIncludeFiles)
	}
}

func TestJiraRequiresEndpoint(t *testing.T) {
	cfg, err := Resolve(domain.WorkflowDefinition{Config: map[string]any{"tracker": map[string]any{"kind": "jira", "email": "user@example.com", "api_token": "token", "project_key": "MOD"}}}, "WORKFLOW.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveServerPortPresence(t *testing.T) {
	cfg, err := Resolve(domain.WorkflowDefinition{Config: map[string]any{}}, "WORKFLOW.md")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerPortSet {
		t.Fatal("server.port should be unset when absent")
	}
	cfg, err = Resolve(domain.WorkflowDefinition{Config: map[string]any{"server": map[string]any{"port": 0}}}, "WORKFLOW.md")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ServerPortSet || cfg.ServerPort != 0 {
		t.Fatalf("server.port presence not preserved: %+v", cfg)
	}
	cfg.ServerPort = -1
	if err := Validate(cfg); err == nil {
		t.Fatal("expected negative server.port to be invalid")
	}
}
