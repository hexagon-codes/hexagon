package structured

import (
	"context"
	"testing"
)

// TestNativeJSONSchema_SetsResponseFormat 验证启用原生强制解码后，请求带上 json_schema 约束。
func TestNativeJSONSchema_SetsResponseFormat(t *testing.T) {
	provider := newMockProvider(`{"name":"张三","age":25,"email":"a@b.com"}`)

	_, err := Generate[testUser](context.Background(), provider, "提取用户信息",
		WithNativeJSONSchema("user_schema"))
	if err != nil {
		t.Fatalf("期望成功: %v", err)
	}

	rf := provider.lastRequest.ResponseFormat
	if rf == nil {
		t.Fatal("启用原生强制解码后 ResponseFormat 应非空")
	}
	if rf.Type != "json_schema" {
		t.Errorf("ResponseFormat.Type 应为 json_schema, got %q", rf.Type)
	}
	if rf.JSONSchema == nil || !rf.JSONSchema.Strict {
		t.Error("应下发 strict 的 json_schema")
	}
	if rf.JSONSchema.Name != "user_schema" {
		t.Errorf("schema 名称应为 user_schema, got %q", rf.JSONSchema.Name)
	}
	if rf.JSONSchema.Schema == nil {
		t.Error("应下发非空 Schema")
	}
}

// TestNativeJSONSchema_DefaultName 验证不传名称时使用默认 schema 名。
func TestNativeJSONSchema_DefaultName(t *testing.T) {
	provider := newMockProvider(`{"name":"李四","age":30,"email":"c@d.com"}`)
	if _, err := Generate[testUser](context.Background(), provider, "x", WithNativeJSONSchema()); err != nil {
		t.Fatalf("期望成功: %v", err)
	}
	if provider.lastRequest.ResponseFormat.JSONSchema.Name != "structured_output" {
		t.Errorf("默认 schema 名应为 structured_output, got %q",
			provider.lastRequest.ResponseFormat.JSONSchema.Name)
	}
}

// TestNativeJSONSchema_DisabledByDefault 验证未启用时不设置 ResponseFormat（行为不变，仅 prompt 注入）。
func TestNativeJSONSchema_DisabledByDefault(t *testing.T) {
	provider := newMockProvider(`{"name":"王五","age":40,"email":"e@f.com"}`)
	if _, err := Generate[testUser](context.Background(), provider, "x"); err != nil {
		t.Fatalf("期望成功: %v", err)
	}
	if provider.lastRequest.ResponseFormat != nil {
		t.Error("未启用原生强制解码时 ResponseFormat 应为 nil（保持 prompt 注入行为）")
	}
}
