package pullrequest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tptodorov/symphony/go/internal/domain"
)

func TestLocalResolverMatchesBranchBeforeIdentifierSearch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prs.json")
	if err := os.WriteFile(path, []byte(`[
		{"issue_identifier":"ABC-1","provider":"github","number":1,"url":"https://example.test/1","state":"merged","head_branch":"feature/other","title":"ABC-1 fallback","updated_at":"2026-01-01T00:00:00Z"},
		{"provider":"github","number":2,"url":"https://example.test/2","state":"mergeable","head_branch":"feature/ABC-1","title":"Branch match","updated_at":"2026-01-02T00:00:00Z"}
	]`), 0o600); err != nil {
		t.Fatal(err)
	}
	branch := "feature/ABC-1"
	resolver := NewLocalResolver(path, time.Minute)
	pr, err := resolver.LookupPullRequest(context.Background(), domain.Issue{ID: "1", Identifier: "ABC-1", BranchName: &branch})
	if err != nil {
		t.Fatal(err)
	}
	if pr == nil || pr.Number != 2 || pr.Match != "branch_name" {
		t.Fatalf("unexpected PR match: %+v", pr)
	}
}

func TestGitHubResolverSearchesIdentifierInTitleAndBody(t *testing.T) {
	var detailCalls int
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		switch {
		case r.URL.Path == "/search/issues":
			q := r.URL.Query().Get("q")
			if strings.Contains(q, "in:title,body") {
				_, _ = fmt.Fprint(rr, `{"items":[{"number":7,"title":"Fix ABC-1","pull_request":{}}]}`)
				return rr.Result(), nil
			}
			_, _ = fmt.Fprint(rr, `{"items":[]}`)
		case r.URL.Path == "/repos/owner/repo/pulls/7":
			detailCalls++
			_, _ = fmt.Fprint(rr, `{"number":7,"html_url":"https://github.com/owner/repo/pull/7","state":"open","title":"Fix ABC-1","body":"Implements ABC-1","draft":false,"mergeable":true,"mergeable_state":"clean","head":{"ref":"feature/work"},"updated_at":"2026-01-02T00:00:00Z"}`)
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		return rr.Result(), nil
	})}

	resolver := NewGitHubResolver("owner/repo", "token", time.Minute, client, "https://api.github.test", nil)
	pr, err := resolver.LookupPullRequest(context.Background(), domain.Issue{ID: "1", Identifier: "ABC-1"})
	if err != nil {
		t.Fatal(err)
	}
	if pr == nil || pr.Provider != "github" || pr.Number != 7 || pr.State != "mergeable" || pr.Match != "identifier_search" || pr.HeadBranch != "feature/work" {
		t.Fatalf("unexpected PR: %+v", pr)
	}
	if detailCalls != 1 {
		t.Fatalf("detail calls = %d", detailCalls)
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
