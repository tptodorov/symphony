package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func EnsurePathInsideRoot(root, path string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	rootEval, err := evalExisting(rootAbs)
	if err != nil {
		return fmt.Errorf("evaluate root: %w", err)
	}
	pathEval, err := evalPath(pathAbs)
	if err != nil {
		return fmt.Errorf("evaluate path: %w", err)
	}
	rel, err := filepath.Rel(rootEval, pathEval)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path escapes workspace root: %s", path)
	}
	return nil
}

func evalExisting(path string) (string, error) {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p, nil
	}
	return filepath.Abs(path)
}

func evalPath(path string) (string, error) {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p, nil
	}
	parent, base := filepath.Dir(path), filepath.Base(path)
	p, err := evalExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(p, base), nil
}
