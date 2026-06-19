package otel

// 本文件提供 Langfuse 可观测后端的接入。Langfuse 原生支持 OTLP traces 摄取，
// 因此这是真实集成路径而非伪后端：用 OTLP 导出器指向 Langfuse 的 OTLP ingestion
// 端点 + Basic 认证头即可。配合 ApplyGenAI 写入的 gen_ai.* 语义约定属性，Langfuse
// 能正确识别模型、用量等 LLM 可观测信息。

import (
	"encoding/base64"
	"fmt"
	"maps"
)

// LangfuseConfig 是 Langfuse 后端配置。
type LangfuseConfig struct {
	// Endpoint Langfuse OTLP ingestion 端点，
	// 如 "https://cloud.langfuse.com/api/public/otel"（自托管换成对应地址）。
	Endpoint string
	// PublicKey / SecretKey Langfuse 项目密钥，用于 Basic 认证头。
	PublicKey string
	SecretKey string
	// Headers 额外请求头（可选）。
	Headers map[string]string
}

// NewLangfuseExporter 构造一个指向 Langfuse OTLP 端点的 OTLP 导出器。
//
// 当提供 PublicKey/SecretKey 时自动注入 Basic 认证头（Langfuse 的鉴权方式）。
// Endpoint 为空返回错误。导出器可像其它 OTLPExporter 一样挂到 tracer 上。
func NewLangfuseExporter(cfg LangfuseConfig) (*OTLPExporter, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("langfuse: endpoint 必填")
	}
	// Basic 认证要求 public+secret 成对：只给其一是配置错误（会导致 Langfuse 401，难排查）。
	if (cfg.PublicKey == "") != (cfg.SecretKey == "") {
		return nil, fmt.Errorf("langfuse: PublicKey 与 SecretKey 必须成对提供（或都不提供）")
	}

	headers := make(map[string]string, len(cfg.Headers)+1)
	maps.Copy(headers, cfg.Headers)
	if cfg.PublicKey != "" || cfg.SecretKey != "" {
		token := base64.StdEncoding.EncodeToString([]byte(cfg.PublicKey + ":" + cfg.SecretKey))
		headers["Authorization"] = "Basic " + token
	}

	return NewOTLPExporter(cfg.Endpoint, WithOTLPHeaders(headers)), nil
}
