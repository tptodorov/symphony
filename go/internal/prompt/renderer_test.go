package prompt

import (
	"strings"
	"testing"

	"github.com/openai/symphony/go/internal/domain"
)

func TestRender(t *testing.T) {
	d := "desc"
	attempt := 2
	out, err := Render("{{ issue.identifier }} {{ issue.title }} {{ issue.description }} {{ attempt }}", domain.Issue{Identifier: "ABC-1", Title: "Fix", Description: &d}, &attempt)
	if err != nil || out != "ABC-1 Fix desc 2" {
		t.Fatalf("%q %v", out, err)
	}
	out, err = Render("{% for label in issue.labels %}{{ label }} {% endfor %}", domain.Issue{Labels: []string{"Bug"}}, nil)
	if err != nil || strings.TrimSpace(out) != "bug" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := Render("{{ issue.nope }}", domain.Issue{}, nil); err == nil {
		t.Fatal("expected unknown variable")
	}
	out, err = Render("", domain.Issue{Identifier: "A", Title: "T"}, nil)
	if err != nil || !strings.Contains(out, "configured tracker") {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := Render("{{ issue.title | upCase }}", domain.Issue{Title: "t"}, nil); err == nil {
		t.Fatal("expected unknown filter")
	}
	if _, err := Render("{{ issue.title | unknown_filter }}", domain.Issue{Title: "t"}, nil); err == nil {
		t.Fatal("expected unknown filter")
	}
	out, err = Render("{{ issue.title | upcase }}", domain.Issue{Title: "t"}, nil)
	if err != nil || out != "T" {
		t.Fatalf("%q %v", out, err)
	}
}
