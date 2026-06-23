package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openai/symphony/go/internal/config"
	"github.com/openai/symphony/go/internal/domain"
)

type Client struct {
	Endpoint, Email, APIToken, ProjectKey, JQL string
	PageSize                                   int
	HTTP                                       *http.Client
}

func New(endpoint, email, apiToken, projectKey, jql string, pageSize int) *Client {
	return &Client{Endpoint: endpoint, Email: email, APIToken: apiToken, ProjectKey: projectKey, JQL: jql, PageSize: pageSize, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) FetchCandidates(ctx context.Context, cfg config.Effective) ([]domain.Issue, error) {
	jql := firstNonEmpty(cfg.TrackerJQL, c.JQL)
	if jql == "" {
		jql = statesJQL(c.projectScope(cfg), cfg.ActiveStates)
	}
	issues, err := c.search(ctx, cfg, jql)
	if err != nil {
		return nil, err
	}
	return filterLabels(issues, cfg.RequiredLabels), nil
}

func (c *Client) FetchStatesByID(ctx context.Context, ids []string) (map[string]domain.Issue, error) {
	out := map[string]domain.Issue{}
	if len(ids) == 0 {
		return out, nil
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, jqlQuote(id))
	}
	issues, err := c.search(ctx, config.Effective{TrackerPageSize: c.PageSize}, "id in ("+strings.Join(parts, ", ")+")")
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		out[issue.ID] = issue
	}
	return out, nil
}

func (c *Client) FetchStatesByIdentifier(ctx context.Context, identifiers []string) (map[string]domain.Issue, error) {
	out := map[string]domain.Issue{}
	if len(identifiers) == 0 {
		return out, nil
	}
	parts := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		parts = append(parts, jqlQuote(identifier))
	}
	issues, err := c.search(ctx, config.Effective{TrackerPageSize: c.PageSize}, "issuekey in ("+strings.Join(parts, ", ")+")")
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		out[issue.Identifier] = issue
	}
	return out, nil
}

func (c *Client) FetchByStates(ctx context.Context, states []string) ([]domain.Issue, error) {
	return c.search(ctx, config.Effective{TrackerPageSize: c.PageSize}, statesJQL(c.ProjectKey, states))
}

func (c *Client) projectScope(cfg config.Effective) string {
	return firstNonEmpty(cfg.TrackerProjectKey, cfg.TrackerProjectSlug, c.ProjectKey)
}

