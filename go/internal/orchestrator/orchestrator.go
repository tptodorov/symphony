package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/openai/symphony/go/internal/agent"
	"github.com/openai/symphony/go/internal/config"
	"github.com/openai/symphony/go/internal/domain"
	"github.com/openai/symphony/go/internal/prompt"
	"github.com/openai/symphony/go/internal/tracker"
	"github.com/openai/symphony/go/internal/workspace"
)

type Orchestrator struct {
	cfg        config.Effective
	tracker    tracker.Tracker
	runner     agent.Runner
	workspaces workspace.Manager
	mu         sync.Mutex
	running    map[string]running
	claimed    map[string]domain.Issue
	attempts   map[string]int
	completed  map[string]time.Time
	cancelled  map[string]bool
	retries    map[string]retryItem
	events     []agent.Event
	totals     domain.AgentTotals
	rateLimits map[string]any
}

func New(cfg config.Effective, tr tracker.Tracker, runner agent.Runner, wm workspace.Manager) *Orchestrator {
	return &Orchestrator{cfg: cfg, tracker: tr, runner: runner, workspaces: wm, running: map[string]running{}, claimed: map[string]domain.Issue{}, attempts: map[string]int{}, completed: map[string]time.Time{}, cancelled: map[string]bool{}, retries: map[string]retryItem{}, rateLimits: nil}
}

func (o *Orchestrator) UpdateConfig(cfg config.Effective) { o.mu.Lock(); o.cfg = cfg; o.mu.Unlock() }

func (o *Orchestrator) Run(ctx context.Context) error {
	o.mu.Lock()
	interval := o.cfg.PollingInterval
	o.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	_ = o.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			o.cancelAll()
			return nil
		case <-ticker.C:
			_ = o.Tick(ctx)
			o.mu.Lock()
			if o.cfg.PollingInterval != interval {
				interval = o.cfg.PollingInterval
				ticker.Reset(interval)
			}
			o.mu.Unlock()
		}
	}
}

func (o *Orchestrator) Tick(ctx context.Context) error {
	if err := o.reconcile(ctx); err != nil {
		return err
	}
	o.mu.Lock()
	cfg := o.cfg
	o.mu.Unlock()
	candidates, err := o.tracker.FetchCandidates(ctx, cfg)
	if err != nil {
		return fmt.Errorf("fetch candidates: %w", err)
	}
	candidateMap := make(map[string]domain.Issue)
	for _, issue := range candidates {
		candidateMap[issue.ID] = issue
	}
	dueIDs := o.dueRetryIDs()
	issues := make([]domain.Issue, 0, len(dueIDs)+len(candidates))
	retrySet := make(map[string]bool)
	for _, id := range dueIDs {
		retrySet[id] = true
		if issue, ok := candidateMap[id]; ok {
			issues = append(issues, issue)
		} else {
			o.releaseClaim(id)
		}
	}
	for _, issue := range candidates {
		if !retrySet[issue.ID] {
			issues = append(issues, issue)
		}
	}
	domain.SortIssuesForDispatch(issues)
	for _, issue := range issues {
		if err := o.dispatch(ctx, issue); err != nil {
			return err
		}
	}
	return nil
}

