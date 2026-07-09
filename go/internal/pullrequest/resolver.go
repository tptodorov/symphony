package pullrequest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tptodorov/symphony/go/internal/config"
	"github.com/tptodorov/symphony/go/internal/domain"
	"github.com/tptodorov/symphony/go/internal/orchestrator"
)

func NewResolver(cfg config.Effective, log *slog.Logger) (orchestrator.PullRequestResolver, error) {
	switch cfg.PullRequests.Provider {
	case "", "none":
		return nil, nil
	case "local":
		return NewLocalResolver(cfg.PullRequests.LocalPath, cfg.PullRequests.CacheTTL), nil
	case "github":
		return NewGitHubResolver(cfg.PullRequests.GitHubRepository, cfg.PullRequests.GitHubToken, cfg.PullRequests.CacheTTL, http.DefaultClient, "https://api.github.com", log), nil
	default:
		return nil, fmt.Errorf("unsupported pull request provider %q", cfg.PullRequests.Provider)
	}
}

type matchCandidate struct {
	pr        orchestrator.PullRequestSnapshot
	title     string
	body      string
	updatedAt *time.Time
}

func choose(candidates []matchCandidate) (*orchestrator.PullRequestSnapshot, error) {
	switch len(candidates) {
	case 0:
		return nil, nil
	case 1:
		pr := candidates[0].pr
		return &pr, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].updatedAt, candidates[j].updatedAt
		if a == nil && b != nil {
			return false
		}
		if a != nil && b == nil {
			return true
		}
		if a != nil && b != nil && !a.Equal(*b) {
			return a.After(*b)
		}
		return candidates[i].pr.Number > candidates[j].pr.Number
	})
	if candidates[0].updatedAt == nil && candidates[1].updatedAt == nil {
		return nil, errors.New("multiple pull requests matched without updated_at for disambiguation")
	}
	pr := candidates[0].pr
	return &pr, nil
}

func normalizedIdentifier(identifier string) string {
	return strings.ToUpper(strings.TrimSpace(identifier))
}

func containsIdentifierToken(text, identifier string) bool {
	identifier = regexp.QuoteMeta(normalizedIdentifier(identifier))
	if identifier == "" {
		return false
	}
	re := regexp.MustCompile(`(^|[^A-Z0-9])` + identifier + `([^A-Z0-9]|$)`)
	return re.MatchString(strings.ToUpper(text))
}

func branchName(issue domain.Issue) string {
	if issue.BranchName == nil {
		return ""
	}
	return strings.TrimSpace(*issue.BranchName)
}

func withMatch(pr orchestrator.PullRequestSnapshot, match string) orchestrator.PullRequestSnapshot {
	pr.Match = match
	return pr
}

type LocalResolver struct {
	path string
	ttl  time.Duration
	mu   sync.Mutex
	data []localPullRequest
	exp  time.Time
}

