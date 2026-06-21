package orchestrator

import (
	"context"
	"time"

	"github.com/openai/symphony/go/internal/domain"
)

type running struct {
	issue                domain.Issue
	sessionID, workspace string
	started, lastEvent   time.Time
	cancel               context.CancelFunc
}
type retryItem struct {
	issue   domain.Issue
	attempt int
	at      time.Time
}
