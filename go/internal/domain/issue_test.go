package domain

import (
	"testing"
	"time"
)

func TestHelpers(t *testing.T) {
	if got := SanitizeWorkspaceKey(" ABC/123 "); got != "ABC_123" {
		t.Fatal(got)
	}
	if got := NormalizeState(" In Progress "); got != "in progress" {
		t.Fatal(got)
	}
	labels := NormalizeLabels([]string{"Bug", " bug ", "P1"})
	if len(labels) != 2 || labels[0] != "bug" || labels[1] != "p1" {
		t.Fatal(labels)
	}
}

func TestSortIssuesForDispatch(t *testing.T) {
	p1, p2 := 1, 2
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	issues := []Issue{
		{Identifier: "C", Priority: nil, CreatedAt: &old},
		{Identifier: "B", Priority: &p2, CreatedAt: &old},
		{Identifier: "A", Priority: &p1, CreatedAt: &newer},
		{Identifier: "D", Priority: &p1, CreatedAt: &old},
	}
	SortIssuesForDispatch(issues)
	want := []string{"D", "A", "B", "C"}
	for i := range want {
		if issues[i].Identifier != want[i] {
			t.Fatalf("%d: got %s want %s", i, issues[i].Identifier, want[i])
		}
	}
}
