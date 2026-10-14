package stub

import (
	"encoding/json"
	"fmt"
)

// jsonSchema is the handful of JSON Schema keywords minimalValid needs: only
// enough to build one example value, not to validate one.
type jsonSchema struct {
	Type       string                `json:"type"`
	Properties map[string]jsonSchema `json:"properties"`
	Required   []string              `json:"required"`
	Enum       []json.RawMessage     `json:"enum"`
}

// minimalValid parses schema and returns the smallest value satisfying it:
// an object with every required property present (recursively minimal), an
// empty array, or the zero value of a scalar type. A field with an enum uses
// its first value. Good enough to exercise a real caller's JSON decoding
// without a model.
func minimalValid(schema json.RawMessage) (any, error) {
	var s jsonSchema
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil, fmt.Errorf("invalid schema: %w", err)
	}
	return minimalValue(s), nil
}

func minimalValue(s jsonSchema) any {
	if len(s.Enum) > 0 {
		var v any
		if err := json.Unmarshal(s.Enum[0], &v); err == nil {
			return v
		}
	}
	switch s.Type {
	case "object":
		obj := map[string]any{}
		for _, name := range s.Required {
			if prop, ok := s.Properties[name]; ok {
				obj[name] = minimalValue(prop)
			} else {
				obj[name] = nil
			}
		}
		return obj
	case "array":
		return []any{}
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "string":
		return ""
	default:
		return nil
	}
}
