package observability

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestLoggerJSON(t *testing.T) {
	var b bytes.Buffer
	log := NewLogger(&b)
	Dispatch(log, "id", "ABC-1", "s")
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["issue_id"] != "id" || got["issue_identifier"] != "ABC-1" || got["session_id"] != "s" {
		t.Fatal(got)
	}
}
