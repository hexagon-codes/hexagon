// Package loader 提供 Hexagon RAG 的文档加载与解析能力
//
// 本文件定义文档解析层抽象：把原始字节解析为结构化 Document。
//
// 解析层与加载层（Loader）正交：Loader 负责从来源取数据（文件/URL/数据库等），
// Parser 负责把取到的字节解析成结构化内容。解析引擎可插拔——既能注册本地解析器，
// 也能注册外部解析服务（如独立的多模态文档理解 gRPC 服务）的适配器，只要其实现
// Parser 接口即可接入。多模态：图片经可注入的 VLM 描述后端转为文本描述 Document。
package loader

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"
)

// ParseInput 解析输入：原始字节 + 内容类型/来源提示。
type ParseInput struct {
	// Content 待解析的原始字节
	Content []byte
	// MIME 内容类型（如 "application/pdf" / "image/png" / "text/markdown"）
	MIME string
	// Filename 可选来源文件名，用于按扩展名辅助推断格式
	Filename string
	// Metadata 透传到产出 Document 的元数据
	Metadata map[string]any
}

// Parser 是文档解析层抽象：把原始字节解析为一个或多个结构化 Document。
type Parser interface {
	// Parse 解析输入为结构化文档。
	Parse(ctx context.Context, in ParseInput) ([]Document, error)
	// CanParse 报告本解析器是否支持给定 MIME 类型。
	CanParse(mime string) bool
}

// ParseEngine 是可插拔的解析引擎：按 MIME 把输入路由到匹配的 Parser。
//
// 未匹配到任何解析器时使用 fallback（若设置），否则返回错误。线程安全。
type ParseEngine struct {
	mu       sync.RWMutex
	parsers  []Parser
	fallback Parser
}

// NewParseEngine 创建解析引擎并注册初始解析器。
func NewParseEngine(parsers ...Parser) *ParseEngine {
	return &ParseEngine{parsers: parsers}
}

// Register 追加注册一个解析器。
func (e *ParseEngine) Register(p Parser) {
	e.mu.Lock()
	e.parsers = append(e.parsers, p)
	e.mu.Unlock()
}

// SetFallback 设置兜底解析器（所有已注册解析器都不支持该 MIME 时使用）。
func (e *ParseEngine) SetFallback(p Parser) {
	e.mu.Lock()
	e.fallback = p
	e.mu.Unlock()
}

// Parse 按 MIME 选择支持的解析器解析输入。
func (e *ParseEngine) Parse(ctx context.Context, in ParseInput) ([]Document, error) {
	e.mu.RLock()
	parsers := e.parsers
	fallback := e.fallback
	e.mu.RUnlock()

	for _, p := range parsers {
		if p.CanParse(in.MIME) {
			return p.Parse(ctx, in)
		}
	}
	if fallback != nil {
		return fallback.Parse(ctx, in)
	}
	return nil, fmt.Errorf("rag/parser: 没有支持 MIME %q 的解析器", in.MIME)
}

// ============== 文本解析器 ==============

// TextParser 解析纯文本/Markdown 等文本类内容，直接把字节作为文档内容。
type TextParser struct{}

// NewTextParser 创建文本解析器。
func NewTextParser() *TextParser { return &TextParser{} }

// CanParse 支持 text/* 与常见结构化文本类型。
func (p *TextParser) CanParse(mime string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch mime {
	case "application/json", "application/xml", "application/yaml", "":
		return true
	}
	return false
}

// Parse 把字节包装为单个 Document。
func (p *TextParser) Parse(_ context.Context, in ParseInput) ([]Document, error) {
	return []Document{{
		Content:  string(in.Content),
		Metadata: in.Metadata,
		Source:   in.Filename,
	}}, nil
}

// ============== 多模态：图片 VLM 描述解析器 ==============

// ImageDescriber 是图片描述后端（VLM）的接口约定（seam）。
//
// 框架定义接口，具体后端（多模态 LLM 客户端）由调用方注入，不内置/不伪造，
// 避免在解析层硬依赖某家 VLM。
type ImageDescriber interface {
	// Describe 返回图片的文本描述。
	Describe(ctx context.Context, image []byte, mime string) (string, error)
}

// ErrNoImageDescriber 未注入图片描述后端时返回。
var ErrNoImageDescriber = fmt.Errorf("rag/parser: 未注入 ImageDescriber（图片 VLM 描述后端）")

// ImageParser 把图片解析为其文本描述 Document（多模态文档理解）。
type ImageParser struct {
	describer ImageDescriber
}

// NewImageParser 创建图片解析器；describer 为图片描述后端（nil 时 Parse 返回错误）。
func NewImageParser(describer ImageDescriber) *ImageParser {
	return &ImageParser{describer: describer}
}

// CanParse 支持 image/* 类型。
func (p *ImageParser) CanParse(mime string) bool {
	return strings.HasPrefix(mime, "image/")
}

// Parse 调用 VLM 描述后端把图片转为文本描述 Document；未注入后端则返回 ErrNoImageDescriber。
func (p *ImageParser) Parse(ctx context.Context, in ParseInput) ([]Document, error) {
	if p.describer == nil {
		return nil, ErrNoImageDescriber
	}
	desc, err := p.describer.Describe(ctx, in.Content, in.MIME)
	if err != nil {
		return nil, fmt.Errorf("rag/parser: 图片描述失败: %w", err)
	}
	meta := map[string]any{"modality": "image", "parsed_by": "vlm_describe"}
	maps.Copy(meta, in.Metadata)
	return []Document{{
		Content:  desc,
		Metadata: meta,
		Source:   in.Filename,
	}}, nil
}
