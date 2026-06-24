package codex

import (
	"encoding/json"

	"github.com/openai/symphony/go/internal/domain"
)

func ExtractUsage(b []byte) domain.TokenUsage {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return domain.TokenUsage{}
	}
	if found := findUsage(m); found != nil {
		return domain.TokenUsage{
			InputTokens:  asInt(first(found["input_tokens"], found["inputTokens"])),
			OutputTokens: asInt(first(found["output_tokens"], found["outputTokens"])),
			TotalTokens:  asInt(first(found["total_tokens"], found["totalTokens"])),
		}
	}
	return domain.TokenUsage{}
}

func ExtractRateLimits(b []byte) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return findRateLimits(m)
}

func findUsage(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"usage", "total_token_usage", "totalTokenUsage"} {
		if child, ok := m[key].(map[string]any); ok {
			return child
		}
	}
	if hasAny(m, "input_tokens", "output_tokens", "total_tokens", "inputTokens", "outputTokens", "totalTokens") {
		return m
	}
	for key, child := range m {
		if key == "last_token_usage" || key == "lastTokenUsage" {
			continue
		}
		if found := findUsage(child); found != nil {
			return found
		}
	}
	return nil
}

func findRateLimits(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"rate_limits", "rateLimits", "rate_limit", "rateLimit"} {
		if child, ok := m[key].(map[string]any); ok {
			return child
		}
	}
	for _, child := range m {
		if found := findRateLimits(child); found != nil {
			return found
		}
	}
	return nil
}

func hasAny(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

func first(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}