type localPullRequest struct {
	IssueIdentifier string     `json:"issue_identifier"`
	Title           string     `json:"title,omitempty"`
	Body            string     `json:"body,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
	orchestrator.PullRequestSnapshot
}

func NewLocalResolver(path string, ttl time.Duration) *LocalResolver {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &LocalResolver{path: path, ttl: ttl}
}

func (r *LocalResolver) LookupPullRequest(_ context.Context, issue domain.Issue) (*orchestrator.PullRequestSnapshot, error) {
	items, err := r.load()
	if err != nil {
		return nil, err
	}
	if b := branchName(issue); b != "" {
		candidates := []matchCandidate{}
		for _, item := range items {
			if item.HeadBranch == b {
				candidates = append(candidates, matchCandidate{pr: withMatch(item.PullRequestSnapshot, "branch_name"), title: item.Title, body: item.Body, updatedAt: item.UpdatedAt})
			}
		}
		if pr, err := choose(candidates); pr != nil || err != nil {
			return pr, err
		}
	}
	identifier := normalizedIdentifier(issue.Identifier)
	candidates := []matchCandidate{}
	for _, item := range items {
		if containsIdentifierToken(item.HeadBranch, identifier) {
			candidates = append(candidates, matchCandidate{pr: withMatch(item.PullRequestSnapshot, "head_branch_identifier"), title: item.Title, body: item.Body, updatedAt: item.UpdatedAt})
		}
	}
	if pr, err := choose(candidates); pr != nil || err != nil {
		return pr, err
	}
	candidates = []matchCandidate{}
	for _, item := range items {
		if normalizedIdentifier(item.IssueIdentifier) == identifier || containsIdentifierToken(item.Title, identifier) || containsIdentifierToken(item.Body, identifier) {
			candidates = append(candidates, matchCandidate{pr: withMatch(item.PullRequestSnapshot, "identifier_search"), title: item.Title, body: item.Body, updatedAt: item.UpdatedAt})
		}
	}
	return choose(candidates)
}

func (r *LocalResolver) load() ([]localPullRequest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Now().Before(r.exp) {
		return append([]localPullRequest(nil), r.data...), nil
	}
	b, err := os.ReadFile(r.path)
	if err != nil {
		return nil, err
	}
	var items []localPullRequest
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Provider == "" {
			items[i].Provider = "local"
		}
	}
	r.data = items
	r.exp = time.Now().Add(r.ttl)
	return append([]localPullRequest(nil), r.data...), nil
}

type GitHubResolver struct {
	repo    string
	token   string
	ttl     time.Duration
	client  *http.Client
	baseURL string
	log     *slog.Logger
	mu      sync.Mutex
	cache   map[string]cachedPR
}

type cachedPR struct {
	pr  *orchestrator.PullRequestSnapshot
	exp time.Time
}

func NewGitHubResolver(repo, token string, ttl time.Duration, client *http.Client, baseURL string, log *slog.Logger) *GitHubResolver {
	if ttl <= 0 {
		ttl = time.Minute
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &GitHubResolver{repo: repo, token: token, ttl: ttl, client: client, baseURL: strings.TrimRight(baseURL, "/"), log: log, cache: map[string]cachedPR{}}
}

func (r *GitHubResolver) LookupPullRequest(ctx context.Context, issue domain.Issue) (*orchestrator.PullRequestSnapshot, error) {
	key := issue.ID + "\x00" + issue.Identifier + "\x00" + branchName(issue)
	r.mu.Lock()
	if cached, ok := r.cache[key]; ok && time.Now().Before(cached.exp) {
		r.mu.Unlock()
		return copyPR(cached.pr), nil
	}
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pr, err := r.lookupUncached(ctx, issue)
	if err == nil {
		r.mu.Lock()
		r.cache[key] = cachedPR{pr: copyPR(pr), exp: time.Now().Add(r.ttl)}
		r.mu.Unlock()
	}
	return pr, err
}

func (r *GitHubResolver) lookupUncached(ctx context.Context, issue domain.Issue) (*orchestrator.PullRequestSnapshot, error) {
	if b := branchName(issue); b != "" {
		pr, err := r.searchPRs(ctx, "branch_name", fmt.Sprintf("repo:%s type:pr head:%s", r.repo, b), func(c matchCandidate) bool {
			return c.pr.HeadBranch == b
		})
		if pr != nil || err != nil {
			return pr, err
		}
	}
	identifier := normalizedIdentifier(issue.Identifier)
	pr, err := r.searchPRs(ctx, "head_branch_identifier", fmt.Sprintf("repo:%s type:pr head:%s", r.repo, identifier), func(c matchCandidate) bool {
		return containsIdentifierToken(c.pr.HeadBranch, identifier)
	})
	if pr != nil || err != nil {
		return pr, err
	}
	return r.searchPRs(ctx, "identifier_search", fmt.Sprintf("repo:%s type:pr %q in:title,body", r.repo, identifier), func(c matchCandidate) bool {
		return containsIdentifierToken(c.title, identifier) || containsIdentifierToken(c.body, identifier)
	})
}

func (r *GitHubResolver) searchPRs(ctx context.Context, match, query string, keep func(matchCandidate) bool) (*orchestrator.PullRequestSnapshot, error) {
	var search githubSearchResponse
	if err := r.getJSON(ctx, "/search/issues", url.Values{"q": []string{query}, "per_page": []string{"10"}}, &search); err != nil {
		return nil, err
	}
	candidates := []matchCandidate{}
	for _, item := range search.Items {
		if item.PullRequest == nil {
			continue
		}
		detail, err := r.pullDetail(ctx, item.Number)
		if err != nil {
			if r.log != nil {
				r.log.Warn("pull request detail lookup failed", "provider", "github", "repo", r.repo, "number", item.Number, "error", err)
			}
			continue
		}
		candidate := matchCandidate{pr: githubSnapshot(detail, match), title: firstNonEmpty(detail.Title, item.Title), body: detail.Body, updatedAt: detail.UpdatedAt}
		if keep(candidate) {
			candidates = append(candidates, candidate)
		}
	}
	return choose(candidates)
}

func (r *GitHubResolver) pullDetail(ctx context.Context, number int) (githubPullResponse, error) {
	var detail githubPullResponse
	err := r.getJSON(ctx, fmt.Sprintf("/repos/%s/pulls/%d", strings.Trim(r.repo, "/"), number), nil, &detail)
	return detail, err
}

func (r *GitHubResolver) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	u := r.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github request %s failed: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type githubSearchResponse struct {
	Items []githubSearchItem `json:"items"`
}

type githubSearchItem struct {
	Number      int                 `json:"number"`
	Title       string              `json:"title"`
	PullRequest *githubSearchPRInfo `json:"pull_request"`
}

type githubSearchPRInfo struct{}

type githubPullResponse struct {
	Number         int        `json:"number"`
	HTMLURL        string     `json:"html_url"`
	State          string     `json:"state"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	Draft          bool       `json:"draft"`
	MergedAt       *time.Time `json:"merged_at"`
	UpdatedAt      *time.Time `json:"updated_at"`
	Mergeable      *bool      `json:"mergeable"`
	MergeableState string     `json:"mergeable_state"`
	Head           struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

func githubSnapshot(detail githubPullResponse, match string) orchestrator.PullRequestSnapshot {
	return orchestrator.PullRequestSnapshot{
		Provider:   "github",
		Number:     detail.Number,
		URL:        detail.HTMLURL,
		State:      githubState(detail),
		IsDraft:    detail.Draft,
		MergedAt:   detail.MergedAt,
		HeadBranch: detail.Head.Ref,
		Match:      match,
	}
}

func githubState(detail githubPullResponse) string {
	if detail.MergedAt != nil {
		return "merged"
	}
	if detail.Draft {
		return "draft"
	}
	if !strings.EqualFold(detail.State, "open") {
		return "unknown"
	}
	if detail.Mergeable == nil && detail.MergeableState == "" {
		return "unknown"
	}
	if detail.Mergeable != nil && *detail.Mergeable && (detail.MergeableState == "" || detail.MergeableState == "clean" || detail.MergeableState == "has_hooks" || detail.MergeableState == "unstable") {
		return "mergeable"
	}
	return "blocked"
}

func copyPR(pr *orchestrator.PullRequestSnapshot) *orchestrator.PullRequestSnapshot {
	if pr == nil {
		return nil
	}
	cp := *pr
	if pr.MergedAt != nil {
		mergedAt := *pr.MergedAt
		cp.MergedAt = &mergedAt
	}
	return &cp
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
