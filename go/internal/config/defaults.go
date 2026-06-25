package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/tptodorov/symphony/go/internal/domain"
)

type Effective = domain.EffectiveConfig

func Defaults() Effective {
	return Effective{
		AgentKind:           "codex",
		TrackerEndpoint:     "https://api.linear.app/graphql",
		TrackerBDCommand:    "bd",
		TrackerPageSize:     50,
		ActiveStates:        []string{"Todo", "In Progress"},
		TerminalStates:      []string{"Closed", "Cancelled", "Canceled", "Duplicate", "Done"},
		PollingInterval:     30 * time.Second,
		WorkspaceRoot:       filepath.Join(os.TempDir(), "symphony_workspaces"),
		Hooks:               domain.HooksConfig{Timeout: time.Minute},
		Agent:               domain.AgentConfig{MaxConcurrentAgents: 10, MaxTurns: 20, MaxRetryBackoff: 5 * time.Minute},
		Codex:               domain.CodexConfig{Command: "codex app-server", TurnTimeout: time.Hour, ReadTimeout: 5 * time.Second, StallTimeout: 5 * time.Minute, Policy: map[string]any{}},
		Pi:                  domain.PiConfig{Command: "pi --mode rpc --no-session", SessionSync: "none", ReadTimeout: 5 * time.Second, TurnTimeout: time.Hour, StallTimeout: 5 * time.Minute},
		PerStateConcurrency: map[string]int{},
	}
}
