package plugin

import (
	"fmt"
	"runtime/debug"
)

// CallbackPanicError 保存插件回调的失败阶段、身份和原始调用栈。
// 回调可能已经产生部分副作用，调用方不能将 panic 当成安全重试依据。
type CallbackPanicError struct {
	Plugin string
	Phase  string
	Value  any
	Stack  []byte
}

func (e *CallbackPanicError) Error() string {
	return fmt.Sprintf("plugin %s %s callback panicked: %v", e.Plugin, e.Phase, e.Value)
}

// Unwrap 保留 panic 携带的原始 error，允许错误链继续核对具体原因。
func (e *CallbackPanicError) Unwrap() error {
	if cause, ok := e.Value.(error); ok {
		return cause
	}
	return nil
}

// UnsafeToReplay 表明失败结果不能在未知副作用下自动重做。
func (*CallbackPanicError) UnsafeToReplay() bool { return true }

func pluginPanic(name, phase string, value any) *CallbackPanicError {
	return &CallbackPanicError{Plugin: name, Phase: phase, Value: value, Stack: debug.Stack()}
}

// 保护边界与用户回调位于同一 goroutine，正常返回的 error 原样保留。
func invokePluginCallback(name, phase string, callback func() error) (err error) {
	returned := false
	defer func() {
		value := recover()
		if !returned {
			err = pluginPanic(name, phase, value)
		}
	}()
	err = callback()
	returned = true
	return err
}
