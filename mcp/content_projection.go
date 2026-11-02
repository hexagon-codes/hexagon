package mcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// BinaryContent 保存协议实际返回的字节；URI 只表示来源，不触发读取或授予访问权限。
// Type 保留原内容种类，调用方据此区分图片观察与资源文件交付。
type BinaryContent struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	MIMEType  string `json:"mime"`
	SourceURI string `json:"source_uri,omitempty"`
	Data      []byte `json:"-"`
}

// BinaryDescriptor 只描述同一份实际字节，不包含编码正文、权限或文件下载地址。
type BinaryDescriptor struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	MIMEType  string `json:"mime"`
	SourceURI string `json:"source_uri,omitempty"`
	Size      int64  `json:"size"`
	Digest    string `json:"digest"`
}

// Descriptor 从实际字节计算大小与摘要，不信任上游声明的 size 或 digest。
func (b BinaryContent) Descriptor() BinaryDescriptor {
	hash := sha256.Sum256(b.Data)
	return BinaryDescriptor{Type: b.Type, Name: b.Name, MIMEType: b.MIMEType, SourceURI: b.SourceURI,
		Size: int64(len(b.Data)), Digest: "sha256:" + hex.EncodeToString(hash[:])}
}

// MarshalJSON 只输出由实际字节计算的描述，不编码二进制正文。
func (b BinaryContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.Descriptor())
}

// ImagePart 仅投影非空、声明为图片的实际字节；其它二进制继续作为文件引用使用。
// 模型是否具备视觉能力由调用方按本次实际模型判断。
func (b BinaryContent) ImagePart() (llm.ContentPart, bool) {
	if !strings.HasPrefix(b.MIMEType, "image/") || len(b.Data) == 0 {
		return llm.ContentPart{}, false
	}
	return llm.NewImageURLPart("data:"+b.MIMEType+";base64,"+base64.StdEncoding.EncodeToString(b.Data), "auto"), true
}

// ContentProjection 是单条 MCP 内容的纯投影。Content 保留非二进制协议字段，
// Binaries 独立持有实际字节；所有描述均从 Binaries 派生。
type ContentProjection struct {
	Content  json.RawMessage `json:"content"`
	Text     string          `json:"text,omitempty"`
	Binaries []BinaryContent `json:"-"`
}

// BinaryFiles 返回原字节对应的描述，不另持久化一份可分叉的元数据。
func (p ContentProjection) BinaryFiles() []BinaryDescriptor {
	var descriptors []BinaryDescriptor
	for _, binary := range p.Binaries {
		descriptors = append(descriptors, binary.Descriptor())
	}
	return descriptors
}

// MarshalJSON 将描述从同一组原字节派生，避免独立元数据与媒体内容分叉。
func (p ContentProjection) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Content     json.RawMessage    `json:"content"`
		Text        string             `json:"text,omitempty"`
		BinaryFiles []BinaryDescriptor `json:"binary_files,omitempty"`
	}{Content: p.Content, Text: p.Text, BinaryFiles: p.BinaryFiles()})
}

// ModelText 保留正文和实际字节描述，避免把媒体编码伪装成可观察的文字内容。
func (p ContentProjection) ModelText() string {
	if len(p.Binaries) == 0 {
		return p.Text
	}
	encoded, err := json.Marshal(p.BinaryFiles())
	if err != nil {
		return fmt.Sprintf("MCP binary descriptor projection failed: %v", err)
	}
	if p.Text == "" {
		return string(encoded)
	}
	return p.Text + "\n" + string(encoded)
}

// ProjectContent 解析官方 SDK 内容，不读取任何资源链接。
func ProjectContent(content sdkmcp.Content, fallbackName string) (ContentProjection, error) {
	raw, err := json.Marshal(content)
	if err != nil {
		return ContentProjection{}, fmt.Errorf("marshal MCP content: %w", err)
	}
	return ProjectContentJSON(raw, fallbackName)
}

