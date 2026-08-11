// Package browser 提供浏览器自动化工具
//
// 本包实现了浏览器自动化功能：
//
//   - 网页抓取：获取网页内容
//
//   - 截图：获取网页截图
//
//   - 元素交互：点击、输入、滚动
//
//   - JavaScript 执行：运行自定义脚本
//
//   - 多标签页管理
//
//   - Playwright: 浏览器自动化
//
//   - Puppeteer: Chrome DevTools Protocol
//
//   - Selenium: WebDriver API
//
// 使用示例：
//
//	browser := browser.NewBrowser()
//	content, err := browser.GetContent(ctx, "https://example.com")
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hexagon-codes/toolkit/net/ssrf"
	"golang.org/x/net/html"
)

// ============== 错误定义 ==============

var (
	// ErrBrowserNotAvailable 浏览器不可用
	ErrBrowserNotAvailable = errors.New("browser not available")

	// ErrPageNotFound 页面未找到
	ErrPageNotFound = errors.New("page not found")

	// ErrElementNotFound 元素未找到
	ErrElementNotFound = errors.New("element not found")

	// ErrTimeout 操作超时
	ErrTimeout = errors.New("operation timeout")

	// ErrNavigationFailed 导航失败
	ErrNavigationFailed = errors.New("navigation failed")
)

// ============== 浏览器配置 ==============

// BrowserConfig 浏览器配置
type BrowserConfig struct {
	// Headless 无头模式
	Headless bool `json:"headless"`

	// Timeout 默认超时时间
	Timeout time.Duration `json:"timeout"`

	// UserAgent 用户代理
	UserAgent string `json:"user_agent"`

	// ProxyURL 代理地址
	ProxyURL string `json:"proxy_url"`

	// ViewportWidth 视口宽度
	ViewportWidth int `json:"viewport_width"`

	// ViewportHeight 视口高度
	ViewportHeight int `json:"viewport_height"`

	// JavaScriptEnabled 是否启用 JavaScript
	JavaScriptEnabled bool `json:"javascript_enabled"`

	// ImageLoadingEnabled 是否加载图片
	ImageLoadingEnabled bool `json:"image_loading_enabled"`
}

// DefaultBrowserConfig 默认浏览器配置
func DefaultBrowserConfig() *BrowserConfig {
	return &BrowserConfig{
		Headless:            true,
		Timeout:             30 * time.Second,
		UserAgent:           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		ViewportWidth:       1920,
		ViewportHeight:      1080,
		JavaScriptEnabled:   true,
		ImageLoadingEnabled: true,
	}
}

// ============== 页面内容 ==============

// PageContent 页面内容
type PageContent struct {
	// URL 页面 URL
	URL string `json:"url"`

	// Title 页面标题
	Title string `json:"title"`

	// HTML 原始 HTML
	HTML string `json:"html,omitempty"`

	// Text 纯文本内容
	Text string `json:"text"`

	// Markdown Markdown 格式内容
	Markdown string `json:"markdown,omitempty"`

	// Links 页面链接
	Links []Link `json:"links,omitempty"`

	// Images 页面图片
	Images []Image `json:"images,omitempty"`

	// Metadata 元数据
	Metadata map[string]string `json:"metadata,omitempty"`

	// LoadTime 加载时间（毫秒）
	LoadTime int64 `json:"load_time_ms"`
}

// Link 链接
type Link struct {
	// Text 链接文本
	Text string `json:"text"`

	// URL 链接地址
	URL string `json:"url"`

	// Title 链接标题
	Title string `json:"title,omitempty"`
}

// Image 图片
type Image struct {
	// Src 图片地址
	Src string `json:"src"`

	// Alt 替代文本
	Alt string `json:"alt"`

	// Width 宽度
	Width int `json:"width,omitempty"`

	// Height 高度
	Height int `json:"height,omitempty"`
}

// ============== 简化浏览器（HTTP 客户端实现）==============

