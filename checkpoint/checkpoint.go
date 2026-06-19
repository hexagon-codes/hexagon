// Package checkpoint 提供整个框架**唯一**的检查点/恢复（checkpoint/resume）抽象。
//
// 设计目标（对标 LangGraph BaseCheckpointSaver 与 Eino CheckpointStore 的最佳实践）：
//   - **单一接口 + 可插拔后端**：所有需要持久化/恢复的子系统（runtime / graph /
//     interrupt / process）统一依赖 Checkpointer 一个接口，后端（内存 / 文件 / Redis…）
//     各自实现，互不重复造轮子。
//   - **后端无关的字节负载**：检查点负载是 []byte（序列化在调用边缘完成），
//     后端只负责存取字节，不耦合任何业务类型；这与 Eino 的 KV 抽象一致，
//     便于任意后端（KV / 对象存储 / DB）落地。
//   - **命名空间 + ID + lineage 寻址**：用 (Namespace, ID) 定位单个检查点，
//     用 ParentID 串起同一次运行的检查点链，支持"时间旅行"与精准恢复。
//
// 典型用法：
//
//	cp := checkpoint.NewMemory()
//	_ = checkpoint.PutValue(ctx, cp, checkpoint.Checkpoint{Namespace: "run-1", ID: "step-3"}, myState)
//	state, c, ok, _ := checkpoint.GetValue[MyState](ctx, cp, "run-1", "step-3")
//
// 线程安全：本包提供的所有后端实现都是并发安全的。
package checkpoint

import (
	"context"
	"maps"
	"time"
)

// Checkpoint 是一次被持久化的状态快照。
//
// 它由 (Namespace, ID) 唯一定位，并通过 ParentID 与同一命名空间内的上一个检查点相连，
// 形成一条按时间推进的快照链（lineage）。Payload 为后端无关的序列化字节。
type Checkpoint struct {
	// ID 本检查点的唯一标识（在其 Namespace 内唯一）。
	ID string `json:"id"`
	// Namespace 逻辑分组，通常为一次运行 / 线程 / 会话的 ID。
	Namespace string `json:"namespace"`
	// ParentID 上一个检查点的 ID；首个检查点为空串。
	ParentID string `json:"parent_id,omitempty"`
	// Payload 后端无关的序列化状态字节（编解码在调用边缘完成）。
	Payload []byte `json:"payload,omitempty"`
	// Metadata 任意字符串元数据（如节点名、状态标签）。
	Metadata map[string]string `json:"metadata,omitempty"`
	// CreatedAt 创建时间；Put 时若为零值则由后端填充为当前时间。
	CreatedAt time.Time `json:"created_at"`
}

// Checkpointer 是框架唯一的检查点持久化端口。
//
// 后端实现它、框架各子系统消费它。语义约定：
//   - Put 以 ID 为幂等键：同一 (Namespace, ID) 重复 Put 覆盖既有快照；新 ID 则追加。
//   - Get 按 (Namespace, ID) 精确取；不存在时 ok=false、err=nil。
//   - Latest 取命名空间内最近写入的检查点；空命名空间 ok=false。
//   - List 按时间从新到旧返回；limit<=0 表示不限。
//   - Delete 删除整个命名空间的检查点链。
type Checkpointer interface {
	Put(ctx context.Context, cp Checkpoint) error
	Get(ctx context.Context, namespace, id string) (Checkpoint, bool, error)
	Latest(ctx context.Context, namespace string) (Checkpoint, bool, error)
	List(ctx context.Context, namespace string, limit int) ([]Checkpoint, error)
	Delete(ctx context.Context, namespace string) error
}

// clone 返回 Checkpoint 的深拷贝，避免后端内部存储与调用方共享可变引用（Payload / Metadata）。
func clone(cp Checkpoint) Checkpoint {
	out := cp
	if cp.Payload != nil {
		out.Payload = append([]byte(nil), cp.Payload...)
	}
	if cp.Metadata != nil {
		out.Metadata = make(map[string]string, len(cp.Metadata))
		maps.Copy(out.Metadata, cp.Metadata)
	}
	return out
}
