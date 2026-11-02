package browserruntime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestComputerUseLoopActionsObserveAndFinal(t *testing.T) {
	var events []string
	ports := ComputerUseLoopPorts{
		MaxExchanges: 2,
		Exchange: func(_ context.Context, ordinal uint64, image Image) (ComputerUseTurn, error) {
			events = append(events, fmt.Sprintf("model:%d:%d", ordinal, image.Width))
			if ordinal == 2 {
				return ComputerUseTurn{Text: "done"}, nil
			}
			return ComputerUseTurn{Calls: []ComputerUseCall{
				{ID: "first", Actions: []Action{{Type: "click"}}},
				{ID: "second", Actions: []Action{{Type: "move"}}},
			}}, nil
		},
		Execute: func(_ context.Context, _ uint64, call ComputerUseCall, _ int, action Action) error {
			events = append(events, fmt.Sprintf("action:%s:%d", call.ID, action.SourceWidth))
			return nil
		},
		Observe: func(_ context.Context, _ uint64, call ComputerUseCall) (Image, error) {
			events = append(events, "observe:"+call.ID)
			width := 20
			if call.ID == "second" {
				width = 30
			}
			return Image{Width: width, Height: width}, nil
		},
	}
	text, err := RunComputerUse(context.Background(), Image{Width: 10, Height: 10}, ports)
	if err != nil || text != "done" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	want := []string{"model:1:10", "action:first:10", "observe:first", "action:second:20", "observe:second", "model:2:30"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
}

func TestComputerUseLoopCancellationStopsNextAction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var actions, observations int
	_, err := RunComputerUse(ctx, Image{}, ComputerUseLoopPorts{
		MaxExchanges: 2,
		Exchange: func(context.Context, uint64, Image) (ComputerUseTurn, error) {
			return ComputerUseTurn{Calls: []ComputerUseCall{{ID: "call", Actions: []Action{{Type: "click"}, {Type: "click"}}}}}, nil
		},
		Execute: func(context.Context, uint64, ComputerUseCall, int, Action) error { actions++; cancel(); return nil },
		Observe: func(context.Context, uint64, ComputerUseCall) (Image, error) { observations++; return Image{}, nil },
	})
	if !errors.Is(err, context.Canceled) || actions != 1 || observations != 0 {
		t.Fatalf("err=%v actions=%d observations=%d", err, actions, observations)
	}
}

func TestComputerUseLoopRequiresFinalResult(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint(pending), func(t *testing.T) {
			_, err := RunComputerUse(context.Background(), Image{}, ComputerUseLoopPorts{
				MaxExchanges: 1,
				Exchange: func(context.Context, uint64, Image) (ComputerUseTurn, error) {
					if pending {
						return ComputerUseTurn{Calls: []ComputerUseCall{{ID: "call", Actions: []Action{{Type: "wait"}}}}}, nil
					}
					return ComputerUseTurn{}, nil
				},
				Execute: func(context.Context, uint64, ComputerUseCall, int, Action) error { return nil },
				Observe: func(context.Context, uint64, ComputerUseCall) (Image, error) { return Image{}, nil },
			})
			want := ErrComputerUseEmptyResult
			if pending {
				want = ErrComputerUseExchangeLimit
			}
			if !errors.Is(err, want) {
				t.Fatalf("error=%v want=%v", err, want)
			}
		})
	}
}
