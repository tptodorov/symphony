package orchestrator

import (
	"time"

	"github.com/openai/symphony/go/internal/domain"
)

type Snapshot struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Running     []RunningSnapshot `json:"running"`
	RetryQueue  []RetrySnapshot   `json:"retry_queue"`
	Counts      map[string]int    `json:"counts"`
	AgentTotals *domain.AgentTotals `json:"agent_totals,omitempty"`
	RateLimits  map[string]any    `json:"rate_limits,omitempty"`
}
type RunningSnapshot struct {
	IssueID         string           `json:"issue_id,omitempty"`
	IssueIdentifier string           `json:"issue_identifier,omitempty"`
	IssueURL        *string          `json:"issue_url,omitempty"`
	SessionID       string           `json:"session_id,omitempty"`
	TurnCount       int              `json:"turn_count,omitempty"`
	Status          string           `json:"status,omitempty"`
	LastEvent       string           `json:"last_event,omitempty"`
	LastMessage     string           `json:"last_message,omitempty"`
	Error           string           `json:"error,omitempty"`
	Workspace       string           `json:"workspace,omitempty"`
	StartedAt       *time.Time       `json:"started_at,omitempty"`
	LastEventAt     *time.Time       `json:"last_event_at,omitempty"`
	Tokens          *domain.TokenUsage `json:"tokens,omitempty"`
	Attempts        *AttemptsSnapshot  `json:"attempts,omitempty"`
}
type AttemptsSnapshot struct {
	RestartCount        int `json:"restart_count,omitempty"`
	CurrentRetryAttempt int `json:"current_retry_attempt,omitempty"`
}
type RetrySnapshot struct {
	IssueID         string    `json:"issue_id"`
	IssueIdentifier string    `json:"issue_identifier"`
	Attempt         int       `json:"attempt"`
	At              time.Time `json:"at"`
	Error           string    `json:"error,omitempty"`
}