func (o *Orchestrator) dispatch(ctx context.Context, issue domain.Issue) error {
	o.mu.Lock()
	cfg := o.cfg
	if !domain.IssueIsEligible(issue, cfg) || !o.shouldDispatchCompletedLocked(issue) || o.running[issue.ID].sessionID != "" || o.claimed[issue.ID].ID != "" || len(o.running) >= cfg.Agent.MaxConcurrentAgents || !o.stateSlotLocked(issue.State, cfg) {
		o.mu.Unlock()
		return nil
	}
	o.claimed[issue.ID] = issue
	attempt := o.attempts[issue.ID]
	o.mu.Unlock()

	ws, created, err := o.workspaces.CreateForIssue(issue.Identifier)
	if err != nil {
		o.releaseClaim(issue.ID)
		return err
	}
	if created && cfg.Hooks.AfterCreate != "" {
		if err := workspace.RunHook(ctx, cfg.Hooks.AfterCreate, ws.Path, cfg.Hooks.Timeout); err != nil {
			o.releaseClaim(issue.ID)
			return err
		}
	}
	if cfg.Hooks.BeforeRun != "" {
		if err := workspace.RunHook(ctx, cfg.Hooks.BeforeRun, ws.Path, cfg.Hooks.Timeout); err != nil {
			o.releaseClaim(issue.ID)
			return err
		}
	}
	var attemptPtr *int
	if attempt > 0 {
		attemptPtr = &attempt
	}
	p, err := prompt.Render(cfg.PromptTemplate, issue, attemptPtr)
	if err != nil {
		o.releaseClaim(issue.ID)
		return err
	}
	if cfg.AgentKind == "pi" {
		p = fmt.Sprintf("%s: %s\n\n%s", issue.Identifier, issue.Title, p)
	}
	sessionID := fmt.Sprintf("%s-%d", domain.SanitizeWorkspaceKey(issue.Identifier), time.Now().UnixNano())
	rctx, cancel := context.WithCancel(ctx)
	o.mu.Lock()
	o.running[issue.ID] = running{issue: issue, sessionID: sessionID, workspace: ws.Path, started: time.Now(), lastEvent: time.Now(), status: "running", cancel: cancel}
	delete(o.claimed, issue.ID)
	delete(o.cancelled, issue.ID)
	o.mu.Unlock()
	ch := make(chan agent.Event, 32)
	go o.forwardEvents(ch)
	go func() {
		start := time.Now()
		res := o.runner.Run(rctx, agent.RunRequest{Issue: issue, Workspace: ws.Path, Prompt: p, Attempt: attempt, SessionID: sessionID, MaxTurns: cfg.Agent.MaxTurns, Command: agentCommand(cfg), TurnTimeout: agentTurnTimeout(cfg), Policy: agentPolicy(cfg)}, ch)
		close(ch)
		if cfg.Hooks.AfterRun != "" {
			_ = workspace.RunHook(context.Background(), cfg.Hooks.AfterRun, ws.Path, cfg.Hooks.Timeout)
		}
		o.workerExit(issue, res, time.Since(start))
	}()
	return nil
}

func agentCommand(cfg config.Effective) string {
	if cfg.AgentKind == "pi" {
		return cfg.Pi.Command
	}
	return cfg.Codex.Command
}

func agentTurnTimeout(cfg config.Effective) time.Duration {
	if cfg.AgentKind == "pi" {
		return cfg.Pi.TurnTimeout
	}
	return cfg.Codex.TurnTimeout
}

func agentPolicy(cfg config.Effective) any {
	if cfg.AgentKind == "pi" {
		return cfg.Pi.Policy
	}
	return cfg.Codex.Policy
}

func agentStallTimeout(cfg config.Effective) time.Duration {
	if cfg.AgentKind == "pi" {
		return cfg.Pi.StallTimeout
	}
	return cfg.Codex.StallTimeout
}

func (o *Orchestrator) shouldDispatchCompletedLocked(issue domain.Issue) bool {
	completedAt, ok := o.completed[issue.ID]
	if !ok {
		return true
	}
	if issue.UpdatedAt != nil && issue.UpdatedAt.After(completedAt) {
		delete(o.completed, issue.ID)
		return true
	}
	return false
}

func (o *Orchestrator) stateSlotLocked(state string, cfg config.Effective) bool {
	limit := cfg.PerStateConcurrency[domain.NormalizeState(state)]
	if limit <= 0 {
		return true
	}
	used := 0
	for _, r := range o.running {
		if domain.NormalizeState(r.issue.State) == domain.NormalizeState(state) {
			used++
		}
	}
	return used < limit
}

func (o *Orchestrator) releaseClaim(id string) { o.mu.Lock(); delete(o.claimed, id); o.mu.Unlock() }

func (o *Orchestrator) dueRetryIDs() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	out := []string{}
	for id, r := range o.retries {
		if !r.at.After(now) {
			out = append(out, id)
			o.attempts[id] = r.attempt
			delete(o.retries, id)
			delete(o.completed, id)
		}
	}
	return out
}

func (o *Orchestrator) forwardEvents(ch <-chan agent.Event) {
	for ev := range ch {
		o.mu.Lock()
		o.events = append(o.events, ev)
		if r := o.running[ev.IssueID]; r.sessionID != "" {
			r.lastEvent = time.Now()
			if ev.Type == "turn_completed" || ev.Type == "turn_started" {
				r.turnCount++
			}
			if ev.RateLimits != nil {
				o.rateLimits = ev.RateLimits
			}
			if ev.Usage.TotalTokens != 0 {
				r.lastReportedInputTokens = ev.Usage.InputTokens
				r.lastReportedOutputTokens = ev.Usage.OutputTokens
				r.lastReportedTotalTokens = ev.Usage.TotalTokens
			}
			o.running[ev.IssueID] = r
		}
		o.mu.Unlock()
	}
}

