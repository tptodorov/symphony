package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	agentfake "github.com/openai/symphony/go/internal/agent/fake"
	"github.com/openai/symphony/go/internal/config"
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
}
