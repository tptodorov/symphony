package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openai/symphony/go/internal/agent"
	agentfake "github.com/openai/symphony/go/internal/agent/fake"
	"github.com/openai/symphony/go/internal/config"
	"github.com/openai/symphony/go/internal/domain"
	"github.com/openai/symphony/go/internal/orchestrator"
	trackerfake "github.com/openai/symphony/go/internal/tracker/fake"
	"github.com/openai/symphony/go/internal/workspace"
)

func TestState(t *testing.T) {
	cfg := config.Defaults()
	o := orchestrator.New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	rr := httptest.NewRecorder()
	New(o).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/state", nil))
	if rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["retrying"]; !ok {
		t.Fatal("state response missing retrying")
	}
	if totals, ok := body["agent_totals"].(map[string]any); !ok || totals["total_tokens"] == nil {
		t.Fatalf("state response missing snake_case agent_totals: %#v", body["agent_totals"])
	}
}

func TestRefreshAccepted(t *testing.T) {
	cfg := config.Defaults()
	o := orchestrator.New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	rr := httptest.NewRecorder()
	New(o).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/refresh", nil))
	if rr.Code != http.StatusAccepted {
		t.Fatal(rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["queued"] != true || body["coalesced"] != false {
		t.Fatalf("unexpected refresh response: %#v", body)
	}
}

func TestIssueDetail(t *testing.T) {
	cfg := config.Defaults()
	cfg.ActiveStates = []string{"Todo"}
	cfg.WorkspaceRoot = t.TempDir()
	issue := domain.Issue{ID: "1", Identifier: "A-1", Title: "T", State: "Todo"}
	tr := &trackerfake.Tracker{Issues: []domain.Issue{issue}}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	o := orchestrator.New(cfg, tr, runner, workspace.NewManager(cfg.WorkspaceRoot))
	if err := o.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	defer close(runner.release)
	time.Sleep(20 * time.Millisecond)

	rr := httptest.NewRecorder()
	New(o).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/A-1", nil))
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "running" || body["issue_identifier"] != "A-1" {
		t.Fatalf("unexpected issue detail: %#v", body)
	}
	running, ok := body["running"].(map[string]any)
	if !ok {
		t.Fatalf("missing running detail: %#v", body)
	}
	if running["last_event"] != "notification" || running["last_message"] != "Working on tests" {
		t.Fatalf("unexpected running event detail: %#v", running)
	}
	if running["session_id"] != "thread-1-turn-1" || running["thread_id"] != "thread-1" || running["turn_id"] != "turn-1" {
		t.Fatalf("unexpected running identity: %#v", running)
	}
	if workspace, ok := body["workspace"].(map[string]any); !ok || workspace["path"] == "" {
		t.Fatalf("missing workspace detail: %#v", body["workspace"])
	}
	if events, ok := body["recent_events"].([]any); !ok || len(events) == 0 {
		t.Fatalf("missing recent events: %#v", body["recent_events"])
	}
}

func TestIssueNotFoundEnvelope(t *testing.T) {
	cfg := config.Defaults()
	o := orchestrator.New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	rr := httptest.NewRecorder()
	New(o).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/A-404", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatal(rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	errBody, ok := body["error"].(map[string]any)
	if !ok || errBody["code"] != "issue_not_found" {
		t.Fatalf("unexpected error body: %#v", body)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	cfg := config.Defaults()
	o := orchestrator.New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	rr := httptest.NewRecorder()
	New(o).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/state", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatal(rr.Code)
	}
	if rr.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("unexpected Allow header: %q", rr.Header().Get("Allow"))
	}
}

func TestDashboardUsesStateAPI(t *testing.T) {
	cfg := config.Defaults()
	o := orchestrator.New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	rr := httptest.NewRecorder()
	New(o).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Running Sessions") || !strings.Contains(body, "/api/v1/state") || !strings.Contains(body, "recent_agent_messages") || !strings.Contains(body, "log_path") || !strings.Contains(body, "chat-bubble") || !strings.Contains(body, "renderChatText") || !strings.Contains(body, "scrollTailLogs") {
		t.Fatalf("dashboard does not render status surface: %s", body)
	}
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingRunner) Run(ctx context.Context, req agent.RunRequest, events chan<- agent.Event) agent.Result {
	events <- agent.Event{
		SessionID: "thread-1-turn-1",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		IssueID:   req.Issue.ID,
		Type:      "notification",
		Message:   "Working on tests",
		Usage:     domain.TokenUsage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		At:        time.Now(),
	}
	close(r.started)
	select {
	case <-r.release:
		return agent.Result{Completed: false, Err: errors.New("released")}
	case <-ctx.Done():
		return agent.Result{Completed: false, Err: ctx.Err()}
	}
}
