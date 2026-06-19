package text

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/tool"
)

func TestTools(t *testing.T) {
	tools := Tools()
	if len(tools) == 0 {
		t.Fatal("Tools() should return non-empty slice")
	}

	// 验证所有工具都实现了 Tool 接口
	for _, tl := range tools {
		if tl.Name() == "" {
			t.Error("Tool name should not be empty")
		}
		if tl.Description() == "" {
			t.Error("Tool description should not be empty")
		}
		if tl.Schema() == nil {
			t.Error("Tool schema should not be nil")
		}
	}
}

func TestTextAnalyze(t *testing.T) {
	tools := Tools()
	var textAnalyze tool.Tool
	for _, tl := range tools {
		if tl.Name() == "text_analyze" {
			textAnalyze = tl
			break
		}
	}
	if textAnalyze == nil {
		t.Fatal("text_analyze tool not found")
	}

	ctx := context.Background()

	tests := []struct {
		name  string
		text  string
		check func(*testing.T, TextAnalysis)
	}{
		{
			name: "简单文本",
			text: "Hello world. This is a test.",
			check: func(t *testing.T, analysis TextAnalysis) {
				if analysis.WordCount == 0 {
					t.Error("WordCount should not be 0")
				}
				if analysis.CharCount == 0 {
					t.Error("CharCount should not be 0")
				}
				if analysis.SentenceCount == 0 {
					t.Error("SentenceCount should not be 0")
				}
			},
		},
		{
			name: "空文本",
			text: "",
			check: func(t *testing.T, analysis TextAnalysis) {
				if analysis.WordCount != 0 {
					t.Error("WordCount should be 0 for empty text")
				}
			},
		},
		{
			name: "中文文本",
			text: "你好世界。这是一个测试。",
			check: func(t *testing.T, analysis TextAnalysis) {
				if analysis.CharCount == 0 {
					t.Error("CharCount should not be 0")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"text": tt.text,
			}

			result, err := textAnalyze.Execute(ctx, args)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if analysis, ok := result.Output.(TextAnalysis); ok {
				tt.check(t, analysis)
			} else {
				t.Fatal("result is not TextAnalysis")
			}
		})
	}
}

func TestTextTransform(t *testing.T) {
	tools := Tools()
	var textTransform tool.Tool
	for _, tl := range tools {
		if tl.Name() == "text_transform" {
			textTransform = tl
			break
		}
	}
	if textTransform == nil {
		t.Fatal("text_transform tool not found")
	}

	ctx := context.Background()

	tests := []struct {
		name      string
		text      string
		transform string
		want      string
	}{
		{
			name:      "转大写",
			text:      "hello",
			transform: "uppercase",
			want:      "HELLO",
		},
		{
			name:      "转小写",
			text:      "HELLO",
			transform: "lowercase",
			want:      "hello",
		},
		{
			name:      "首字母大写",
			text:      "hello",
			transform: "capitalize",
			want:      "Hello",
		},
		{
			name:      "反转",
			text:      "hello",
			transform: "reverse",
			want:      "olleh",
		},
		{
			name:      "去空格",
			text:      "  hello  ",
			transform: "trim",
			want:      "hello",
		},
		{
			name:      "slug",
			text:      "Hello World!",
			transform: "slug",
			want:      "hello-world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"text":      tt.text,
				"transform": tt.transform,
			}

			result, err := textTransform.Execute(ctx, args)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if resultMap, ok := result.Output.(map[string]any); ok {
				if got, ok := resultMap["result"].(string); ok {
					if got != tt.want {
						t.Errorf("got %q, want %q", got, tt.want)
					}
				}
			}
		})
	}
}

func TestTextExtract(t *testing.T) {
	tools := Tools()
	var textExtract tool.Tool
	for _, tl := range tools {
		if tl.Name() == "text_extract" {
			textExtract = tl
			break
		}
	}
	if textExtract == nil {
		t.Fatal("text_extract tool not found")
	}

	ctx := context.Background()

	tests := []struct {
		name       string
		text       string
		pattern    string
		wantErr    bool
		wantCount  int
		checkMatch func(*testing.T, []string)
	}{
		{
			name:      "提取数字",
			text:      "I have 3 apples and 5 oranges",
			pattern:   `\d+`,
			wantCount: 2,
			checkMatch: func(t *testing.T, matches []string) {
				if len(matches) != 2 {
					t.Errorf("got %d matches, want 2", len(matches))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"text":    tt.text,
				"pattern": tt.pattern,
			}

			result, err := textExtract.Execute(ctx, args)
			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.checkMatch != nil {
				if resultMap, ok := result.Output.(map[string]any); ok {
					if matches, ok := resultMap["matches"].([]string); ok {
						tt.checkMatch(t, matches)
					}
				}
			}
		})
	}
}

