package domain

import "time"

type EffectiveConfig struct {
	PromptTemplate      string
	PromptIncludeFiles  []string
	WorkflowDir         string
	AgentKind           string
	TrackerKind         string
	TrackerEndpoint     string
	TrackerAPIKey       string
	TrackerEmail        string
	TrackerProjectKey   string
	TrackerProjectSlug  string
	TrackerBDCommand    string
	TrackerAssignee     string
	TrackerJQL          string
	TrackerPageSize     int
	RequiredLabels      []string
	ActiveStates        []string
	TerminalStates      []string
	PollingInterval     time.Duration
	WorkspaceRoot       string
	Hooks               HooksConfig
	Agent               AgentConfig
	Codex               CodexConfig
	Pi                  PiConfig
	PerStateConcurrency map[string]int
	ServerPort          int
	ServerPortSet       bool
	EnableBeadsCLI      bool
	EnableLinearGraphQL bool
}

type HooksConfig struct {
	Timeout      time.Duration
	AfterCreate  string
	BeforeRun    string
	AfterRun     string
	BeforeRemove string
}

type AgentConfig struct {
	MaxConcurrentAgents int
	MaxTurns            int
	MaxRetryBackoff     time.Duration
}

type CodexConfig struct {
	Command      string
	TurnTimeout  time.Duration
	ReadTimeout  time.Duration
	StallTimeout time.Duration
	Policy       map[string]any
}

type PiConfig struct {
	Command      string
	Provider     string
	Model        string
	SessionSync  string
	ReadTimeout  time.Duration
	TurnTimeout  time.Duration
	StallTimeout time.Duration
	Policy       any
}
