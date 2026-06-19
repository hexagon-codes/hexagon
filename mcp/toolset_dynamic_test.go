package mcp

import (
	"testing"

	"github.com/hexagon-codes/ai-core/tool"
)

// TestMCPToolSet_DynamicRebuild 验证工具集可动态重建（模拟 tools/list_changed）：
// 新增/移除工具后 Tools/Get 反映最新集合，并触发 OnToolsChanged 回调。
func TestMCPToolSet_DynamicRebuild(t *testing.T) {
	ts := NewMCPToolSet(nil, []Tool{{Name: "a"}})
	if len(ts.Tools()) != 1 {
		t.Fatalf("初始应有 1 个工具, got %d", len(ts.Tools()))
	}

	var changed []tool.Tool
	ts.SetOnToolsChanged(func(tools []tool.Tool) { changed = tools })

	// 模拟收到 list_changed：服务器新增了 b
	ts.rebuild([]Tool{{Name: "a"}, {Name: "b"}})

	if len(ts.Tools()) != 2 {
		t.Fatalf("刷新后应有 2 个工具, got %d", len(ts.Tools()))
	}
	if _, ok := ts.Get("b"); !ok {
		t.Error("动态发现的工具 b 应可见")
	}
	if len(changed) != 2 {
		t.Errorf("OnToolsChanged 回调应收到 2 个工具, got %d", len(changed))
	}

	// 再次变化：移除 a、b，只剩 c —— 验证旧工具被移除
	ts.rebuild([]Tool{{Name: "c"}})
	if len(ts.Tools()) != 1 {
		t.Fatalf("再次刷新后应有 1 个工具, got %d", len(ts.Tools()))
	}
	if _, ok := ts.Get("a"); ok {
		t.Error("已移除的工具 a 不应再可见")
	}
	if _, ok := ts.Get("c"); !ok {
		t.Error("新工具 c 应可见")
	}
}
