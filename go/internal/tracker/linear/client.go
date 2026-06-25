package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tptodorov/symphony/go/internal/config"
	"github.com/tptodorov/symphony/go/internal/domain"
)

type Client struct {
	Endpoint, APIKey, ProjectSlug string
	HTTP                          *http.Client
}

func New(endpoint, apiKey, projectSlug string) *Client {
	return &Client{Endpoint: endpoint, APIKey: apiKey, ProjectSlug: projectSlug, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) FetchCandidates(ctx context.Context, cfg config.Effective) ([]domain.Issue, error) {
	states := cfg.ActiveStates
	out := []domain.Issue{}
	after := any(nil)
	for {
		var resp struct {
			Issues struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []gqlIssue `json:"nodes"`
			} `json:"issues"`
		}
		if err := c.do(ctx, candidatesQuery, map[string]any{"projectSlug": first(c.ProjectSlug, cfg.TrackerProjectSlug), "states": states, "after": after}, &resp); err != nil {
			return nil, err
		}
		for _, n := range resp.Issues.Nodes {
			issue := normalizeIssue(n)
			if hasLabels(issue, cfg.RequiredLabels) {
				out = append(out, issue)
			}
		}
		if !resp.Issues.PageInfo.HasNextPage {
			break
		}
		after = resp.Issues.PageInfo.EndCursor
	}
	return out, nil
}
func (c *Client) FetchStatesByID(ctx context.Context, ids []string) (map[string]domain.Issue, error) {
	out := map[string]domain.Issue{}
	if len(ids) == 0 {
		return out, nil
	}
	var resp struct {
		Issues struct {
			Nodes []gqlIssue `json:"nodes"`
		} `json:"issues"`
	}
	if err := c.do(ctx, statesQuery, map[string]any{"ids": ids}, &resp); err != nil {
		return nil, err
	}
	for _, n := range resp.Issues.Nodes {
		issue := normalizeIssue(n)
		out[issue.ID] = issue
	}
	return out, nil
}
func (c *Client) FetchByStates(ctx context.Context, states []string) ([]domain.Issue, error) {
	if len(states) == 0 {
		return nil, nil
	}
	return c.FetchCandidates(ctx, config.Effective{ActiveStates: states, TrackerProjectSlug: c.ProjectSlug})
}

func (c *Client) do(ctx context.Context, query string, vars map[string]any, data any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return fmt.Errorf("encode graphql request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create graphql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", c.APIKey)
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("linear request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("linear non-200 status: %d", res.StatusCode)
	}
	var env struct {
		Data   json.RawMessage  `json:"data"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		return fmt.Errorf("decode linear response: %w", err)
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("linear graphql error: %v", env.Errors)
	}
	if err := json.Unmarshal(env.Data, data); err != nil {
		return fmt.Errorf("decode linear data: %w", err)
	}
	return nil
}
func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func hasLabels(issue domain.Issue, required []string) bool {
	have := map[string]bool{}
	for _, l := range domain.NormalizeLabels(issue.Labels) {
		have[l] = true
	}
	for _, r := range domain.NormalizeLabels(required) {
		if !have[r] {
			return false
		}
	}
	return true
}
