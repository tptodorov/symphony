package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tptodorov/symphony/go/internal/config"
)

type capturedRequest struct {
	JQL           string   `json:"jql"`
	MaxResults    int      `json:"maxResults"`
	Fields        []string `json:"fields"`
	NextPageToken string   `json:"nextPageToken"`
}

func TestClientFetchCandidatesDefaultJQLAndNormalization(t *testing.T) {
	var seen capturedRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireJiraRequest(t, r)
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"isLast":true,"issues":[{"id":"10001","key":"MOD-1","fields":{"summary":"Fix cache","description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Body"}]}]},"priority":{"id":"2","name":"High"},"status":{"name":"To Do"},"assignee":{"emailAddress":"owner@example.com"},"labels":["Go","Backend"],"created":"2026-06-22T09:10:11.000+0000","updated":"2026-06-22T10:10:11.000+0000","issuelinks":[{"type":{"name":"Blocks"},"inwardIssue":{"id":"10000","key":"MOD-0","fields":{"status":{"name":"In Progress"}}}},{"type":{"name":"Blocks"},"outwardIssue":{"id":"10002","key":"MOD-2","fields":{"status":{"name":"To Do"}}}},{"type":{"name":"Relates"},"inwardIssue":{"id":"10003","key":"MOD-3","fields":{"status":{"name":"To Do"}}}}]}}]}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "user@example.com", "token", "MOD", "", 50)
	issues, err := c.FetchCandidates(context.Background(), config.Effective{ActiveStates: []string{"To Do"}, RequiredLabels: []string{"go"}, TrackerPageSize: 25})
	if err != nil {
		t.Fatal(err)
	}
	if seen.JQL != `project = "MOD" AND status in ("To Do") ORDER BY priority ASC, created ASC` {
		t.Fatalf("bad jql %q", seen.JQL)
	}
	if seen.MaxResults != 25 {
		t.Fatalf("bad maxResults %d", seen.MaxResults)
	}
	if !containsString(seen.Fields, "issuelinks") {
		t.Fatalf("issuelinks not requested: %#v", seen.Fields)
	}
	if len(issues) != 1 {
		t.Fatalf("%+v", issues)
	}
	issue := issues[0]
	if issue.ID != "10001" || issue.Identifier != "MOD-1" || issue.State != "To Do" {
		t.Fatalf("%+v", issue)
	}
	if issue.Description == nil || *issue.Description != "Body" {
		t.Fatalf("bad description %+v", issue.Description)
	}
	if issue.Priority == nil || *issue.Priority != 2 {
		t.Fatalf("bad priority %+v", issue.Priority)
	}
	if issue.Assignee == nil || *issue.Assignee != "owner@example.com" {
		t.Fatalf("bad assignee %+v", issue.Assignee)
	}
	if issue.URL == nil || *issue.URL != ts.URL+"/browse/MOD-1" {
		t.Fatalf("bad url %+v", issue.URL)
	}
	if strings.Join(issue.Labels, ",") != "backend,go" {
		t.Fatalf("bad labels %#v", issue.Labels)
	}
	if len(issue.BlockedBy) != 1 {
		t.Fatalf("bad blockers %#v", issue.BlockedBy)
	}
	if issue.BlockedBy[0].ID == nil || *issue.BlockedBy[0].ID != "10000" || issue.BlockedBy[0].Identifier == nil || *issue.BlockedBy[0].Identifier != "MOD-0" || issue.BlockedBy[0].State == nil || *issue.BlockedBy[0].State != "In Progress" {
		t.Fatalf("bad blocker %#v", issue.BlockedBy[0])
	}
	if issue.CreatedAt == nil || issue.UpdatedAt == nil {
		t.Fatalf("timestamps not parsed: %+v", issue)
	}
}

func TestClientFetchCandidatesUsesProjectSlugAlias(t *testing.T) {
	var seen capturedRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireJiraRequest(t, r)
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"isLast":true,"issues":[]}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "user@example.com", "token", "", "", 50)
	_, err := c.FetchCandidates(context.Background(), config.Effective{TrackerProjectSlug: "MOD", ActiveStates: []string{"To Do"}})
	if err != nil {
		t.Fatal(err)
	}
	if seen.JQL != `project = "MOD" AND status in ("To Do") ORDER BY priority ASC, created ASC` {
		t.Fatalf("bad jql %q", seen.JQL)
	}
}

