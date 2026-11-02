package mcp

import (
	"encoding/json"
	"testing"

	"github.com/hexagon-codes/ai-core/schema"
)

func TestSchemaRoundTripPreservesUnionsAndBooleanSchemas(t *testing.T) {
	for _, test := range []struct {
		name     string
		document string
		valid    []string
		invalid  []string
	}{
		{"nullable", `{"type":"object","properties":{"value":{"type":["string","null"]}},"required":["value"]}`, []string{`{"value":null}`, `{"value":"text"}`}, []string{`{"value":42}`, `{}`}},
		{"forbidden property", `{"type":"object","properties":{"value":false}}`, []string{`{}`, `{"other":42}`}, []string{`{"value":null}`, `{"value":42}`}},
		{"free property", `{"type":"object","properties":{"value":true}}`, []string{`{"value":42}`, `{"value":[1,2]}`}, []string{`[]`}},
		{"no items", `{"type":"object","properties":{"items":{"type":"array","items":false}}}`, []string{`{"items":[]}`}, []string{`{"items":[1]}`}},
		{"referenced false", `{"type":"object","properties":{"value":{"$ref":"#/$defs/none"}},"$defs":{"none":false}}`, []string{`{}`}, []string{`{"value":null}`}},
		{"union and existing anyOf", `{"type":"object","properties":{"value":{"type":["integer","null"],"anyOf":[{"enum":[1,null,"wrong"]}]}}}`, []string{`{"value":1}`, `{"value":null}`}, []string{`{"value":2}`, `{"value":"wrong"}`}},
		{"boolean composition", `{"type":"object","properties":{"value":{"anyOf":[false,{"type":"integer"}],"allOf":[true],"not":false}}}`, []string{`{"value":1}`}, []string{`{"value":"wrong"}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var original map[string]any
			if err := json.Unmarshal([]byte(test.document), &original); err != nil {
				t.Fatal(err)
			}
			converted := sdkSchemaToAICore(original)
			for direction, document := range map[string]any{"original": original, "received": converted, "returned": aicoreSchemaToSDK(converted)} {
				validator, err := schema.NewValidator(document)
				if err != nil {
					t.Fatalf("%s compile: %v", direction, err)
				}
				for _, input := range test.valid {
					if err := validator.ValidateJSON([]byte(input)); err != nil {
						t.Fatalf("%s rejected valid %s: %v", direction, input, err)
					}
				}
				for _, input := range test.invalid {
					if err := validator.ValidateJSON([]byte(input)); err == nil {
						t.Fatalf("%s accepted invalid %s", direction, input)
					}
				}
			}
		})
	}
}
