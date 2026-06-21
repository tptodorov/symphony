package tracker

import (
	"context"

	"github.com/openai/symphony/go/internal/config"
	"github.com/openai/symphony/go/internal/domain"
)

type Tracker interface {
	FetchCandidates(ctx context.Context, cfg config.Effective) ([]domain.Issue, error)
	FetchStatesByID(ctx context.Context, ids []string) (map[string]domain.Issue, error)
	FetchByStates(ctx context.Context, states []string) ([]domain.Issue, error)
}
