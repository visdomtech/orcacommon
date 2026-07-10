package utils

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStructToMapLC_Basic(t *testing.T) {
	type TestStruct struct {
		FirstName string
		LastName  string
		Age       int
	}

	s := TestStruct{
		FirstName: "John",
		LastName:  "Doe",
		Age:       30,
	}

	result := StructToMapLC(s)

	if result["firstName"] != "John" {
		t.Errorf("Expected firstName='John', got %v", result["firstName"])
	}
	if result["lastName"] != "Doe" {
		t.Errorf("Expected lastName='Doe', got %v", result["lastName"])
	}
	if result["age"] != 30 {
		t.Errorf("Expected age=30, got %v", result["age"])
	}
}

func TestStructToMapLC_Pointer(t *testing.T) {
	type TestStruct struct {
		Name string
	}

	s := &TestStruct{Name: "Test"}
	result := StructToMapLC(s)

	if result["name"] != "Test" {
		t.Errorf("Expected name='Test', got %v", result["name"])
	}
}

func TestStructToMapLC_NilPointer(t *testing.T) {
	var s *struct{ Name string }
	result := StructToMapLC(s)

	if result != nil {
		t.Errorf("Expected nil for nil pointer, got %v", result)
	}
}

func TestStructToMapLC_Nested(t *testing.T) {
	type Address struct {
		StreetName string
		City       string
	}

	type Person struct {
		FullName string
		HomeAddress Address
	}

	p := Person{
		FullName: "Jane Doe",
		HomeAddress: Address{
			StreetName: "Main St",
			City:       "NYC",
		},
	}

	result := StructToMapLC(p)

	if result["fullName"] != "Jane Doe" {
		t.Errorf("Expected fullName='Jane Doe', got %v", result["fullName"])
	}

	addr, ok := result["homeAddress"].(map[string]any)
	if !ok {
		t.Fatalf("Expected homeAddress to be map, got %T", result["homeAddress"])
	}
	if addr["streetName"] != "Main St" {
		t.Errorf("Expected streetName='Main St', got %v", addr["streetName"])
	}
	if addr["city"] != "NYC" {
		t.Errorf("Expected city='NYC', got %v", addr["city"])
	}
}

func TestStructToMapLC_Slice(t *testing.T) {
	type Item struct {
		ItemName string
	}

	type Container struct {
		Items []Item
	}

	c := Container{
		Items: []Item{
			{ItemName: "Item1"},
			{ItemName: "Item2"},
		},
	}

	result := StructToMapLC(c)

	items, ok := result["items"].([]any)
	if !ok {
		t.Fatalf("Expected items to be slice, got %T", result["items"])
	}
	if len(items) != 2 {
		t.Errorf("Expected 2 items, got %d", len(items))
	}

	item0, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("Expected item to be map, got %T", items[0])
	}
	if item0["itemName"] != "Item1" {
		t.Errorf("Expected itemName='Item1', got %v", item0["itemName"])
	}
}

func TestStructToMapLC_UnexportedFields(t *testing.T) {
	type TestStruct struct {
		Public  string
		private string
	}

	s := TestStruct{
		Public:  "visible",
		private: "hidden",
	}

	result := StructToMapLC(s)

	if result["public"] != "visible" {
		t.Errorf("Expected public='visible', got %v", result["public"])
	}
	if _, exists := result["private"]; exists {
		t.Errorf("Expected private field to be skipped")
	}
}

func TestStructToMapLC_FieldPointer(t *testing.T) {
	type TestStruct struct {
		Name *string
		Age  *int
	}

	name := "Test"
	s := TestStruct{
		Name: &name,
		Age:  nil,
	}

	result := StructToMapLC(s)

	if result["name"] != "Test" {
		t.Errorf("Expected name='Test', got %v", result["name"])
	}
	if result["age"] != nil {
		t.Errorf("Expected age=nil, got %v", result["age"])
	}
}

func TestStructToMapLC_EmbeddedStruct(t *testing.T) {
	type Base struct {
		BaseName string
		BaseAge  int
	}
	type Extended struct {
		Base
		Extra string
	}

	s := Extended{
		Base:  Base{BaseName: "base", BaseAge: 10},
		Extra: "extra",
	}

	result := StructToMapLC(s)

	if result["baseName"] != "base" {
		t.Errorf("Expected baseName='base', got %v", result["baseName"])
	}
	if result["baseAge"] != 10 {
		t.Errorf("Expected baseAge=10, got %v", result["baseAge"])
	}
	if result["extra"] != "extra" {
		t.Errorf("Expected extra='extra', got %v", result["extra"])
	}
	// embedded struct should NOT appear as a nested key
	if _, exists := result["base"]; exists {
		t.Errorf("Embedded struct 'base' should be flattened, not nested")
	}
}

