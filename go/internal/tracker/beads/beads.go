package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tptodorov/symphony/go/internal/config"
	"github.com/tptodorov/symphony/go/internal/domain"
)

type Tracker struct {
	Command string
	WorkDir string
}

func New() *Tracker { return &Tracker{Command: "bd"} }

func (t *Tracker) FetchCandidates(ctx context.Context, cfg config.Effective) ([]domain.Issue, error) {
	issues, err := t.runList(ctx, cfg, []string{"ready", "--json"})
	if err != nil || len(issues) == 0 {
		args := []string{"list", "--json"}
		for _, s := range cfg.ActiveStates {
			args = append(args, "--status", s)
		}
		issues, err = t.runList(ctx, cfg, args)
		if err != nil {
			return nil, err
		}
	}
	return filterLabels(issues, cfg.RequiredLabels), nil
}

func (t *Tracker) FetchStatesByID(ctx context.Context, ids []string) (map[string]domain.Issue, error) {
	out := map[string]domain.Issue{}
	for _, id := range ids {
		issues, err := t.runList(ctx, config.Effective{TrackerBDCommand: t.command(), WorkflowDir: t.WorkDir}, []string{"show", id, "--json"})
		if err != nil {
			return nil, err
		}
		if len(issues) > 0 {
			out[issues[0].ID] = issues[0]
		}
	}
	return out, nil
}

func (t *Tracker) FetchByStates(ctx context.Context, states []string) ([]domain.Issue, error) {
	cfg := config.Effective{TrackerBDCommand: t.command(), WorkflowDir: t.WorkDir}
	args := []string{"list", "--json"}
	for _, s := range states {
		args = append(args, "--status", s)
	}
	return t.runList(ctx, cfg, args)
}

func (t *Tracker) command() string {
	if t.Command != "" {
		return t.Command
	}
	return "bd"
}

func (t *Tracker) runList(ctx context.Context, cfg config.Effective, args []string) ([]domain.Issue, error) {
	cmd := cfg.TrackerBDCommand
	if cmd == "" {
		cmd = t.command()
	}
	sh := cmd
	for _, a := range args {
		sh += " " + strconv.Quote(a)
	}
	c := exec.CommandContext(ctx, "bash", "-lc", sh)
	if cfg.WorkflowDir != "" {
		c.Dir = cfg.WorkflowDir
	} else if t.WorkDir != "" {
		c.Dir = t.WorkDir
	}
	c.Env = append(c.Environ(), "BD_JSON_ENVELOPE=1")
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("beads cli exec error: %w: %s", err, stderr.String())
	}
	return parseIssues(stdout.Bytes())
}

func parseIssues(b []byte) ([]domain.Issue, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("beads json parse error: %w", err)
	}
	if m, ok := v.(map[string]any); ok {
		if data, ok := m["data"]; ok {
			v = data
		}
	}
	arr, ok := v.([]any)
	if !ok {
		arr = []any{v}
	}
	out := []domain.Issue{}
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, normalize(m))
		}
	}
	return out, nil
}

func normalize(m map[string]any) domain.Issue {
	id := stringField(m, "id", "identifier")
	desc := ptrString(stringField(m, "description"))
	assignee := ptrString(stringField(m, "assignee", "owner"))
	p := intPtr(m["priority"])
	return domain.Issue{ID: id, Identifier: id, Title: stringField(m, "title", "summary"), Description: desc, Priority: p, State: stringField(m, "status", "state"), Assignee: assignee, Labels: domain.NormalizeLabels(stringsSlice(m["tags"])), BlockedBy: blockers(m), CreatedAt: timePtr(stringField(m, "created_at")), UpdatedAt: timePtr(stringField(m, "updated_at"))}
}
func blockers(m map[string]any) []domain.BlockerRef {
	deps, _ := m["dependencies"].([]any)
	out := []domain.BlockerRef{}
	blocking := map[string]bool{"blocks": true, "waits-for": true, "conditional-blocks": true, "parent-child": true}
	for _, d := range deps {
		dm, ok := d.(map[string]any)
		if !ok || !blocking[stringField(dm, "type")] {
			continue
		}
		id := stringField(dm, "id", "issue_id", "target", "target_id")
		state := stringField(dm, "status", "state")
		out = append(out, domain.BlockerRef{ID: &id, Identifier: &id, State: &state})
	}
	return out
}
func filterLabels(issues []domain.Issue, required []string) []domain.Issue {
	out := issues[:0]
	for _, issue := range issues {
		ok := true
		have := map[string]bool{}
		for _, l := range domain.NormalizeLabels(issue.Labels) {
			have[l] = true
		}
		for _, r := range domain.NormalizeLabels(required) {
			if !have[r] {
				ok = false
			}
		}
		if ok {
			out = append(out, issue)
		}
	}
	return out
}
func stringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
func ptrString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func intPtr(v any) *int {
	switch x := v.(type) {
	case float64:
		n := int(x)
		return &n
	case int:
		return &x
	case string:
		if n, err := strconv.Atoi(x); err == nil {
			return &n
		}
	}
	return nil
}
func stringsSlice(v any) []string {
	a, _ := v.([]any)
	out := []string{}
	for _, e := range a {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
func timePtr(s string) *time.Time {
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	return nil
}
