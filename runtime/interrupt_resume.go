package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hexagon-codes/hexagon/interrupt"
)

type resumeTargetsKey struct{}

// ResumeInput 指向已经由 TaskStore.Signal 接受的输入。Data 由宿主按原合同具体解码。
type ResumeInput struct {
	InputID string
	Data    any
}

// ResumeTargets 提供轻量检查点的定向恢复；不声称输入已持久接受。
func (r *DefaultRunner) ResumeTargets(ctx context.Context, runID string, req Request, targets map[string]any, sink EventSink) (*Result, error) {
	return r.Resume(context.WithValue(ctx, resumeTargetsKey{}, targets), runID, req, sink)
}

func prepareResumeContext(ctx context.Context, signal *interrupt.InterruptSignal, targets map[string]any) (context.Context, error) {
	addresses, states := interrupt.SignalToPersistenceMaps(signal)
	for id := range targets {
		if _, ok := addresses[id]; !ok {
			return nil, fmt.Errorf("runtime: resume target is not waiting: %s", id)
		}
	}
	ctx = interrupt.PopulateResumeInfo(ctx, addresses, states)
	return interrupt.BatchResumeWithData(ctx, targets), nil
}

func selectedSignalActivation(ctx context.Context, signal *interrupt.InterruptSignal) string {
	addresses, _ := interrupt.SignalToPersistenceMaps(signal)
	var selected []string
	for id := range addresses {
		if interrupt.IsResumeRequested(ctx, id) {
			selected = append(selected, id)
		}
	}
	sort.Strings(selected)
	if len(selected) == 0 {
		return ""
	}
	return "resume/" + strings.Join(selected, "/")
}

func signalForTool(signal *interrupt.InterruptSignal, taskID string) *interrupt.InterruptSignal {
	if signal == nil {
		return nil
	}
	for _, segment := range signal.Address {
		if segment.Type == interrupt.SegmentTool && segment.SubID == taskID {
			return signal
		}
	}
	for _, child := range signal.Subs {
		if found := signalForTool(child, taskID); found != nil {
			return found
		}
	}
	return nil
}

func (r *DefaultRunner) setWaiting(ctx context.Context, state *State, waiting []error) error {
	if len(waiting) == 0 {
		state.waitingSignal, state.waitingSaved = nil, nil
		return nil
	}
	signal, _ := interrupt.IsInterruptSignal(waiting[0])
	if len(waiting) > 1 {
		signal, _ = interrupt.IsInterruptSignal(interrupt.CompositeInterrupt(ctx, "Tool calls are waiting", nil, waiting...))
	}
	state.waitingSignal = signal
	if r.durable != nil {
		encoded, err := interrupt.EncodeSignal(signal, r.interruptCodecs)
		if err != nil {
			return err
		}
		state.waitingSaved = encoded
	}
	return nil
}

type durableToolResult struct {
	Result    ToolResult                 `json:"result"`
	Waiting   *interrupt.PersistedSignal `json:"waiting,omitempty"`
	Unsafe    bool                       `json:"unsafe_to_replay,omitempty"`
	Reconcile bool                       `json:"needs_reconciliation,omitempty"`
}

func clonePersistedSignal(signal *interrupt.PersistedSignal) *interrupt.PersistedSignal {
	if signal == nil {
		return nil
	}
	data, _ := json.Marshal(signal)
	var copy interrupt.PersistedSignal
	_ = json.Unmarshal(data, &copy)
	return &copy
}
