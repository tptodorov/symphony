package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

func RunHook(ctx context.Context, script, cwd string, timeout time.Duration) error {
	if script == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = time.Minute
	}
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(hctx, "bash", "-lc", script)
	cmd.Dir = cwd
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if hctx.Err() != nil {
			return fmt.Errorf("hook timeout: %w", hctx.Err())
		}
		return fmt.Errorf("hook failed: %w: %s", err, truncate(out.String(), 4096))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
