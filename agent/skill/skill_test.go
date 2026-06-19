package skill

import (
	"context"
	"testing"
)

// TestRegistry_RegisterAndGet 测试技能注册和获取
func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()

	s := &Skill{
		Name:        "test-skill",
		Description: "测试技能",
		Version:     "1.0.0",
		Handler: func(ctx context.Context, input map[string]any) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		},
	}

	if err := r.Register(s); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	got, err := r.Get("test-skill")
	if err != nil {
		t.Fatalf("获取失败: %v", err)
	}
	if got.Name != "test-skill" {
		t.Errorf("名称不匹配: got %s, want test-skill", got.Name)
	}
	if !got.Enabled {
		t.Error("新注册的技能应该默认启用")
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt 应自动设置")
	}
}

// TestRegistry_RegisterDuplicate 测试重复注册
func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	s := &Skill{
		Name: "dup",
		Handler: func(ctx context.Context, input map[string]any) (map[string]any, error) {
			return nil, nil
		},
	}
	r.Register(s)
	if err := r.Register(s); err != ErrSkillExists {
		t.Errorf("期望 ErrSkillExists, got %v", err)
	}
}

// TestRegistry_RegisterInvalid 测试无效技能注册
func TestRegistry_RegisterInvalid(t *testing.T) {
	r := NewRegistry()

	// 空名称
	if err := r.Register(&Skill{Handler: func(ctx context.Context, input map[string]any) (map[string]any, error) { return nil, nil }}); err != ErrInvalidSkill {
		t.Errorf("空名称应返回 ErrInvalidSkill, got %v", err)
	}

	// 无 Handler
	if err := r.Register(&Skill{Name: "no-handler"}); err != ErrInvalidSkill {
		t.Errorf("无 Handler 应返回 ErrInvalidSkill, got %v", err)
	}

	// nil
	if err := r.Register(nil); err != ErrInvalidSkill {
		t.Errorf("nil 应返回 ErrInvalidSkill, got %v", err)
	}
}

// TestRegistry_Unregister 测试注销
func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	r.Register(&Skill{
		Name: "to-remove",
		Handler: func(ctx context.Context, input map[string]any) (map[string]any, error) {
			return nil, nil
		},
	})

	if err := r.Unregister("to-remove"); err != nil {
		t.Fatalf("注销失败: %v", err)
	}
	if _, err := r.Get("to-remove"); err != ErrSkillNotFound {
		t.Error("注销后应找不到技能")
	}
	if err := r.Unregister("nonexistent"); err != ErrSkillNotFound {
		t.Errorf("注销不存在的技能应返回 ErrSkillNotFound, got %v", err)
	}
}

// TestRegistry_Search 测试搜索
func TestRegistry_Search(t *testing.T) {
	r := NewRegistry()
	handler := func(ctx context.Context, input map[string]any) (map[string]any, error) { return nil, nil }

	r.Register(&Skill{Name: "translate", Description: "多语言翻译", Tags: []string{"nlp"}, Handler: handler})
	r.Register(&Skill{Name: "summarize", Description: "文本摘要", Tags: []string{"nlp"}, Handler: handler})
	r.Register(&Skill{Name: "calculator", Description: "数学计算", Tags: []string{"math"}, Handler: handler})

	results := r.Search("nlp")
	if len(results) != 2 {
		t.Errorf("搜索 'nlp' 应返回 2 个结果, got %d", len(results))
	}

	results = r.Search("翻译")
	if len(results) != 1 || results[0].Name != "translate" {
		t.Errorf("搜索 '翻译' 应返回 translate")
	}
}

// TestRegistry_EnableDisable 测试启用/禁用
func TestRegistry_EnableDisable(t *testing.T) {
	r := NewRegistry()
	r.Register(&Skill{
		Name: "toggleable",
		Handler: func(ctx context.Context, input map[string]any) (map[string]any, error) {
			return nil, nil
		},
	})

	r.Disable("toggleable")
	if list := r.List(); len(list) != 0 {
		t.Error("禁用后 List 不应包含该技能")
	}

	r.Enable("toggleable")
	if list := r.List(); len(list) != 1 {
		t.Error("启用后 List 应包含该技能")
	}
}

// TestRegistry_Execute 测试执行技能
func TestRegistry_Execute(t *testing.T) {
	r := NewRegistry()
	r.Register(&Skill{
		Name: "echo",
		Handler: func(ctx context.Context, input map[string]any) (map[string]any, error) {
			return map[string]any{"echo": input["msg"]}, nil
		},
	})

	result, err := r.Execute(context.Background(), "echo", map[string]any{"msg": "hello"})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if result["echo"] != "hello" {
		t.Errorf("结果不匹配: got %v", result["echo"])
	}

	// 执行不存在的技能
	_, err = r.Execute(context.Background(), "nonexistent", nil)
	if err != ErrSkillNotFound {
		t.Errorf("期望 ErrSkillNotFound, got %v", err)
	}
}

// TestRegistry_Hook 测试变更钩子
func TestRegistry_Hook(t *testing.T) {
	r := NewRegistry()

	var events []HookEvent
	r.OnChange(func(event HookEvent, s *Skill) {
		events = append(events, event)
	})

	r.Register(&Skill{
		Name: "hooked",
		Handler: func(ctx context.Context, input map[string]any) (map[string]any, error) {
			return nil, nil
		},
	})
	r.Disable("hooked")
	r.Enable("hooked")
	r.Unregister("hooked")

	expected := []HookEvent{EventRegistered, EventDisabled, EventEnabled, EventUnregistered}
	if len(events) != len(expected) {
		t.Fatalf("事件数量不匹配: got %d, want %d", len(events), len(expected))
	}
	for i, e := range expected {
		if events[i] != e {
			t.Errorf("事件[%d] = %s, want %s", i, events[i], e)
		}
	}
}

// TestHMACSigner 测试 HMAC 签名器
func TestHMACSigner(t *testing.T) {
	signer := NewHMACSigner([]byte("test-secret"))

	data := []byte("hello world")
	sig := signer.Sign(data)

	if err := signer.Verify(data, sig); err != nil {
		t.Fatalf("验证失败: %v", err)
	}

	if signer.Algorithm() != "hmac-sha256" {
		t.Errorf("算法名称错误: %s", signer.Algorithm())
	}

	// 篡改数据
	if err := signer.Verify([]byte("tampered"), sig); err != ErrInvalidSignature {
		t.Errorf("篡改数据应返回 ErrInvalidSignature, got %v", err)
	}

	// 篡改签名
	badSig := make([]byte, len(sig))
	copy(badSig, sig)
	badSig[0] ^= 0xFF
	if err := signer.Verify(data, badSig); err != ErrInvalidSignature {
		t.Errorf("篡改签名应返回 ErrInvalidSignature, got %v", err)
	}
}

// TestSignAndVerifySkill 测试技能签名验证
func TestSignAndVerifySkill(t *testing.T) {
	signer := NewHMACSigner([]byte("skill-secret"))
	s := &Skill{Name: "signed-skill", Version: "1.0.0"}

	sig := SignSkill(s, signer)

	if err := VerifySkill(s, sig, signer); err != nil {
		t.Fatalf("技能签名验证失败: %v", err)
	}

	// 版本不匹配
	s2 := &Skill{Name: "signed-skill", Version: "2.0.0"}
	if err := VerifySkill(s2, sig, signer); err == nil {
		t.Error("版本不匹配应验证失败")
	}

	// 无验证器
	if err := VerifySkill(s, sig, nil); err != ErrNoVerifier {
		t.Errorf("无验证器应返回 ErrNoVerifier, got %v", err)
	}
}
