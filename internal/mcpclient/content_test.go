package mcpclient

import "testing"

func TestDecodeContentTextParsesPlainJSON(t *testing.T) {
	value := decodeContentText("{\"current_data\":[{\"uv\":12}]}")
	object, ok := value.(map[string]any)
	if !ok || object["current_data"] == nil {
		t.Fatalf("expected plain JSON object, got %#v", value)
	}
}

func TestDecodeContentTextParsesJSONCodeBlock(t *testing.T) {
	value := decodeContentText("```json\n{\"current_data\":[{\"uv\":12}]}\n```")
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected decoded object, got %#v", value)
	}
	rows, ok := object["current_data"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("expected current_data array, got %#v", object["current_data"])
	}
}
