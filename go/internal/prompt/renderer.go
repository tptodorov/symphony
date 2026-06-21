package prompt

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/openai/symphony/go/internal/domain"
	"github.com/osteele/liquid"
)

var (
	varTag = regexp.MustCompile(`\{\{([^}]*)\}\}`)
	forTag = regexp.MustCompile(`\{%\s*for\s+(\w+)\s+in\s+([^%]+?)\s*%\}`)
)

func Render(template string, issue domain.Issue, attempt *int) (string, error) {
	ctx := map[string]any{"issue": issueMap(issue), "attempt": attempt}
	if strings.TrimSpace(template) == "" {
		desc := ""
		if issue.Description != nil {
			desc = "\n\n" + *issue.Description
		}
		return fmt.Sprintf("Work on %s: %s%s", issue.Identifier, issue.Title, desc), nil
	}
	if err := checkKnown(template, ctx); err != nil {
		return "", err
	}
	engine := liquid.NewEngine()
	out, err := engine.ParseAndRenderString(template, ctx)
	if err != nil {
		return "", fmt.Errorf("render prompt: %w", err)
	}
	return out, nil
}

func issueMap(i domain.Issue) map[string]any {
	blockers := make([]map[string]any, 0, len(i.BlockedBy))
	for _, b := range i.BlockedBy {
		blockers = append(blockers, map[string]any{"id": deref(b.ID), "identifier": deref(b.Identifier), "state": deref(b.State)})
	}
	return map[string]any{
		"id": i.ID, "identifier": i.Identifier, "title": i.Title, "description": deref(i.Description),
		"priority": i.Priority, "state": i.State, "branch_name": deref(i.BranchName), "assignee": deref(i.Assignee),
		"url": deref(i.URL), "labels": domain.NormalizeLabels(i.Labels), "blocked_by": blockers,
		"created_at": i.CreatedAt, "updated_at": i.UpdatedAt,
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func checkKnown(tpl string, ctx map[string]any) error {
	locals := map[string]bool{}
	for _, m := range forTag.FindAllStringSubmatch(tpl, -1) {
		locals[m[1]] = true
		if err := checkName(ctx, locals, strings.TrimSpace(m[2])); err != nil {
			return err
		}
	}
	for _, m := range varTag.FindAllStringSubmatch(tpl, -1) {
		expr := strings.TrimSpace(strings.Split(m[1], "|")[0])
		parts := strings.Fields(expr)
		if len(parts) == 0 {
			continue
		}
		if err := checkName(ctx, locals, parts[0]); err != nil {
			return err
		}
	}
	return nil
}

func checkName(ctx map[string]any, locals map[string]bool, name string) error {
	if strings.HasPrefix(name, "'") || strings.HasPrefix(name, "\"") || isNumber(name) || locals[strings.Split(name, ".")[0]] {
		return nil
	}
	if !pathExists(ctx, strings.Split(name, ".")) {
		return fmt.Errorf("unknown prompt variable %q", name)
	}
	return nil
}

func pathExists(v any, parts []string) bool {
	if len(parts) == 0 {
		return true
	}
	m, ok := v.(map[string]any)
	if !ok {
		return true
	}
	next, ok := m[parts[0]]
	if !ok {
		return false
	}
	return pathExists(next, parts[1:])
}

func isNumber(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
