package utils

import (
	"encoding/json"
	"reflect"
	"strings"
)

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

// StructToMapLC converts a struct to map[string]any using reflection,
// converting PascalCase field names to camelCase (first character lowercased).
// Returns nil if v is nil or not a struct.
func StructToMapLC(v any) map[string]any {
	if v == nil {
		return nil
	}

	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return nil
	}

	result := make(map[string]any)
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		fieldValue := val.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Convert PascalCase to camelCase
		key := toCamelCase(field.Name)

		// Handle the field value
		result[key] = convertValue(fieldValue)
	}

	return result
}

// toCamelCase converts PascalCase to camelCase by lowercasing the first character
func toCamelCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// convertValue handles different types of field values
func convertValue(v reflect.Value) any {
	// Handle pointers
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		return convertValue(v.Elem())
	}

	// Handle nested structs
	if v.Kind() == reflect.Struct {
		return StructToMapLC(v.Interface())
	}

	// Handle slices
	if v.Kind() == reflect.Slice {
		if v.IsNil() {
			return nil
		}
		result := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			result[i] = convertValue(v.Index(i))
		}
		return result
	}

	// Return primitive values as-is
	return v.Interface()
}
