package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/openai/symphony/go/internal/domain"
)

type Workspace struct{ Path string }

type Manager struct{ Root string }

func NewManager(root string) Manager { return Manager{Root: root} }

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
