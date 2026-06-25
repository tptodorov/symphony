package fake

import (
	"context"
	"sync"

	"github.com/tptodorov/symphony/go/internal/agent"
)

type Runner struct {
	mu       sync.Mutex
	Requests []agent.RunRequest
	Result   agent.Result
}

func (r *Runner) Run(ctx context.Context, req agent.RunRequest, events chan<- agent.Event) agent.Result {
	r.mu.Lock()
	r.Requests = append(r.Requests, req)
	res := r.Result
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		res.Err = ctx.Err()
		return res
	default:
	}
	res.SessionID = req.SessionID
	return res
}
func (r *Runner) Count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.Requests) }
