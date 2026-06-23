package orchestrator

import (
	"context"
	"time"

	"github.com/openai/symphony/go/internal/domain"
)

type running struct {
	issue                    domain.Issue
	sessionID, workspace     string
	threadID, turnID         string
	started, lastEvent       time.Time
	status, lastEventType    string
	lastMessage              string
	error                    *string
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

// cancelled tracks issues whose worker was terminated by reconciliation
// so workerExit can distinguish cancellation from normal/completed exit.