// Browser 简化浏览器
// 使用 HTTP 客户端实现基本的网页抓取功能
type Browser struct {
	config *BrowserConfig
	client *http.Client
}

// NewBrowser 创建浏览器
func NewBrowser(config ...*BrowserConfig) *Browser {
	cfg := DefaultBrowserConfig()
	if len(config) > 0 && config[0] != nil {
		cfg = config[0]
	}

	transport := &http.Transport{
		MaxIdleConns:       100,
		IdleConnTimeout:    90 * time.Second,
		DisableCompression: false,
	}

	if cfg.ProxyURL != "" {
		proxyURL, err := url.Parse(cfg.ProxyURL)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	return &Browser{
		config: cfg,
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: transport,
		},
	}
}

// MaxBrowserResponseSize 浏览器最大响应体大小（10MB）
const MaxBrowserResponseSize = 10 * 1024 * 1024

// GetContent 获取页面内容
func (b *Browser) GetContent(ctx context.Context, pageURL string) (*PageContent, error) {
	startTime := time.Now()

	// SSRF 防护：验证目标 URL 安全性
	if err := validateBrowserURL(ctx, pageURL); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNavigationFailed, err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNavigationFailed, err)
	}

	req.Header.Set("User-Agent", b.config.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNavigationFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, ErrPageNotFound
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: status code %d", ErrNavigationFailed, resp.StatusCode)
	}

	// 使用 LimitReader 防止 OOM
	limitedReader := io.LimitReader(resp.Body, MaxBrowserResponseSize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNavigationFailed, err)
	}
	if len(body) > MaxBrowserResponseSize {
		return nil, fmt.Errorf("%w: 响应体过大（超过 %d bytes）", ErrNavigationFailed, MaxBrowserResponseSize)
	}

	htmlContent := string(body)

	// 解析 HTML
	content := &PageContent{
		URL:      pageURL,
		HTML:     htmlContent,
		LoadTime: time.Since(startTime).Milliseconds(),
		Metadata: make(map[string]string),
	}

	// 提取标题
	content.Title = extractTitle(htmlContent)

	// 提取纯文本
	content.Text = extractText(htmlContent)

	// 转换为 Markdown
	content.Markdown = htmlToMarkdown(htmlContent)

	// 提取链接
	content.Links = extractLinks(htmlContent, pageURL)

	// 提取图片
	content.Images = extractImages(htmlContent, pageURL)

	// 提取元数据
	content.Metadata = extractMetadata(htmlContent)

	return content, nil
}

// GetText 获取页面纯文本
func (b *Browser) GetText(ctx context.Context, pageURL string) (string, error) {
	content, err := b.GetContent(ctx, pageURL)
	if err != nil {
		return "", err
	}
	return content.Text, nil
}

// GetMarkdown 获取页面 Markdown 内容
func (b *Browser) GetMarkdown(ctx context.Context, pageURL string) (string, error) {
	content, err := b.GetContent(ctx, pageURL)
	if err != nil {
		return "", err
	}
	return content.Markdown, nil
}

// GetLinks 获取页面链接
func (b *Browser) GetLinks(ctx context.Context, pageURL string) ([]Link, error) {
	content, err := b.GetContent(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	return content.Links, nil
}

// ============== 浏览器工具接口 ==============

// BrowserTool 浏览器工具
type BrowserTool struct {
	browser *Browser
}

// NewBrowserTool 创建浏览器工具
func NewBrowserTool(config ...*BrowserConfig) *BrowserTool {
	return &BrowserTool{
		browser: NewBrowser(config...),
	}
}

// Name 工具名称
func (bt *BrowserTool) Name() string {
	return "browser"
}

// Description 工具描述
func (bt *BrowserTool) Description() string {
	return "Browse web pages, extract content, links, and images. Use this tool to access and read web content."
}

// Schema 工具 Schema
func (bt *BrowserTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL of the web page to browse",
			},
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"get_content", "get_text", "get_markdown", "get_links"},
				"description": "The action to perform",
				"default":     "get_text",
			},
		},
		"required": []string{"url"},
	}
}

