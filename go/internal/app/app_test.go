package app

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentfake "github.com/tptodorov/symphony/go/internal/agent/fake"
	"github.com/tptodorov/symphony/go/internal/config"
	"github.com/tptodorov/symphony/go/internal/domain"
	"github.com/tptodorov/symphony/go/internal/orchestrator"
	trackerfake "github.com/tptodorov/symphony/go/internal/tracker/fake"
	"github.com/tptodorov/symphony/go/internal/workspace"
)

func TestNew(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, "WORKFLOW.md")
	if err := os.WriteFile(wf, []byte("---\ntracker:\n  kind: linear\n  api_key: k\n  project_slug: p\n  active_states: [ready]\n  terminal_states: [done]\nworkspace:\n  root: work\n---\nPrompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), Options{WorkflowPath: wf, Tracker: &trackerfake.Tracker{}, Runner: &agentfake.Runner{}})
	if err != nil || app.Orch == nil {
		t.Fatal(err)
	}
}

func TestNewCleansOldPreparationWorkspaces(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "work")
	oldFailed := filepath.Join(root, workspace.FailedDirName, "A-1-old")
	newPreparing := filepath.Join(root, workspace.PreparingDirName, "A-1-new")
	for _, path := range []string{oldFailed, newPreparing} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldFailed, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	wf := filepath.Join(dir, "WORKFLOW.md")
	body := "---\ntracker:\n  kind: linear\n  api_key: k\n  project_slug: p\n  active_states: [ready]\n  terminal_states: [done]\nworkspace:\n  root: " + root + "\n---\nPrompt"
	if err := os.WriteFile(wf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), Options{WorkflowPath: wf, Tracker: &trackerfake.Tracker{}, Runner: &agentfake.Runner{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldFailed); !os.IsNotExist(err) {
		t.Fatalf("old failed workspace should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(newPreparing); err != nil {
		t.Fatalf("new preparing workspace should remain: %v", err)
	}
}

func TestNewCleansTerminalIssueWorkspaces(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "work")
	wm := workspace.NewManager(root)
	ws, _, err := wm.CreateForIssue("SYM-1")
	if err != nil {
		t.Fatal(err)
	}
	wf := filepath.Join(dir, "WORKFLOW.md")
	body := "---\ntracker:\n  kind: beads\n  active_states: [ready]\n  terminal_states: [done]\nworkspace:\n  root: " + root + "\n---\nPrompt"
	if err := os.WriteFile(wf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := &trackerfake.Tracker{Issues: []domain.Issue{
		{ID: "SYM-1", Identifier: "SYM-1", Title: "Closed today", State: "done"},
	}}

	app, err := New(context.Background(), Options{WorkflowPath: wf, Tracker: tr, Runner: &agentfake.Runner{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ws.Path); !os.IsNotExist(err) {
		t.Fatalf("terminal issue workspace should be removed, stat err=%v", err)
	}
	if _, ok := app.Orch.Snapshot().Counts["completed"]; ok {
		t.Fatal("startup cleanup should not populate completed dashboard count")
	}
}

func TestRunStartsEphemeralStatusServer(t *testing.T) {
	cfg := config.Defaults()
	cfg.WorkspaceRoot = t.TempDir()
	o := orchestrator.New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(cfg.WorkspaceRoot))
	a := &App{Opt: Options{Port: 0, PortSet: true}, Orch: o, cfg: cfg}
	oldListenTCP := listenTCP
	listenCalled := false
	listener := newBlockingListener()
	listenTCP = func(network, address string) (net.Listener, error) {
		listenCalled = true
		if network != "tcp" || address != "127.0.0.1:0" {
			t.Fatalf("unexpected listen target %s %s", network, address)
		}
		return listener, nil
	}
	defer func() { listenTCP = oldListenTCP }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	if !listenCalled {
		t.Fatal("status server did not start listener")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not stop")
	}
}

type blockingListener struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingListener() *blockingListener {
	return &blockingListener{closed: make(chan struct{})}
}

func (l *blockingListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *blockingListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *blockingListener) Addr() net.Addr {
	return testAddr("127.0.0.1:0")
}

type testAddr string

func (a testAddr) Network() string { return "tcp" }
func (a testAddr) String() string  { return string(a) }

func TestRunRejectsNegativePort(t *testing.T) {
	cfg := config.Defaults()
	o := orchestrator.New(cfg, &trackerfake.Tracker{}, &agentfake.Runner{}, workspace.NewManager(t.TempDir()))
	a := &App{Opt: Options{Port: -1, PortSet: true}, Orch: o, cfg: cfg}
	err := a.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "server.port") {
		t.Fatalf("expected server.port error, got %v", err)
	}
}
