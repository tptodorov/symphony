package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tptodorov/symphony/go/internal/config"
)

func TestClient(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "key" {
			t.Errorf("missing auth")
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		vars := req["variables"].(map[string]any)
		if vars["projectSlug"] != "proj" {
			t.Errorf("bad slug %v", vars)
		}
		_, _ = w.Write([]byte(`{"data":{"issues":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"id":"1","identifier":"A-1","title":"T","priority":1,"state":{"name":"Todo"},"labels":{"nodes":[{"name":"Bug"}]},"relations":{"nodes":[]}}]}}}`))
	}))
	defer ts.Close()
	c := New(ts.URL, "key", "proj")
	issues, err := c.FetchCandidates(context.Background(), config.Effective{ActiveStates: []string{"Todo"}, RequiredLabels: []string{"bug"}})
	if err != nil || len(issues) != 1 || issues[0].Labels[0] != "bug" {
		t.Fatalf("%+v %v", issues, err)
	}
	m, err := c.FetchStatesByID(context.Background(), nil)
	if err != nil || len(m) != 0 {
		t.Fatal(err, m)
	}
}
