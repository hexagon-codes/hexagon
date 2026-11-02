package structured

import (
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/schema"
)

type validatedCount struct {
	Count int    `json:"count"`
	Kind  string `json:"kind"`
}

func countSchema() *schema.Schema {
	minimum, maximum := 1.0, 3.0
	return &schema.Schema{
		Type: "object", Required: []string{"count", "kind"}, AdditionalProperties: false,
		Properties: map[string]*schema.Schema{
			"count": {Type: "integer", Minimum: &minimum, Maximum: &maximum},
			"kind":  {Type: "string", Enum: []any{"clip"}},
		},
	}
}

func TestGenerateRepairsSchemaViolation(t *testing.T) {
	provider := newMockProviderWithResponses(`{"count":9,"kind":"clip"}`, `{"count":2,"kind":"clip"}`)
	result, err := Generate[validatedCount](t.Context(), provider, "result", WithOutputSchema(countSchema()), WithMaxRetries(1))
	if err != nil || result.Count != 2 || provider.callCount != 2 {
		t.Fatalf("result=%+v calls=%d error=%v", result, provider.callCount, err)
	}
}

func TestGenerateValidatesBeforeStructProjection(t *testing.T) {
	provider := newMockProvider(`{"count":2,"kind":"clip","hidden":true}`)
	_, err := Generate[validatedCount](t.Context(), provider, "result", WithOutputSchema(countSchema()), WithMaxRetries(0))
	var failure *Error
	if !errors.As(err, &failure) || len(failure.Attempts) != 1 || provider.callCount != 1 {
		t.Fatalf("unvalidated fields became success: calls=%d error=%v", provider.callCount, err)
	}
}

func TestGenerateRejectsMissingRequiredField(t *testing.T) {
	provider := newMockProvider(`{"count":2}`)
	if _, err := Generate[validatedCount](t.Context(), provider, "result", WithOutputSchema(countSchema()), WithMaxRetries(0)); err == nil {
		t.Fatal("missing required field became a zero-valued successful struct")
	}
}

func TestGenerateRejectsInvalidSchemaBeforeCallingProvider(t *testing.T) {
	provider := newMockProvider(`{"count":2,"kind":"clip"}`)
	_, err := Generate[validatedCount](t.Context(), provider, "result", WithOutputSchema(&schema.Schema{Ref: "#/$defs/missing"}), WithMaxRetries(0))
	if err == nil || provider.callCount != 0 {
		t.Fatalf("calls=%d error=%v", provider.callCount, err)
	}
}
