package agent

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/openai/symphony/go/internal/domain"
)

type RunLogs struct {
	mu       sync.Mutex
	paths    domain.RunLogPaths
	protocol *os.File
	stderr   *os.File
	result   *os.File
}

func OpenRunLogs(paths domain.RunLogPaths) (*RunLogs, error) {
	logs := &RunLogs{paths: paths}
	if paths.Protocol != "" {
		if err := os.MkdirAll(filepath.Dir(paths.Protocol), 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(paths.Protocol, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
		logs.protocol = f
	}
	if paths.Stderr != "" {
		if err := os.MkdirAll(filepath.Dir(paths.Stderr), 0o700); err != nil {
			logs.Close()
			return nil, err
		}
		f, err := os.OpenFile(paths.Stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			logs.Close()
			return nil, err
		}
		logs.stderr = f
	}
	if paths.Result != "" {
		if err := os.MkdirAll(filepath.Dir(paths.Result), 0o700); err != nil {
			logs.Close()
			return nil, err
		}
		f, err := os.OpenFile(paths.Result, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			logs.Close()
			return nil, err
		}
		logs.result = f
	}
	return logs, nil
}

func (l *RunLogs) Paths() domain.RunLogPaths {
	if l == nil {
		return domain.RunLogPaths{}
	}
	return l.paths
}

func (l *RunLogs) StderrWriter(fallback io.Writer) io.Writer {
	if l == nil || l.stderr == nil {
		return fallback
	}
	return io.MultiWriter(fallback, l.stderr)
}

func (l *RunLogs) WriteProtocol(direction string, line []byte) {
	if l == nil || l.protocol == nil {
		return
	}
	entry := map[string]any{
		"at":        time.Now().UTC(),
		"direction": direction,
		"line":      string(line),
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.protocol.Write(append(b, '\n'))
}

func (l *RunLogs) WriteResult(res Result) {
	if l == nil || l.result == nil {
		return
	}
	body := map[string]any{
		"at":         time.Now().UTC(),
		"session_id": res.SessionID,
		"thread_id":  res.ThreadID,
		"turn_id":    res.TurnID,
		"completed":  res.Completed,
		"usage":      res.Usage,
		"logs":       res.Logs,
	}
	if res.Err != nil {
		body["error"] = res.Err.Error()
	}
	b, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.result.Write(append(b, '\n'))
}

func (l *RunLogs) Close() {
	if l == nil {
		return
	}
	if l.protocol != nil {
		_ = l.protocol.Close()
	}
	if l.stderr != nil {
		_ = l.stderr.Close()
	}
	if l.result != nil {
		_ = l.result.Close()
	}
}