// Execute 执行工具
func (bt *BrowserTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return nil, errors.New("url is required")
	}

	action, _ := args["action"].(string)
	if action == "" {
		action = "get_text"
	}

	switch action {
	case "get_content":
		return bt.browser.GetContent(ctx, urlStr)
	case "get_text":
		return bt.browser.GetText(ctx, urlStr)
	case "get_markdown":
		return bt.browser.GetMarkdown(ctx, urlStr)
	case "get_links":
		return bt.browser.GetLinks(ctx, urlStr)
	default:
		return bt.browser.GetText(ctx, urlStr)
	}
}

// Validate 验证参数
func (bt *BrowserTool) Validate(args map[string]any) error {
	if _, ok := args["url"].(string); !ok {
		return errors.New("url must be a string")
	}
	return nil
}

// ============== 截图工具 ==============

// ScreenshotOptions 截图参数。
type ScreenshotOptions struct {
	FullPage bool // 是否整页截图
	Width    int  // 视口宽度
	Height   int  // 视口高度
}

// ScreenshotBackend 是真实截图后端接口。
//
// 框架不内置浏览器自动化（Playwright/Puppeteer/chromedp 等为重依赖），通过本接口
// 让用户注入真实后端。注入后 ScreenshotTool 返回真实截图字节；未注入时返回带明确
// 标注的占位 SVG（不伪造成真实截图）。
//
// Capture 返回截图字节与图像格式（如 "png"/"jpeg"）。
type ScreenshotBackend interface {
	Capture(ctx context.Context, url string, opts ScreenshotOptions) (image []byte, format string, err error)
}

// ScreenshotTool 截图工具
type ScreenshotTool struct {
	config  *BrowserConfig
	backend ScreenshotBackend
}

// NewScreenshotTool 创建截图工具
func NewScreenshotTool(config ...*BrowserConfig) *ScreenshotTool {
	cfg := DefaultBrowserConfig()
	if len(config) > 0 && config[0] != nil {
		cfg = config[0]
	}
	return &ScreenshotTool{config: cfg}
}

// WithBackend 注入真实截图后端并返回自身（链式）。
// 注入后 Execute 返回真实截图；未注入时返回占位 SVG。
func (st *ScreenshotTool) WithBackend(b ScreenshotBackend) *ScreenshotTool {
	st.backend = b
	return st
}

// Name 工具名称
func (st *ScreenshotTool) Name() string {
	return "screenshot"
}

// Description 工具描述
func (st *ScreenshotTool) Description() string {
	return "Take a screenshot of a web page. Returns the screenshot as base64 encoded image. Note: This is a placeholder - requires browser automation backend."
}

// Schema 工具 Schema
func (st *ScreenshotTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL of the web page to screenshot",
			},
			"full_page": map[string]any{
				"type":        "boolean",
				"description": "Whether to capture the full page",
				"default":     false,
			},
			"width": map[string]any{
				"type":        "integer",
				"description": "Viewport width",
				"default":     1920,
			},
			"height": map[string]any{
				"type":        "integer",
				"description": "Viewport height",
				"default":     1080,
			},
		},
		"required": []string{"url"},
	}
}

