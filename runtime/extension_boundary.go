package runtime

import (
	"fmt"
	"runtime/debug"
)

// ExtensionPanicError 是扩展回调的失败事实，不能自动当作可重放错误。
type ExtensionPanicError struct {
	Boundary string
	Value    any
	Stack    []byte
}

func (e *ExtensionPanicError) Error() string {
	return fmt.Sprintf("extension panic at %s: %v", e.Boundary, e.Value)
}
func (*ExtensionPanicError) UnsafeToReplay() bool { return true }

// invokeExtension 只保护当前调用 goroutine；扩展自行创建的 goroutine 仍由扩展负责。
func invokeExtension(boundary string, fn func() error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = &ExtensionPanicError{Boundary: boundary, Value: value, Stack: debug.Stack()}
		}
	}()
	return fn()
}
