package fake

import (
	"context"
	"sync"

	"github.com/openai/symphony/go/internal/config"
	"github.com/openai/symphony/go/internal/domain"
)

type Tracker struct {
	mu     sync.Mutex
	Issues []domain.Issue
	Err    error
}

func (t *Tracker) FetchCandidates(context.Context, config.Effective) ([]domain.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]domain.Issue(nil), t.Issues...), t.Err
}
func (t *Tracker) FetchStatesByID(_ context.Context, ids []string) (map[string]domain.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]domain.Issue{}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, issue := range t.Issues {
		if want[issue.ID] {
			out[issue.ID] = issue
		}
	}
	return out, t.Err
}
func (t *Tracker) FetchByStates(_ context.Context, states []string) ([]domain.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	want := map[string]bool{}
	for _, s := range states {
		want[domain.NormalizeState(s)] = true
	}
	out := []domain.Issue{}
	for _, issue := range t.Issues {
		if want[domain.NormalizeState(issue.State)] {
			out = append(out, issue)
		}
	}
	return out, t.Err
}
