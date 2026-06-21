package codex

import (
	"encoding/json"

	"github.com/openai/symphony/go/internal/domain"
)

func ExtractUsage(b []byte) domain.TokenUsage {
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if u, ok := m["usage"].(map[string]any); ok {
		m = u
	}
	return domain.TokenUsage{InputTokens: asInt(m["input_tokens"]), OutputTokens: asInt(m["output_tokens"]), TotalTokens: asInt(m["total_tokens"])}
}
func asInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}
