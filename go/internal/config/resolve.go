package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tptodorov/symphony/go/internal/domain"
)

var envRef = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*`)

func Resolve(wf domain.WorkflowDefinition, workflowPath string) (Effective, error) {
	cfg := Defaults()
	cfg.PromptTemplate = wf.PromptTemplate
	cfg.PromptIncludeFiles = stringSlice(wf.Config, "prompt.include_files", cfg.PromptIncludeFiles)
	cfg.WorkflowDir = filepath.Dir(workflowPath)
	m := wf.Config
	cfg.AgentKind = str(m, "agent_kind", cfg.AgentKind)
	cfg.TrackerKind = str(m, "tracker.kind", cfg.TrackerKind)
	cfg.TrackerEndpoint = str(m, "tracker.endpoint", cfg.TrackerEndpoint)
	if cfg.TrackerKind == "jira" {
		if _, ok := lookup(m, "tracker.endpoint"); !ok {
			cfg.TrackerEndpoint = ""
		}
	}
	cfg.TrackerAPIKey = str(m, "tracker.api_key", cfg.TrackerAPIKey)
	cfg.TrackerAPIKey = str(m, "tracker.api_token", cfg.TrackerAPIKey)
	cfg.TrackerEmail = str(m, "tracker.email", cfg.TrackerEmail)
	cfg.TrackerProjectKey = str(m, "tracker.project_key", cfg.TrackerProjectKey)
	cfg.TrackerProjectSlug = str(m, "tracker.project_slug", cfg.TrackerProjectSlug)
	if cfg.TrackerKind == "jira" && strings.TrimSpace(cfg.TrackerProjectKey) == "" {
		cfg.TrackerProjectKey = cfg.TrackerProjectSlug
	}
	cfg.TrackerAssignee = str(m, "tracker.assignee", cfg.TrackerAssignee)
	cfg.TrackerBDCommand = str(m, "tracker.bd_command", cfg.TrackerBDCommand)
	cfg.TrackerJQL = str(m, "tracker.jql", cfg.TrackerJQL)
	cfg.TrackerPageSize = integer(m, "tracker.page_size", cfg.TrackerPageSize)
	cfg.RequiredLabels = stringSlice(m, "tracker.required_labels", cfg.RequiredLabels)
	if cfg.TrackerKind == "beads" {
		cfg.ActiveStates = []string{"open", "in_progress"}
		cfg.TerminalStates = []string{"closed", "tombstone"}
	}
	cfg.ActiveStates = stringSlice(m, "tracker.active_states", cfg.ActiveStates)
	cfg.TerminalStates = stringSlice(m, "tracker.terminal_states", cfg.TerminalStates)
	cfg.PollingInterval = millis(m, "polling.interval_ms", cfg.PollingInterval)
	cfg.WorkspaceRoot = str(m, "workspace.root", cfg.WorkspaceRoot)
	cfg.Hooks.Timeout = millis(m, "hooks.timeout_ms", cfg.Hooks.Timeout)
	cfg.Hooks.AfterCreate = str(m, "hooks.after_create", cfg.Hooks.AfterCreate)
	cfg.Hooks.BeforeRun = str(m, "hooks.before_run", cfg.Hooks.BeforeRun)
	cfg.Hooks.AfterRun = str(m, "hooks.after_run", cfg.Hooks.AfterRun)
	cfg.Hooks.BeforeRemove = str(m, "hooks.before_remove", cfg.Hooks.BeforeRemove)
	cfg.Agent.MaxConcurrentAgents = integer(m, "agent.max_concurrent_agents", cfg.Agent.MaxConcurrentAgents)
	cfg.Agent.MaxTurns = integer(m, "agent.max_turns", cfg.Agent.MaxTurns)
	cfg.Agent.MaxRetryBackoff = millis(m, "agent.max_retry_backoff_ms", cfg.Agent.MaxRetryBackoff)
	cfg.Codex.Command = str(m, "codex.command", cfg.Codex.Command)
	cfg.Codex.TurnTimeout = millis(m, "codex.turn_timeout_ms", cfg.Codex.TurnTimeout)
	cfg.Codex.ReadTimeout = millis(m, "codex.read_timeout_ms", cfg.Codex.ReadTimeout)
	cfg.Codex.StallTimeout = millis(m, "codex.stall_timeout_ms", cfg.Codex.StallTimeout)
	cfg.Pi.Command = str(m, "pi.command", cfg.Pi.Command)
	cfg.Pi.Provider = str(m, "pi.provider", cfg.Pi.Provider)
	cfg.Pi.Model = str(m, "pi.model", cfg.Pi.Model)
	cfg.Pi.SessionSync = str(m, "pi.session_sync", cfg.Pi.SessionSync)
	cfg.Pi.ReadTimeout = millis(m, "pi.read_timeout_ms", cfg.Pi.ReadTimeout)
	cfg.Pi.TurnTimeout = millis(m, "pi.turn_timeout_ms", cfg.Pi.TurnTimeout)
	cfg.Pi.StallTimeout = millis(m, "pi.stall_timeout_ms", cfg.Pi.StallTimeout)
	if v, ok := lookup(m, "pi.approval_policy"); ok {
		cfg.Pi.Policy = v
	}
	if _, ok := lookup(m, "server.port"); ok {
		cfg.ServerPort = integer(m, "server.port", cfg.ServerPort)
		cfg.ServerPortSet = true
	}
	if p := mapAny(m, "codex.policy"); p != nil {
		cfg.Codex.Policy = cloneMap(p)
	}
	if cfg.Codex.Policy == nil {
		cfg.Codex.Policy = map[string]any{}
	}
	for _, key := range []string{"approval_policy", "thread_sandbox", "turn_sandbox_policy"} {
		if v, ok := lookup(m, "codex."+key); ok {
			cfg.Codex.Policy[key] = v
		}
	}
	pc := mapAny(m, "agent.max_concurrent_agents_by_state")
	if pc == nil {
		pc = mapAny(m, "agent.per_state_concurrency")
	}
	if pc != nil {
		cfg.PerStateConcurrency = map[string]int{}
		for k, v := range pc {
			if n, ok := toInt(v); ok && n > 0 {
				cfg.PerStateConcurrency[domain.NormalizeState(k)] = n
			}
		}
	}
	cfg.EnableBeadsCLI = cfg.TrackerKind == "beads"
	cfg.EnableLinearGraphQL = cfg.TrackerKind == "linear"
	resolveEnvStrings(&cfg)
	root, err := resolvePath(cfg.WorkspaceRoot, filepath.Dir(workflowPath))
	if err != nil {
		return cfg, err
	}
	cfg.WorkspaceRoot = root
	return cfg, nil
}

func resolveEnvStrings(cfg *Effective) {
	strings := []*string{&cfg.TrackerEndpoint, &cfg.TrackerAPIKey, &cfg.TrackerEmail, &cfg.TrackerProjectKey, &cfg.TrackerProjectSlug, &cfg.TrackerAssignee, &cfg.TrackerBDCommand, &cfg.TrackerJQL, &cfg.WorkspaceRoot, &cfg.Codex.Command, &cfg.Pi.Command, &cfg.Pi.Provider, &cfg.Pi.Model}
	for _, p := range strings {
		*p = envRef.ReplaceAllStringFunc(*p, func(s string) string { return os.Getenv(s[1:]) })
	}
}

func resolvePath(path, base string) (string, error) {
	if strings.HasPrefix(path, "~/") || path == "~" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		path = filepath.Join(h, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	return abs, nil
}

func str(m map[string]any, path, def string) string {
	if v, ok := lookup(m, path); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}
func integer(m map[string]any, path string, def int) int {
	if v, ok := lookup(m, path); ok {
		if n, ok := toInt(v); ok {
			return n
		}
	}
	return def
}
func millis(m map[string]any, path string, def time.Duration) time.Duration {
	if v, ok := lookup(m, path); ok {
		if n, ok := toInt(v); ok {
			return time.Duration(n) * time.Millisecond
		}
	}
	return def
}
func stringSlice(m map[string]any, path string, def []string) []string {
	if v, ok := lookup(m, path); ok {
		switch x := v.(type) {
		case []string:
			return x
		case []any:
			out := []string{}
			for _, e := range x {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return def
}
func mapAny(m map[string]any, path string) map[string]any {
	if v, ok := lookup(m, path); ok {
		if mm, ok := v.(map[string]any); ok {
			return mm
		}
	}
	return nil
}
func cloneMap(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}
func lookup(m map[string]any, path string) (any, bool) {
	var cur any = m
	for _, p := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mm[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(x)
		return n, err == nil
	}
	return 0, false
}
