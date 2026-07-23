package mcp

import "testing"

func githubReviewCommentsSchemaFixture() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"comments": map[string]any{
				"type": "array",
				"items": map[string]any{
					"anyOf": []any{
						map[string]any{
							"type":                 "object",
							"required":             []any{"path", "position", "body"},
							"additionalProperties": false,
							"properties": map[string]any{
								"path":     map[string]any{"type": "string"},
								"position": map[string]any{"type": "integer"},
								"body":     map[string]any{"type": "string"},
							},
						},
						map[string]any{
							"type":                 "object",
							"required":             []any{"path", "line", "body"},
							"additionalProperties": false,
							"properties": map[string]any{
								"path": map[string]any{"type": "string"},
								"line": map[string]any{"type": "integer"},
								"body": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
		},
	}
}

func TestSdkSchemaToAICore_GitHubReviewCommentsAnyOfRegression(t *testing.T) {
	s := sdkSchemaToAICore(githubReviewCommentsSchemaFixture())
	if s == nil {
		t.Fatal("sdkSchemaToAICore returned nil")
	}
	items := s.Properties["comments"].Items
	if items == nil {
		t.Fatal("comments.items is nil")
	}
	if items.Type != "" {
		t.Fatalf("composition-only comments.items type = %q, want omitted", items.Type)
	}
	if len(items.AnyOf) != 2 {
		t.Fatalf("comments.items.anyOf branches = %d, want 2", len(items.AnyOf))
	}
	for i, branch := range items.AnyOf {
		if branch.Type != "object" {
			t.Fatalf("anyOf[%d].type = %q, want object", i, branch.Type)
		}
		if branch.AdditionalProperties != false {
			t.Fatalf("anyOf[%d].additionalProperties = %#v, want false", i, branch.AdditionalProperties)
		}
		if len(branch.Required) != 3 {
			t.Fatalf("anyOf[%d].required = %#v, want 3 fields", i, branch.Required)
		}
		if len(branch.Properties) != 3 {
			t.Fatalf("anyOf[%d].properties = %#v, want 3 fields", i, branch.Properties)
		}
	}
}

func TestSchemaRoundTripV2_CompositionKeywords(t *testing.T) {
	first := sdkSchemaToAICore(githubReviewCommentsSchemaFixture())
	wire := aicoreSchemaToSDK(first)
	second := sdkSchemaToAICore(wire)

	items := second.Properties["comments"].Items
	if items.Type != "" {
		t.Fatalf("round-trip invented comments.items.type = %q", items.Type)
	}
	if len(items.AnyOf) != 2 {
		t.Fatalf("round-trip anyOf branches = %d, want 2", len(items.AnyOf))
	}
	for i, branch := range items.AnyOf {
		if branch.AdditionalProperties != false {
			t.Fatalf("round-trip anyOf[%d].additionalProperties = %#v, want false", i, branch.AdditionalProperties)
		}
	}
}
