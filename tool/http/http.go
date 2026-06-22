// Package http 提供 HTTP API 调用工具
//
// 支持 REST API、GraphQL 等 HTTP 调用场景。
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/toolkit/net/ssrf"
)

const (
	// MaxResponseBodySize 最大响应体大小（10MB）
	// 防止恶意服务器返回超大响应导致 OOM
	MaxResponseBodySize = 10 * 1024 * 1024
)

// HTTPTool HTTP API 调用工具
type HTTPTool struct {
	client       *http.Client
	baseURL      string
	headers      map[string]string
	allowPrivate bool // 是否允许访问内网地址，默认不允许
}

// Option HTTP 工具选项
type Option func(*HTTPTool)

// WithBaseURL 设置基础 URL
func WithBaseURL(url string) Option {
	return func(t *HTTPTool) {
		t.baseURL = url
	}
}

// WithHeaders 设置默认请求头
func WithHeaders(headers map[string]string) Option {
	return func(t *HTTPTool) {
		t.headers = headers
	}
}

// WithTimeout 设置超时时间
func WithTimeout(timeout time.Duration) Option {
	return func(t *HTTPTool) {
		t.client.Timeout = timeout
	}
}

// WithAllowPrivateNetwork 允许访问内网地址（默认禁止，防止 SSRF）
func WithAllowPrivateNetwork() Option {
	return func(t *HTTPTool) {
		t.allowPrivate = true
	}
}

// NewHTTPTool 创建 HTTP 工具
func NewHTTPTool(opts ...Option) *HTTPTool {
	t := &HTTPTool{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		headers: make(map[string]string),
	}

	for _, opt := range opts {
		opt(t)
	}

	// 限制重定向次数为 5 次，并在每次重定向时检查目标 URL 安全性
	t.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("重定向次数过多（最大 5 次）")
		}
		if !t.allowPrivate {
			if err := validateURLSafety(req.URL); err != nil {
				return fmt.Errorf("重定向目标不安全: %w", err)
			}
		}
		return nil
	}

	return t
}

// RequestInput GET 请求输入
type RequestInput struct {
	URL     string            `json:"url" description:"请求 URL"`
	Method  string            `json:"method" description:"HTTP 方法 (GET/POST/PUT/DELETE)"`
	Headers map[string]string `json:"headers,omitempty" description:"请求头"`
	Body    string            `json:"body,omitempty" description:"请求体 (JSON 字符串)"`
}

// RequestOutput 请求输出
type RequestOutput struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}

// Tools 返回 HTTP 相关工具集合
func Tools(opts ...Option) []tool.Tool {
	t := NewHTTPTool(opts...)

	return []tool.Tool{
		// GET 请求
		tool.NewFunc(
			"http_get",
			"发送 HTTP GET 请求获取数据",
			func(ctx context.Context, input struct {
				URL     string            `json:"url" description:"请求 URL"`
				Headers map[string]string `json:"headers,omitempty" description:"请求头"`
			}) (RequestOutput, error) {
				return t.request(ctx, "GET", input.URL, input.Headers, nil)
			},
		),

		// POST 请求
		tool.NewFunc(
			"http_post",
			"发送 HTTP POST 请求提交数据",
			func(ctx context.Context, input struct {
				URL     string            `json:"url" description:"请求 URL"`
				Headers map[string]string `json:"headers,omitempty" description:"请求头"`
				Body    string            `json:"body" description:"请求体 (JSON 字符串)"`
			}) (RequestOutput, error) {
				var body io.Reader
				if input.Body != "" {
					body = bytes.NewBufferString(input.Body)
				}
				return t.request(ctx, "POST", input.URL, input.Headers, body)
			},
		),

		// 通用请求
		tool.NewFunc(
			"http_request",
			"发送自定义 HTTP 请求",
			func(ctx context.Context, input RequestInput) (RequestOutput, error) {
				var body io.Reader
				if input.Body != "" {
					body = bytes.NewBufferString(input.Body)
				}
				return t.request(ctx, input.Method, input.URL, input.Headers, body)
			},
		),
	}
}

