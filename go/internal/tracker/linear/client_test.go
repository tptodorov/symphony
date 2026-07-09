package linear

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tptodorov/symphony/go/internal/config"
)

func TestClient(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		if r.Header.Get("Authorization") != "key" {
			t.Errorf("missing auth")
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		vars := req["variables"].(map[string]any)
		if vars["projectSlug"] != "proj" {
			t.Errorf("bad slug %v", vars)
		}
		_, _ = rr.Write([]byte(`{"data":{"issues":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"id":"1","identifier":"A-1","title":"T","priority":1,"state":{"name":"Todo"},"labels":{"nodes":[{"name":"Bug"}]},"relations":{"nodes":[]}}]}}}`))
		return rr.Result(), nil
	})}
	c := New("https://linear.example/graphql", "key", "proj")
	c.HTTP = client
	issues, err := c.FetchCandidates(context.Background(), config.Effective{ActiveStates: []string{"Todo"}, RequiredLabels: []string{"bug"}})
	if err != nil || len(issues) != 1 || issues[0].Labels[0] != "bug" {
		t.Fatalf("%+v %v", issues, err)
	}
	m, err := c.FetchStatesByID(context.Background(), nil)
	if err != nil || len(m) != 0 {
		t.Fatal(err, m)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := f(r)
	if resp != nil && resp.Body == nil {
		resp.Body = io.NopCloser(strings.NewReader(""))
	}
	return resp, err
}
