package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/openai/symphony/go/internal/agent"
	"github.com/openai/symphony/go/internal/config"
	"github.com/openai/symphony/go/internal/domain"
	"github.com/openai/symphony/go/internal/observability"
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
	cancelled  map[string]cancellationReason
	retries    map[string]retryItem
	readyQueue []domain.Issue
	setup      map[string]SetupSnapshot
	events     []agent.Event
	totals     domain.AgentTotals
	rateLimits map[string]any
	runHistory map[string][]domain.RunAttempt
	log        *slog.Logger
	logsRoot   string
}

func New(cfg config.Effective, tr tracker.Tracker, runner agent.Runner, wm workspace.Manager) *Orchestrator {
	return NewWithLogger(cfg, tr, runner, wm, nil)
}

func NewWithLogger(cfg config.Effective, tr tracker.Tracker, runner agent.Runner, wm workspace.Manager, log *slog.Logger) *Orchestrator {
	return &Orchestrator{cfg: cfg, tracker: tr, runner: runner, workspaces: wm, running: map[string]running{}, claimed: map[string]domain.Issue{}, attempts: map[string]int{}, completed: map[string]time.Time{}, cancelled: map[string]cancellationReason{}, retries: map[string]retryItem{}, setup: map[string]SetupSnapshot{}, rateLimits: nil, runHistory: map[string][]domain.RunAttempt{}, log: log}
}

func (o *Orchestrator) UpdateConfig(cfg config.Effective) { o.mu.Lock(); o.cfg = cfg; o.mu.Unlock() }

func (o *Orchestrator) SetLogsRoot(root string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.logsRoot = root
}

