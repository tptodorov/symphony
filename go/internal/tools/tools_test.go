package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecuteJiraREST(t *testing.T) {
	oldClient := toolHTTPClient
	toolHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/ABC-1" || r.URL.Query().Get("fields") != "summary,status" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		user, token, ok := r.BasicAuth()
		if !ok || user != "user@example.com" || token != "token" {
			t.Fatalf("missing auth: %q %q %v", user, token, ok)
		}
		rr.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rr).Encode(map[string]any{"key": "ABC-1"})
		return rr.Result(), nil
	})}
	defer func() { toolHTTPClient = oldClient }()

	result := ExecuteJiraREST(context.Background(), "https://jira.example", "user@example.com", "token", "GET", "/rest/api/3/issue/ABC-1", map[string]any{"fields": "summary,status"}, nil)
	if !result.Success || result.Error != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	parsed, ok := result.ParsedJSON.(map[string]any)
	if !ok || parsed["key"] != "ABC-1" {
		t.Fatalf("unexpected parsed json: %#v", result.ParsedJSON)
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

func TestExecuteJiraRESTRejectsUnsafePath(t *testing.T) {
	result := ExecuteJiraREST(context.Background(), "https://jira.example", "user@example.com", "token", "GET", "https://evil.example/rest/api/3/issue/ABC-1", nil, nil)
	if result.Success || result.Error == "" {
		t.Fatalf("expected unsafe path failure: %+v", result)
	}
}

func TestExactlyOneGraphQLOperation(t *testing.T) {
	for _, query := range []string{
		`{ viewer { id } }`,
		`query Issue { issue(id: "1") { id } } fragment F on Issue { id }`,
		`mutation Update { updateIssue(id: "1") { id } }`,
	} {
		if !exactlyOneGraphQLOperation(query) {
			t.Fatalf("expected one operation: %s", query)
		}
	}
	if exactlyOneGraphQLOperation(`query A { a } query B { b }`) {
		t.Fatal("expected multiple operations to be rejected")
	}
}
