package linear

import (
	"time"

	"github.com/tptodorov/symphony/go/internal/domain"
)

type gqlIssue struct {
	ID          string  `json:"id"`
	Identifier  string  `json:"identifier"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Priority    *int    `json:"priority"`
	BranchName  *string `json:"branchName"`
	URL         *string `json:"url"`
	CreatedAt   *string `json:"createdAt"`
	UpdatedAt   *string `json:"updatedAt"`
	State       struct {
		Name string `json:"name"`
	} `json:"state"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Relations struct {
		Nodes []struct {
			Type         string `json:"type"`
			RelatedIssue *struct {
				ID, Identifier string
				State          struct {
					Name string `json:"name"`
				} `json:"state"`
			} `json:"relatedIssue"`
		} `json:"nodes"`
	} `json:"relations"`
}

func normalizeIssue(g gqlIssue) domain.Issue {
	labels := []string{}
	for _, l := range g.Labels.Nodes {
		labels = append(labels, l.Name)
	}
	blockers := []domain.BlockerRef{}
	for _, r := range g.Relations.Nodes {
		if r.Type == "blocks" && r.RelatedIssue != nil {
			id, ident, state := r.RelatedIssue.ID, r.RelatedIssue.Identifier, r.RelatedIssue.State.Name
			blockers = append(blockers, domain.BlockerRef{ID: &id, Identifier: &ident, State: &state})
		}
	}
	return domain.Issue{ID: g.ID, Identifier: g.Identifier, Title: g.Title, Description: g.Description, Priority: g.Priority, BranchName: g.BranchName, URL: g.URL, State: g.State.Name, Labels: domain.NormalizeLabels(labels), BlockedBy: blockers, CreatedAt: parseTime(g.CreatedAt), UpdatedAt: parseTime(g.UpdatedAt)}
}
func parseTime(s *string) *time.Time {
	if s == nil {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, *s); err == nil {
		return &t
	}
	return nil
}
