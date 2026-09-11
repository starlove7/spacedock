package tools

import "encoding/json"

func Schema(properties map[string]any, required []string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required})
	return b
}

func StringProp() map[string]any { return map[string]any{"type": "string"} }
func BoolProp() map[string]any   { return map[string]any{"type": "boolean"} }
func IntProp(min, max int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": max}
}
