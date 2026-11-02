package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/schema"
	"github.com/hexagon-codes/ai-core/tool"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

type falseReplayMarker struct{}

func (falseReplayMarker) Error() string        { return "safe sibling" }
func (falseReplayMarker) UnsafeToReplay() bool { return false }

type unknownOutcomeTool struct{ failure error }

func (unknownOutcomeTool) Name() string { return "unknown_outcome" }
func (unknownOutcomeTool) Description() string {
	return "original operation outcome requires reconciliation"
}
func (unknownOutcomeTool) Schema() *schema.Schema        { return &schema.Schema{Type: "object"} }
func (unknownOutcomeTool) Validate(map[string]any) error { return nil }
func (t unknownOutcomeTool) Execute(context.Context, map[string]any) (tool.Result, error) {
	return tool.Result{Success: false, Output: "original operation pending"}, t.failure
}

func TestAgentToolRetainsUnsafeMarkerAcrossAllErrorBranches(t *testing.T) {
	unknown := &agentruntime.TaskOutcomeUnknownError{TaskID: "original", Activation: "initial"}
	for name, failure := range map[string]error{"bare": unknown, "joined": errors.Join(falseReplayMarker{}, unknown)} {
		t.Run(name, func(t *testing.T) {
			result, err := NewToolExecutor(unknownOutcomeTool{failure: failure}).Execute(t.Context(), llm.ToolCall{ID: "original-call", Name: "unknown_outcome", Arguments: "{}"})
			if !errors.Is(err, unknown) || !llm.IsUnsafeToReplay(err) || result.Status != agentruntime.ToolStatusError {
				t.Fatalf("unknown outcome became ordinary tool result: %+v, %v", result, err)
			}
		})
	}
}
