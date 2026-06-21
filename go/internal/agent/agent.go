package agent

import (
	"context"
	"time"

	"github.com/openai/symphony/go/internal/domain"
)

type RunRequest struct {
	Issue       domain.Issue
	Workspace   string
	Prompt      string
	Attempt     int
	SessionID   string
	MaxTurns    int
	Command     string
	TurnTimeout time.Duration
	Policy      any
}

type Event struct {
	SessionID   string
	IssueID     string
	Type        string
	Message     string
	Usage       domain.TokenUsage
	RateLimits  map[string]any
	At          time.Time
}

type Result struct {
	SessionID string
	Usage     domain.TokenUsage
	Err       error
	Completed bool
}

type Runner interface {
	Run(ctx context.Context, req RunRequest, events chan<- Event) Result
}
