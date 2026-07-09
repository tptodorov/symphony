package orchestrator

import (
	"context"

	"github.com/tptodorov/symphony/go/internal/domain"
)

type PullRequestResolver interface {
	LookupPullRequest(context.Context, domain.Issue) (*PullRequestSnapshot, error)
}

func (o *Orchestrator) SetPullRequestResolver(resolver PullRequestResolver) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pullRequests = resolver
}

func (o *Orchestrator) enrichPullRequests(ctx context.Context, s *Snapshot, resolver PullRequestResolver) {
	if resolver == nil || s == nil {
		return
	}
	cache := map[string]*PullRequestSnapshot{}
	lookup := func(issue domain.Issue) *PullRequestSnapshot {
		if issue.ID == "" && issue.Identifier == "" {
			return nil
		}
		key := issue.ID + "\x00" + issue.Identifier
		if pr, ok := cache[key]; ok {
			return pr
		}
		pr, err := resolver.LookupPullRequest(ctx, issue)
		if err != nil {
			if o.log != nil {
				o.log.Warn("pull request lookup failed", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "error", err)
			}
			cache[key] = nil
			return nil
		}
		cache[key] = copyPullRequestSnapshot(pr)
		return cache[key]
	}
	for i := range s.Ready {
		s.Ready[i].PullRequest = copyPullRequestSnapshot(lookup(s.Ready[i].sourceIssue))
	}
	for i := range s.Setup {
		s.Setup[i].PullRequest = copyPullRequestSnapshot(lookup(s.Setup[i].sourceIssue))
	}
	for i := range s.Running {
		s.Running[i].PullRequest = copyPullRequestSnapshot(lookup(s.Running[i].sourceIssue))
	}
	for i := range s.Retrying {
		s.Retrying[i].PullRequest = copyPullRequestSnapshot(lookup(s.Retrying[i].sourceIssue))
	}
	for i := range s.Completed {
		s.Completed[i].PullRequest = copyPullRequestSnapshot(lookup(s.Completed[i].sourceIssue))
	}
	s.RetryQueue = append([]RetrySnapshot(nil), s.Retrying...)
}

func (o *Orchestrator) enrichIssueDetail(ctx context.Context, detail *IssueDetailSnapshot, resolver PullRequestResolver) {
	if resolver == nil || detail == nil {
		return
	}
	if detail.Running != nil {
		pr, err := resolver.LookupPullRequest(ctx, detail.Running.sourceIssue)
		if err != nil {
			if o.log != nil {
				o.log.Warn("pull request lookup failed", "issue_id", detail.IssueID, "issue_identifier", detail.IssueIdentifier, "error", err)
			}
		} else {
			detail.Running.PullRequest = copyPullRequestSnapshot(pr)
		}
	}
	if detail.Retry != nil {
		pr, err := resolver.LookupPullRequest(ctx, detail.Retry.sourceIssue)
		if err != nil {
			if o.log != nil {
				o.log.Warn("pull request lookup failed", "issue_id", detail.IssueID, "issue_identifier", detail.IssueIdentifier, "error", err)
			}
		} else {
			detail.Retry.PullRequest = copyPullRequestSnapshot(pr)
		}
	}
	if detail.Setup != nil {
		pr, err := resolver.LookupPullRequest(ctx, detail.Setup.sourceIssue)
		if err != nil {
			if o.log != nil {
				o.log.Warn("pull request lookup failed", "issue_id", detail.IssueID, "issue_identifier", detail.IssueIdentifier, "error", err)
			}
		} else {
			detail.Setup.PullRequest = copyPullRequestSnapshot(pr)
		}
	}
}

func copyPullRequestSnapshot(pr *PullRequestSnapshot) *PullRequestSnapshot {
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