func TestStructToMapLC_WithIDSuffix(t *testing.T) {
	type TestStruct struct {
		UserID              string
		OrderID             int
		Name                string
		SimpleID            string
		LongRunningToolIDs  []string
		ItemIDs             []int
	}

	s := TestStruct{
		UserID:             "u1",
		OrderID:            42,
		Name:               "test",
		SimpleID:           "s1",
		LongRunningToolIDs: []string{"t1", "t2"},
		ItemIDs:            []int{1, 2, 3},
	}
	result := StructToMapLC(s, WithIDSuffix())

	if _, exists := result["userID"]; exists {
		t.Errorf("Expected userID to be converted to userId")
	}
	if result["userId"] != "u1" {
		t.Errorf("Expected userId='u1', got %v", result["userId"])
	}
	if result["orderId"] != 42 {
		t.Errorf("Expected orderId=42, got %v", result["orderId"])
	}
	if result["name"] != "test" {
		t.Errorf("Expected name='test', got %v", result["name"])
	}
	if result["simpleId"] != "s1" {
		t.Errorf("Expected simpleId='s1', got %v", result["simpleId"])
	}
	// plural IDs
	if _, exists := result["longRunningToolIDs"]; exists {
		t.Errorf("Expected longRunningToolIDs to be converted to longRunningToolIds")
	}
	ids, ok := result["longRunningToolIds"].([]any)
	if !ok || len(ids) != 2 {
		t.Errorf("Expected longRunningToolIds=[t1,t2], got %v", result["longRunningToolIds"])
	}
	itemIds, ok := result["itemIds"].([]any)
	if !ok || len(itemIds) != 3 {
		t.Errorf("Expected itemIds with 3 elements, got %v", result["itemIds"])
	}
}

func TestStructToMapLC_WithOmitEmpty(t *testing.T) {
	type TestStruct struct {
		Name   string
		Age    int
		Active bool
		Tag    *string
		Items  []string
		Score  float64
	}

	s := TestStruct{
		Name:   "",
		Age:    0,
		Active: false,
		Tag:    nil,
		Items:  nil,
		Score:  0.0,
	}
	result := StructToMapLC(s, WithOmitEmpty())

	// empty string should be omitted
	if _, exists := result["name"]; exists {
		t.Errorf("Expected empty string 'name' to be omitted")
	}
	// nil pointer should be omitted
	if _, exists := result["tag"]; exists {
		t.Errorf("Expected nil pointer 'tag' to be omitted")
	}
	// nil slice should be omitted
	if _, exists := result["items"]; exists {
		t.Errorf("Expected nil slice 'items' to be omitted")
	}
	// int zero should NOT be omitted (numbers are never omitted)
	if _, exists := result["age"]; !exists {
		t.Errorf("Expected zero int 'age' to be present (numbers not omitted)")
	}
	// bool false should NOT be omitted
	if _, exists := result["active"]; !exists {
		t.Errorf("Expected false bool 'active' to be present (booleans not omitted)")
	}
	// float zero should NOT be omitted
	if _, exists := result["score"]; !exists {
		t.Errorf("Expected zero float 'score' to be present (numbers not omitted)")
	}
}

func TestStructToMapLC_WithOmitEmpty_EmptySlice(t *testing.T) {
	type TestStruct struct {
		Tags []string
	}
	s := TestStruct{Tags: []string{}}
	result := StructToMapLC(s, WithOmitEmpty())

	if _, exists := result["tags"]; exists {
		t.Errorf("Expected empty slice 'tags' to be omitted")
	}
}

func TestStructToMapLC_WithOmitEmpty_NonEmptyValues(t *testing.T) {
	type TestStruct struct {
		Name  string
		Tag   *string
		Items []int
	}
	tag := "go"
	s := TestStruct{
		Name:  "hello",
		Tag:   &tag,
		Items: []int{1, 2},
	}
	result := StructToMapLC(s, WithOmitEmpty())

	if result["name"] != "hello" {
		t.Errorf("Expected name='hello', got %v", result["name"])
	}
	if result["tag"] != "go" {
		t.Errorf("Expected tag='go', got %v", result["tag"])
	}
	items, ok := result["items"].([]any)
	if !ok || len(items) != 2 {
		t.Errorf("Expected items with 2 elements, got %v", result["items"])
	}
}

