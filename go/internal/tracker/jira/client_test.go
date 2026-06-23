package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/symphony/go/internal/config"
)

func TestClientFetchCandidates(t *testing.T) {
	var seenJQL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "user@example.com" || pass != "token" {
			t.Fatalf("missing basic auth")
		}
		if r.URL.Path != "/rest/api/3/search/jql" {
			t.Fatalf("bad path %s", r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		seenJQL, _ = req["jql"].(string)
		fields, _ := req["fields"].([]any)
		if len(fields) == 0 {
			t.Fatal("fields not requested")
		}
		_, _ = w.Write([]byte(`{"isLast":true,"issues":[{"id":"10001","key":"MOD-1","fields":{"summary":"Fix cache","description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Body"}]}]},"priority":{"id":"2","name":"High"},"status":{"name":"To Do"},"assignee":{"emailAddress":"owner@example.com"},"labels":["Go"],"created":"2026-06-22T09:10:11.000+0000","updated":"2026-06-22T10:10:11.000+0000"}}]}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "user@example.com", "token", "MOD", "", 50)
	issues, err := c.FetchCandidates(context.Background(), config.Effective{ActiveStates: []string{"To Do"}, RequiredLabels: []string{"go"}, TrackerPageSize: 25})
	if err != nil {
		t.Fatal(err)
	}
	if seenJQL != `project = "MOD" AND status in ("To Do") ORDER BY priority ASC, created ASC` {
		t.Fatalf("bad jql %q", seenJQL)
	}
	if len(issues) != 1 || issues[0].Identifier != "MOD-1" || issues[0].Description == nil || *issues[0].Description != "Body" || issues[0].State != "To Do" {
		t.Fatalf("%+v", issues)
	}
}

func TestFetchStatesByIDEmpty(t *testing.T) {
	c := New("https://example.atlassian.net", "user@example.com", "token", "MOD", "", 50)
	got, err := c.FetchStatesByID(context.Background(), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}
