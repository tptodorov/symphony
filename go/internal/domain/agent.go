package domain

type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type AgentTotals struct {
	InputTokens    int     `json:"input_tokens"`
	OutputTokens   int     `json:"output_tokens"`
	TotalTokens    int     `json:"total_tokens"`
	SecondsRunning float64 `json:"seconds_running"`
}

type RunLogPaths struct {
	Protocol string `json:"protocol,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Result   string `json:"result,omitempty"`
}