func TestStructToMapLC_CombinedOptions(t *testing.T) {
	type Base struct {
		BaseID string
	}
	type TestStruct struct {
		Base
		UserName string
		AccountID string
		Age      int
	}

	s := TestStruct{
		Base:      Base{BaseID: "b1"},
		UserName:  "",
		AccountID: "a1",
		Age:       0,
	}

	result := StructToMapLC(s, WithIDSuffix(), WithOmitEmpty())

	// embedded field with ID suffix
	if result["baseId"] != "b1" {
		t.Errorf("Expected baseId='b1', got %v", result["baseId"])
	}
	// empty string should be omitted
	if _, exists := result["userName"]; exists {
		t.Errorf("Expected empty userName to be omitted")
	}
	// ID suffix + non-empty
	if result["accountId"] != "a1" {
		t.Errorf("Expected accountId='a1', got %v", result["accountId"])
	}
	// zero int should NOT be omitted
	if _, exists := result["age"]; !exists {
		t.Errorf("Expected zero int 'age' to be present")
	}
}

func TestStructToMapLC_NoOptions_BackwardCompat(t *testing.T) {
	type TestStruct struct {
		UserID string
		Name   string
	}
	s := TestStruct{UserID: "u1", Name: ""}
	result := StructToMapLC(s)

	// Without WithIDSuffix, "UserID" -> "userID" (not "userId")
	if result["userID"] != "u1" {
		t.Errorf("Expected userID='u1', got %v", result["userID"])
	}
	// Without WithOmitEmpty, empty string should be present
	if _, exists := result["name"]; !exists {
		t.Errorf("Expected empty name to be present without WithOmitEmpty")
	}
}

func TestStructToMapLC_WithNilMapToEmpty(t *testing.T) {
	type TestStruct struct {
		Name   string
		Labels map[string]string
		Tags   map[string]any
	}

	// nil maps without option -> null
	s := TestStruct{Name: "test", Labels: nil, Tags: nil}
	result := StructToMapLC(s)
	if result["labels"] != nil {
		t.Errorf("Expected nil labels without option, got %v", result["labels"])
	}

	// nil maps with option -> {}
	result = StructToMapLC(s, WithNilMapToEmpty())
	labels, ok := result["labels"].(map[string]any)
	if !ok {
		t.Fatalf("Expected labels to be map, got %T", result["labels"])
	}
	if len(labels) != 0 {
		t.Errorf("Expected empty map, got %v", labels)
	}
	tags, ok := result["tags"].(map[string]any)
	if !ok || len(tags) != 0 {
		t.Errorf("Expected tags to be empty map, got %v", result["tags"])
	}

	// non-nil maps should be unaffected
	s2 := TestStruct{Name: "test2", Labels: map[string]string{"env": "prod"}}
	result2 := StructToMapLC(s2, WithNilMapToEmpty())
	labels2, ok := result2["labels"].(map[string]any)
	if !ok {
		t.Fatalf("Expected labels to be map, got %T", result2["labels"])
	}
	if labels2["env"] != "prod" {
		t.Errorf("Expected env='prod', got %v", labels2["env"])
	}
}

func TestStructToMapLC_WithNilSliceToEmpty(t *testing.T) {
	type TestStruct struct {
		Name  string
		Items []string
		Tags  []int
	}

	// nil slices without option -> null
	s := TestStruct{Name: "test"}
	result := StructToMapLC(s)
	if result["items"] != nil {
		t.Errorf("Expected nil items without option, got %v", result["items"])
	}

	// nil slices with option -> []
	result = StructToMapLC(s, WithNilSliceToEmpty())
	items, ok := result["items"].([]any)
	if !ok {
		t.Fatalf("Expected items to be slice, got %T", result["items"])
	}
	if len(items) != 0 {
		t.Errorf("Expected empty slice, got %v", items)
	}
	tags, ok := result["tags"].([]any)
	if !ok || len(tags) != 0 {
		t.Errorf("Expected tags to be empty slice, got %v", result["tags"])
	}

	// non-nil slices should be unaffected
	s2 := TestStruct{Name: "test2", Items: []string{"a", "b"}}
	result2 := StructToMapLC(s2, WithNilSliceToEmpty())
	items2, ok := result2["items"].([]any)
	if !ok || len(items2) != 2 {
		t.Errorf("Expected items with 2 elements, got %v", result2["items"])
	}
}

