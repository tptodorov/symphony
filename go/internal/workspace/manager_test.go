package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestPrepareForIssueStagesAndPromotes(t *testing.T) {
	m := NewManager(t.TempDir())
	ws, created, err := m.PrepareForIssue(context.Background(), "ABC/1", "echo prepared > marker", time.Second)
	if err != nil || !created {
		t.Fatalf("%+v %v %v", ws, created, err)
	}
	if ws.Path != filepath.Join(m.Root, "ABC_1") {
		t.Fatalf("bad workspace path %q", ws.Path)
	}
	if b, err := os.ReadFile(filepath.Join(ws.Path, "marker")); err != nil || strings.TrimSpace(string(b)) != "prepared" {
		t.Fatalf("marker missing: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(m.Root, PreparingDirName)); err != nil {
		t.Fatalf("preparing root should exist: %v", err)
	}
	_, created, err = m.PrepareForIssue(context.Background(), "ABC/1", "exit 2", time.Second)
	if err != nil || created {
		t.Fatalf("non-empty workspace should be reused: created=%v err=%v", created, err)
	}
}

func TestPrepareForIssueRetainsFailedStagingAndRemovesEmptyCanonical(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := os.Mkdir(filepath.Join(m.Root, "ABC_1"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, created, err := m.PrepareForIssue(context.Background(), "ABC/1", "echo failed > marker; exit 2", time.Second)
	if err == nil || !created {
		t.Fatalf("expected failed staged preparation, created=%v err=%v", created, err)
	}
	var prepErr *PrepareHookError
	if !errors.As(err, &prepErr) || prepErr.FailedPath == "" {
		t.Fatalf("expected PrepareHookError with failed path, got %T %v", err, err)
	}
	if _, err := os.Stat(filepath.Join(m.Root, "ABC_1")); !os.IsNotExist(err) {
		t.Fatalf("empty canonical workspace should be removed, stat err=%v", err)
	}
	if b, err := os.ReadFile(filepath.Join(prepErr.FailedPath, "marker")); err != nil || strings.TrimSpace(string(b)) != "failed" {
		t.Fatalf("failed workspace was not retained: %q %v", b, err)
	}
	errorBytes, err := os.ReadFile(filepath.Join(prepErr.FailedPath, "prepare-error.txt"))
	if err != nil {
		t.Fatal(err)
	}
	errorText := string(errorBytes)
	if !strings.Contains(errorText, "after_create failed") || !strings.Contains(errorText, "exit status 2") {
		t.Fatalf("prepare-error.txt missing hook failure details: %q", errorText)
	}
}

func TestPreparationCleanupAndIdentifierListing(t *testing.T) {
	m := NewManager(t.TempDir())
	for _, dir := range []string{PreparingDirName, FailedDirName, "ABC-1"} {
		if err := os.MkdirAll(filepath.Join(m.Root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldPreparing := filepath.Join(m.Root, PreparingDirName, "old")
	newFailed := filepath.Join(m.Root, FailedDirName, "new")
	oldFile := filepath.Join(m.Root, FailedDirName, "old-file")
	for _, path := range []string{oldPreparing, newFailed} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(oldFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPreparing, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := m.CleanupPreparationDirs(PreparationRetention); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPreparing); !os.IsNotExist(err) {
		t.Fatalf("old preparing workspace should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatalf("old failed file should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(newFailed); err != nil {
		t.Fatalf("new failed workspace should remain: %v", err)
	}
	ids, err := m.ListIssueIdentifiers()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "ABC-1" {
		t.Fatalf("special directories should not be listed as issues: %#v", ids)
	}
}

func TestRemoveForIssueReportsBeforeRemoveHookFailureAndStillRemovesWorkspace(t *testing.T) {
	m := NewManager(t.TempDir())
	ws, created, err := m.CreateForIssue("ABC-1")
	if err != nil || !created {
		t.Fatalf("create workspace: %+v %v %v", ws, created, err)
	}

	err = m.RemoveForIssue(context.Background(), "ABC-1", "echo cleanup failed; exit 2", time.Second)
	if err == nil {
		t.Fatal("expected before_remove hook error")
	}
	var hookErr *BeforeRemoveHookError
	if !errors.As(err, &hookErr) || !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("expected typed hook error, got %T %v", err, err)
	}
	if _, err := os.Stat(ws.Path); !os.IsNotExist(err) {
		t.Fatalf("workspace should still be removed, stat err=%v", err)
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

func TestHookEnvInheritsSymphonyWorkdirWithoutSourceDirAlias(t *testing.T) {
	workdir := t.TempDir()
	out := filepath.Join(t.TempDir(), "hook-env.txt")
	t.Setenv("SYMPHONY_WORKDIR", workdir)
	t.Setenv("SOURCE_DIR", "")

	if err := RunHook(context.Background(), fmt.Sprintf("printf '%%s\n%%s\n' \"$SYMPHONY_WORKDIR\" \"$SOURCE_DIR\" > %q", out), t.TempDir(), time.Second); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), workdir+"\n\n"; got != want {
		t.Fatalf("unexpected hook env output: %q", b)
	}
}
