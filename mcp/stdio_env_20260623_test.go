package mcp

import (
	"context"
	"strings"
	"testing"
)

// AUDIT 2026-06-23 MCP env keystone：stdio MCP server 需注入 env —— MySQL/Redis 等数据连接器
// 靠环境变量配凭证（MYSQL_HOST/PASSWORD 等）。buildStdioCmd 把 env 合并进子进程环境
// （保留 os.Environ，否则 npx/uvx 找不到 PATH），是"数据与工具走 MCP"的地基能力。
func TestBuildStdioCmd_InjectsEnv(t *testing.T) {
	cmd := buildStdioCmd(context.Background(), "npx", map[string]string{
		"MYSQL_HOST":     "localhost",
		"MYSQL_PASSWORD": "s3cret",
	}, "-y", "@benborla29/mcp-server-mysql")

	if cmd == nil || cmd.Path == "" {
		t.Fatal("cmd 未构造")
	}
	joined := strings.Join(cmd.Env, "\n")
	if !strings.Contains(joined, "MYSQL_HOST=localhost") {
		t.Errorf("env 未注入 MYSQL_HOST，cmd.Env=%v", cmd.Env)
	}
	if !strings.Contains(joined, "MYSQL_PASSWORD=s3cret") {
		t.Errorf("env 未注入 MYSQL_PASSWORD")
	}
	if !strings.Contains(joined, "PATH=") {
		t.Errorf("应保留 os.Environ()（含 PATH），否则 npx/uvx 不可用；cmd.Env=%v", cmd.Env)
	}
	if len(cmd.Args) < 3 {
		t.Errorf("args 未透传: %v", cmd.Args)
	}
}

// 无 env 时不动 cmd.Env（继承父进程），保持既有行为不回归。
func TestBuildStdioCmd_NilEnv_KeepsInherit(t *testing.T) {
	cmd := buildStdioCmd(context.Background(), "echo", nil, "hi")
	if cmd.Env != nil {
		t.Errorf("无 env 应保持 cmd.Env=nil（继承父进程），got %v", cmd.Env)
	}
}
