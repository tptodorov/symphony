package domain

type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type AgentTotals struct {
	InputTokens    int
	OutputTokens   int
	TotalTokens    int
	SecondsRunning float64
}