func TestTextReplace(t *testing.T) {
	tools := Tools()
	var textReplace tool.Tool
	for _, tl := range tools {
		if tl.Name() == "text_replace" {
			textReplace = tl
			break
		}
	}
	if textReplace == nil {
		t.Fatal("text_replace tool not found")
	}

	ctx := context.Background()

	tests := []struct {
		name        string
		text        string
		pattern     string
		replacement string
		want        string
		wantErr     bool
	}{
		{
			name:        "替换数字",
			text:        "I have 3 apples",
			pattern:     `\d+`,
			replacement: "X",
			want:        "I have X apples",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"text":        tt.text,
				"pattern":     tt.pattern,
				"replacement": tt.replacement,
			}

			result, err := textReplace.Execute(ctx, args)
			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if resultMap, ok := result.Output.(map[string]any); ok {
					if got, ok := resultMap["result"].(string); ok {
						if got != tt.want {
							t.Errorf("got %q, want %q", got, tt.want)
						}
					}
				}
			}
		})
	}
}

func TestTextSplit(t *testing.T) {
	tools := Tools()
	var textSplit tool.Tool
	for _, tl := range tools {
		if tl.Name() == "text_split" {
			textSplit = tl
			break
		}
	}
	if textSplit == nil {
		t.Fatal("text_split tool not found")
	}

	ctx := context.Background()

	tests := []struct {
		name      string
		text      string
		separator string
		limit     int
		wantCount int
	}{
		{
			name:      "按换行符分割",
			text:      "line1\nline2\nline3",
			separator: "newline",
			wantCount: 3,
		},
		{
			name:      "按单词分割",
			text:      "hello world test",
			separator: "word",
			wantCount: 3,
		},
		{
			name:      "按字符分割",
			text:      "hello",
			separator: "char",
			wantCount: 5,
		},
		{
			name:      "自定义分隔符",
			text:      "a,b,c",
			separator: ",",
			wantCount: 3,
		},
		{
			name:      "限制数量",
			text:      "a,b,c,d,e",
			separator: ",",
			limit:     3,
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"text":      tt.text,
				"separator": tt.separator,
			}
			if tt.limit > 0 {
				args["limit"] = tt.limit
			}

			result, err := textSplit.Execute(ctx, args)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if resultMap, ok := result.Output.(map[string]any); ok {
				if count, ok := resultMap["count"].(int); ok {
					if count != tt.wantCount {
						t.Errorf("got count = %d, want %d", count, tt.wantCount)
					}
				}
			}
		})
	}
}

func TestTextEncode(t *testing.T) {
	tools := Tools()
	var textEncode tool.Tool
	for _, tl := range tools {
		if tl.Name() == "text_encode" {
			textEncode = tl
			break
		}
	}
	if textEncode == nil {
		t.Fatal("text_encode tool not found")
	}

	ctx := context.Background()

	tests := []struct {
		name      string
		text      string
		operation string
		wantErr   bool
		check     func(*testing.T, string)
	}{
		{
			name:      "Base64编码",
			text:      "hello",
			operation: "base64_encode",
			check: func(t *testing.T, result string) {
				if result == "" {
					t.Error("encoded result should not be empty")
				}
			},
		},
		{
			name:      "Base64解码",
			text:      "aGVsbG8=",
			operation: "base64_decode",
			check: func(t *testing.T, result string) {
				if result != "hello" {
					t.Errorf("got %q, want %q", result, "hello")
				}
			},
		},
		{
			name:      "HTML转义",
			text:      "<div>test</div>",
			operation: "html_escape",
			check: func(t *testing.T, result string) {
				if result == "<div>test</div>" {
					t.Error("html should be escaped")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"text":      tt.text,
				"operation": tt.operation,
			}

			result, err := textEncode.Execute(ctx, args)
			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.check != nil {
				if resultMap, ok := result.Output.(map[string]any); ok {
					if res, ok := resultMap["result"].(string); ok {
						tt.check(t, res)
					}
				}
			}
		})
	}
}

