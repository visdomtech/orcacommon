package utils

import (
	"testing"
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