func (o *Orchestrator) Run(ctx context.Context) error {
	o.mu.Lock()
	interval := o.cfg.PollingInterval
	o.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	if o.log != nil {
		o.log.Info("orchestrator started", "polling_interval_ms", interval.Milliseconds())
	}
	if err := o.Tick(ctx); err != nil && o.log != nil {
		observability.TrackerError(o.log, err)
	}
	for {
		select {
		case <-ctx.Done():
			o.cancelAll()
			if o.log != nil {
				o.log.Info("orchestrator stopped")
			}
			return nil
		case <-ticker.C:
			if err := o.Tick(ctx); err != nil && o.log != nil {
				observability.TrackerError(o.log, err)
			}
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
	if o.log != nil {
		o.log.Info("poll completed", "candidate_count", len(candidates))
	}
	candidateMap := make(map[string]domain.Issue)
	for _, issue := range candidates {
		candidateMap[issue.ID] = issue
	}
	dueIDs := o.dueRetryIDs()
	pendingRetryIDs := o.pendingRetryIDs()
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
		if !retrySet[issue.ID] && !pendingRetryIDs[issue.ID] {
			issues = append(issues, issue)
		}
	}
	domain.SortIssuesForDispatch(issues)
	for _, issue := range issues {
		if err := o.dispatch(ctx, issue); err != nil {
			return err
		}
	}
	o.updateReadyQueue(issues)
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

	started := time.Now()
	o.recordAttempt(issue.ID, issue.Identifier, attempt, "", started, string(domain.RunAttemptPreparingWorkspace), nil)
	setupStage, setupHook := "preparing_workspace", ""
	if cfg.Hooks.AfterCreate != "" {
		setupStage, setupHook = "after_create", "after_create"
	}
	o.recordSetup(issue, attempt, setupStage, "running", setupHook, "", "", "", nil)
	if o.log != nil {
		o.log.Info("workspace preparation started", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "attempt", attempt, "workspace_root", cfg.WorkspaceRoot, "after_create_configured", cfg.Hooks.AfterCreate != "")
	}
	if err := o.workspaces.CleanupPreparationDirs(workspace.PreparationRetention); err != nil && o.log != nil {
		o.log.Warn("workspace preparation cleanup failed", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "error", err)
	}
	ws, created, err := o.workspaces.PrepareForIssue(ctx, issue.Identifier, cfg.Hooks.AfterCreate, cfg.Hooks.Timeout)
	if err != nil {
		var hookErr *workspace.PrepareHookError
		workspacePath := ""
		failedWorkspace := ""
		logs := []LogSnapshot(nil)
		if errors.As(err, &hookErr) {
			workspacePath = hookErr.FailedPath
			failedWorkspace = hookErr.FailedPath
			if failedWorkspace != "" {
				prepareErrorPath := filepath.Join(failedWorkspace, "prepare-error.txt")
				logs = append(logs, LogSnapshot{Label: "prepare-error", Path: prepareErrorPath})
			}
			if o.log != nil {
				o.log.Error("workflow hook failed", "hook", "after_create", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "attempt", attempt, "error", err, "failed_workspace", hookErr.FailedPath)
				o.log.Error("workspace preparation retained failed workspace", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "failed_workspace", hookErr.FailedPath)
			}
		} else if o.log != nil {
			o.log.Error("workspace preparation failed", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "attempt", attempt, "error", err)
		}
		o.recordSetup(issue, attempt, setupStage, "failed", setupHook, "", failedWorkspace, err.Error(), logs)
		o.failDispatchAttempt(issue, attempt, workspacePath, err)
		return nil
	}
	if o.log != nil {
		o.log.Info("workspace preparation completed", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "attempt", attempt, "workspace", ws.Path, "created", created)
		if created && cfg.Hooks.AfterCreate != "" {
			o.log.Info("workflow hook completed", "hook", "after_create", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "attempt", attempt, "workspace", ws.Path)
		}
	}
	completedStage, completedHook := setupStage, setupHook
	if !created {
		completedStage, completedHook = "preparing_workspace", ""
	}
	o.recordSetup(issue, attempt, completedStage, "completed", completedHook, ws.Path, "", "", nil)
	o.updateAttempt(issue.ID, issue.Identifier, attempt, ws.Path, string(domain.RunAttemptPreparingWorkspace), nil)
	if cfg.Hooks.BeforeRun != "" {
		o.recordSetup(issue, attempt, "before_run", "running", "before_run", ws.Path, "", "", nil)
		if o.log != nil {
			o.log.Info("workflow hook started", "hook", "before_run", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "attempt", attempt, "workspace", ws.Path)
		}
		if err := workspace.RunHook(ctx, cfg.Hooks.BeforeRun, ws.Path, cfg.Hooks.Timeout); err != nil {
			if o.log != nil {
				o.log.Error("workflow hook failed", "hook", "before_run", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "attempt", attempt, "workspace", ws.Path, "error", err)
			}
			o.recordSetup(issue, attempt, "before_run", "failed", "before_run", ws.Path, "", err.Error(), nil)
			o.failDispatchAttempt(issue, attempt, ws.Path, err)
			return nil
		}
		if o.log != nil {
			o.log.Info("workflow hook completed", "hook", "before_run", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "attempt", attempt, "workspace", ws.Path)
		}
		o.recordSetup(issue, attempt, "before_run", "completed", "before_run", ws.Path, "", "", nil)
	}
	o.updateAttempt(issue.ID, issue.Identifier, attempt, ws.Path, string(domain.RunAttemptBuildingPrompt), nil)
	o.recordSetup(issue, attempt, "building_prompt", "running", "", ws.Path, "", "", nil)
	if o.log != nil {
		o.log.Info("prompt render started", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "attempt", attempt, "workspace", ws.Path)
	}
	var attemptPtr *int
	if attempt > 0 {
		attemptPtr = &attempt
	}
	p, err := prompt.Render(cfg.PromptTemplate, issue, attemptPtr)
	if err != nil {
		if o.log != nil {
			o.log.Error("prompt render failed", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "attempt", attempt, "workspace", ws.Path, "error", err)
		}
		o.recordSetup(issue, attempt, "building_prompt", "failed", "", ws.Path, "", err.Error(), nil)
		o.failDispatchAttempt(issue, attempt, ws.Path, err)
		return nil
	}
	if o.log != nil {
		o.log.Info("prompt render completed", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "attempt", attempt, "workspace", ws.Path)
	}
	o.recordSetup(issue, attempt, "building_prompt", "completed", "", ws.Path, "", "", nil)
	if cfg.AgentKind == "pi" {
		p = fmt.Sprintf("%s: %s\n\n%s", issue.Identifier, issue.Title, p)
	}
	sessionID := fmt.Sprintf("%s-%d", domain.SanitizeWorkspaceKey(issue.Identifier), time.Now().UnixNano())
	logs := o.prepareRunLogs(issue.Identifier, sessionID)
	rctx, cancel := context.WithCancel(ctx)
	o.mu.Lock()
	runStarted := time.Now()
	o.running[issue.ID] = running{issue: issue, sessionID: sessionID, workspace: ws.Path, started: runStarted, lastEvent: runStarted, status: "running", lastEventType: "session_started", logs: logs, cancel: cancel}
	o.updateAttemptLogsLocked(issue.ID, attempt, logs)
	delete(o.claimed, issue.ID)
	delete(o.cancelled, issue.ID)
	o.mu.Unlock()
	if o.log != nil {
		observability.Dispatch(o.log, issue.ID, issue.Identifier, sessionID)
		observability.WorkerStart(o.log, issue.ID, issue.Identifier, sessionID)
		if logs.Protocol != "" || logs.Stderr != "" || logs.Result != "" {
			o.log.Info("agent logs prepared", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "session_id", sessionID, "protocol_log", logs.Protocol, "stderr_log", logs.Stderr, "result_log", logs.Result)
		}
	}
	ch := make(chan agent.Event, 32)
	go o.forwardEvents(ch)
	o.updateAttempt(issue.ID, issue.Identifier, attempt, ws.Path, string(domain.RunAttemptStreaming), nil)
	go func() {
		start := time.Now()
		res := o.runner.Run(rctx, agent.RunRequest{Issue: issue, Workspace: ws.Path, Prompt: p, Attempt: attempt, SessionID: sessionID, MaxTurns: cfg.Agent.MaxTurns, Command: agentCommand(cfg), ReadTimeout: agentReadTimeout(cfg), TurnTimeout: agentTurnTimeout(cfg), Policy: agentPolicy(cfg), EnableBeadsCLI: cfg.EnableBeadsCLI, EnableLinearGraphQL: cfg.EnableLinearGraphQL, TrackerBDCommand: cfg.TrackerBDCommand, TrackerEndpoint: cfg.TrackerEndpoint, TrackerAPIKey: cfg.TrackerAPIKey, Logs: logs}, ch)
		close(ch)
		if cfg.Hooks.AfterRun != "" {
			_ = workspace.RunHook(context.Background(), cfg.Hooks.AfterRun, ws.Path, cfg.Hooks.Timeout)
		}
		if res.SessionID != "" && res.SessionID != sessionID {
			o.mu.Lock()
			if r, ok := o.running[issue.ID]; ok {
				r.sessionID = res.SessionID
				if res.ThreadID != "" {
					r.threadID = res.ThreadID
				}
				if res.TurnID != "" {
					r.turnID = res.TurnID
				}
				o.running[issue.ID] = r
			}
			o.mu.Unlock()
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

func agentReadTimeout(cfg config.Effective) time.Duration {
	if cfg.AgentKind == "pi" {
		return cfg.Pi.ReadTimeout
	}
	return cfg.Codex.ReadTimeout
}

func agentPolicy(cfg config.Effective) any {
	if cfg.AgentKind == "pi" {
		return cfg.Pi.Policy
	}
	return cfg.Codex.Policy
}

func (o *Orchestrator) prepareRunLogs(identifier, sessionID string) domain.RunLogPaths {
	o.mu.Lock()
	root := o.logsRoot
	o.mu.Unlock()
	if root == "" {
		return domain.RunLogPaths{}
	}
	dir := filepath.Join(root, "agents", domain.SanitizeWorkspaceKey(identifier), domain.SanitizeWorkspaceKey(sessionID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		if o.log != nil {
			o.log.Warn("agent log directory unavailable", "issue_identifier", identifier, "session_id", sessionID, "path", dir, "error", err)
		}
		return domain.RunLogPaths{}
	}
	return domain.RunLogPaths{
		Protocol: filepath.Join(dir, "protocol.jsonl"),
		Stderr:   filepath.Join(dir, "stderr.log"),
		Result:   filepath.Join(dir, "result.json"),
	}
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

func (o *Orchestrator) releaseClaim(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.claimed, id)
}

func (o *Orchestrator) updateReadyQueue(issues []domain.Issue) {
	o.mu.Lock()
	defer o.mu.Unlock()
	cfg := o.cfg
	ready := make([]domain.Issue, 0, len(issues))
	for _, issue := range issues {
		if !domain.IssueIsEligible(issue, cfg) || !o.shouldDispatchCompletedLocked(issue) {
			continue
		}
		if o.running[issue.ID].sessionID != "" || o.claimed[issue.ID].ID != "" {
			continue
		}
		if _, ok := o.retries[issue.ID]; ok {
			continue
		}
		ready = append(ready, issue)
	}
	o.readyQueue = ready
}

func (o *Orchestrator) recordSetup(issue domain.Issue, attempt int, stage, status, hook, workspacePath, failedWorkspace, errText string, logs []LogSnapshot) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	startedAt := &now
	if previous, ok := o.setup[issue.ID]; ok && previous.StartedAt != nil {
		startedAt = previous.StartedAt
	}
	sn := SetupSnapshot{
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		Title:           issue.Title,
		State:           issue.State,
		Attempt:         attempt,
		Stage:           stage,
		Status:          status,
		Hook:            hook,
		Workspace:       workspacePath,
		FailedWorkspace: failedWorkspace,
		Error:           errText,
		Logs:            append([]LogSnapshot(nil), logs...),
		StartedAt:       startedAt,
		UpdatedAt:       now,
	}
	if issue.URL != nil {
		url := *issue.URL
		sn.IssueURL = &url
	}
	if len(sn.Logs) > 0 {
		sn.LogPath = sn.Logs[0].Path
	}
	o.setup[issue.ID] = sn
}

func (o *Orchestrator) failDispatchAttempt(issue domain.Issue, attempt int, workspacePath string, err error) {
	errStr := err.Error()
	o.mu.Lock()
	o.updateAttemptLocked(issue.ID, issue.Identifier, attempt, workspacePath, string(domain.RunAttemptFailed), &errStr)
	delete(o.claimed, issue.ID)
	o.attempts[issue.ID]++
	nextAttempt := o.attempts[issue.ID]
	delay := backoff(nextAttempt, o.cfg.Agent.MaxRetryBackoff)
	o.retries[issue.ID] = retryItem{issue: issue, attempt: nextAttempt, at: time.Now().Add(delay), err: errStr}
	o.mu.Unlock()
	if o.log != nil {
		observability.RetryScheduled(o.log, issue.ID, issue.Identifier, delay)
	}
}

func (o *Orchestrator) recordAttempt(issueID, identifier string, attempt int, workspace string, startedAt time.Time, status string, err *string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	entry := domain.RunAttempt{IssueID: issueID, IssueIdentifier: identifier, Attempt: attempt, WorkspacePath: workspace, StartedAt: startedAt, Status: domain.RunAttemptStatus(status), Error: err}
	o.runHistory[issueID] = append(o.runHistory[issueID], entry)
}

func (o *Orchestrator) updateAttempt(issueID, identifier string, attempt int, workspace string, status string, err *string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.updateAttemptLocked(issueID, identifier, attempt, workspace, status, err)
}

func (o *Orchestrator) updateAttemptLocked(issueID, identifier string, attempt int, workspace string, status string, err *string) {
	hist, ok := o.runHistory[issueID]
	if !ok || len(hist) == 0 {
		return
	}
	entry := hist[len(hist)-1]
	if entry.Attempt == attempt {
		if workspace != "" {
			entry.WorkspacePath = workspace
		}
		entry.Status = domain.RunAttemptStatus(status)
		entry.Error = err
		hist[len(hist)-1] = entry
		o.runHistory[issueID] = hist
	}
}

func (o *Orchestrator) updateAttemptLogsLocked(issueID string, attempt int, logs domain.RunLogPaths) {
	if logs.Protocol == "" && logs.Stderr == "" && logs.Result == "" {
		return
	}
	hist, ok := o.runHistory[issueID]
	if !ok || len(hist) == 0 {
		return
	}
	entry := hist[len(hist)-1]
	if entry.Attempt == attempt {
		entry.Logs = logs
		hist[len(hist)-1] = entry
		o.runHistory[issueID] = hist
	}
}

func (o *Orchestrator) pendingRetryIDs() map[string]bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := map[string]bool{}
	for id := range o.retries {
		out[id] = true
	}
	return out
}

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
		if ev.At.IsZero() {
			ev.At = time.Now()
		}
		o.mu.Lock()
		o.events = append(o.events, ev)
		if r := o.running[ev.IssueID]; r.sessionID != "" {
			if ev.SessionID != "" {
				r.sessionID = ev.SessionID
			}
			if ev.ThreadID != "" {
				r.threadID = ev.ThreadID
			}
			if ev.TurnID != "" {
				r.turnID = ev.TurnID
			}
			r.lastEvent = ev.At
			r.lastEventType = ev.Type
			r.lastMessage = ev.Message
			r.agentTextTail = appendAgentTextMessage(r.agentTextTail, ev, 100)
			if ev.Type == "turn_completed" || ev.Type == "turn_started" {
				r.turnCount++
			}
			if ev.RateLimits != nil {
				o.rateLimits = ev.RateLimits
			}
			if ev.Usage.TotalTokens != 0 {
				deltaIn := tokenDelta(ev.Usage.InputTokens, r.lastReportedInputTokens)
				deltaOut := tokenDelta(ev.Usage.OutputTokens, r.lastReportedOutputTokens)
				deltaTotal := tokenDelta(ev.Usage.TotalTokens, r.lastReportedTotalTokens)
				r.agentInputTokens += deltaIn
				r.agentOutputTokens += deltaOut
				r.agentTotalTokens += deltaTotal
				o.totals.TotalTokens += deltaTotal
				o.totals.InputTokens += deltaIn
				o.totals.OutputTokens += deltaOut
				r.lastReportedInputTokens = ev.Usage.InputTokens
				r.lastReportedOutputTokens = ev.Usage.OutputTokens
				r.lastReportedTotalTokens = ev.Usage.TotalTokens
			}
			o.running[ev.IssueID] = r
		}
		o.mu.Unlock()
	}
}

func tokenDelta(current, previous int) int {
	if current == 0 {
		return 0
	}
	if current >= previous {
		return current - previous
	}
	return current
}

func appendAgentTextMessage(tail []AgentTextMessage, ev agent.Event, limit int) []AgentTextMessage {
	if ev.Text == "" {
		return tail
	}
	at := ev.At
	if at.IsZero() {
		at = time.Now()
	}
	if ev.ItemID != "" {
		for i := len(tail) - 1; i >= 0; i-- {
			if tail[i].itemID != ev.ItemID {
				continue
			}
			if ev.Type == "item_agentMessage_delta" {
				tail[i].Text += ev.Text
			} else {
				tail[i].Text = ev.Text
			}
			tail[i].At = at
			tail[i].Event = ev.Type
			return trimAgentTextTail(tail, limit)
		}
		tail = append(tail, AgentTextMessage{At: at, Event: ev.Type, Text: ev.Text, itemID: ev.ItemID})
		return trimAgentTextTail(tail, limit)
	}
	if len(tail) > 0 && ev.Type == "item_agentMessage_delta" && tail[len(tail)-1].Event == ev.Type && tail[len(tail)-1].itemID == "" {
		tail[len(tail)-1].Text += ev.Text
		tail[len(tail)-1].At = at
		return trimAgentTextTail(tail, limit)
	}
	tail = append(tail, AgentTextMessage{At: at, Event: ev.Type, Text: ev.Text})
	return trimAgentTextTail(tail, limit)
}

func trimAgentTextTail(tail []AgentTextMessage, limit int) []AgentTextMessage {
	if limit <= 0 || len(tail) <= limit {
		return tail
	}
	return append([]AgentTextMessage(nil), tail[len(tail)-limit:]...)
}

func (o *Orchestrator) workerExit(issue domain.Issue, res agent.Result, elapsed time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.running[issue.ID]
	delete(o.running, issue.ID)
	cancelReason, wasCancelled := o.cancelled[issue.ID]
	if wasCancelled {
		delete(o.cancelled, issue.ID)
	}
	r.status = "succeeded"
	r.error = nil
	if !res.Completed {
		r.status = "failed"
		if res.Err == nil {
			res.Err = fmt.Errorf("agent exited without terminal event")
		}
	}
	if res.Err != nil {
		r.status = "failed"
		errStr := res.Err.Error()
		r.error = &errStr
	}
	if wasCancelled {
		r.status = string(cancelReason.status)
		r.error = nil
		if cancelReason.err != "" {
			errStr := cancelReason.err
			r.error = &errStr
		}
	}
	if res.Logs.Protocol != "" || res.Logs.Stderr != "" || res.Logs.Result != "" {
		r.logs = res.Logs
	}
	o.totals.SecondsRunning += elapsed.Seconds()
	attempt := o.attempts[issue.ID]
	finalStatus := r.status
	hist, ok := o.runHistory[issue.ID]
	if ok && len(hist) > 0 {
		entry := hist[len(hist)-1]
		if entry.Attempt == attempt {
			entry.Status = domain.RunAttemptStatus(finalStatus)
			entry.Error = r.error
			entry.Logs = r.logs
			hist[len(hist)-1] = entry
			o.runHistory[issue.ID] = hist
		}
	}
	if wasCancelled && !cancelReason.retry {
		if o.log != nil {
			o.log.Info("worker exit", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "session_id", r.sessionID, "status", finalStatus)
		}
		return
	}
	if res.Completed && !wasCancelled {
		o.completed[issue.ID] = time.Now()
		o.retries[issue.ID] = retryItem{issue: issue, attempt: 1, at: time.Now().Add(time.Second)}
		if o.log != nil {
			o.log.Info("worker exit", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "session_id", r.sessionID, "status", finalStatus, "completed", true)
			observability.RetryScheduled(o.log, issue.ID, issue.Identifier, time.Second)
		}
		return
	}
	o.attempts[issue.ID]++
	delay := time.Second
	if res.Err != nil || (wasCancelled && cancelReason.retry) {
		delay = backoff(o.attempts[issue.ID], o.cfg.Agent.MaxRetryBackoff)
	}
	errStr := ""
	if r.error != nil {
		errStr = *r.error
	}
	o.retries[issue.ID] = retryItem{issue: issue, attempt: o.attempts[issue.ID], at: time.Now().Add(delay), err: errStr}
	if o.log != nil {
		o.log.Info("worker exit", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "session_id", r.sessionID, "status", finalStatus, "error", r.error)
		observability.RetryScheduled(o.log, issue.ID, issue.Identifier, delay)
	}
}

func (o *Orchestrator) reconcile(ctx context.Context) error {
	o.mu.Lock()
	cfg := o.cfg
	now := time.Now()
	ids := make([]string, 0, len(o.running))
	for id, r := range o.running {
		if stall := agentStallTimeout(cfg); stall > 0 && now.Sub(r.lastEvent) > stall {
			errStr := fmt.Sprintf("stalled: no agent event for %s", now.Sub(r.lastEvent).Round(time.Millisecond))
			r.status = string(domain.RunAttemptStalled)
			r.error = &errStr
			o.running[id] = r
			o.cancelled[id] = cancellationReason{status: domain.RunAttemptStalled, err: errStr, retry: true}
			if o.log != nil {
				observability.Reconciliation(o.log, id, r.issue.Identifier, "stall_timeout")
			}
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
				o.cancelled[id] = cancellationReason{status: domain.RunAttemptCanceled}
				o.completed[id] = time.Now()
				if o.log != nil {
					observability.Reconciliation(o.log, id, r.issue.Identifier, "terminal_cancel")
				}
				go func(identifier string, hooks domain.HooksConfig) {
					_ = o.workspaces.RemoveForIssue(context.Background(), identifier, hooks.BeforeRemove, hooks.Timeout)
				}(r.issue.Identifier, cfg.Hooks)
			} else if !containsNorm(cfg.ActiveStates, issue.State) || !domain.IssueIsEligible(issue, cfg) {
				r.cancel()
				o.cancelled[id] = cancellationReason{status: domain.RunAttemptCanceled}
				if o.log != nil {
					observability.Reconciliation(o.log, id, r.issue.Identifier, "inactive_cancel")
				}
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
		o.cancelled[id] = cancellationReason{status: domain.RunAttemptCanceled}
		r.cancel()
	}
}

func (o *Orchestrator) Snapshot() Snapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	setupRows := o.visibleSetupLocked()
	s := Snapshot{
		GeneratedAt: time.Now(),
		Counts:      map[string]int{"ready": len(o.readyQueue), "setup": len(setupRows), "running": len(o.running), "retrying": len(o.retries), "completed": len(o.completed)},
		AgentTotals: &o.totals,
		RateLimits:  o.rateLimits,
	}
	for _, issue := range o.readyQueue {
		s.Ready = append(s.Ready, issueQueueSnapshot(issue))
	}
	s.Setup = setupRows
	for _, r := range o.running {
		s.Running = append(s.Running, o.runningSnapshotLocked(r))
	}
	for _, r := range o.retries {
		s.Retrying = append(s.Retrying, o.retrySnapshotLocked(r))
	}
	s.RetryQueue = append([]RetrySnapshot(nil), s.Retrying...)
	return s
}

func (o *Orchestrator) IssueSnapshot(identifier string) (IssueDetailSnapshot, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range o.running {
		if r.issue.Identifier == identifier {
			sn := o.runningSnapshotLocked(r)
			return o.issueDetailLocked(r.issue, "running", &sn, nil), true
		}
	}
	for _, r := range o.retries {
		if r.issue.Identifier == identifier {
			sn := o.retrySnapshotLocked(r)
			return o.issueDetailLocked(r.issue, "retrying", nil, &sn), true
		}
	}
	for _, setup := range o.visibleSetupLocked() {
		if setup.IssueIdentifier == identifier {
			return o.issueDetailLocked(issueFromSetup(setup), "setup", nil, nil), true
		}
	}
	return IssueDetailSnapshot{}, false
}

func (o *Orchestrator) runningSnapshotLocked(r running) RunningSnapshot {
	logs := logsSnapshot(r.logs)
	sn := RunningSnapshot{
		IssueID: r.issue.ID, IssueIdentifier: r.issue.Identifier, SessionID: r.sessionID,
		ThreadID: r.threadID, TurnID: r.turnID,
		Title: r.issue.Title, Workspace: r.workspace, TurnCount: r.turnCount, State: r.issue.State, Status: r.status,
		LastEvent: r.lastEventType, LastMessage: r.lastMessage, LogPath: r.logs.Protocol,
		RecentAgentMessages: append([]AgentTextMessage(nil), r.agentTextTail...),
		StartedAt:           &r.started, LastEventAt: &r.lastEvent,
	}
	if len(logs.CodexSessionLogs) > 0 {
		sn.Logs = &logs
	}
	if r.issue.URL != nil {
		url := *r.issue.URL
		sn.IssueURL = &url
	}
	if r.error != nil {
		sn.Error = *r.error
	}
	if r.agentTotalTokens != 0 || r.agentInputTokens != 0 || r.agentOutputTokens != 0 {
		sn.Tokens = &domain.TokenUsage{
			InputTokens: r.agentInputTokens, OutputTokens: r.agentOutputTokens, TotalTokens: r.agentTotalTokens,
		}
	}
	attempts := o.attemptsSnapshotLocked(r.issue.ID)
	if attempts.RestartCount != 0 || attempts.CurrentRetryAttempt != 0 {
		sn.Attempts = &attempts
	}
	sn.Setup = o.setupSnapshotLocked(r.issue.ID)
	return sn
}

func issueQueueSnapshot(issue domain.Issue) IssueQueueSnapshot {
	sn := IssueQueueSnapshot{
		IssueID: issue.ID, IssueIdentifier: issue.Identifier, Title: issue.Title, State: issue.State,
		Priority: issue.Priority,
	}
	if issue.URL != nil {
		url := *issue.URL
		sn.IssueURL = &url
	}
	return sn
}

func (o *Orchestrator) attemptsSnapshotLocked(issueID string) AttemptsSnapshot {
	attempt := o.attempts[issueID]
	if attempt <= 0 {
		return AttemptsSnapshot{}
	}
	return AttemptsSnapshot{RestartCount: attempt - 1, CurrentRetryAttempt: attempt}
}

func (o *Orchestrator) retrySnapshotLocked(r retryItem) RetrySnapshot {
	due := r.at
	sn := RetrySnapshot{IssueID: r.issue.ID, IssueIdentifier: r.issue.Identifier, Attempt: r.attempt, DueAt: r.at, At: &due, Error: r.err}
	if r.issue.URL != nil {
		url := *r.issue.URL
		sn.IssueURL = &url
	}
	sn.Setup = o.setupSnapshotLocked(r.issue.ID)
	return sn
}

func (o *Orchestrator) issueDetailLocked(issue domain.Issue, status string, running *RunningSnapshot, retry *RetrySnapshot) IssueDetailSnapshot {
	workspace := o.workspaceSnapshotLocked(issue.ID, running)
	lastError := o.lastErrorLocked(issue.ID, running, retry)
	logs := o.logsSnapshotLocked(issue.ID, running)
	return IssueDetailSnapshot{
		IssueIdentifier:     issue.Identifier,
		IssueID:             issue.ID,
		Status:              status,
		Workspace:           workspace,
		Attempts:            o.attemptsSnapshotLocked(issue.ID),
		Running:             running,
		Retry:               retry,
		Logs:                logs,
		RecentAgentMessages: o.recentAgentMessagesLocked(issue.ID, 100),
		RecentEvents:        o.recentEventsLocked(issue.ID, 20),
		LastError:           lastError,
		Setup:               o.setupSnapshotLocked(issue.ID),
		Tracked:             trackedIssue(issue),
	}
}

func (o *Orchestrator) visibleSetupLocked() []SetupSnapshot {
	rows := []SetupSnapshot{}
	for id, setup := range o.setup {
		if setup.Status != "running" && setup.Status != "failed" {
			continue
		}
		if r := o.running[id]; r.sessionID != "" && setup.Status != "failed" {
			continue
		}
		rows = append(rows, setup)
	}
	return rows
}

func (o *Orchestrator) setupSnapshotLocked(issueID string) *SetupSnapshot {
	setup, ok := o.setup[issueID]
	if !ok {
		return nil
	}
	cp := setup
	cp.Logs = append([]LogSnapshot(nil), setup.Logs...)
	return &cp
}

func issueFromSetup(setup SetupSnapshot) domain.Issue {
	return domain.Issue{
		ID:         setup.IssueID,
		Identifier: setup.IssueIdentifier,
		Title:      setup.Title,
		State:      setup.State,
		URL:        setup.IssueURL,
	}
}

func (o *Orchestrator) logsSnapshotLocked(issueID string, running *RunningSnapshot) LogsSnapshot {
	if running != nil && running.Logs != nil {
		return *running.Logs
	}
	hist := o.runHistory[issueID]
	for i := len(hist) - 1; i >= 0; i-- {
		logs := logsSnapshot(hist[i].Logs)
		if len(logs.CodexSessionLogs) > 0 {
			return logs
		}
	}
	return LogsSnapshot{CodexSessionLogs: []LogSnapshot{}}
}

func logsSnapshot(paths domain.RunLogPaths) LogsSnapshot {
	out := LogsSnapshot{CodexSessionLogs: []LogSnapshot{}}
	for _, item := range []struct {
		label string
		path  string
	}{
		{label: "protocol", path: paths.Protocol},
		{label: "stderr", path: paths.Stderr},
		{label: "result", path: paths.Result},
	} {
		if item.path != "" {
			out.CodexSessionLogs = append(out.CodexSessionLogs, LogSnapshot{Label: item.label, Path: item.path})
		}
	}
	return out
}

func (o *Orchestrator) workspaceSnapshotLocked(issueID string, running *RunningSnapshot) *WorkspaceSnapshot {
	if running != nil && running.Workspace != "" {
		return &WorkspaceSnapshot{Path: running.Workspace}
	}
	hist := o.runHistory[issueID]
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].WorkspacePath != "" {
			return &WorkspaceSnapshot{Path: hist[i].WorkspacePath}
		}
	}
	return nil
}

