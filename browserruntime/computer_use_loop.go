package browserruntime

import (
	"context"
	"errors"
	"strings"
)

var (
	// ErrComputerUseEmptyResult 表示模型未给出可交付的终稿。
	ErrComputerUseEmptyResult = errors.New("computer use model returned no final task result")
	// ErrComputerUseExchangeLimit 表示已达到调用方指定的轮次上限。
	ErrComputerUseExchangeLimit = errors.New("computer use reached its model exchange limit before a final result")
)

// ComputerUseCall 只表达有序浏览器动作；Provider协议、审批载荷和回执留在适配器。
type ComputerUseCall struct {
	ID      string
	Actions []Action
}

// ComputerUseTurn 是一次已校验的模型交换产生的动作或终稿。
type ComputerUseTurn struct {
	Text  string
	Calls []ComputerUseCall
}

// ComputerUseLoopPorts 由同一任务持有。所有回调同步执行，框架不重试交换或动作。
// Exchange成功时应已完成协议校验及计量；Observe应返回本次调用执行后的真实截图。
type ComputerUseLoopPorts struct {
	MaxExchanges int
	Exchange     func(context.Context, uint64, Image) (ComputerUseTurn, error)
	BeforeCall   func(context.Context, uint64, ComputerUseCall) error
	Execute      func(context.Context, uint64, ComputerUseCall, int, Action) error
	Observe      func(context.Context, uint64, ComputerUseCall) (Image, error)
}

// RunComputerUse 串行执行模型—动作—观察循环，不取得或释放调用方的浏览器租约。
// 每次派发前响应取消；成功返回必须有无待执行动作的非空模型终稿。
func RunComputerUse(ctx context.Context, image Image, ports ComputerUseLoopPorts) (string, error) {
	if ports.Exchange == nil || ports.Execute == nil || ports.Observe == nil {
		return "", errors.New("computer use loop ports are required")
	}
	for ordinal := uint64(1); ordinal <= uint64(max(0, ports.MaxExchanges)); ordinal++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// 同轮所有调用都基于模型本次看到的截图，观察结果仅供下一轮使用。
		sourceWidth, sourceHeight := image.Width, image.Height
		turn, exchangeErr := ports.Exchange(ctx, ordinal, image)
		if exchangeErr != nil {
			return "", exchangeErr
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if len(turn.Calls) == 0 {
			if text := strings.TrimSpace(turn.Text); text != "" {
				return text, nil
			}
			return "", ErrComputerUseEmptyResult
		}
		for _, call := range turn.Calls {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if ports.BeforeCall != nil {
				if err := ports.BeforeCall(ctx, ordinal, call); err != nil {
					return "", err
				}
			}
			for index := range call.Actions {
				action := call.Actions[index]
				if err := ctx.Err(); err != nil {
					return "", err
				}
				switch action.Type {
				case "click", "double_click", "move", "scroll", "drag":
					action.SourceWidth, action.SourceHeight = sourceWidth, sourceHeight
				}
				if err := ports.Execute(ctx, ordinal, call, index, action); err != nil {
					return "", err
				}
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			observed, observeErr := ports.Observe(ctx, ordinal, call)
			if observeErr != nil {
				return "", observeErr
			}
			// 最后一次观察期间也可能取消；不能把取消误报为轮次耗尽。
			if err := ctx.Err(); err != nil {
				return "", err
			}
			image = observed
		}
	}
	return "", ErrComputerUseExchangeLimit
}
