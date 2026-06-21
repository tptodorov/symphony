package workflow

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "missing.md")); !errors.Is(err, ErrMissingWorkflowFile) {
		t.Fatalf("got %v", err)
	}
	path := filepath.Join(dir, "WORKFLOW.md")
	if err := os.WriteFile(path, []byte(" hello \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wf, err := Load(path)
	if err != nil || wf.PromptTemplate != "hello" || len(wf.Config) != 0 {
		t.Fatalf("%+v %v", wf, err)
	}
	if err := os.WriteFile(path, []byte("---\ntracker:\n  kind: linear\n---\n body \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wf, err = Load(path)
	if err != nil || wf.PromptTemplate != "body" || wf.Config["tracker"].(map[string]any)["kind"] != "linear" {
		t.Fatalf("%+v %v", wf, err)
	}
	if err := os.WriteFile(path, []byte("---\n- nope\n---\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(path)
	if !errors.Is(err, ErrWorkflowFrontMatterNotMap) {
		t.Fatalf("got %v", err)
	}
}
