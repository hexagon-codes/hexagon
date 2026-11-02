package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tool"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// PreservedToolResult 保留完整协议结果，并提供不含媒体编码的文本投影。
type PreservedToolResult struct{ *sdkmcp.CallToolResult }

// ResultIsError 仅依据实际协议中的 isError，兼容保留协议结果的装饰器。
// 不能按错误文本猜测远端是否执行。
func ResultIsError(result tool.Result) bool {
	if result.Success || result.Output == nil {
		return false
	}
	encoded, err := json.Marshal(result.Output)
	if err != nil {
		return false
	}
	var protocol struct {
		IsError bool `json:"isError"`
	}
	return json.Unmarshal(encoded, &protocol) == nil && protocol.IsError
}

// MarshalJSON 仅序列化去编码的内容与字节描述；原协议对象仍供内部附件链读取。
func (r *PreservedToolResult) MarshalJSON() ([]byte, error) {
	if r == nil || r.CallToolResult == nil {
		return []byte("null"), nil
	}
	contents := make([]json.RawMessage, 0, len(r.Content))
	var descriptors []BinaryDescriptor
	for index, content := range r.Content {
		projection, err := ProjectContent(content, fmt.Sprintf("tool-content-%d", index+1))
		if err != nil {
			return nil, err
		}
		contents = append(contents, projection.Content)
		descriptors = append(descriptors, projection.BinaryFiles()...)
	}
	return json.Marshal(struct {
		Meta              sdkmcp.Meta        `json:"_meta,omitempty"`
		Content           []json.RawMessage  `json:"content"`
		StructuredContent any                `json:"structuredContent,omitempty"`
		IsError           bool               `json:"isError,omitempty"`
		BinaryFiles       []BinaryDescriptor `json:"binary_files,omitempty"`
	}{Meta: r.Meta, Content: contents, StructuredContent: r.StructuredContent, IsError: r.IsError, BinaryFiles: descriptors})
}

func (r *PreservedToolResult) ToolText() string {
	if r == nil || r.CallToolResult == nil {
		return ""
	}
	var text []string
	for index, content := range r.Content {
		projection, err := ProjectContent(content, fmt.Sprintf("tool-content-%d", index+1))
		if err != nil {
			text = append(text, fmt.Sprintf("MCP content projection failed: %v", err))
			continue
		}
		text = append(text, projection.ModelText())
	}
	if r.StructuredContent != nil {
		if encoded, err := json.Marshal(r.StructuredContent); err == nil {
			text = append(text, string(encoded))
		}
	}
	return strings.Join(text, "\n")
}

// ToolMessageContent 只承载上游实际返回的图片，不附加解题或批改指令。
func (r *PreservedToolResult) ToolMessageContent() []llm.ContentPart {
	var parts []llm.ContentPart
	for _, binary := range r.ToolBinaryContents() {
		if image, ok := binary.ImagePart(); ok {
			parts = append(parts, image)
		}
	}
	return parts
}

// ToolBinaryContents 提供图片、音频及嵌入资源的实际字节，不绑定应用附件或归属模型。
func (r *PreservedToolResult) ToolBinaryContents() []BinaryContent {
	if r == nil || r.CallToolResult == nil {
		return nil
	}
	var binaries []BinaryContent
	for index, content := range r.Content {
		projection, err := ProjectContent(content, fmt.Sprintf("tool-content-%d", index+1))
		if err == nil {
			binaries = append(binaries, projection.Binaries...)
		}
	}
	return binaries
}