func TestClientFetchCandidatesUsesCustomJQL(t *testing.T) {
	var seen capturedRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireJiraRequest(t, r)
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"isLast":true,"issues":[]}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "user@example.com", "token", "MOD", "", 50)
	_, err := c.FetchCandidates(context.Background(), config.Effective{TrackerJQL: `assignee = currentUser() ORDER BY created ASC`, ActiveStates: []string{"To Do"}})
	if err != nil {
		t.Fatal(err)
	}
	if seen.JQL != `assignee = currentUser() ORDER BY created ASC` {
		t.Fatalf("bad jql %q", seen.JQL)
	}
}

func TestClientPagination(t *testing.T) {
	requests := []capturedRequest{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireJiraRequest(t, r)
		var req capturedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, req)
		if len(requests) == 1 {
			_, _ = w.Write([]byte(`{"isLast":false,"nextPageToken":"next","issues":[{"id":"10001","key":"MOD-1","fields":{"summary":"First","status":{"name":"To Do"}}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"isLast":true,"issues":[{"id":"10002","key":"MOD-2","fields":{"summary":"Second","status":{"name":"To Do"}}}]}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "user@example.com", "token", "MOD", "", 2)
	issues, err := c.FetchCandidates(context.Background(), config.Effective{ActiveStates: []string{"To Do"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[1].NextPageToken != "next" {
		t.Fatalf("bad requests %#v", requests)
	}
	if len(issues) != 2 || issues[0].Identifier != "MOD-1" || issues[1].Identifier != "MOD-2" {
		t.Fatalf("%+v", issues)
	}
}

func TestClientFetchStatesByIDAndIdentifier(t *testing.T) {
	requests := []capturedRequest{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireJiraRequest(t, r)
		var req capturedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, req)
		_, _ = w.Write([]byte(`{"isLast":true,"issues":[{"id":"10001","key":"MOD-1","fields":{"summary":"Issue","status":{"name":"Done"}}}]}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "user@example.com", "token", "MOD", "", 50)
	byID, err := c.FetchStatesByID(context.Background(), []string{"10001"})
	if err != nil {
		t.Fatal(err)
	}
	byIdentifier, err := c.FetchStatesByIdentifier(context.Background(), []string{"MOD-1"})
	if err != nil {
		t.Fatal(err)
	}
	if requests[0].JQL != `id in ("10001")` || requests[1].JQL != `issuekey in ("MOD-1")` {
		t.Fatalf("bad jqls %#v", requests)
	}
	if byID["10001"].Identifier != "MOD-1" || byIdentifier["MOD-1"].ID != "10001" {
		t.Fatalf("%+v %+v", byID, byIdentifier)
	}
}

func TestClientErrors(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad auth", http.StatusUnauthorized)
		}))
		defer ts.Close()

		c := New(ts.URL, "user@example.com", "token", "MOD", "", 50)
		_, err := c.FetchCandidates(context.Background(), config.Effective{ActiveStates: []string{"To Do"}})
		if err == nil || !strings.Contains(err.Error(), "jira_api_status") {
			t.Fatalf("expected jira_api_status, got %v", err)
		}
	})

	t.Run("missing issues", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"isLast":true}`))
		}))
		defer ts.Close()

		c := New(ts.URL, "user@example.com", "token", "MOD", "", 50)
		_, err := c.FetchCandidates(context.Background(), config.Effective{ActiveStates: []string{"To Do"}})
		if err == nil || !strings.Contains(err.Error(), "jira_unknown_payload") {
			t.Fatalf("expected jira_unknown_payload, got %v", err)
		}
	})

	t.Run("missing next page token", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"isLast":false,"issues":[]}`))
		}))
		defer ts.Close()

		c := New(ts.URL, "user@example.com", "token", "MOD", "", 50)
		_, err := c.FetchCandidates(context.Background(), config.Effective{ActiveStates: []string{"To Do"}})
		if err == nil || !strings.Contains(err.Error(), "jira_missing_next_page_token") {
			t.Fatalf("expected jira_missing_next_page_token, got %v", err)
		}
	})
}

func TestFetchStatesByIDEmpty(t *testing.T) {
	c := New("https://example.atlassian.net", "user@example.com", "token", "MOD", "", 50)
	got, err := c.FetchStatesByID(context.Background(), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}

func requireJiraRequest(t *testing.T, r *http.Request) {
	t.Helper()
	user, pass, ok := r.BasicAuth()
	if !ok || user != "user@example.com" || pass != "token" {
		t.Fatalf("missing basic auth")
	}
	if r.URL.Path != "/rest/api/3/search/jql" {
		t.Fatalf("bad path %s", r.URL.Path)
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
