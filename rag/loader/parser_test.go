package loader

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeDescriber 是注入的图片描述后端桩。
type fakeDescriber struct {
	desc string
	err  error
}

func (f *fakeDescriber) Describe(_ context.Context, _ []byte, _ string) (string, error) {
	return f.desc, f.err
}

// TestParseEngine_RoutesByMIME 验证引擎按 MIME 路由到支持的解析器。
func TestParseEngine_RoutesByMIME(t *testing.T) {
	eng := NewParseEngine(NewTextParser(), NewImageParser(&fakeDescriber{desc: "一只猫"}))

	// 文本走 TextParser
	docs, err := eng.Parse(context.Background(), ParseInput{Content: []byte("hello"), MIME: "text/plain"})
	if err != nil {
		t.Fatalf("文本解析: %v", err)
	}
	if len(docs) != 1 || docs[0].Content != "hello" {
		t.Errorf("文本解析结果不符: %+v", docs)
	}

	// 图片走 ImageParser → VLM 描述
	docs, err = eng.Parse(context.Background(), ParseInput{Content: []byte{0x89, 0x50}, MIME: "image/png"})
	if err != nil {
		t.Fatalf("图片解析: %v", err)
	}
	if len(docs) != 1 || docs[0].Content != "一只猫" {
		t.Errorf("图片应解析为 VLM 描述, got %+v", docs)
	}
	if docs[0].Metadata["modality"] != "image" {
		t.Errorf("图片文档应标注 modality=image")
	}
}

// TestParseEngine_UnknownMIMENoFallback 验证无匹配解析器且无 fallback 时报错。
func TestParseEngine_UnknownMIMENoFallback(t *testing.T) {
	eng := NewParseEngine(NewImageParser(nil)) // 只支持 image/*
	if _, err := eng.Parse(context.Background(), ParseInput{MIME: "application/pdf"}); err == nil {
		t.Error("未匹配解析器且无 fallback 应报错")
	}
}

// TestParseEngine_Fallback 验证未匹配时走 fallback。
func TestParseEngine_Fallback(t *testing.T) {
	eng := NewParseEngine() // 不注册任何解析器
	eng.SetFallback(NewTextParser())

	docs, err := eng.Parse(context.Background(), ParseInput{Content: []byte("raw"), MIME: "application/octet-stream"})
	if err != nil {
		t.Fatalf("fallback 解析: %v", err)
	}
	if len(docs) != 1 || docs[0].Content != "raw" {
		t.Errorf("fallback 结果不符: %+v", docs)
	}
}

// TestImageParser_NoBackend 验证未注入描述后端时返回 ErrNoImageDescriber（不伪造结果）。
func TestImageParser_NoBackend(t *testing.T) {
	p := NewImageParser(nil)
	if _, err := p.Parse(context.Background(), ParseInput{MIME: "image/jpeg"}); !errors.Is(err, ErrNoImageDescriber) {
		t.Errorf("无后端应返回 ErrNoImageDescriber, got %v", err)
	}
}

// TestImageParser_DescribeError 验证描述后端报错时包装传播。
func TestImageParser_DescribeError(t *testing.T) {
	p := NewImageParser(&fakeDescriber{err: errors.New("vlm down")})
	_, err := p.Parse(context.Background(), ParseInput{MIME: "image/png"})
	if err == nil || !strings.Contains(err.Error(), "vlm down") {
		t.Errorf("应传播描述后端错误, got %v", err)
	}
}

// TestTextParser_CanParse 验证文本解析器的 MIME 支持判定。
func TestTextParser_CanParse(t *testing.T) {
	p := NewTextParser()
	for _, mime := range []string{"text/plain", "text/markdown", "application/json", ""} {
		if !p.CanParse(mime) {
			t.Errorf("应支持 %q", mime)
		}
	}
	if p.CanParse("image/png") {
		t.Error("不应支持 image/png")
	}
}
