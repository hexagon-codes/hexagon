package runtime

import (
	"fmt"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/toolkit/util/reflectx"
)

// CloneRequest 冻结消息、工具 Schema 和生成参数。Strategy 及宿主资源为只读借用句柄，
// 由装配入口管理生命周期；Metadata 中不能隔离的句柄不被复制为另一份连接或锁。
func CloneRequest(req Request) (Request, error) {
	out := req
	out.Messages = CloneOutputMessages(req.Messages)
	var err error
	out.Tools, err = llm.CloneToolDefinitions(req.Tools)
	if err != nil {
		return Request{}, fmt.Errorf("clone request tools: %w", err)
	}
	out.Generation, err = req.Generation.Clone()
	if err != nil {
		return Request{}, fmt.Errorf("clone request generation: %w", err)
	}
	out.Metadata = cloneRuntimeMetadata(req.Metadata)
	out.StructuredOutput, err = cloneStructuredOutputSpec(req.StructuredOutput)
	if err != nil {
		return Request{}, err
	}
	return out, nil
}

func cloneRuntimeMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = cloneRuntimeValue(value)
	}
	return out
}

func cloneRuntimeValue(value any) any {
	copy, err := reflectx.DeepCopyChecked(value)
	if err != nil {
		return value
	} // 不可复制的宿主资源按只读借用合同处理。
	return copy
}
