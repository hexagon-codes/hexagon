package typecompat

import "reflect"

// Result 描述静态可赋值、接口实现和待运行期判断三种兼容情况。
type Result int

const (
	Incompatible Result = iota
	Equal
	Implements
	RuntimeAssert
)

func Check(out, in reflect.Type) Result {
	if out == nil || in == nil {
		return RuntimeAssert
	}
	if out == in || out.AssignableTo(in) {
		return Equal
	}
	if in.Kind() == reflect.Interface && out.Implements(in) {
		return Implements
	}
	if out.Kind() == reflect.Interface {
		return RuntimeAssert
	}
	return Incompatible
}
