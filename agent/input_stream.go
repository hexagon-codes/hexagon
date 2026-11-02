package agent

import (
	"fmt"
	"reflect"
	"strings"

	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/toolkit/util/reflectx"
)

func init() {
	stream.RegisterConcatFunc[Input](concatInputs)
}

// concatInputs 合并同一输入的有序文本增量；Context保留所有字段，同键冲突不静默覆盖。
func concatInputs(items []Input) (Input, error) {
	var result Input
	var query strings.Builder
	for _, item := range items {
		query.WriteString(item.Query)
		copied, err := reflectx.DeepCopyChecked(item.Context)
		if err != nil {
			return Input{}, fmt.Errorf("agent input context cannot be isolated: %w", err)
		}
		for key, value := range copied {
			if previous, exists := result.Context[key]; exists && !reflect.DeepEqual(previous, value) {
				return Input{}, fmt.Errorf("agent input context field %q conflicts across stream chunks", key)
			}
			if result.Context == nil {
				result.Context = make(map[string]any)
			}
			result.Context[key] = value
		}
	}
	result.Query = query.String()
	return result, nil
}