func TestStructToMapLC_AllOptionsCombined(t *testing.T) {
	type Base struct {
		BaseID string
	}
	type TestStruct struct {
		Base
		UserName  string
		AccountID string
		ToolIDs   []string
		Labels    map[string]string
		Items     []int
		Age       int
	}

	s := TestStruct{
		Base:      Base{BaseID: "b1"},
		UserName:  "",
		AccountID: "a1",
		ToolIDs:   nil,
		Labels:    nil,
		Items:     nil,
		Age:       0,
	}

	result := StructToMapLC(s,
		WithIDSuffix(),
		WithOmitEmpty(),
		WithNilMapToEmpty(),
		WithNilSliceToEmpty(),
	)

	// ID suffix
	if result["baseId"] != "b1" {
		t.Errorf("Expected baseId='b1', got %v", result["baseId"])
	}
	if result["accountId"] != "a1" {
		t.Errorf("Expected accountId='a1', got %v", result["accountId"])
	}
	// omitEmpty: empty string omitted
	if _, exists := result["userName"]; exists {
		t.Errorf("Expected empty userName to be omitted")
	}
	// zero int present
	if _, exists := result["age"]; !exists {
		t.Errorf("Expected zero age to be present")
	}
	// nil slice with WithNilSliceToEmpty -> [] (not omitted, because WithNilSliceToEmpty applies after omitEmpty check)
	// Actually: WithOmitEmpty omits nil slices. So items and toolIDs are omitted before value conversion.
	// The omitEmpty check skips nil slices entirely, so WithNilSliceToEmpty doesn't apply.
	if _, exists := result["items"]; exists {
		t.Errorf("Expected nil items to be omitted by WithOmitEmpty")
	}
	// nil map with WithNilMapToEmpty -> {}
	labels, ok := result["labels"].(map[string]any)
	if !ok || len(labels) != 0 {
		t.Errorf("Expected labels to be empty map, got %v", result["labels"])
	}
}

func TestToCamelCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"FirstName", "firstName"},
		{"ID", "iD"},
		{"Name", "name"},
		{"A", "a"},
		{"", ""},
		{"ABC", "aBC"},
	}

	for _, tt := range tests {
		result := toCamelCase(tt.input)
		if result != tt.expected {
			t.Errorf("toCamelCase(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func tConvertIDSuffix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"userID", "userId"},
		{"orderId", "orderId"},
		{"iD", "id"},
		{"name", "name"},
		{"", ""},
		{"orderID", "orderId"},
		{"longRunningToolIDs", "longRunningToolIds"},
		{"itemIDs", "itemIds"},
		{"iDs", "ids"},
		{"IDs", "Ids"},
	}

	for _, tt := range tests {
		result := convertIDSuffix(tt.input)
		if result != tt.expected {
			t.Errorf("convertIDSuffix(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

// valueMarshaler implements json.Marshaler with a value receiver.
type valueMarshaler struct {
	X int
	Y int
}

func (v valueMarshaler) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"x": v.X, "y": v.Y, "custom": true})
}

// ptrMarshaler implements json.Marshaler with a pointer receiver.
type ptrMarshaler struct {
	A string
	B string
}

func (p *ptrMarshaler) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"a": p.A, "b": p.B, "ptrCustom": true})
}

func TestStructToMapLC_MarshalJSON_ValueReceiver(t *testing.T) {
	type Wrapper struct {
		Name   string
		Coords valueMarshaler
	}

	s := Wrapper{
		Name:   "origin",
		Coords: valueMarshaler{X: 1, Y: 2},
	}

	result := StructToMapLC(s)

	if result["name"] != "origin" {
		t.Errorf("Expected name='origin', got %v", result["name"])
	}

	coords, ok := result["coords"].(map[string]any)
	if !ok {
		t.Fatalf("Expected coords to be map, got %T (%v)", result["coords"], result["coords"])
	}
	if coords["custom"] != true {
		t.Errorf("Expected custom=true from MarshalJSON, got %v", coords["custom"])
	}
	// json.Unmarshal decodes numbers as float64
	if coords["x"] != float64(1) {
		t.Errorf("Expected x=1, got %v", coords["x"])
	}
	if coords["y"] != float64(2) {
		t.Errorf("Expected y=2, got %v", coords["y"])
	}
}

func TestStructToMapLC_MarshalJSON_PointerReceiver(t *testing.T) {
	type Wrapper struct {
		Label string
		Data  ptrMarshaler
	}

	s := Wrapper{
		Label: "test",
		Data:  ptrMarshaler{A: "alpha", B: "beta"},
	}

	result := StructToMapLC(s)

	data, ok := result["data"].(map[string]any)
	if !ok {
		t.Fatalf("Expected data to be map, got %T (%v)", result["data"], result["data"])
	}
	if data["ptrCustom"] != true {
		t.Errorf("Expected ptrCustom=true from MarshalJSON, got %v", data["ptrCustom"])
	}
	if data["a"] != "alpha" {
		t.Errorf("Expected a='alpha', got %v", data["a"])
	}
}

