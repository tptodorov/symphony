package codex

import "testing"

func TestExtractUsagePrefersAppServerAbsoluteTotal(t *testing.T) {
	usage := ExtractUsage([]byte(`{
		"method": "thread/tokenUsage/updated",
		"params": {
			"threadId": "thr_1",
			"tokenUsage": {
				"last": {"inputTokens": 1, "outputTokens": 2, "totalTokens": 3},
				"total": {"inputTokens": 100, "outputTokens": 50, "totalTokens": 150}
			}
		}
	}`))
	if usage.InputTokens != 100 || usage.OutputTokens != 50 || usage.TotalTokens != 150 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestExtractUsageKeepsTotalTokenUsageCompatibility(t *testing.T) {
	usage := ExtractUsage([]byte(`{
		"method": "thread/tokenUsage/updated",
		"params": {
			"threadId": "thr_1",
			"total_token_usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
		}
	}`))
	if usage.InputTokens != 10 || usage.OutputTokens != 5 || usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", usage)
	}
}
