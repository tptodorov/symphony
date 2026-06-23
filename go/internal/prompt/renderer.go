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
	var attemptValue any
	if attempt != nil {
		attemptValue = *attempt
	}
	ctx := map[string]any{"issue": issueMap(issue), "attempt": attemptValue}
	if strings.TrimSpace(template) == "" {
		desc := ""
		if issue.Description != nil {
			desc = "\n\n" + *issue.Description
		}
		return fmt.Sprintf("You are working on an issue from the configured tracker.%s", desc), nil
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
		expr := strings.TrimSpace(m[1])
		parts := strings.Split(expr, "|")
		if err := checkName(ctx, locals, strings.TrimSpace(parts[0])); err != nil {
			return err
		}
		for _, filter := range parts[1:] {
			filter = strings.TrimSpace(filter)
			if filter == "" {
				continue
			}
			name := strings.Split(filter, ":")[0]
			name = strings.Split(name, " ")[0]
			if !isValidFilterName(name) {
				return fmt.Errorf("unknown filter %q", name)
			}
		}
	}
	return nil
}

var knownFilters = map[string]bool{
	"upcase": true, "downcase": true, "capitalize": true, "titlecase": true,
	"escape": true, "escape_once": true, "strip_html": true, "strip": true,
	"lstrip": true, "rstrip": true, "strip_newlines": true,
	"newline_to_br": true, "replace": true, "replace_first": true,
	"remove": true, "remove_first": true,
	"truncate": true, "truncatewords": true, "split": true, "join": true, "concat": true,
	"sort": true, "sort_natural": true, "reverse": true, "uniq": true, "compact": true,
	"where": true, "group_by": true,
	"map": true, "pluck": true, "sum": true, "average": true, "min": true, "max": true,
	"size": true, "first": true, "last": true,
	"date": true, "json": true, "inspect": true,
	"base64_encode": true, "base64_decode": true, "url_encode": true, "url_decode": true,
	"md5": true, "sha1": true, "sha256": true,
	"plus": true, "minus": true, "times": true, "divided_by": true, "modulo": true,
	"ceil": true, "floor": true, "round": true, "at_most": true, "at_least": true, "abs": true,
	"default": true, "blank": true, "empty": true, "present": true,
	"prepend": true, "append": true, "push": true, "pop": true, "shift": true, "unshift": true,
	"slice": true, "keys": true, "values": true,
	"handleize": true, "slugify": true, "parameterize": true,
	"t": true, "translate": true,
	"highlight": true, "markdownify": true,
	"img_tag": true, "img_url": true,
	"link_to": true,
	"include": true, "render": true,
	"cycle": true, "tablerow": true,
	"and": true, "or": true, "not": true, "contains": true,
	"is_array": true, "is_hash": true, "is_empty": true, "is_nil": true, "is_number": true, "is_string": true,
	"to_integer": true, "to_float": true, "to_string": true, "to_bool": true,
	"increment": true, "decrement": true,
	"xml_escape": true, "uri_escape": true,
	"textileize": true, "textileize_without_paragraph": true,
	"lightbox": true, "thumbnail": true,
	"stylesheet_tag": true, "javascript_tag": true,
	"paginate":           true,
	"asset_url":          true,
	"google_analytics":   true,
	"cache":              true,
	"content_for_header": true, "content_for_footer": true,
	"highlight_active": true, "tab_active": true, "cart_count": true,
	"sample": true, "window": true, "offset": true, "limit": true, "chunk": true,
	"for_form": true, "input": true, "area": true, "select": true, "option": true,
	"form": true, "submit_tag": true,
	"raw": true, "literal": true, "parse": true,
	"ordinal": true, "ordinalize": true, "pluralize": true, "singularize": true,
}

func isValidFilterName(name string) bool {
	return knownFilters[name]
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
