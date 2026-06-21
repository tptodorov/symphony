package observability

import (
	"io"
	"log/slog"
)

func NewLogger(w io.Writer, attrs ...slog.Attr) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{})
	args := make([]any, len(attrs))
	for i, a := range attrs {
		args[i] = a
	}
	return slog.New(h).With(args...)
}

func ValidationFailure(log *slog.Logger, err error) { log.Error("validation failure", "error", err) }
func Dispatch(log *slog.Logger, issueID, issueIdentifier, sessionID string) {
	log.Info("dispatch", "issue_id", issueID, "issue_identifier", issueIdentifier, "session_id", sessionID)
}
func WorkerStart(log *slog.Logger, issueID, issueIdentifier, sessionID string) {
	log.Info("worker start", "issue_id", issueID, "issue_identifier", issueIdentifier, "session_id", sessionID)
}
func WorkerExit(log *slog.Logger, issueID, issueIdentifier, sessionID string, err error) {
	if err != nil {
		log.Error("worker exit", "issue_id", issueID, "issue_identifier", issueIdentifier, "session_id", sessionID, "error", err)
		return
	}
	log.Info("worker exit", "issue_id", issueID, "issue_identifier", issueIdentifier, "session_id", sessionID)
}
func RetryScheduled(log *slog.Logger, issueID, issueIdentifier string, delay any) {
	log.Info("retry scheduled", "issue_id", issueID, "issue_identifier", issueIdentifier, "delay", delay)
}
func Reconciliation(log *slog.Logger, issueID, issueIdentifier, action string) {
	log.Info("reconciliation", "issue_id", issueID, "issue_identifier", issueIdentifier, "action", action)
}
func HookFailure(log *slog.Logger, hook string, err error) {
	log.Error("hook failure", "hook", hook, "error", err)
}
func TrackerError(log *slog.Logger, err error) { log.Error("tracker error", "error", err) }
func ReloadError(log *slog.Logger, err error)  { log.Error("reload error", "error", err) }
