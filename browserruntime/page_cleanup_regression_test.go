package browserruntime

import (
	"context"
	"errors"
	"github.com/hexagon-codes/ai-core/tool"
	"strings"
	"testing"
	"time"
)

func TestMCPDriverFailedCreationKeepsNoUnreleasedPage(t *testing.T) {
	live, closeCalls := false, 0
	d := &MCPDriver{ListTools: func(context.Context, string) ([]ToolSchema, error) {
		return []ToolSchema{{Name: "browser_tabs"}, {Name: "browser_run_code_unsafe"}}, nil
	}, CallTool: func(_ context.Context, _ string, name string, args map[string]any) (tool.Result, error) {
		code, _ := args["code"].(string)
		if (name == "browser_tabs" && args["action"] == "close") || strings.Contains(code, "hexclaw_closed_page") {
			closeCalls++
			live = false
			return tool.NewResult(map[string]any{"hexclaw_closed_page": true}), nil
		}
		live = true
		// 创建调用完成但观察输出损坏，不能丢失已经发生的页面分配。
		return tool.NewResult("malformed observation"), nil
	}}
	_, err := d.Bind(context.Background(), "server", "", true)
	if err == nil {
		t.Fatal("malformed observation accepted")
	}
	if live || closeCalls != 1 {
		t.Fatalf("live=%v cleanup calls=%d", live, closeCalls)
	}
}

func TestMCPDriverUnconfirmedClosePreservesTaskOwnership(t *testing.T) {
	confirmed := false
	closeCalls := 0
	d := &MCPDriver{ListTools: func(context.Context, string) ([]ToolSchema, error) {
		return []ToolSchema{{Name: "browser_tabs"}, {Name: "browser_run_code_unsafe"}}, nil
	}, WaitIdle: func(context.Context, string) error { return nil }, CallTool: func(_ context.Context, _ string, name string, args map[string]any) (tool.Result, error) {
		code, _ := args["code"].(string)
		if strings.Contains(code, "hexclaw_closed_page") {
			closeCalls++
			return tool.NewResult(map[string]any{"hexclaw_closed_page": confirmed}), nil
		}
		if name == "browser_tabs" {
			return tool.NewResult("selected"), nil
		}
		return tool.NewResult(map[string]any{"hexclaw_browser_observation": Observation{Page: Page{ID: "owned", Index: 0}, Pages: []Page{{ID: "owned", Index: 0}}, Width: 100, Height: 100}}), nil
	}}
	c := New(d, Options{})
	if err := c.RegisterSession(SessionRef{ID: "s", ServerName: "server", ProfileID: "p", Generation: 1, OwnsBrowser: true}); err != nil {
		t.Fatal(err)
	}
	task := TaskRef{ID: "task", OwnerID: "owner"}
	_, err := c.Acquire(context.Background(), task, Target{ProfileID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.FinishTask(context.Background(), task); err == nil {
		t.Fatal("unconfirmed close reported success")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls=%d", closeCalls)
	}
	confirmed = true
	if err := c.FinishTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if closeCalls != 2 {
		t.Fatalf("ownership lost before confirmed close: calls=%d", closeCalls)
	}
	if err := c.FinishTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if closeCalls != 2 {
		t.Fatal("completed cleanup was repeated")
	}
}

func TestMCPDriverUnconfirmedCreationCleanupDoesNotAllocateAgain(t *testing.T) {
	calls := 0
	d := &MCPDriver{ListTools: func(context.Context, string) ([]ToolSchema, error) {
		return []ToolSchema{{Name: "browser_tabs"}, {Name: "browser_run_code_unsafe"}}, nil
	}, CallTool: func(_ context.Context, _ string, _ string, args map[string]any) (tool.Result, error) {
		calls++
		code, _ := args["code"].(string)
		if strings.Contains(code, "hexclaw_closed_page") {
			return tool.NewErrorResult(errors.New("cleanup unavailable")), nil
		}
		return tool.NewResult("malformed observation"), nil
	}}
	c := New(d, Options{})
	if err := c.RegisterSession(SessionRef{ID: "s", ServerName: "server", ProfileID: "p", Generation: 1, OwnsBrowser: true}); err != nil {
		t.Fatal(err)
	}
	_, err := c.Acquire(context.Background(), TaskRef{ID: "first", OwnerID: "owner"}, Target{ProfileID: "p"})
	var unknown *UnknownCallError
	if !errors.As(err, &unknown) {
		t.Fatalf("cleanup uncertainty missing: %v", err)
	}
	before := calls
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err = c.Acquire(ctx, TaskRef{ID: "second", OwnerID: "owner"}, Target{ProfileID: "p"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("profile reused before reconciliation: %v", err)
	}
	if calls != before {
		t.Fatal("page allocation repeated after unknown cleanup")
	}
}
