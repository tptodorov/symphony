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
	if found := findAbsoluteUsage(m); found != nil {
		return usageFromMap(found)
	}
	if found := findDefinedGenericUsage(m); found != nil {
		return usageFromMap(found)
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

func findAbsoluteUsage(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if tokenUsage, ok := m["tokenUsage"].(map[string]any); ok {
		if total, ok := tokenUsage["total"].(map[string]any); ok && hasUsageShape(total) {
			return total
		}
	}
	if tokenUsage, ok := m["token_usage"].(map[string]any); ok {
		if total, ok := tokenUsage["total"].(map[string]any); ok && hasUsageShape(total) {
			return total
		}
	}
	for _, key := range []string{"total_token_usage", "totalTokenUsage"} {
		if child, ok := m[key].(map[string]any); ok {
			return child
		}
	}
	for key, child := range m {
		if isDeltaUsageKey(key) {
			continue
		}
		if found := findAbsoluteUsage(child); found != nil {
			return found
		}
	}
	return nil
}

func findDefinedGenericUsage(m map[string]any) map[string]any {
	method, _ := m["method"].(string)
	if method != "thread/tokenUsage/updated" && method != "turn/completed" {
		return nil
	}
	return findUsageKey(m)
}

func findUsageKey(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if child, ok := m["usage"].(map[string]any); ok && hasUsageShape(child) {
		return child
	}
	for key, child := range m {
		if isDeltaUsageKey(key) {
			continue
		}
		if found := findUsageKey(child); found != nil {
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

func hasUsageShape(m map[string]any) bool {
	return hasAny(m, "input_tokens", "output_tokens", "total_tokens", "inputTokens", "outputTokens", "totalTokens")
}

func isDeltaUsageKey(key string) bool {
	return key == "last" || key == "last_token_usage" || key == "lastTokenUsage"
}

func usageFromMap(found map[string]any) domain.TokenUsage {
	return domain.TokenUsage{
		InputTokens:  asInt(first(found["input_tokens"], found["inputTokens"])),
		OutputTokens: asInt(first(found["output_tokens"], found["outputTokens"])),
		TotalTokens:  asInt(first(found["total_tokens"], found["totalTokens"])),
	}
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
