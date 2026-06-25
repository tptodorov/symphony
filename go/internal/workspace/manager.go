package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tptodorov/symphony/go/internal/domain"
)

const (
	PreparingDirName     = ".preparing"
	FailedDirName        = ".failed"
	PreparationRetention = 24 * time.Hour
)

type Workspace struct{ Path string }

type Manager struct{ Root string }

func NewManager(root string) Manager { return Manager{Root: root} }

type PrepareHookError struct {
	Err        error
	FailedPath string
}

func (e *PrepareHookError) Error() string {
	if e.FailedPath == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s (failed workspace: %s)", e.Err.Error(), e.FailedPath)
}

func (e *PrepareHookError) Unwrap() error { return e.Err }

func (m Manager) ListIssueIdentifiers() ([]string, error) {
	root, err := filepath.Abs(m.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workspace root: %w", err)
	}
	identifiers := []string{}
	for _, entry := range entries {
		if entry.Name() == PreparingDirName || entry.Name() == FailedDirName {
			continue
		}
		if entry.IsDir() {
			identifiers = append(identifiers, entry.Name())
		}
	}
	return identifiers, nil
}

func (m Manager) CleanupPreparationDirs(maxAge time.Duration) error {
	if maxAge <= 0 {
		return nil
	}
	root, err := filepath.Abs(m.Root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	cutoff := time.Now().Add(-maxAge)
	var errs []error
	for _, name := range []string{PreparingDirName, FailedDirName} {
		parent := filepath.Join(root, name)
		entries, err := os.ReadDir(parent)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", parent, err))
			continue
		}
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				errs = append(errs, fmt.Errorf("stat %s: %w", filepath.Join(parent, entry.Name()), err))
				continue
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			path := filepath.Join(parent, entry.Name())
			if err := EnsurePathInsideRoot(root, path); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := os.RemoveAll(path); err != nil {
				errs = append(errs, fmt.Errorf("remove old preparation workspace %s: %w", path, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (m Manager) CreateForIssue(identifier string) (Workspace, bool, error) {
	root, err := filepath.Abs(m.Root)
	if err != nil {
		return Workspace{}, false, fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Workspace{}, false, fmt.Errorf("create workspace root: %w", err)
	}
	path := filepath.Join(root, domain.SanitizeWorkspaceKey(identifier))
	if err := EnsurePathInsideRoot(root, path); err != nil {
		return Workspace{}, false, err
	}
	st, err := os.Stat(path)
	if err == nil {
		if !st.IsDir() {
			return Workspace{}, false, fmt.Errorf("workspace path is not a directory: %s", path)
		}
		return Workspace{Path: path}, false, nil
	}
	if !os.IsNotExist(err) {
		return Workspace{}, false, fmt.Errorf("stat workspace: %w", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return Workspace{}, false, fmt.Errorf("create workspace: %w", err)
	}
	return Workspace{Path: path}, true, nil
}

func (m Manager) PrepareForIssue(ctx context.Context, identifier, afterCreate string, timeout time.Duration) (Workspace, bool, error) {
	if afterCreate == "" {
		return m.CreateForIssue(identifier)
	}
	root, err := filepath.Abs(m.Root)
	if err != nil {
		return Workspace{}, false, fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Workspace{}, false, fmt.Errorf("create workspace root: %w", err)
	}
	key := domain.SanitizeWorkspaceKey(identifier)
	canonical := filepath.Join(root, key)
	if err := EnsurePathInsideRoot(root, canonical); err != nil {
		return Workspace{}, false, err
	}

	emptyCanonical := false
	if st, err := os.Stat(canonical); err == nil {
		if !st.IsDir() {
			return Workspace{}, false, fmt.Errorf("workspace path is not a directory: %s", canonical)
		}
		empty, err := isEmptyDir(canonical)
		if err != nil {
			return Workspace{}, false, err
		}
		if !empty {
			return Workspace{Path: canonical}, false, nil
		}
		emptyCanonical = true
	} else if !os.IsNotExist(err) {
		return Workspace{}, false, fmt.Errorf("stat workspace: %w", err)
	}

	staging, err := m.createStagingDir(root, key)
	if err != nil {
		return Workspace{}, false, err
	}
	if err := RunHook(ctx, afterCreate, staging, timeout); err != nil {
		failedPath, retainErr := m.retainFailedWorkspace(root, staging)
		if emptyCanonical {
			_ = os.Remove(canonical)
		}
		if retainErr != nil {
			return Workspace{}, true, fmt.Errorf("after_create failed: %w; retain failed workspace: %v", err, retainErr)
		}
		if writeErr := writePrepareError(failedPath, err); writeErr != nil {
			return Workspace{}, true, fmt.Errorf("after_create failed: %w; write prepare error: %v", err, writeErr)
		}
		return Workspace{}, true, &PrepareHookError{Err: err, FailedPath: failedPath}
	}
	if emptyCanonical {
		if err := os.Remove(canonical); err != nil {
			_ = os.RemoveAll(staging)
			return Workspace{}, true, fmt.Errorf("remove empty workspace before promotion: %w", err)
		}
	}
	if err := os.Rename(staging, canonical); err != nil {
		_ = os.RemoveAll(staging)
		return Workspace{}, true, fmt.Errorf("promote prepared workspace: %w", err)
	}
	return Workspace{Path: canonical}, true, nil
}

func (m Manager) RemoveForIssue(ctx context.Context, identifier, beforeRemove string, timeout time.Duration) error {
	path := filepath.Join(m.Root, domain.SanitizeWorkspaceKey(identifier))
	if err := EnsurePathInsideRoot(m.Root, path); err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	if beforeRemove != "" {
		_ = RunHook(ctx, beforeRemove, path, timeout)
	}
	return os.RemoveAll(path)
}

func (m Manager) createStagingDir(root, key string) (string, error) {
	parent := filepath.Join(root, PreparingDirName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create preparing workspace root: %w", err)
	}
	for i := 0; i < 100; i++ {
		name := key + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		if i > 0 {
			name += "-" + strconv.Itoa(i)
		}
		path := filepath.Join(parent, name)
		if err := EnsurePathInsideRoot(root, path); err != nil {
			return "", err
		}
		if err := os.Mkdir(path, 0o700); err == nil {
			return path, nil
		} else if !os.IsExist(err) {
			return "", fmt.Errorf("create preparing workspace: %w", err)
		}
	}
	return "", fmt.Errorf("create preparing workspace: exhausted unique names")
}

func (m Manager) retainFailedWorkspace(root, staging string) (string, error) {
	parent := filepath.Join(root, FailedDirName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("create failed workspace root: %w", err)
	}
	base := filepath.Base(staging)
	for i := 0; i < 100; i++ {
		name := base
		if i > 0 {
			name += "-" + strconv.Itoa(i)
		}
		path := filepath.Join(parent, name)
		if err := EnsurePathInsideRoot(root, path); err != nil {
			_ = os.RemoveAll(staging)
			return "", err
		}
		if err := os.Rename(staging, path); err == nil {
			return path, nil
		} else if !os.IsExist(err) {
			_ = os.RemoveAll(staging)
			return "", fmt.Errorf("retain failed workspace: %w", err)
		}
	}
	_ = os.RemoveAll(staging)
	return "", fmt.Errorf("retain failed workspace: exhausted unique names")
}

func isEmptyDir(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("read workspace: %w", err)
	}
	return len(entries) == 0, nil
}

func writePrepareError(path string, err error) error {
	body := "after_create failed\n\n" + err.Error() + "\n"
	return os.WriteFile(filepath.Join(path, "prepare-error.txt"), []byte(body), 0o600)
}