// Execute 执行工具
func (st *ScreenshotTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return nil, errors.New("url is required")
	}

	// 已注入真实后端：返回真实截图字节。
	if st.backend != nil {
		full, _ := args["full_page"].(bool)
		img, format, err := st.backend.Capture(ctx, urlStr, ScreenshotOptions{
			FullPage: full,
			Width:    st.config.ViewportWidth,
			Height:   st.config.ViewportHeight,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"url":    urlStr,
			"image":  base64.StdEncoding.EncodeToString(img),
			"format": format,
			"width":  st.config.ViewportWidth,
			"height": st.config.ViewportHeight,
		}, nil
	}

	// 未注入后端：返回一个明确标注的 SVG 占位图（不伪造成真实截图）
	placeholder := fmt.Sprintf(`<svg width="%d" height="%d" xmlns="http://www.w3.org/2000/svg">
		<rect width="100%%" height="100%%" fill="#f0f0f0"/>
		<text x="50%%" y="50%%" text-anchor="middle" dy=".3em" fill="#666">
			Screenshot placeholder for: %s
		</text>
	</svg>`, st.config.ViewportWidth, st.config.ViewportHeight, urlStr)

	return map[string]any{
		"url":    urlStr,
		"image":  base64.StdEncoding.EncodeToString([]byte(placeholder)),
		"format": "svg",
		"width":  st.config.ViewportWidth,
		"height": st.config.ViewportHeight,
		"note":   "This is a placeholder. Full screenshot functionality requires browser automation backend (e.g., Playwright, Puppeteer).",
	}, nil
}

// Validate 验证参数
func (st *ScreenshotTool) Validate(args map[string]any) error {
	if _, ok := args["url"].(string); !ok {
		return errors.New("url must be a string")
	}
	return nil
}

// ============== HTML 解析辅助函数 ==============

