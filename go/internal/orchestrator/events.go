package orchestrator

import (
	"time"

	"github.com/tptodorov/symphony/go/internal/domain"
)

type Snapshot struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Ready       []IssueQueueSnapshot `json:"ready"`
	Setup       []SetupSnapshot      `json:"setup"`
	Running     []RunningSnapshot    `json:"running"`
	Retrying    []RetrySnapshot      `json:"retrying"`
	RetryQueue  []RetrySnapshot      `json:"retry_queue,omitempty"`
	Counts      map[string]int       `json:"counts"`
	AgentTotals *domain.AgentTotals  `json:"agent_totals,omitempty"`
	RateLimits  map[string]any       `json:"rate_limits,omitempty"`
}
type IssueQueueSnapshot struct {
	IssueID         string  `json:"issue_id"`
	IssueIdentifier string  `json:"issue_identifier"`
	IssueURL        *string `json:"issue_url,omitempty"`
	Title           string  `json:"title,omitempty"`
	State           string  `json:"state,omitempty"`
	Priority        *int    `json:"priority,omitempty"`
}
type RunningSnapshot struct {
	IssueID             string             `json:"issue_id,omitempty"`
	IssueIdentifier     string             `json:"issue_identifier,omitempty"`
	IssueURL            *string            `json:"issue_url,omitempty"`
	Title               string             `json:"title,omitempty"`
	SessionID           string             `json:"session_id,omitempty"`
	ThreadID            string             `json:"thread_id,omitempty"`
	TurnID              string             `json:"turn_id,omitempty"`
	TurnCount           int                `json:"turn_count,omitempty"`
	State               string             `json:"state,omitempty"`
	Status              string             `json:"status,omitempty"`
	LastEvent           string             `json:"last_event,omitempty"`
	LastMessage         string             `json:"last_message,omitempty"`
	LogPath             string             `json:"log_path,omitempty"`
	Logs                *LogsSnapshot      `json:"logs,omitempty"`
	RecentAgentMessages []AgentTextMessage `json:"recent_agent_messages,omitempty"`
	Error               string             `json:"error,omitempty"`
	Workspace           string             `json:"workspace,omitempty"`
	StartedAt           *time.Time         `json:"started_at,omitempty"`
	LastEventAt         *time.Time         `json:"last_event_at,omitempty"`
	Tokens              *domain.TokenUsage `json:"tokens,omitempty"`
	Attempts            *AttemptsSnapshot  `json:"attempts,omitempty"`
	Setup               *SetupSnapshot     `json:"setup,omitempty"`
}
type AttemptsSnapshot struct {
	RestartCount        int `json:"restart_count,omitempty"`
	CurrentRetryAttempt int `json:"current_retry_attempt,omitempty"`
}
type RetrySnapshot struct {
	IssueID         string         `json:"issue_id"`
	IssueIdentifier string         `json:"issue_identifier"`
	IssueURL        *string        `json:"issue_url,omitempty"`
	Attempt         int            `json:"attempt"`
	DueAt           time.Time      `json:"due_at"`
	At              *time.Time     `json:"at,omitempty"`
	Error           string         `json:"error,omitempty"`
	Setup           *SetupSnapshot `json:"setup,omitempty"`
}

type IssueDetailSnapshot struct {
	IssueIdentifier     string             `json:"issue_identifier"`
	IssueID             string             `json:"issue_id"`
	Status              string             `json:"status"`
	Workspace           *WorkspaceSnapshot `json:"workspace"`
	Attempts            AttemptsSnapshot   `json:"attempts"`
	Running             *RunningSnapshot   `json:"running"`
	Retry               *RetrySnapshot     `json:"retry"`
	Logs                LogsSnapshot       `json:"logs"`
	RecentAgentMessages []AgentTextMessage `json:"recent_agent_messages,omitempty"`
	RecentEvents        []EventSnapshot    `json:"recent_events"`
	LastError           *string            `json:"last_error"`
	Setup               *SetupSnapshot     `json:"setup,omitempty"`
	Tracked             map[string]any     `json:"tracked"`
}

type SetupSnapshot struct {
	IssueID         string        `json:"issue_id"`
	IssueIdentifier string        `json:"issue_identifier"`
	IssueURL        *string       `json:"issue_url,omitempty"`
	Title           string        `json:"title,omitempty"`
	State           string        `json:"state,omitempty"`
	Attempt         int           `json:"attempt,omitempty"`
	Stage           string        `json:"stage"`
	Status          string        `json:"status"`
	Hook            string        `json:"hook,omitempty"`
	Workspace       string        `json:"workspace,omitempty"`
	FailedWorkspace string        `json:"failed_workspace,omitempty"`
	Error           string        `json:"error,omitempty"`
	LogPath         string        `json:"log_path,omitempty"`
	Logs            []LogSnapshot `json:"logs,omitempty"`
	StartedAt       *time.Time    `json:"started_at,omitempty"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

type WorkspaceSnapshot struct {
	Path string `json:"path"`
}

type LogsSnapshot struct {
	CodexSessionLogs []LogSnapshot `json:"codex_session_logs"`
}

type LogSnapshot struct {
	Label string  `json:"label"`
	Path  string  `json:"path"`
	URL   *string `json:"url"`
}

type EventSnapshot struct {
	At      time.Time `json:"at"`
	Event   string    `json:"event"`
	Message string    `json:"message,omitempty"`
}

type AgentTextMessage struct {
	At     time.Time `json:"at"`
	Event  string    `json:"event"`
	Text   string    `json:"text"`
	itemID string
}
