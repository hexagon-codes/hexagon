package parser

import (
	"encoding/json"
	"testing"
)

func TestJSONParserPreservesCompleteScalarValues(t *testing.T) {
	for _, raw := range []string{`"valid"`, `15`, `1.5`, `true`, `null`, `"text containing {braces}"`} {
		t.Run(raw, func(t *testing.T) {
			value, err := NewJSONParser[json.RawMessage]().Parse(t.Context(), raw)
			if err != nil || string(value) != raw {
				t.Fatalf("valid JSON value changed: got %s, error %v", value, err)
			}
		})
	}
}
