package orchestrator

import "time"

type Snapshot struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Running     []RunningSnapshot `json:"running"`
	RetryQueue  []RetrySnapshot   `json:"retry_queue"`
	Counts      map[string]int    `json:"counts"`
}
type RunningSnapshot struct {
	IssueID         string `json:"issue_id,omitempty"`
	IssueIdentifier string `json:"issue_identifier,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	Workspace       string `json:"workspace,omitempty"`
}
type RetrySnapshot struct {
	IssueID         string    `json:"issue_id"`
	IssueIdentifier string    `json:"issue_identifier"`
	Attempt         int       `json:"attempt"`
	At              time.Time `json:"at"`
}