func TestTextHash(t *testing.T) {
	tools := Tools()
	var textHash tool.Tool
	for _, tl := range tools {
		if tl.Name() == "text_hash" {
			textHash = tl
			break
		}
	}
	if textHash == nil {
		t.Fatal("text_hash tool not found")
	}

	ctx := context.Background()

	tests := []struct {
		name      string
		text      string
		algorithm string
		want      string // 期望哈希值，用于钉死与标准库等价（复用 toolkit/util/hash 后行为不变）
		wantErr   bool
	}{
		{
			name:      "MD5哈希",
			text:      "hello",
			algorithm: "md5",
			want:      "5d41402abc4b2a76b9719d911017c592",
		},
		{
			name:      "SHA256哈希",
			text:      "hello",
			algorithm: "sha256",
			want:      "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"text":      tt.text,
				"algorithm": tt.algorithm,
			}

			result, err := textHash.Execute(ctx, args)
			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if resultMap, ok := result.Output.(map[string]any); ok {
					if hash, ok := resultMap["hash"].(string); ok {
						if hash == "" {
							t.Error("hash should not be empty")
						}
						if tt.want != "" && hash != tt.want {
							t.Errorf("hash = %q, want %q", hash, tt.want)
						}
					}
				}
			}
		})
	}
}

func TestTextSimilarity(t *testing.T) {
	tools := Tools()
	var textSimilarity tool.Tool
	for _, tl := range tools {
		if tl.Name() == "text_similarity" {
			textSimilarity = tl
			break
		}
	}
	if textSimilarity == nil {
		t.Fatal("text_similarity tool not found")
	}

	ctx := context.Background()

	tests := []struct {
		name   string
		text1  string
		text2  string
		method string
	}{
		{
			name:   "Levenshtein相似度",
			text1:  "hello",
			text2:  "hallo",
			method: "levenshtein",
		},
		{
			name:   "Jaccard相似度",
			text1:  "hello world",
			text2:  "hello there",
			method: "jaccard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"text1":  tt.text1,
				"text2":  tt.text2,
				"method": tt.method,
			}

			result, err := textSimilarity.Execute(ctx, args)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if resultMap, ok := result.Output.(map[string]any); ok {
				if _, ok := resultMap["similarity"].(float64); !ok {
					t.Error("result should contain similarity field")
				}
			}
		})
	}
}

func TestTextTruncate(t *testing.T) {
	tools := Tools()
	var textTruncate tool.Tool
	for _, tl := range tools {
		if tl.Name() == "text_truncate" {
			textTruncate = tl
			break
		}
	}
	if textTruncate == nil {
		t.Fatal("text_truncate tool not found")
	}

	ctx := context.Background()

	tests := []struct {
		name          string
		text          string
		length        int
		suffix        string
		wantTruncated bool
	}{
		{
			name:          "需要截断",
			text:          "This is a very long text",
			length:        10,
			suffix:        "...",
			wantTruncated: true,
		},
		{
			name:          "不需要截断",
			text:          "Short",
			length:        10,
			suffix:        "...",
			wantTruncated: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"text":   tt.text,
				"length": tt.length,
				"suffix": tt.suffix,
			}

			result, err := textTruncate.Execute(ctx, args)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if resultMap, ok := result.Output.(map[string]any); ok {
				if truncated, ok := resultMap["truncated"].(bool); ok {
					if truncated != tt.wantTruncated {
						t.Errorf("truncated = %v, want %v", truncated, tt.wantTruncated)
					}
				}
			}
		})
	}
}

func TestTextExtractEntities(t *testing.T) {
	tools := Tools()
	var textExtractEntities tool.Tool
	for _, tl := range tools {
		if tl.Name() == "text_extract_entities" {
			textExtractEntities = tl
			break
		}
	}
	if textExtractEntities == nil {
		t.Fatal("text_extract_entities tool not found")
	}

	ctx := context.Background()

	tests := []struct {
		name      string
		text      string
		typ       string
		wantCount int
	}{
		{
			name:      "提取邮箱",
			text:      "Contact me at test@example.com",
			typ:       "email",
			wantCount: 1,
		},
		{
			name:      "提取URL",
			text:      "Visit https://example.com for more info",
			typ:       "url",
			wantCount: 1,
		},
		{
			name: "提取所有实体",
			text: "Email test@example.com, visit https://example.com, #hashtag",
			typ:  "all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"text": tt.text,
				"type": tt.typ,
			}

			result, err := textExtractEntities.Execute(ctx, args)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if resultMap, ok := result.Output.(map[string]any); ok {
				if entities, ok := resultMap["entities"].([]string); ok {
					if tt.wantCount > 0 && len(entities) != tt.wantCount {
						t.Errorf("got %d entities, want %d", len(entities), tt.wantCount)
					}
				}
			}
		})
	}
}