func (c *Client) search(ctx context.Context, cfg config.Effective, jql string) ([]domain.Issue, error) {
	if strings.TrimSpace(jql) == "" {
		return nil, fmt.Errorf("jira_jql_required")
	}
	pageSize := cfg.TrackerPageSize
	if pageSize <= 0 {
		pageSize = c.PageSize
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	out := []domain.Issue{}
	nextPageToken := ""
	for {
		reqBody := map[string]any{
			"jql":        jql,
			"maxResults": pageSize,
			"fields":     []string{"summary", "description", "priority", "status", "assignee", "labels", "created", "updated", "issuelinks"},
		}
		if nextPageToken != "" {
			reqBody["nextPageToken"] = nextPageToken
		}
		var resp searchResponse
		if err := c.do(ctx, reqBody, &resp); err != nil {
			return nil, err
		}
		if resp.Issues == nil {
			return nil, fmt.Errorf("jira_unknown_payload: issues missing")
		}
		for _, issue := range resp.Issues {
			out = append(out, normalizeIssue(c.Endpoint, issue))
		}
		if resp.IsLast {
			break
		}
		if resp.NextPageToken == "" {
			return nil, fmt.Errorf("jira_missing_next_page_token")
		}
		nextPageToken = resp.NextPageToken
	}
	return out, nil
}

func (c *Client) do(ctx context.Context, reqBody map[string]any, data any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("encode jira request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Endpoint, "/")+"/rest/api/3/search/jql", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("jira_api_request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.Email, c.APIToken)
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("jira_api_request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("jira_api_status: %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(res.Body).Decode(data); err != nil {
		return fmt.Errorf("jira_unknown_payload: %w", err)
	}
	return nil
}

func statesJQL(projectKey string, states []string) string {
	parts := make([]string, 0, len(states))
	for _, state := range states {
		if strings.TrimSpace(state) != "" {
			parts = append(parts, jqlQuote(state))
		}
	}
	if projectKey == "" || len(parts) == 0 {
		return ""
	}
	return "project = " + jqlQuote(projectKey) + " AND status in (" + strings.Join(parts, ", ") + ") ORDER BY priority ASC, created ASC"
}

func jqlQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type searchResponse struct {
	Issues        []restIssue `json:"issues"`
	NextPageToken string      `json:"nextPageToken"`
	IsLast        bool        `json:"isLast"`
}

type restIssue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Self   string `json:"self"`
	Fields struct {
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		Priority    *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"priority"`
		Status *struct {
			Name string `json:"name"`
		} `json:"status"`
		Assignee *struct {
			DisplayName  string `json:"displayName"`
			EmailAddress string `json:"emailAddress"`
			AccountID    string `json:"accountId"`
		} `json:"assignee"`
		Labels  []string    `json:"labels"`
		Created string      `json:"created"`
		Updated string      `json:"updated"`
		Links   []issueLink `json:"issuelinks"`
	} `json:"fields"`
}

type issueLink struct {
	Type *struct {
		Name string `json:"name"`
	} `json:"type"`
	InwardIssue  *linkedIssue `json:"inwardIssue"`
	OutwardIssue *linkedIssue `json:"outwardIssue"`
}

type linkedIssue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Status *struct {
			Name string `json:"name"`
		} `json:"status"`
	} `json:"fields"`
}

func normalizeIssue(endpoint string, issue restIssue) domain.Issue {
	var priority *int
	if issue.Fields.Priority != nil {
		if n, err := strconv.Atoi(issue.Fields.Priority.ID); err == nil {
			priority = &n
		}
	}
	var assignee *string
	if issue.Fields.Assignee != nil {
		assignee = stringPtr(firstNonEmpty(issue.Fields.Assignee.EmailAddress, issue.Fields.Assignee.DisplayName, issue.Fields.Assignee.AccountID))
	}
	state := ""
	if issue.Fields.Status != nil {
		state = issue.Fields.Status.Name
	}
	return domain.Issue{
		ID:          issue.ID,
		Identifier:  issue.Key,
		Title:       issue.Fields.Summary,
		Description: stringPtr(adfText(issue.Fields.Description)),
		Priority:    priority,
		State:       state,
		Assignee:    assignee,
		URL:         browseURL(endpoint, issue.Key),
		Labels:      domain.NormalizeLabels(issue.Fields.Labels),
		BlockedBy:   blockedBy(issue.Fields.Links),
		CreatedAt:   parseJiraTime(issue.Fields.Created),
		UpdatedAt:   parseJiraTime(issue.Fields.Updated),
	}
}

func blockedBy(links []issueLink) []domain.BlockerRef {
	out := []domain.BlockerRef{}
	for _, link := range links {
		if link.Type == nil || !strings.EqualFold(strings.TrimSpace(link.Type.Name), "Blocks") || link.InwardIssue == nil {
			continue
		}
		state := ""
		if link.InwardIssue.Fields.Status != nil {
			state = link.InwardIssue.Fields.Status.Name
		}
		out = append(out, domain.BlockerRef{
			ID:         stringPtr(link.InwardIssue.ID),
			Identifier: stringPtr(link.InwardIssue.Key),
			State:      stringPtr(state),
		})
	}
	return out
}

func browseURL(endpoint, key string) *string {
	if key == "" {
		return nil
	}
	base, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil
	}
	base.Path = "/browse/" + key
	base.RawQuery = ""
	base.Fragment = ""
	return stringPtr(base.String())
}

func stringPtr(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func parseJiraTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05.000Z0700"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

func adfText(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return strings.TrimSpace(s)
		}
		return ""
	}
	lines := []string{}
	collectText(v, &lines)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func collectText(v any, lines *[]string) {
	switch x := v.(type) {
	case map[string]any:
		if text, ok := x["text"].(string); ok && text != "" {
			*lines = append(*lines, text)
		}
		if content, ok := x["content"].([]any); ok {
			before := len(*lines)
			for _, child := range content {
				collectText(child, lines)
			}
			if len(*lines) > before && blockNode(x["type"]) {
				*lines = append(*lines, "")
			}
		}
	case []any:
		for _, item := range x {
			collectText(item, lines)
		}
	}
}

func blockNode(v any) bool {
	t, _ := v.(string)
	switch t {
	case "paragraph", "heading", "blockquote", "listItem":
		return true
	default:
		return false
	}
}

func filterLabels(issues []domain.Issue, required []string) []domain.Issue {
	out := issues[:0]
	haveRequired := domain.NormalizeLabels(required)
	for _, issue := range issues {
		have := map[string]bool{}
		for _, label := range domain.NormalizeLabels(issue.Labels) {
			have[label] = true
		}
		ok := true
		for _, label := range haveRequired {
			if !have[label] {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, issue)
		}
	}
	return out
}
