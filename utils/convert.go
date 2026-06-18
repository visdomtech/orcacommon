package utils

import "encoding/json"

// StructToMap serializes a pointer to any json-tagged struct into a map[string]any.
// Returns nil if v is nil.
func StructToMap[T any](v *T) map[string]any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// MapToStruct deserializes a map[string]any back into a typed struct pointer.
// Returns nil if m is nil or empty.
func MapToStruct[T any](m map[string]any) *T {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return nil
	}
	return &v
}

func PrettyJSON(v any) json.RawMessage {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return json.RawMessage("")
	}
	return b
}
