package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalCheckpointJSONReplacesEmbeddedNUL(t *testing.T) {
	data, err := marshalCheckpointJSON(map[string]any{
		"nested": []any{"before\x00after", map[string]string{"literal": `\u0000`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	if !strings.Contains(encoded, `before\ufffdafter`) {
		t.Fatalf("embedded NUL was not replaced: %s", encoded)
	}
	if !strings.Contains(encoded, `\\u0000`) {
		t.Fatalf("literal backslash-u text was altered: %s", encoded)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("sanitized checkpoint JSON is invalid: %v", err)
	}
}
