package mcp

import (
	"encoding/json"
	"testing"

	"github.com/hexagon-codes/ai-core/schema"
)

func TestSchemaRoundTripV2PreservesRecursiveReferences(t *testing.T) {
	type node struct {
		Name     string `json:"name" required:"true"`
		Children []node `json:"children,omitempty"`
	}
	original := schema.Of[node]().WithDialect(schema.Draft2020_12)
	wire := aicoreSchemaToSDK(original)
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var transported map[string]any
	if err := json.Unmarshal(encoded, &transported); err != nil {
		t.Fatal(err)
	}
	received := sdkSchemaToAICore(transported)
	if received.Dialect != schema.Draft2020_12 || received.Properties["children"].Items.Ref == "" || len(received.Defs) == 0 {
		t.Fatalf("recursive schema fields were lost: %s", received)
	}
	for name, document := range map[string]any{"outbound": wire, "inbound": received} {
		t.Run(name, func(t *testing.T) {
			validator, err := schema.NewValidator(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := validator.ValidateJSON([]byte(`{"name":"root","children":[{"name":"child"}]}`)); err != nil {
				t.Fatalf("valid recursive input rejected: %v", err)
			}
			if err := validator.ValidateJSON([]byte(`{"name":"root","children":[{"name":42}]}`)); err == nil {
				t.Fatal("recursive child type constraint was lost")
			}
			if err := validator.ValidateJSON([]byte(`{"name":"root","children":[{}]}`)); err == nil {
				t.Fatal("recursive child required constraint was lost")
			}
		})
	}
}
