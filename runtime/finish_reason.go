package runtime

import "fmt"

func modelStopReason(reason string) (StopReason, error) {
	switch reason {
	case "", "stop", "end_turn", "stop_sequence", "completed":
		return StopReasonEndTurn, nil
	case "length", "max_tokens", "max_output_tokens":
		return StopReasonOutputLimit, nil
	case "refusal":
		return StopReasonRefusal, nil
	case "content_filter":
		return StopReasonContentFilter, nil
	case "pause_turn":
		return StopReasonPauseTurn, nil
	case "cancelled", "canceled":
		return StopReasonModelCancelled, nil
	case "tool_calls", "tool_use", "function_call":
		return "", fmt.Errorf("runtime: model ended with tool calls but returned no complete tool call")
	default:
		return StopReasonModelStopped, nil
	}
}
