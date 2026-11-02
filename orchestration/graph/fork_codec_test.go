package graph

import (
	"context"
	"encoding/json"
	"testing"
)

type forkCodecState struct{ Value int }

func (s forkCodecState) Clone() (State, error) { return s, nil }

type forkEnvelopeCodec struct{}

func (forkEnvelopeCodec) Type() string    { return "fork-envelope" }
func (forkEnvelopeCodec) Version() string { return "1" }
func (forkEnvelopeCodec) Encode(state forkCodecState) ([]byte, error) {
	return json.Marshal(struct {
		Wrapped int `json:"wrapped"`
	}{state.Value})
}
func (forkEnvelopeCodec) Decode(data []byte) (forkCodecState, error) {
	var envelope struct {
		Wrapped int `json:"wrapped"`
	}
	err := json.Unmarshal(data, &envelope)
	return forkCodecState{Value: envelope.Wrapped}, err
}

func TestCheckpointRunnerForkUsesDeclaredCodec(t *testing.T) {
	for name, delta := range map[string]int{"unmodified": 0, "modified": 3} {
		t.Run(name, func(t *testing.T) {
			graph, err := NewGraph[forkCodecState]("codec-fork").WithStateCodec(forkEnvelopeCodec{}).
				AddNode("increment", func(_ context.Context, state forkCodecState) (forkCodecState, error) {
					state.Value++
					return state, nil
				}).AddEdge(START, "increment").AddEdge("increment", END).Build()
			if err != nil {
				t.Fatal(err)
			}
			saver := NewMemoryEnhancedCheckpointSaver()
			runner := NewCheckpointRunner(graph, saver, nil)
			if original, err := runner.Run(t.Context(), "thread", forkCodecState{Value: 7}); err != nil || original.Value != 8 {
				t.Fatalf("original=%+v error=%v", original, err)
			}
			checkpoint, err := saver.LoadEnhanced(t.Context(), "thread")
			if err != nil {
				t.Fatal(err)
			}
			var modify func(forkCodecState) forkCodecState
			if delta != 0 {
				modify = func(state forkCodecState) forkCodecState { state.Value += delta; return state }
			}
			result, err := NewCheckpointRunner(graph, saver, nil).Fork(t.Context(), checkpoint.ID, name, modify)
			if err != nil || result.Value != 8+delta {
				t.Fatalf("fork=%+v error=%v", result, err)
			}
			latest, err := saver.LoadEnhanced(t.Context(), "thread")
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := (forkEnvelopeCodec{}).Decode(latest.State)
			if err != nil || persisted.Value != 8+delta {
				t.Fatalf("fork persisted using another codec: %+v error=%v", persisted, err)
			}
		})
	}
}
