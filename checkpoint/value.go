package checkpoint

import (
	"context"
	"encoding/json"
	"fmt"
)

// PutValue 是 Put 的类型化便利封装：把任意值 v 用 JSON 序列化进 cp.Payload 后持久化。
//
// 它让调用方无需手动处理 []byte 序列化，同时保持后端的字节无关性。
func PutValue[T any](ctx context.Context, store Checkpointer, cp Checkpoint, v T) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("checkpoint: marshal value: %w", err)
	}
	cp.Payload = data
	return store.Put(ctx, cp)
}

// GetValue 是 Get 的类型化便利封装：取出 (namespace, id) 的检查点并把 Payload 反序列化为 T。
//
// 返回值：反序列化后的值、原始 Checkpoint、是否存在、错误。不存在时 ok=false 且不报错。
func GetValue[T any](ctx context.Context, store Checkpointer, namespace, id string) (T, Checkpoint, bool, error) {
	var v T
	cp, ok, err := store.Get(ctx, namespace, id)
	if err != nil || !ok {
		return v, cp, ok, err
	}
	if err := json.Unmarshal(cp.Payload, &v); err != nil {
		return v, cp, true, fmt.Errorf("checkpoint: unmarshal value: %w", err)
	}
	return v, cp, true, nil
}

// LatestValue 是 Latest 的类型化便利封装：取命名空间内最近的检查点并反序列化为 T。
func LatestValue[T any](ctx context.Context, store Checkpointer, namespace string) (T, Checkpoint, bool, error) {
	var v T
	cp, ok, err := store.Latest(ctx, namespace)
	if err != nil || !ok {
		return v, cp, ok, err
	}
	if err := json.Unmarshal(cp.Payload, &v); err != nil {
		return v, cp, true, fmt.Errorf("checkpoint: unmarshal value: %w", err)
	}
	return v, cp, true, nil
}
