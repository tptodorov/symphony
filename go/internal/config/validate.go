package config

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
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
		} else if !isCommandReachable(cfg.TrackerBDCommand) {
			errs = append(errs, fmt.Errorf("tracker.bd_command %q is not reachable", cfg.TrackerBDCommand))
		}
	case "jira":
		if cfg.TrackerEndpoint == "" {
			errs = append(errs, errors.New("tracker.endpoint is required for jira"))
		}
		if cfg.TrackerEmail == "" {
			errs = append(errs, errors.New("tracker.email is required for jira"))
		}
		if cfg.TrackerAPIKey == "" {
			errs = append(errs, errors.New("tracker.api_token is required for jira"))
		}
		if cfg.TrackerJQL == "" && cfg.TrackerProjectKey == "" && cfg.TrackerProjectSlug == "" {
			errs = append(errs, errors.New("tracker.jql, tracker.project_key, or tracker.project_slug is required for jira"))
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
	if cfg.TrackerPageSize <= 0 {
		errs = append(errs, errors.New("tracker.page_size must be positive"))
	}
	if cfg.Hooks.Timeout <= 0 {
		errs = append(errs, errors.New("hooks.timeout_ms must be positive"))
	}
	if cfg.AgentKind == "codex" && cfg.Codex.Command == "" {
		errs = append(errs, errors.New("codex.command is required"))
	}
	if cfg.AgentKind == "pi" {
		if cfg.Pi.Command == "" {
			errs = append(errs, errors.New("pi.command is required"))
		} else if !isCommandReachable(cfg.Pi.Command) {
			errs = append(errs, fmt.Errorf("pi.command %q is not reachable", cfg.Pi.Command))
		}
	}
	if cfg.AgentKind != "codex" && cfg.AgentKind != "pi" {
		errs = append(errs, fmt.Errorf("unsupported agent_kind %q", cfg.AgentKind))
	}
	if cfg.Pi.SessionSync != "" && cfg.Pi.SessionSync != "none" && cfg.Pi.SessionSync != "export" && cfg.Pi.SessionSync != "sync" {
		errs = append(errs, errors.New("pi.session_sync must be none, export, or sync"))
	}
	if cfg.ServerPortSet && cfg.ServerPort < 0 {
		errs = append(errs, errors.New("server.port must be non-negative"))
	}
	switch cfg.PullRequests.Provider {
	case "", "none":
	case "github":
		if cfg.PullRequests.GitHubRepository == "" {
			errs = append(errs, errors.New("server.pull_requests.github_repository is required when provider is github"))
		}
	case "local":
		if cfg.PullRequests.LocalPath == "" {
			errs = append(errs, errors.New("server.pull_requests.local_path is required when provider is local"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported server.pull_requests.provider %q", cfg.PullRequests.Provider))
	}
	if cfg.PullRequests.Provider != "" && cfg.PullRequests.Provider != "none" && cfg.PullRequests.CacheTTL <= 0 {
		errs = append(errs, errors.New("server.pull_requests.cache_ttl_ms must be positive"))
	}
	return errors.Join(errs...)
}

func isCommandReachable(command string) bool {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false
	}
	_, err := exec.LookPath(parts[0])
	return err == nil
}