func (o *Orchestrator) workerExit(issue domain.Issue, res agent.Result, elapsed time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.running[issue.ID]
	delete(o.running, issue.ID)
	r.status = "succeeded"
	r.error = nil
	if !res.Completed {
		r.status = "failed"
	}
	if res.Err != nil {
		r.status = "failed"
		errStr := res.Err.Error()
		r.error = &errStr
	}
	o.totals.InputTokens += res.Usage.InputTokens
	o.totals.OutputTokens += res.Usage.OutputTokens
	o.totals.TotalTokens += res.Usage.TotalTokens
	o.totals.SecondsRunning += elapsed.Seconds()
	if o.cancelled[issue.ID] {
		delete(o.cancelled, issue.ID)
		return
	}
	if res.Completed {
		o.completed[issue.ID] = time.Now()
		o.retries[issue.ID] = retryItem{issue: issue, attempt: 1, at: time.Now().Add(time.Second)}
		return
	}
	o.attempts[issue.ID]++
	delay := time.Second
	if res.Err != nil {
		delay = backoff(o.attempts[issue.ID], o.cfg.Agent.MaxRetryBackoff)
	}
	o.retries[issue.ID] = retryItem{issue: issue, attempt: o.attempts[issue.ID], at: time.Now().Add(delay)}
}

func (o *Orchestrator) reconcile(ctx context.Context) error {
	o.mu.Lock()
	cfg := o.cfg
	now := time.Now()
	ids := make([]string, 0, len(o.running))
	for id, r := range o.running {
		if stall := agentStallTimeout(cfg); stall > 0 && now.Sub(r.lastEvent) > stall {
			r.cancel()
			continue
		}
		ids = append(ids, id)
	}
	o.mu.Unlock()
	if len(ids) == 0 {
		return nil
	}
	states, err := o.tracker.FetchStatesByID(ctx, ids)
	if err != nil {
		return fmt.Errorf("refresh states: %w", err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for id, r := range o.running {
		if issue, ok := states[id]; ok {
			if containsNorm(cfg.TerminalStates, issue.State) {
				r.cancel()
				o.cancelled[id] = true
				o.completed[id] = time.Now()
				go func(identifier string, hooks domain.HooksConfig) {
					_ = o.workspaces.RemoveForIssue(context.Background(), identifier, hooks.BeforeRemove, hooks.Timeout)
				}(r.issue.Identifier, cfg.Hooks)
			} else if !containsNorm(cfg.ActiveStates, issue.State) || !domain.IssueIsEligible(issue, cfg) {
				r.cancel()
				o.cancelled[id] = true
			} else {
				r.issue = issue
				o.running[id] = r
			}
		}
	}
	return nil
}

func containsNorm(values []string, s string) bool {
	for _, v := range values {
		if domain.NormalizeState(v) == domain.NormalizeState(s) {
			return true
		}
	}
	return false
}

func (o *Orchestrator) cancelAll() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for id, r := range o.running {
		o.cancelled[id] = true
		r.cancel()
	}
}

func (o *Orchestrator) Snapshot() Snapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := Snapshot{
		GeneratedAt: time.Now(),
		Counts:      map[string]int{"running": len(o.running), "retrying": len(o.retries), "completed": len(o.completed)},
		AgentTotals: &o.totals,
		RateLimits:  o.rateLimits,
	}
	for _, r := range o.running {
		sn := RunningSnapshot{
			IssueID: r.issue.ID, IssueIdentifier: r.issue.Identifier, SessionID: r.sessionID,
			Workspace: r.workspace, TurnCount: r.turnCount, Status: r.status,
			LastEvent: r.lastEvent.Format(time.RFC3339), StartedAt: &r.started, LastEventAt: &r.lastEvent,
		}
		if r.issue.URL != nil {
			url := *r.issue.URL
			sn.IssueURL = &url
		}
		if r.error != nil {
			sn.Error = *r.error
		}
		if r.lastReportedTotalTokens != 0 || r.lastReportedInputTokens != 0 || r.lastReportedOutputTokens != 0 {
			sn.Tokens = &domain.TokenUsage{
				InputTokens: r.lastReportedInputTokens, OutputTokens: r.lastReportedOutputTokens, TotalTokens: r.lastReportedTotalTokens,
			}
		}
		s.Running = append(s.Running, sn)
	}
	for _, r := range o.retries {
		err := ""
		if r.issue.UpdatedAt != nil && !r.issue.UpdatedAt.IsZero() {
			_ = r.issue.UpdatedAt
		}
		sn := RetrySnapshot{IssueID: r.issue.ID, IssueIdentifier: r.issue.Identifier, Attempt: r.attempt, At: r.at, Error: err}
		s.RetryQueue = append(s.RetryQueue, sn)
	}
	return s
}

func (o *Orchestrator) IssueSnapshot(identifier string) (domain.Issue, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range o.running {
		if r.issue.Identifier == identifier {
			return r.issue, true
		}
	}
	for _, r := range o.retries {
		if r.issue.Identifier == identifier {
			return r.issue, true
		}
	}
	return domain.Issue{}, false
}