// request 执行 HTTP 请求
func (t *HTTPTool) request(ctx context.Context, method, rawURL string, headers map[string]string, body io.Reader) (RequestOutput, error) {
	// 构建完整 URL
	if t.baseURL != "" && len(rawURL) > 0 && rawURL[0] == '/' {
		rawURL = t.baseURL + rawURL
	}

	// SSRF 防护：验证目标 URL 安全性
	if !t.allowPrivate {
		parsedURL, err := url.Parse(rawURL)
		if err != nil {
			return RequestOutput{}, fmt.Errorf("无效的 URL: %w", err)
		}
		if err := validateURLSafety(parsedURL); err != nil {
			return RequestOutput{}, fmt.Errorf("URL 安全检查失败: %w", err)
		}
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return RequestOutput{}, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置默认请求头
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	// 设置自定义请求头
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// 如果有 body 且没有 Content-Type，设置为 JSON
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	// 发送请求
	resp, err := t.client.Do(req)
	if err != nil {
		return RequestOutput{}, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 检查 Content-Length（如果服务器提供）
	if resp.ContentLength > MaxResponseBodySize {
		return RequestOutput{}, fmt.Errorf("响应体过大: %d bytes (最大: %d)", resp.ContentLength, MaxResponseBodySize)
	}

	// 使用 LimitReader 限制读取大小，防止 OOM
	limitedReader := io.LimitReader(resp.Body, MaxResponseBodySize+1)
	respBody, err := io.ReadAll(limitedReader)
	if err != nil {
		return RequestOutput{}, fmt.Errorf("读取响应失败: %w", err)
	}

	// 检查是否超过限制
	if len(respBody) > MaxResponseBodySize {
		return RequestOutput{}, fmt.Errorf("响应体过大: 超过 %d bytes 限制", MaxResponseBodySize)
	}

	// 构建输出
	output := RequestOutput{
		StatusCode: resp.StatusCode,
		Headers:    make(map[string]string),
		Body:       string(respBody),
	}

	// 复制响应头
	for k := range resp.Header {
		output.Headers[k] = resp.Header.Get(k)
	}

	return output, nil
}

// GraphQLTool GraphQL 工具
type GraphQLTool struct {
	endpoint string
	headers  map[string]string
	client   *http.Client
}

// NewGraphQLTool 创建 GraphQL 工具
func NewGraphQLTool(endpoint string, opts ...Option) *GraphQLTool {
	t := &GraphQLTool{
		endpoint: endpoint,
		headers:  make(map[string]string),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// 应用选项
	httpTool := &HTTPTool{headers: t.headers, client: t.client}
	for _, opt := range opts {
		opt(httpTool)
	}

	return t
}

// GraphQLInput GraphQL 查询输入
type GraphQLInput struct {
	Query     string         `json:"query" description:"GraphQL 查询语句"`
	Variables map[string]any `json:"variables,omitempty" description:"查询变量"`
}

// GraphQLOutput GraphQL 查询输出
type GraphQLOutput struct {
	Data   any      `json:"data"`
	Errors []string `json:"errors,omitempty"`
}

// Tool 返回 GraphQL 工具
func (t *GraphQLTool) Tool() tool.Tool {
	return tool.NewFunc(
		"graphql_query",
		"执行 GraphQL 查询",
		func(ctx context.Context, input GraphQLInput) (GraphQLOutput, error) {
			// 构建请求体
			reqBody, err := json.Marshal(map[string]any{
				"query":     input.Query,
				"variables": input.Variables,
			})
			if err != nil {
				return GraphQLOutput{}, fmt.Errorf("序列化请求失败: %w", err)
			}

			// 创建请求
			req, err := http.NewRequestWithContext(ctx, "POST", t.endpoint, bytes.NewReader(reqBody))
			if err != nil {
				return GraphQLOutput{}, fmt.Errorf("创建请求失败: %w", err)
			}

			// 设置请求头
			req.Header.Set("Content-Type", "application/json")
			for k, v := range t.headers {
				req.Header.Set(k, v)
			}

			// 发送请求
			resp, err := t.client.Do(req)
			if err != nil {
				return GraphQLOutput{}, fmt.Errorf("请求失败: %w", err)
			}
			defer resp.Body.Close()

			// 检查 Content-Length
			if resp.ContentLength > MaxResponseBodySize {
				return GraphQLOutput{}, fmt.Errorf("响应体过大: %d bytes (最大: %d)", resp.ContentLength, MaxResponseBodySize)
			}

			// 使用 LimitReader 限制读取大小
			limitedReader := io.LimitReader(resp.Body, MaxResponseBodySize)

			// 解析响应
			var result struct {
				Data   any `json:"data"`
				Errors []struct {
					Message string `json:"message"`
				} `json:"errors,omitempty"`
			}

			if err := json.NewDecoder(limitedReader).Decode(&result); err != nil {
				return GraphQLOutput{}, fmt.Errorf("解析响应失败: %w", err)
			}

			// 构建输出
			output := GraphQLOutput{
				Data: result.Data,
			}

			if len(result.Errors) > 0 {
				output.Errors = make([]string, len(result.Errors))
				for i, e := range result.Errors {
					output.Errors[i] = e.Message
				}
			}

			return output, nil
		},
	)
}

// validateURLSafety 验证 URL 是否安全，防止 SSRF 攻击
// 禁止访问内网地址、元数据服务、非 HTTP(S) 协议等
func validateURLSafety(u *url.URL) error {
	// scheme 限制：仅允许 http/https。toolkit ssrf.ValidateURL 不校验协议，故在此保留。
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("不允许的协议: %s（仅支持 http/https）", u.Scheme)
	}
	// SSRF 核心校验（元数据/localhost 阻断 + DNS 解析逐 IP 私网检查，抗 DNS rebinding）
	// 复用 toolkit/net/ssrf，避免在多处各维护一份私网名单产生防护漂移。
	return ssrf.ValidateURL(u.String())
}
