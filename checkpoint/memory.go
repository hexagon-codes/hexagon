package checkpoint

import (
	"context"
	"sync"
	"time"
)

// Memory 是基于内存的 Checkpointer 实现，进程内有效、并发安全。
//
// 适用于测试、单进程运行或不要求跨重启持久化的场景。每个命名空间维护一条
// 按写入顺序排列的检查点链（同 ID 覆盖、新 ID 追加）。
type Memory struct {
	mu sync.RWMutex
	// ns 命名空间 -> 按写入顺序排列的检查点链。
	ns map[string][]Checkpoint
	// now 可注入的时钟，便于测试；nil 时使用 time.Now。
	now func() time.Time
}

// NewMemory 创建一个内存 Checkpointer。
func NewMemory() *Memory {
	return &Memory{ns: make(map[string][]Checkpoint)}
}

func (m *Memory) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// Put 持久化一个检查点：同 (Namespace, ID) 覆盖，新 ID 追加到链尾。
func (m *Memory) Put(_ context.Context, cp Checkpoint) error {
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = m.clock()
	}
	stored := clone(cp)

	m.mu.Lock()
	defer m.mu.Unlock()
	chain := m.ns[cp.Namespace]
	for i := range chain {
		if chain[i].ID == cp.ID {
			chain[i] = stored // 幂等覆盖
			m.ns[cp.Namespace] = chain
			return nil
		}
	}
	m.ns[cp.Namespace] = append(chain, stored)
	return nil
}

// Get 按 (namespace, id) 精确取检查点。
func (m *Memory) Get(_ context.Context, namespace, id string) (Checkpoint, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, cp := range m.ns[namespace] {
		if cp.ID == id {
			return clone(cp), true, nil
		}
	}
	return Checkpoint{}, false, nil
}

// Latest 取命名空间内最近写入的检查点。
func (m *Memory) Latest(_ context.Context, namespace string) (Checkpoint, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	chain := m.ns[namespace]
	if len(chain) == 0 {
		return Checkpoint{}, false, nil
	}
	return clone(chain[len(chain)-1]), true, nil
}

// List 按写入顺序从新到旧返回命名空间内的检查点；limit<=0 表示不限。
func (m *Memory) List(_ context.Context, namespace string, limit int) ([]Checkpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	chain := m.ns[namespace]
	out := make([]Checkpoint, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, clone(chain[i]))
	}
	return out, nil
}

// Delete 删除整个命名空间的检查点链。
func (m *Memory) Delete(_ context.Context, namespace string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.ns, namespace)
	return nil
}
