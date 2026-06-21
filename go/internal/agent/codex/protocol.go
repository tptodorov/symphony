package codex

import "strings"

func IsTerminalEvent(line string) bool {
	return strings.Contains(line, "turn.completed") || strings.Contains(line, "task_complete") || strings.Contains(line, "completed")
}
