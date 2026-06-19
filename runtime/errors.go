package runtime

import "errors"

var (
	// ErrNoProvider means no provider was configured or selected.
	ErrNoProvider = errors.New("runtime: no provider selected")
	// ErrNoFallback means provider fallback is unavailable.
	ErrNoFallback = errors.New("runtime: no fallback provider")
	// ErrMaxTurns means the run stopped before a final answer.
	ErrMaxTurns = errors.New("runtime: max turns reached")
	// ErrNilStream means the provider returned no stream for a streaming request.
	ErrNilStream = errors.New("runtime: provider returned nil stream")
	// ErrNoDurable means Resume was called but no DurableExecution was configured.
	ErrNoDurable = errors.New("runtime: no DurableExecution configured")
	// ErrNoSnapshot means Resume found no persisted snapshot for the run ID.
	ErrNoSnapshot = errors.New("runtime: no snapshot to resume")
	// ErrUnsafeReplay means Resume would replay an in-flight step whose tool calls
	// are not declared replay-safe (default), risking duplicated side effects.
	// Resolve by declaring the tool idempotent/read-only (SideEffectClassifier) or
	// performing manual dedup before resuming.
	ErrUnsafeReplay = errors.New("runtime: resume would replay an in-flight unsafe tool call; refusing to re-execute")
	// ErrBudgetExceeded 表示预算（token/时间/成本）超限，执行被 fail-closed 终止。
	ErrBudgetExceeded = errors.New("runtime budget exceeded")
)

// RuntimeError is a structured error payload for event consumers.
type RuntimeError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Cause   string `json:"cause,omitempty"`
}

func runtimeError(code string, err error) *RuntimeError {
	if err == nil {
		return nil
	}
	return &RuntimeError{Code: code, Message: err.Error(), Cause: err.Error()}
}
