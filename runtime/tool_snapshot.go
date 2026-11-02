package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/hexagon-codes/ai-core/llm"
)

func cloneRuntimeToolSnapshot(snapshot *ToolSnapshot) *ToolSnapshot {
	if snapshot == nil {
		return nil
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil
	}
	var copy ToolSnapshot
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&copy) != nil {
		return nil
	}
	return &copy
}

func (r *DefaultRunner) restoreToolSnapshot(ctx context.Context, state *State) error {
	if binder, ok := r.toolExecutor.(ToolRunBinder); ok {
		if err := invokeExtension("bind_run_tools", func() error { return binder.BindRun(state.Request.ID) }); err != nil {
			return err
		}
	}
	if state.toolSnapshot != nil {
		restorer, ok := r.toolExecutor.(ToolSnapshotRestorer)
		if !ok {
			return fmt.Errorf("%w: fixed tool bindings cannot be restored", ErrUnrecoverableSpec)
		}
		copy := cloneRuntimeToolSnapshot(state.toolSnapshot)
		if copy == nil {
			return fmt.Errorf("%w: invalid tool snapshot", ErrUnrecoverableSpec)
		}
		if err := invokeExtension("restore_run_tools", func() error { return restorer.RestoreTools(ctx, *copy) }); err != nil {
			return err
		}
	}
	return r.refreshToolSnapshot(ctx, state)
}

func (r *DefaultRunner) refreshToolSnapshot(ctx context.Context, state *State) error {
	reader, ok := r.toolExecutor.(ToolSnapshotter)
	if !ok {
		return nil
	}
	var snapshot ToolSnapshot
	err := invokeExtension("snapshot_run_tools", func() error { var err error; snapshot, err = reader.SnapshotTools(ctx); return err })
	if err != nil {
		return err
	}
	if snapshot.RunID != state.Request.ID {
		return fmt.Errorf("runtime: tool snapshot run identity differs")
	}
	definitions, err := snapshot.ToolDefinitions()
	if err != nil {
		return err
	}
	copy := cloneRuntimeToolSnapshot(&snapshot)
	if copy == nil {
		return fmt.Errorf("runtime: tool snapshot cannot be encoded")
	}
	state.Request.Tools, state.toolSnapshot = definitions, copy
	return nil
}

func (r *DefaultRunner) buildRunModelRequest(ctx context.Context, state *State, selection ProviderSelection) (request llm.CompletionRequest, err error) {
	if err := r.refreshToolSnapshot(ctx, state); err != nil {
		return request, err
	}
	return buildModelRequest(state, selection)
}
