package graph

import (
	"reflect"
	"testing"
	"time"
)

func TestDynamicWriteRestoreKeepsConcreteType(t *testing.T) {
	nodeIO := &NodeIO{
		OutType: reflect.TypeFor[any](),
		Output:  []FieldMapping{{From: "", To: "Value"}},
	}
	cases := []struct {
		name  string
		value any
	}{
		{"int", int(12)},
		{"int64", int64(12)},
		{"float64", float64(12.5)},
		{"string", "value"},
		{"bool", true},
		{"int-slice", []int{1, 2}},
		{"string-map", map[string]int{"a": 1}},
		{"any-slice", []any{"first", "value"}},
		{"any-map", map[string]any{"text": "value"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := encodeWrites([]StateWrite{{Path: "Value", Operation: WriteReplace, Value: tc.value}})
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeWrites(nodeIO, encoded)
			if err != nil {
				t.Fatal(err)
			}
			if len(decoded) != 1 || !reflect.DeepEqual(decoded[0].Value, tc.value) {
				t.Fatalf("decoded=%#v want=%#v", decoded, tc.value)
			}
		})
	}
}

func TestDynamicWriteRestoreRejectsUnknownType(t *testing.T) {
	nodeIO := &NodeIO{
		OutType: reflect.TypeFor[any](),
		Output:  []FieldMapping{{From: "", To: "Value"}},
	}
	encoded, err := encodeWrites([]StateWrite{{Path: "Value", Operation: WriteReplace, Value: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeWrites(nodeIO, encoded); err == nil {
		t.Fatal("unknown dynamic type should be rejected explicitly")
	}
}
