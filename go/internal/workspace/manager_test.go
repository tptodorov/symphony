package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManager(t *testing.T) {
	m := NewManager(t.TempDir())
	ws, created, err := m.CreateForIssue("ABC/1")
	if err != nil || !created {
		t.Fatalf("%+v %v %v", ws, created, err)
	}
	_, created, err = m.CreateForIssue("ABC/1")
	if err != nil || created {
		t.Fatalf("reuse failed %v %v", created, err)
	}
	if err := os.WriteFile(filepath.Join(m.Root, "BAD"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CreateForIssue("BAD"); err == nil {
		t.Fatal("expected non-directory error")
	}
	if err := EnsurePathInsideRoot(m.Root, filepath.Join(m.Root, "..", "x")); err == nil {
		t.Fatal("expected escape")
	}
}

func TestHook(t *testing.T) {
	ctx := context.Background()
	if err := RunHook(ctx, "true", t.TempDir(), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := RunHook(ctx, "exit 2", t.TempDir(), time.Second); err == nil {
		t.Fatal("expected failure")
	}
	if err := RunHook(ctx, "sleep 1", t.TempDir(), time.Millisecond); err == nil {
		t.Fatal("expected timeout")
	}
}
