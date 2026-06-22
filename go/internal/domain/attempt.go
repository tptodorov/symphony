package domain

import "time"

type RunAttemptStatus string

const (
	RunAttemptPreparingWorkspace RunAttemptStatus = "preparing_workspace"
	RunAttemptBuildingPrompt     RunAttemptStatus = "building_prompt"
	RunAttemptLaunchingAgent     RunAttemptStatus = "launching_agent_process"
	RunAttemptStreaming          RunAttemptStatus = "streaming_turn"
	RunAttemptSucceeded          RunAttemptStatus = "succeeded"
	RunAttemptFailed             RunAttemptStatus = "failed"
	RunAttemptTimedOut           RunAttemptStatus = "timed_out"
	RunAttemptStalled            RunAttemptStatus = "stalled"
	RunAttemptCanceled           RunAttemptStatus = "canceled_by_reconciliation"
)

type RunAttempt struct {
	IssueID         string
	IssueIdentifier string
	Attempt         int
	WorkspacePath   string
	StartedAt       time.Time
	Status          RunAttemptStatus
	Error           *string
}
