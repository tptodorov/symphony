package agent

import (
	"context"
	"time"

	"github.com/openai/symphony/go/internal/domain"
)

type RunRequest struct {
	Issue               domain.Issue
	Workspace           string
	Prompt              string
	Attempt             int
	SessionID           string
	MaxTurns            int
	Command             string
	ReadTimeout         time.Duration
	TurnTimeout         time.Duration
	Policy              any
	EnableBeadsCLI      bool
	EnableLinearGraphQL bool
	TrackerBDCommand    string
	TrackerEndpoint     string
	TrackerAPIKey       string
}

type Event struct {
	SessionID  string
	ThreadID   string
	TurnID     string
	IssueID    string
	Type       string
	Message    string
	Usage      domain.TokenUsage
	RateLimits map[string]any
	At         time.Time
}

type Result struct {
	SessionID string
	ThreadID  string
	TurnID    string
	Usage     domain.TokenUsage
	Err       error
	Completed bool
}

type Runner interface {
	Run(ctx context.Context, req RunRequest, events chan<- Event) Result
}
