package domain

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

type Issue struct {
	ID          string
	Identifier  string
	Title       string
	Description *string
	Priority    *int
	State       string
	BranchName  *string
	Assignee    *string
	URL         *string
	Labels      []string
	BlockedBy   []BlockerRef
	CreatedAt   *time.Time
	UpdatedAt   *time.Time
	ClosedAt    *time.Time
}

type BlockerRef struct {
	ID         *string
	Identifier *string
	State      *string
}

var workspaceUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func SanitizeWorkspaceKey(identifier string) string {
	s := strings.Trim(workspaceUnsafe.ReplaceAllString(identifier, "_"), "._")
	if s == "" {
		return "issue"
	}
	return s
}

func NormalizeState(state string) string { return strings.ToLower(strings.TrimSpace(state)) }

func NormalizeLabels(labels []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.ToLower(strings.TrimSpace(label))
		if label != "" && !seen[label] {
			seen[label] = true
			out = append(out, label)
		}
	}
	sort.Strings(out)
	return out
}

func IssueIsEligible(issue Issue, cfg EffectiveConfig) bool {
	if strings.TrimSpace(cfg.TrackerAssignee) != "" {
		if issue.Assignee == nil || NormalizeState(*issue.Assignee) != NormalizeState(cfg.TrackerAssignee) {
			return false
		}
	}
	state := NormalizeState(issue.State)
	if containsNorm(cfg.TerminalStates, state) {
		return false
	}
	if !containsNorm(cfg.ActiveStates, state) {
		return false
	}
	labels := NormalizeLabels(issue.Labels)
	for _, need := range NormalizeLabels(cfg.RequiredLabels) {
		if !contains(labels, need) {
			return false
		}
	}
	for _, b := range issue.BlockedBy {
		if b.State == nil || !containsNorm(cfg.TerminalStates, NormalizeState(*b.State)) {
			return false
		}
	}
	return true
}

func SortIssuesForDispatch(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		if a.Priority == nil && b.Priority != nil {
			return false
		}
		if a.Priority != nil && b.Priority == nil {
			return true
		}
		if a.Priority != nil && b.Priority != nil && *a.Priority != *b.Priority {
			return *a.Priority < *b.Priority
		}
		if a.CreatedAt == nil && b.CreatedAt != nil {
			return false
		}
		if a.CreatedAt != nil && b.CreatedAt == nil {
			return true
		}
		if a.CreatedAt != nil && b.CreatedAt != nil && !a.CreatedAt.Equal(*b.CreatedAt) {
			return a.CreatedAt.Before(*b.CreatedAt)
		}
		return a.Identifier < b.Identifier
	})
}

func containsNorm(values []string, state string) bool {
	for _, v := range values {
		if NormalizeState(v) == state {
			return true
		}
	}
	return false
}

func contains(values []string, needle string) bool {
	for _, v := range values {
		if v == needle {
			return true
		}
	}
	return false
}