func TestStructToMapLC_MarshalJSON_TimeField(t *testing.T) {
	type Event struct {
		Title     string
		StartedAt time.Time
	}

	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	e := Event{Title: "launch", StartedAt: ts}

	result := StructToMapLC(e)

	if result["title"] != "launch" {
		t.Errorf("Expected title='launch', got %v", result["title"])
	}

	// time.Time.MarshalJSON produces an RFC3339 string.
	startedAt, ok := result["startedAt"].(string)
	if !ok {
		t.Fatalf("Expected startedAt to be string, got %T (%v)", result["startedAt"], result["startedAt"])
	}
	if startedAt != ts.Format(time.RFC3339) {
		t.Errorf("Expected startedAt=%q, got %q", ts.Format(time.RFC3339), startedAt)
	}
}

func TestStructToMapLC_MarshalJSON_PointerField(t *testing.T) {
	type Wrapper struct {
		Coords *valueMarshaler
	}

	s := Wrapper{Coords: &valueMarshaler{X: 5, Y: 6}}
	result := StructToMapLC(s)

	coords, ok := result["coords"].(map[string]any)
	if !ok {
		t.Fatalf("Expected coords to be map, got %T (%v)", result["coords"], result["coords"])
	}
	if coords["custom"] != true {
		t.Errorf("Expected custom=true, got %v", coords["custom"])
	}
}

func TestStructToMapLC_MarshalJSON_NilPointerField(t *testing.T) {
	type Wrapper struct {
		Coords *valueMarshaler
	}

	s := Wrapper{Coords: nil}
	result := StructToMapLC(s)

	if result["coords"] != nil {
		t.Errorf("Expected coords=nil, got %v", result["coords"])
	}
}

func TestStructToMapLC_MarshalJSON_SliceOfMarshalers(t *testing.T) {
	type Wrapper struct {
		Points []valueMarshaler
	}

	s := Wrapper{
		Points: []valueMarshaler{
			{X: 1, Y: 2},
			{X: 3, Y: 4},
		},
	}

	result := StructToMapLC(s)

	points, ok := result["points"].([]any)
	if !ok {
		t.Fatalf("Expected points to be slice, got %T (%v)", result["points"], result["points"])
	}
	if len(points) != 2 {
		t.Errorf("Expected 2 points, got %d", len(points))
	}

	p0, ok := points[0].(map[string]any)
	if !ok {
		t.Fatalf("Expected point to be map, got %T", points[0])
	}
	if p0["custom"] != true {
		t.Errorf("Expected custom=true for slice element, got %v", p0["custom"])
	}
}

func TestStructToMapLC_NoMarshalJSON_StillReflects(t *testing.T) {
	// A struct without MarshalJSON should still be converted field-by-field.
	type Inner struct {
		Foo string
		Bar int
	}
	type Outer struct {
		Nested Inner
	}

	s := Outer{Nested: Inner{Foo: "hello", Bar: 42}}
	result := StructToMapLC(s)

	nested, ok := result["nested"].(map[string]any)
	if !ok {
		t.Fatalf("Expected nested to be map, got %T", result["nested"])
	}
	if nested["foo"] != "hello" {
		t.Errorf("Expected foo='hello', got %v", nested["foo"])
	}
	if nested["bar"] != 42 {
		t.Errorf("Expected bar=42, got %v", nested["bar"])
	}
}

func TestStructToMapLC_MarshalJSON_WithOptions(t *testing.T) {
	type Wrapper struct {
		UserID string
		Coords valueMarshaler
	}

	s := Wrapper{
		UserID: "u1",
		Coords: valueMarshaler{X: 10, Y: 20},
	}

	result := StructToMapLC(s, WithIDSuffix())

	// ID suffix conversion should still apply to the parent field.
	if result["userId"] != "u1" {
		t.Errorf("Expected userId='u1', got %v", result["userId"])
	}

	// MarshalJSON struct should still use custom marshaling.
	coords, ok := result["coords"].(map[string]any)
	if !ok {
		t.Fatalf("Expected coords to be map, got %T", result["coords"])
	}
	if coords["custom"] != true {
		t.Errorf("Expected custom=true, got %v", coords["custom"])
	}
}
