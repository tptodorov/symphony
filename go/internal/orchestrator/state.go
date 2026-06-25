package orchestrator

import (
	"context"
	"time"

	"github.com/tptodorov/symphony/go/internal/domain"
)

type running struct {
	issue                    domain.Issue
	sessionID, workspace     string
	threadID, turnID         string
	started, lastEvent       time.Time
	status, lastEventType    string
	lastMessage              string
	error                    *string
	logs                     domain.RunLogPaths
	agentTextTail            []AgentTextMessage
	turnCount                int
	agentInputTokens         int
	agentOutputTokens        int
	agentTotalTokens         int
	lastReportedInputTokens  int
	lastReportedOutputTokens int
	lastReportedTotalTokens  int
	cancel                   context.CancelFunc
}
type retryItem struct {
	issue   domain.Issue
	attempt int
	at      time.Time
	err     string
}

type cancellationReason struct {
	status domain.RunAttemptStatus
	err    string
	retry  bool
}
