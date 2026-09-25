package proactivity

import "testing"

func TestValidateResponseFormat(t *testing.T) {
	valid := map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "result", "strict": true, "schema": map[string]any{"type": "object"}}}
	if err := validateResponseFormat(valid); err != nil {
		t.Fatal(err)
	}
	valid["json_schema"].(map[string]any)["strict"] = false
	if err := validateResponseFormat(valid); err == nil {
		t.Fatal("expected strict response format validation")
	}
}
