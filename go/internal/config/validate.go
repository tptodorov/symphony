package config

import (
	"errors"
	"fmt"
)

func Validate(cfg Effective) error {
	var errs []error
	switch cfg.TrackerKind {
	case "linear":
		if cfg.TrackerAPIKey == "" {
			errs = append(errs, errors.New("tracker.api_key is required for linear"))
		}
		if cfg.TrackerProjectSlug == "" {
			errs = append(errs, errors.New("tracker.project_slug is required for linear"))
		}
	case "beads":
		if cfg.TrackerAssignee != "" {
			errs = append(errs, errors.New("tracker.assignee is not supported for beads"))
		}
		if cfg.TrackerBDCommand == "" {
			errs = append(errs, errors.New("tracker.bd_command is required for beads"))
		}
	case "":
		errs = append(errs, errors.New("tracker.kind is required"))
	default:
		errs = append(errs, fmt.Errorf("unsupported tracker.kind %q", cfg.TrackerKind))
	}
	if cfg.Agent.MaxConcurrentAgents <= 0 {
		errs = append(errs, errors.New("agent.max_concurrent_agents must be positive"))
	}
	if cfg.Agent.MaxTurns <= 0 {
		errs = append(errs, errors.New("agent.max_turns must be positive"))
	}
	if cfg.Agent.MaxRetryBackoff < 0 {
		errs = append(errs, errors.New("agent.max_retry_backoff_ms must be non-negative"))
	}
	if cfg.Hooks.Timeout <= 0 {
		errs = append(errs, errors.New("hooks.timeout_ms must be positive"))
	}
	if cfg.AgentKind == "codex" && cfg.Codex.Command == "" {
		errs = append(errs, errors.New("codex.command is required"))
	}
	if cfg.AgentKind == "pi" && cfg.Pi.Command == "" {
		errs = append(errs, errors.New("pi.command is required"))
	}
	if cfg.AgentKind != "codex" && cfg.AgentKind != "pi" {
		errs = append(errs, fmt.Errorf("unsupported agent_kind %q", cfg.AgentKind))
	}
	if cfg.Pi.SessionSync != "" && cfg.Pi.SessionSync != "none" && cfg.Pi.SessionSync != "export" && cfg.Pi.SessionSync != "sync" {
		errs = append(errs, errors.New("pi.session_sync must be none, export, or sync"))
	}
	return errors.Join(errs...)
}
