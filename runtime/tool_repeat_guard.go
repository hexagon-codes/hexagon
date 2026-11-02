package runtime

import (
	"encoding/json"
	"sync"
)

// ToolRepeatGuard 按单次运行统计语义相同的工具参数，不决定调用限额或副作用政策。
// 每次运行应创建独立实例；参数必须可JSON序列化。零值可用，支持并发观察。
type ToolRepeatGuard struct {
	mu     sync.Mutex
	counts map[string]int
}

// Observe 返回包含本次观察的计数。对象键顺序不影响身份，数组顺序保持。
// 序列化失败时不累计；调用方负责决定是否执行及如何返回业务结果。
func (g *ToolRepeatGuard) Observe(name string, arguments any) (int, error) {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return 0, err
	}
	key := name + "\x00" + string(encoded)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.counts == nil {
		g.counts = make(map[string]int)
	}
	g.counts[key]++
	return g.counts[key], nil
}