// ProjectContentJSON 将一条 MCP 内容的编码字段转为原字节来源，其余字段原样保留。
// 它也供持有协议 JSON 的适配器复用，避免重新实现图片、音频和嵌入资源解析。
func ProjectContentJSON(raw json.RawMessage, fallbackName string) (ContentProjection, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ContentProjection{}, fmt.Errorf("decode MCP content: %w", err)
	}
	var wire struct {
		Type     string                   `json:"type"`
		Text     string                   `json:"text"`
		URI      string                   `json:"uri"`
		MIMEType string                   `json:"mimeType"`
		Data     []byte                   `json:"data"`
		Resource *sdkmcp.ResourceContents `json:"resource"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return ContentProjection{}, fmt.Errorf("decode MCP content fields: %w", err)
	}
	if wire.Type == "" {
		return ContentProjection{}, fmt.Errorf("MCP content type is missing")
	}
	projection := ContentProjection{Text: wire.Text}
	switch wire.Type {
	case "image", "audio":
		projection.Binaries = []BinaryContent{protocolBinary(wire.Type, wire.URI, wire.MIMEType, wire.Data, fallbackName)}
		projection.Text = "[MCP " + wire.Type + " content]"
		delete(fields, "data")
	case "resource":
		resource, err := ProjectResource(wire.Resource, fallbackName)
		if err != nil {
			return ContentProjection{}, err
		}
		projection.Text, projection.Binaries = resource.Text, resource.Binaries
		fields["resource"] = resource.Content
	case "resource_link":
		projection.Text = wire.URI
	}
	content, err := json.Marshal(fields)
	if err != nil {
		return ContentProjection{}, fmt.Errorf("encode MCP content projection: %w", err)
	}
	projection.Content = content
	return projection, nil
}

// ProjectResource 保留文本、URI 与 MIME，零字节 Blob 或已返回的空文本资源都保留为文件。
// SDK 的 Text 字符串不保留 wire 字段存在性；此处处理已取得的资源，不替代协议必填校验。
func ProjectResource(content *sdkmcp.ResourceContents, fallbackName string) (ContentProjection, error) {
	if content == nil {
		return ContentProjection{}, fmt.Errorf("MCP resource content is missing")
	}
	projection := ContentProjection{Text: content.Text}
	copy := *content
	if content.Blob != nil || content.Text == "" {
		projection.Binaries = []BinaryContent{protocolBinary("resource", content.URI, content.MIMEType, content.Blob, fallbackName)}
		copy.Blob = nil
	}
	encoded, err := json.Marshal(&copy)
	if err != nil {
		return ContentProjection{}, fmt.Errorf("encode MCP resource projection: %w", err)
	}
	projection.Content = encoded
	return projection, nil
}

// ParseImagePart 解析已标记为 data:image 的实际图片观察，不获取远程 URL。
// recognized=false 表示该内容不是本函数处理的内联图片。
func ParseImagePart(part llm.ContentPart, fallbackName string) (binary BinaryContent, recognized bool, err error) {
	if part.Type != "image_url" || part.ImageURL == nil || !strings.HasPrefix(part.ImageURL.URL, "data:image/") {
		return BinaryContent{}, false, nil
	}
	header, encoded, ok := strings.Cut(part.ImageURL.URL, ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		return BinaryContent{}, true, fmt.Errorf("tool returned an unsupported image encoding")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return BinaryContent{}, true, fmt.Errorf("tool returned invalid image bytes")
	}
	contentType := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	return protocolBinary("image", "", contentType, data, fallbackName), true, nil
}

// ToolImageContents 读取工具实际持有的图片，不把安全 JSON 描述当作媒体正文。
// 类型化结果只使用其原字节或消息投影；普通协议 JSON 复用内容解析，均不读取远程 URL。
func ToolImageContents(output any) ([]BinaryContent, error) {
	var images []BinaryContent
	switch typed := output.(type) {
	case interface{ ToolBinaryContents() []BinaryContent }:
		for _, binary := range typed.ToolBinaryContents() {
			if strings.HasPrefix(binary.MIMEType, "image/") {
				images = append(images, binary)
			}
		}
		return images, nil
	case interface{ ToolMessageContent() []llm.ContentPart }:
		for index, part := range typed.ToolMessageContent() {
			binary, recognized, err := ParseImagePart(part, fmt.Sprintf("tool-image-%d", index+1))
			if err != nil {
				return nil, err
			}
			if recognized {
				images = append(images, binary)
			}
		}
		return images, nil
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode MCP tool image content: %w", err)
	}
	for index, content := range envelope.Content {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(content, &kind); err != nil {
			return nil, err
		}
		if kind.Type != "image" {
			continue
		}
		projection, err := ProjectContentJSON(content, fmt.Sprintf("tool-image-%d", index+1))
		if err != nil {
			return nil, err
		}
		images = append(images, projection.Binaries...)
	}
	return images, nil
}

func protocolBinary(kind, uri, mimeType string, data []byte, fallback string) BinaryContent {
	name := ""
	if parsed, err := url.Parse(uri); err == nil {
		name = path.Base(parsed.Path)
	}
	if name == "" || name == "." || name == "/" || name == ".." {
		name = fallback
	}
	if name == "" {
		name = "mcp-content"
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if path.Ext(name) == "" {
		extension := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif",
			"application/pdf": ".pdf", "audio/mpeg": ".mp3", "audio/wav": ".wav", "audio/x-wav": ".wav", "audio/ogg": ".ogg",
			"audio/flac": ".flac", "audio/mp4": ".m4a"}[mimeType]
		if extension == "" {
			extension = ".bin"
		}
		name += extension
	}
	bytes := make([]byte, len(data))
	copy(bytes, data)
	return BinaryContent{Type: kind, Name: name, MIMEType: mimeType, SourceURI: uri, Data: bytes}
}