// extractTitle 提取标题
func extractTitle(htmlContent string) string {
	re := regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`)
	matches := re.FindStringSubmatch(htmlContent)
	if len(matches) > 1 {
		return strings.TrimSpace(html.UnescapeString(matches[1]))
	}
	return ""
}

// extractText 提取纯文本
func extractText(htmlContent string) string {
	// 移除 script 和 style
	re := regexp.MustCompile(`(?is)<(?:script|style|noscript)[^>]*>.*?</(?:script|style|noscript)>`)
	text := re.ReplaceAllString(htmlContent, "")

	// 移除 HTML 注释
	re = regexp.MustCompile(`(?s)<!--.*?-->`)
	text = re.ReplaceAllString(text, "")

	// 移除所有标签
	re = regexp.MustCompile(`<[^>]+>`)
	text = re.ReplaceAllString(text, " ")

	// 解码 HTML 实体
	text = html.UnescapeString(text)

	// 清理空白
	re = regexp.MustCompile(`\s+`)
	text = re.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

// htmlToMarkdown 将 HTML 转换为 Markdown
func htmlToMarkdown(htmlContent string) string {
	// 简化实现
	text := htmlContent

	// 移除 script 和 style
	re := regexp.MustCompile(`(?is)<(?:script|style|noscript)[^>]*>.*?</(?:script|style|noscript)>`)
	text = re.ReplaceAllString(text, "")

	// 转换标题
	for i := 6; i >= 1; i-- {
		pattern := fmt.Sprintf(`(?is)<h%d[^>]*>([^<]*)</h%d>`, i, i)
		replacement := fmt.Sprintf("\n%s $1\n", strings.Repeat("#", i))
		re = regexp.MustCompile(pattern)
		text = re.ReplaceAllString(text, replacement)
	}

	// 转换段落
	re = regexp.MustCompile(`(?is)<p[^>]*>([^<]*)</p>`)
	text = re.ReplaceAllString(text, "\n$1\n")

	// 转换链接
	re = regexp.MustCompile(`(?is)<a[^>]*href=["']([^"']+)["'][^>]*>([^<]*)</a>`)
	text = re.ReplaceAllString(text, "[$2]($1)")

	// 转换粗体
	re = regexp.MustCompile(`(?is)<(strong|b)[^>]*>([^<]*)</(?:strong|b)>`)
	text = re.ReplaceAllString(text, "**$2**")

	// 转换斜体
	re = regexp.MustCompile(`(?is)<(em|i)[^>]*>([^<]*)</(?:em|i)>`)
	text = re.ReplaceAllString(text, "*$2*")

	// 转换代码
	re = regexp.MustCompile(`(?is)<code[^>]*>([^<]*)</code>`)
	text = re.ReplaceAllString(text, "`$1`")

	// 转换列表项
	re = regexp.MustCompile(`(?is)<li[^>]*>([^<]*)</li>`)
	text = re.ReplaceAllString(text, "- $1\n")

	// 移除剩余标签
	re = regexp.MustCompile(`<[^>]+>`)
	text = re.ReplaceAllString(text, "")

	// 解码 HTML 实体
	text = html.UnescapeString(text)

	// 清理多余空行
	re = regexp.MustCompile(`\n{3,}`)
	text = re.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}

// extractLinks 提取链接
func extractLinks(htmlContent, baseURL string) []Link {
	var links []Link

	re := regexp.MustCompile(`(?is)<a[^>]*href=["']([^"']+)["'][^>]*(?:title=["']([^"']*?)["'])?[^>]*>([^<]*)</a>`)
	matches := re.FindAllStringSubmatch(htmlContent, -1)

	base, _ := url.Parse(baseURL)

	for _, match := range matches {
		if len(match) < 4 {
			continue
		}

		href := match[1]
		title := match[2]
		text := strings.TrimSpace(html.UnescapeString(match[3]))

		// 解析相对链接
		linkURL, err := url.Parse(href)
		if err != nil {
			continue
		}
		if !linkURL.IsAbs() && base != nil {
			linkURL = base.ResolveReference(linkURL)
		}

		links = append(links, Link{
			Text:  text,
			URL:   linkURL.String(),
			Title: title,
		})
	}

	return links
}

// extractImages 提取图片
func extractImages(htmlContent, baseURL string) []Image {
	var images []Image

	re := regexp.MustCompile(`(?is)<img[^>]*src=["']([^"']+)["'][^>]*(?:alt=["']([^"']*?)["'])?[^>]*>`)
	matches := re.FindAllStringSubmatch(htmlContent, -1)

	base, _ := url.Parse(baseURL)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		src := match[1]
		alt := match[2]

		// 解析相对链接
		imgURL, err := url.Parse(src)
		if err != nil {
			continue
		}
		if !imgURL.IsAbs() && base != nil {
			imgURL = base.ResolveReference(imgURL)
		}

		images = append(images, Image{
			Src: imgURL.String(),
			Alt: alt,
		})
	}

	return images
}

// extractMetadata 提取元数据
func extractMetadata(htmlContent string) map[string]string {
	metadata := make(map[string]string)

	// 提取 meta 标签
	re := regexp.MustCompile(`(?is)<meta[^>]*(?:name|property)=["']([^"']+)["'][^>]*content=["']([^"']+)["'][^>]*>`)
	matches := re.FindAllStringSubmatch(htmlContent, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			name := strings.ToLower(match[1])
			content := html.UnescapeString(match[2])
			metadata[name] = content
		}
	}

	// 也尝试反向顺序 (content 在前)
	re = regexp.MustCompile(`(?is)<meta[^>]*content=["']([^"']+)["'][^>]*(?:name|property)=["']([^"']+)["'][^>]*>`)
	matches = re.FindAllStringSubmatch(htmlContent, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			content := html.UnescapeString(match[1])
			name := strings.ToLower(match[2])
			metadata[name] = content
		}
	}

	return metadata
}

// ============== URL 解析工具 ==============

// URLParseTool URL 解析工具
type URLParseTool struct{}

// NewURLParseTool 创建 URL 解析工具
func NewURLParseTool() *URLParseTool {
	return &URLParseTool{}
}

// Name 工具名称
func (ut *URLParseTool) Name() string {
	return "url_parse"
}

// Description 工具描述
func (ut *URLParseTool) Description() string {
	return "Parse a URL and extract its components (scheme, host, path, query parameters, etc.)"
}

// Schema 工具 Schema
func (ut *URLParseTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to parse",
			},
		},
		"required": []string{"url"},
	}
}

// Execute 执行工具
func (ut *URLParseTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return nil, errors.New("url is required")
	}

	parsed, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	// 解析查询参数
	queryParams := make(map[string]any)
	for key, values := range parsed.Query() {
		if len(values) == 1 {
			queryParams[key] = values[0]
		} else {
			queryParams[key] = values
		}
	}

	return map[string]any{
		"scheme":       parsed.Scheme,
		"host":         parsed.Host,
		"hostname":     parsed.Hostname(),
		"port":         parsed.Port(),
		"path":         parsed.Path,
		"query":        parsed.RawQuery,
		"query_params": queryParams,
		"fragment":     parsed.Fragment,
		"user":         parsed.User.String(),
	}, nil
}

// Validate 验证参数
func (ut *URLParseTool) Validate(args map[string]any) error {
	if _, ok := args["url"].(string); !ok {
		return errors.New("url must be a string")
	}
	return nil
}

// ============== JSON API 工具 ==============

// JSONAPITool JSON API 请求工具
type JSONAPITool struct {
	client *http.Client
}

// NewJSONAPITool 创建 JSON API 工具
func NewJSONAPITool(timeout ...time.Duration) *JSONAPITool {
	t := 30 * time.Second
	if len(timeout) > 0 {
		t = timeout[0]
	}
	return &JSONAPITool{
		client: &http.Client{Timeout: t},
	}
}

// Name 工具名称
func (jt *JSONAPITool) Name() string {
	return "json_api"
}

// Description 工具描述
func (jt *JSONAPITool) Description() string {
	return "Make HTTP requests to JSON APIs. Supports GET, POST, PUT, DELETE methods."
}

// Schema 工具 Schema
func (jt *JSONAPITool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The API endpoint URL",
			},
			"method": map[string]any{
				"type":        "string",
				"enum":        []string{"GET", "POST", "PUT", "DELETE", "PATCH"},
				"description": "HTTP method",
				"default":     "GET",
			},
			"headers": map[string]any{
				"type":        "object",
				"description": "HTTP headers",
			},
			"body": map[string]any{
				"type":        "object",
				"description": "Request body (for POST/PUT/PATCH)",
			},
		},
		"required": []string{"url"},
	}
}

// Execute 执行工具
func (jt *JSONAPITool) Execute(ctx context.Context, args map[string]any) (any, error) {
	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return nil, errors.New("url is required")
	}

	method, _ := args["method"].(string)
	if method == "" {
		method = "GET"
	}
	method = strings.ToUpper(method)

	var bodyReader io.Reader
	if body, ok := args["body"].(map[string]any); ok && len(body) > 0 {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal body: %w", err)
		}
		bodyReader = strings.NewReader(string(bodyBytes))
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if headers, ok := args["headers"].(map[string]any); ok {
		for key, value := range headers {
			if strVal, ok := value.(string); ok {
				req.Header.Set(key, strVal)
			}
		}
	}

	resp, err := jt.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result any
	if err := json.Unmarshal(respBody, &result); err != nil {
		// 如果不是 JSON，返回原始文本
		result = string(respBody)
	}

	return map[string]any{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        result,
	}, nil
}

// Validate 验证参数
func (jt *JSONAPITool) Validate(args map[string]any) error {
	if _, ok := args["url"].(string); !ok {
		return errors.New("url must be a string")
	}
	return nil
}

// validateBrowserURL 验证浏览器请求的 URL 安全性，防止 SSRF 攻击
func validateBrowserURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("无效的 URL: %w", err)
	}

	// scheme 限制：仅允许 http/https。toolkit ssrf.ValidateURL 不校验协议，故在此保留。
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("不允许的协议: %s（仅支持 http/https）", u.Scheme)
	}
	// SSRF 核心校验复用 toolkit/net/ssrf（含 DNS 解析逐 IP 私网检查，较旧实现增强了抗 DNS rebinding）。
	return ssrf.ValidateURL(ctx, rawURL)
}
