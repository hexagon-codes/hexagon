package runtime

import (
	"context"
	"fmt"

	"github.com/hexagon-codes/ai-core/llm"
)

// ContextTransform 只能根据冻结候选计算消息，不能持有或修改活跃 State。
type ContextTransform interface {
	Apply(uint64, []llm.Message) ([]llm.Message, error)
}

type ContextUpdate struct {
	ID        string
	RunID     string
	Transform ContextTransform
}
type contextUpdatesKey struct{}

// ContextWithUpdates 的队列由宿主持有；Runner 每个模型边界最多接收一个候选。
// 计算候选的异步模型调用应使用独立 ModelSession，不能并发使用活跃 State 的调用上下文。
func ContextWithUpdates(ctx context.Context, updates <-chan ContextUpdate) context.Context {
	return context.WithValue(ctx, contextUpdatesKey{}, updates)
}

func (r *DefaultRunner) applyContextUpdate(ctx context.Context, state *State, emitter *runEmitter) error {
	updates, ok := ctx.Value(contextUpdatesKey{}).(<-chan ContextUpdate)
	if !ok || updates == nil {
		return nil
	}
	select {
	case update, open := <-updates:
		if !open {
			return nil
		}
		if update.ID == "" || update.RunID != state.Request.ID || update.Transform == nil {
			return fmt.Errorf("runtime: context update identity or transform is invalid")
		}
		if state.appliedContextUpdates[update.ID] {
			return nil
		}
		var messages []llm.Message
		err := invokeExtension("context_transform", func() error {
			var err error
			messages, err = update.Transform.Apply(state.MessageRevision, CloneOutputMessages(state.Messages))
			return err
		})
		if err != nil {
			return emitter.emit(ctx, Event{Type: "context_update_rejected", State: state, Error: err, Metadata: map[string]any{"update_id": update.ID}})
		}
		state.Messages = CloneOutputMessages(messages)
		state.MessageRevision++
		if state.appliedContextUpdates == nil {
			state.appliedContextUpdates = make(map[string]bool)
		}
		state.appliedContextUpdates[update.ID] = true
		if err := r.saveSnapshot(ctx, state, state.Request.ID, emitter); err != nil {
			return err
		}
		return emitter.emit(ctx, Event{Type: EventContextCompacted, State: state, Metadata: map[string]any{"update_id": update.ID, "message_revision": state.MessageRevision}})
	default:
		return nil
	}
}

type modelPurposeKey struct{}

func WithModelPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, modelPurposeKey{}, purpose)
}
func modelPurpose(ctx context.Context, fallback string) string {
	if purpose, ok := ctx.Value(modelPurposeKey{}).(string); ok && purpose != "" {
		return purpose
	}
	return fallback
}
