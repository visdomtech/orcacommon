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

// Option configures the behavior of StructToMapLC.
type Option func(*options)

type options struct {
	idSuffix  bool // convert "ID" suffix to "Id"
	omitEmpty bool // omit empty string, nil pointer, nil/empty slice
}

// WithIDSuffix converts field names ending in "ID" to end in "Id".
// e.g. "UserID" becomes "userId" instead of "userID".
func WithIDSuffix() Option {
	return func(o *options) {
		o.idSuffix = true
	}
}

// WithOmitEmpty skips fields with empty values: empty string, nil pointer,
// nil or zero-length slice. Numeric and boolean fields are never omitted.
func WithOmitEmpty() Option {
	return func(o *options) {
		o.omitEmpty = true
	}
}

// StructToMapLC converts a struct to map[string]any using reflection,
// converting PascalCase field names to camelCase (first character lowercased).
// Embedded structs are flattened into the parent map.
// Optional behaviors (ID suffix conversion, omit-empty) can be enabled via Option.
// Returns nil if v is nil or not a struct.
func StructToMapLC(v any, opts ...Option) map[string]any {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return structToMap(v, &o)
}

func structToMap(v any, o *options) map[string]any {
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

		// Flatten embedded structs into the parent map
		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			embedded := structToMap(fieldValue.Interface(), o)
			for k, v := range embedded {
				result[k] = v
			}
			continue
		}

		// Convert field name
		key := toCamelCase(field.Name)
		if o.idSuffix {
			key = convertIDSuffix(key)
		}

		// Omit empty: only for string, pointer, slice
		if o.omitEmpty && isEmptyOmittable(fieldValue) {
			continue
		}

		// Handle the field value
		result[key] = convertValue(fieldValue, o)
	}

	return result
}

// convertIDSuffix replaces a trailing "ID" with "Id".
// e.g. "userID" -> "userId", "iD" (from field "ID") -> "id".
func convertIDSuffix(s string) string {
	if s == "iD" {
		return "id"
	}
	if strings.HasSuffix(s, "ID") {
		return s[:len(s)-2] + "Id"
	}
	return s
}

// isEmptyOmittable returns true for empty strings, nil pointers,
// and nil or zero-length slices. All other types return false.
func isEmptyOmittable(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.Len() == 0
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	case reflect.Slice:
		return v.IsNil() || v.Len() == 0
	}
	return false
}

// toCamelCase converts PascalCase to camelCase by lowercasing the first character
func toCamelCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// convertValue handles different types of field values
func convertValue(v reflect.Value, o *options) any {
	// Handle pointers
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		return convertValue(v.Elem(), o)
	}

	// Handle nested structs
	if v.Kind() == reflect.Struct {
		return structToMap(v.Interface(), o)
	}

	// Handle slices
	if v.Kind() == reflect.Slice {
		if v.IsNil() {
			return nil
		}
		result := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			result[i] = convertValue(v.Index(i), o)
		}
		return result
	}

	// Return primitive values as-is
	return v.Interface()
}
