package beads

import "testing"

func TestParseIssuesReadsClosedAtAndLabels(t *testing.T) {
	issues, err := parseIssues([]byte(`[{"id":"SYM-1","title":"Done","status":"closed","labels":["Dashboard","done"],"created_at":"2026-07-15T08:00:00Z","updated_at":"2026-07-15T09:00:00Z","closed_at":"2026-07-15T10:00:00Z"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues=%+v", issues)
	}
	if issues[0].ClosedAt == nil || issues[0].ClosedAt.Format("15:04:05") != "10:00:00" {
		t.Fatalf("closed_at not parsed: %+v", issues[0])
	}
	if len(issues[0].Labels) != 2 || issues[0].Labels[0] != "dashboard" || issues[0].Labels[1] != "done" {
		t.Fatalf("labels not parsed: %+v", issues[0].Labels)
	}
}