func (o *Orchestrator) lastErrorLocked(issueID string, running *RunningSnapshot, retry *RetrySnapshot) *string {
	if running != nil && running.Error != "" {
		err := running.Error
		return &err
	}
	if retry != nil && retry.Error != "" {
		err := retry.Error
		return &err
	}
	hist := o.runHistory[issueID]
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Error != nil && *hist[i].Error != "" {
			return hist[i].Error
		}
	}
	return nil
}

func (o *Orchestrator) recentEventsLocked(issueID string, limit int) []EventSnapshot {
	events := []EventSnapshot{}
	for i := len(o.events) - 1; i >= 0 && len(events) < limit; i-- {
		ev := o.events[i]
		if ev.IssueID != issueID {
			continue
		}
		at := ev.At
		if at.IsZero() {
			at = time.Now()
		}
		events = append(events, EventSnapshot{At: at, Event: ev.Type, Message: ev.Message})
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events
}

func (o *Orchestrator) recentAgentMessagesLocked(issueID string, limit int) []AgentTextMessage {
	tail := []AgentTextMessage{}
	for _, ev := range o.events {
		if ev.IssueID != issueID {
			continue
		}
		tail = appendAgentTextMessage(tail, ev, limit)
	}
	return tail
}

func trackedIssue(issue domain.Issue) map[string]any {
	tracked := map[string]any{
		"title": issue.Title,
		"state": issue.State,
	}
	if issue.Assignee != nil {
		tracked["assignee"] = *issue.Assignee
	}
	if issue.Priority != nil {
		tracked["priority"] = *issue.Priority
	}
	if len(issue.Labels) > 0 {
		tracked["labels"] = append([]string(nil), issue.Labels...)
	}
	return tracked
}
