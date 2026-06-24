package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/fsnotify/fsnotify"
	"github.com/openai/symphony/go/internal/agent"
	"github.com/openai/symphony/go/internal/agent/codex"
	"github.com/openai/symphony/go/internal/agent/pi"
	"github.com/openai/symphony/go/internal/config"
	"github.com/openai/symphony/go/internal/domain"
	"github.com/openai/symphony/go/internal/observability"
	"github.com/openai/symphony/go/internal/orchestrator"
	"github.com/openai/symphony/go/internal/server"
	"github.com/openai/symphony/go/internal/tracker"
	"github.com/openai/symphony/go/internal/tracker/beads"
	"github.com/openai/symphony/go/internal/tracker/jira"
	"github.com/openai/symphony/go/internal/tracker/linear"
	"github.com/openai/symphony/go/internal/workflow"
	"github.com/openai/symphony/go/internal/workspace"
)

type Options struct {
	WorkflowPath string
	LogsRoot     string
	Port         int
	PortSet      bool
	Logger       *slog.Logger
	Tracker      tracker.Tracker
	Runner       agent.Runner
}

type App struct {
	Opt  Options
	Orch *orchestrator.Orchestrator
	cfg  config.Effective
}

func New(ctx context.Context, opt Options) (*App, error) {
	if opt.WorkflowPath == "" {
		opt.WorkflowPath = "WORKFLOW.md"
	}
	wf, err := workflow.Load(opt.WorkflowPath)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Resolve(wf, opt.WorkflowPath)
	if err != nil {
		return nil, err
	}
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}
	tr := opt.Tracker
	if tr == nil {
		tr, err = newTracker(cfg)
		if err != nil {
			return nil, err
		}
	}
	runner := opt.Runner
	if runner == nil {
		runner = newRunner(cfg)
	}
	wm := workspace.NewManager(cfg.WorkspaceRoot)
	startupCleanup(ctx, cfg, tr, wm, opt.Logger)
	o := orchestrator.NewWithLogger(cfg, tr, runner, wm, opt.Logger)
	o.SetLogsRoot(opt.LogsRoot)
	app := &App{Opt: opt, Orch: o, cfg: cfg}
	go app.watch(ctx)
	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	var srv *http.Server
	port := a.Opt.Port
	portSet := a.Opt.PortSet
	if !portSet && a.cfg.ServerPortSet {
		port = a.cfg.ServerPort
		portSet = true
	}
	if portSet {
		if port < 0 {
			return fmt.Errorf("server.port must be non-negative")
		}
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return fmt.Errorf("start status server: %w", err)
		}
		srv = &http.Server{Handler: server.New(a.Orch)}
		if a.Opt.Logger != nil {
			a.Opt.Logger.Info("status server starting", "addr", ln.Addr().String())
		}
		go func() {
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed && a.Opt.Logger != nil {
				a.Opt.Logger.Error("status server failed", "error", err)
			}
		}()
	}
	err := a.Orch.Run(ctx)
	if srv != nil {
		_ = srv.Shutdown(context.Background())
	}
	return err
}

func (a *App) watch(ctx context.Context) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer w.Close()
	_ = w.Add(a.Opt.WorkflowPath)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.Events:
			wf, err := workflow.Load(a.Opt.WorkflowPath)
			if err != nil {
				if a.Opt.Logger != nil {
					observability.ReloadError(a.Opt.Logger, err)
				}
				continue
			}
			cfg, err := config.Resolve(wf, a.Opt.WorkflowPath)
			if err != nil || config.Validate(cfg) != nil {
				if a.Opt.Logger != nil {
					observability.ReloadError(a.Opt.Logger, err)
				}
				continue
			}
			a.Orch.UpdateConfig(cfg)
			a.cfg = cfg
		case <-w.Errors:
		}
	}
}
func startupCleanup(ctx context.Context, cfg config.Effective, tr tracker.Tracker, wm workspace.Manager, log *slog.Logger) {
	if err := wm.CleanupPreparationDirs(workspace.PreparationRetention); err != nil && log != nil {
		log.Warn("workspace preparation cleanup failed", "error", err)
	}
	if keyFetcher, ok := tr.(interface {
		FetchStatesByIdentifier(context.Context, []string) (map[string]domain.Issue, error)
	}); ok {
		identifiers, err := wm.ListIssueIdentifiers()
		if err != nil {
			if log != nil {
				log.Warn("startup cleanup skipped", "error", err)
			}
			return
		}
		if log != nil {
			log.Info("startup cleanup checked existing workspaces", "workspace_count", len(identifiers))
		}
		issues, err := keyFetcher.FetchStatesByIdentifier(ctx, identifiers)
		if err != nil {
			if log != nil {
				log.Warn("startup cleanup skipped", "error", err)
			}
			return
		}
		for identifier, issue := range issues {
			if containsNorm(cfg.TerminalStates, issue.State) {
				if log != nil {
					log.Info("startup cleanup removing terminal workspace", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "state", issue.State)
				}
				if err := wm.RemoveForIssue(ctx, identifier, cfg.Hooks.BeforeRemove, cfg.Hooks.Timeout); err != nil && log != nil {
					log.Warn("workspace cleanup failed", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "error", err)
				}
			}
		}
		return
	}
	issues, err := tr.FetchByStates(ctx, cfg.TerminalStates)
	if err != nil {
		if log != nil {
			log.Warn("startup cleanup skipped", "error", err)
		}
		return
	}
	for _, issue := range issues {
		if log != nil {
			log.Info("startup cleanup removing terminal workspace", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "state", issue.State)
		}
		if err := wm.RemoveForIssue(ctx, issue.Identifier, cfg.Hooks.BeforeRemove, cfg.Hooks.Timeout); err != nil && log != nil {
			log.Warn("workspace cleanup failed", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "error", err)
		}
	}
}

func containsNorm(values []string, state string) bool {
	for _, value := range values {
		if domain.NormalizeState(value) == domain.NormalizeState(state) {
			return true
		}
	}
	return false
}

func newTracker(cfg config.Effective) (tracker.Tracker, error) {
	switch cfg.TrackerKind {
	case "linear":
		return linear.New(cfg.TrackerEndpoint, cfg.TrackerAPIKey, cfg.TrackerProjectSlug), nil
	case "beads":
		return &beads.Tracker{Command: cfg.TrackerBDCommand, WorkDir: cfg.WorkflowDir}, nil
	case "jira":
		projectKey := cfg.TrackerProjectKey
		if projectKey == "" {
			projectKey = cfg.TrackerProjectSlug
		}
		return jira.New(cfg.TrackerEndpoint, cfg.TrackerEmail, cfg.TrackerAPIKey, projectKey, cfg.TrackerJQL, cfg.TrackerPageSize), nil
	default:
		return nil, fmt.Errorf("unsupported tracker.kind %q", cfg.TrackerKind)
	}
}
func newRunner(cfg config.Effective) agent.Runner {
	if cfg.AgentKind == "pi" {
		return pi.New(piCommand(cfg.Pi))
	}
	return codex.New(cfg.Codex.Command)
}

func piCommand(cfg domain.PiConfig) string {
	cmd := cfg.Command
	if cfg.Provider != "" {
		cmd += " --provider " + strconv.Quote(cfg.Provider)
	}
	if cfg.Model != "" {
		cmd += " --model " + strconv.Quote(cfg.Model)
	}
	return cmd
}
