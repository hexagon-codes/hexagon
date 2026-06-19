package checkpoint

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/quick"
)

// backends 列出所有 Checkpointer 实现；一致性测试对每个后端跑同一套断言。
func backends(t *testing.T) []struct {
	name string
	make func() Checkpointer
} {
	t.Helper()
	return []struct {
		name string
		make func() Checkpointer
	}{
		{"memory", func() Checkpointer { return NewMemory() }},
		{"file", func() Checkpointer {
			f, err := NewFile(t.TempDir())
			if err != nil {
				t.Fatalf("NewFile: %v", err)
			}
			return f
		}},
	}
}

// TestConformance 对每个后端验证 Checkpointer 接口语义。
func TestConformance(t *testing.T) {
	ctx := context.Background()
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			cp := be.make()

			// 空命名空间：Get/Latest 不存在、List 空。
			if _, ok, err := cp.Get(ctx, "ns", "x"); ok || err != nil {
				t.Fatalf("Get empty: ok=%v err=%v", ok, err)
			}
			if _, ok, err := cp.Latest(ctx, "ns"); ok || err != nil {
				t.Fatalf("Latest empty: ok=%v err=%v", ok, err)
			}
			if list, err := cp.List(ctx, "ns", 0); err != nil || len(list) != 0 {
				t.Fatalf("List empty: %v err=%v", list, err)
			}

			// 追加三个检查点，链式 ParentID。
			must(t, cp.Put(ctx, Checkpoint{Namespace: "ns", ID: "a", Payload: []byte("A")}))
			must(t, cp.Put(ctx, Checkpoint{Namespace: "ns", ID: "b", ParentID: "a", Payload: []byte("B"),
				Metadata: map[string]string{"node": "plan"}}))
			must(t, cp.Put(ctx, Checkpoint{Namespace: "ns", ID: "c", ParentID: "b", Payload: []byte("C")}))

			// Get 精确取 + ParentID/Payload/Metadata 正确。
			got, ok, err := cp.Get(ctx, "ns", "b")
			if err != nil || !ok {
				t.Fatalf("Get b: ok=%v err=%v", ok, err)
			}
			if got.ParentID != "a" || !bytes.Equal(got.Payload, []byte("B")) || got.Metadata["node"] != "plan" {
				t.Fatalf("Get b mismatch: %+v", got)
			}

			// Latest = 最近写入。
			latest, ok, err := cp.Latest(ctx, "ns")
			if err != nil || !ok || latest.ID != "c" {
				t.Fatalf("Latest: id=%q ok=%v err=%v", latest.ID, ok, err)
			}

			// List 从新到旧。
			list, err := cp.List(ctx, "ns", 0)
			if err != nil || len(list) != 3 || list[0].ID != "c" || list[2].ID != "a" {
				t.Fatalf("List order wrong: %v err=%v", ids(list), err)
			}
			// limit 生效。
			if l, _ := cp.List(ctx, "ns", 2); len(l) != 2 || l[0].ID != "c" || l[1].ID != "b" {
				t.Fatalf("List limit wrong: %v", ids(l))
			}

			// Put 同 ID 幂等覆盖（不新增条目）。
			must(t, cp.Put(ctx, Checkpoint{Namespace: "ns", ID: "b", Payload: []byte("B2")}))
			if l, _ := cp.List(ctx, "ns", 0); len(l) != 3 {
				t.Fatalf("idempotent put changed count: %d", len(l))
			}
			if g, _, _ := cp.Get(ctx, "ns", "b"); !bytes.Equal(g.Payload, []byte("B2")) {
				t.Fatalf("idempotent put did not overwrite: %s", g.Payload)
			}

			// Delete 清空命名空间。
			must(t, cp.Delete(ctx, "ns"))
			if l, _ := cp.List(ctx, "ns", 0); len(l) != 0 {
				t.Fatalf("Delete did not clear: %v", ids(l))
			}
		})
	}
}

// TestDeepCopyIsolation 确保返回的 Payload 与后端内部存储互不影响（防共享可变引用）。
func TestDeepCopyIsolation(t *testing.T) {
	ctx := context.Background()
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			cp := be.make()
			must(t, cp.Put(ctx, Checkpoint{Namespace: "ns", ID: "x", Payload: []byte("orig")}))
			got, _, _ := cp.Get(ctx, "ns", "x")
			got.Payload[0] = 'X' // 篡改返回值
			again, _, _ := cp.Get(ctx, "ns", "x")
			if !bytes.Equal(again.Payload, []byte("orig")) {
				t.Fatalf("backend state mutated via returned payload: %s", again.Payload)
			}
		})
	}
}

// TestValueRoundTrip 验证类型化便利封装 PutValue/GetValue/LatestValue 往返一致。
func TestValueRoundTrip(t *testing.T) {
	ctx := context.Background()
	type state struct {
		Step int      `json:"step"`
		Msgs []string `json:"msgs"`
	}
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			cp := be.make()
			want := state{Step: 7, Msgs: []string{"a", "b"}}
			must(t, PutValue(ctx, cp, Checkpoint{Namespace: "r", ID: "s7"}, want))

			got, _, ok, err := GetValue[state](ctx, cp, "r", "s7")
			if err != nil || !ok || got.Step != 7 || len(got.Msgs) != 2 {
				t.Fatalf("GetValue: %+v ok=%v err=%v", got, ok, err)
			}
			latest, _, ok, err := LatestValue[state](ctx, cp, "r")
			if err != nil || !ok || latest.Step != 7 {
				t.Fatalf("LatestValue: %+v ok=%v err=%v", latest, ok, err)
			}
		})
	}
}

// TestConcurrent 并发 Put/Get（配合 -race 验证后端线程安全）。
func TestConcurrent(t *testing.T) {
	ctx := context.Background()
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			cp := be.make()
			var wg sync.WaitGroup
			for i := range 32 {
				wg.Add(1)
				go func(n int) {
					defer wg.Done()
					ns := fmt.Sprintf("ns-%d", n)
					_ = cp.Put(ctx, Checkpoint{Namespace: ns, ID: "only", Payload: []byte(ns)})
					_, _, _ = cp.Get(ctx, ns, "only")
					_, _, _ = cp.Latest(ctx, ns)
				}(i)
			}
			wg.Wait()
		})
	}
}

// TestPayloadRoundTripProperty 字节负载经 Put→Get 后逐字节不变（对两个后端各跑随机 fuzz）。
func TestPayloadRoundTripProperty(t *testing.T) {
	ctx := context.Background()
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			cp := be.make()
			prop := func(ns, id string, payload []byte) bool {
				if ns == "" || id == "" {
					return true // 跳过空键的退化场景
				}
				if err := cp.Put(ctx, Checkpoint{Namespace: ns, ID: id, Payload: payload}); err != nil {
					return false
				}
				got, ok, err := cp.Get(ctx, ns, id)
				if err != nil || !ok {
					return false
				}
				return bytes.Equal(got.Payload, payload)
			}
			if err := quick.Check(prop, &quick.Config{MaxCount: 500}); err != nil {
				t.Fatalf("payload round-trip violated: %v", err)
			}
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func ids(list []Checkpoint) []string {
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.ID
	}
	return out
}
