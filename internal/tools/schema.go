package tools

import "encoding/json"

func Schema(properties map[string]any, required []string) json.RawMessage {
	schema := map[string]any{"type": "object", "additionalProperties": false}
	if len(properties) > 0 {
		schema["properties"] = properties
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	b, _ := json.Marshal(schema)
	return b
}

func StringProp() map[string]any { return map[string]any{"type": "string"} }
func BoolProp() map[string]any   { return map[string]any{"type": "boolean"} }
func IntProp(min, max int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": max}
}
