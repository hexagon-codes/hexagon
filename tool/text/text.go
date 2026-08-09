// Package text 提供文本处理工具
//
// 支持文本分析、转换、提取等功能。
// 适用于 Agent 需要处理文本数据的场景。
package text

import (
	"context"
	"crypto/md5" // #nosec G501 -- 仅实现调用者明确选择的非安全 MD5 内容摘要。
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/toolkit/util/hash"
)

// Tools 返回文本处理工具集合
func Tools() []tool.Tool {
	return []tool.Tool{
		// 文本分析
		tool.NewFunc(
			"text_analyze",
			"分析文本内容 (字数、词数、句子数、段落数等)",
			func(ctx context.Context, input struct {
				Text string `json:"text" description:"要分析的文本"`
			}) (TextAnalysis, error) {
				return analyzeText(input.Text), nil
			},
		),

		// 文本转换
		tool.NewFunc(
			"text_transform",
			"文本转换 (大小写、编码、去空格等)",
			func(ctx context.Context, input struct {
				Text      string `json:"text" description:"要转换的文本"`
				Transform string `json:"transform" description:"转换类型: uppercase, lowercase, capitalize, title, reverse, trim, slug"`
			}) (struct {
				Result string `json:"result"`
			}, error) {
				result := transformText(input.Text, input.Transform)
				return struct {
					Result string `json:"result"`
				}{Result: result}, nil
			},
		),

		// 正则提取
		tool.NewFunc(
			"text_extract",
			"使用正则表达式提取文本",
			func(ctx context.Context, input struct {
				Text    string `json:"text" description:"源文本"`
				Pattern string `json:"pattern" description:"正则表达式"`
			}) (struct {
				Matches []string `json:"matches"`
				Count   int      `json:"count"`
			}, error) {
				re, err := regexp.Compile(input.Pattern)
				if err != nil {
					return struct {
						Matches []string `json:"matches"`
						Count   int      `json:"count"`
					}{}, fmt.Errorf("invalid pattern: %w", err)
				}
				matches := re.FindAllString(input.Text, -1)
				return struct {
					Matches []string `json:"matches"`
					Count   int      `json:"count"`
				}{
					Matches: matches,
					Count:   len(matches),
				}, nil
			},
		),

		// 正则替换
		tool.NewFunc(
			"text_replace",
			"使用正则表达式替换文本",
			func(ctx context.Context, input struct {
				Text        string `json:"text" description:"源文本"`
				Pattern     string `json:"pattern" description:"正则表达式"`
				Replacement string `json:"replacement" description:"替换内容"`
			}) (struct {
				Result string `json:"result"`
			}, error) {
				re, err := regexp.Compile(input.Pattern)
				if err != nil {
					return struct {
						Result string `json:"result"`
					}{}, fmt.Errorf("invalid pattern: %w", err)
				}
				result := re.ReplaceAllString(input.Text, input.Replacement)
				return struct {
					Result string `json:"result"`
				}{Result: result}, nil
			},
		),

		// 文本分割
		tool.NewFunc(
			"text_split",
			"分割文本",
			func(ctx context.Context, input struct {
				Text      string `json:"text" description:"源文本"`
				Separator string `json:"separator" description:"分隔符 (支持: newline, paragraph, sentence, word, char, 或自定义)"`
				Limit     int    `json:"limit,omitempty" description:"最大分割数量 (0 表示无限制)"`
			}) (struct {
				Parts []string `json:"parts"`
				Count int      `json:"count"`
			}, error) {
				parts := splitText(input.Text, input.Separator, input.Limit)
				return struct {
					Parts []string `json:"parts"`
					Count int      `json:"count"`
				}{
					Parts: parts,
					Count: len(parts),
				}, nil
			},
		),

		// 编码/解码
		tool.NewFunc(
			"text_encode",
			"文本编码/解码 (Base64, URL, HTML 实体等)",
			func(ctx context.Context, input struct {
				Text      string `json:"text" description:"要处理的文本"`
				Operation string `json:"operation" description:"操作类型: base64_encode, base64_decode, url_encode, url_decode, html_escape, html_unescape"`
			}) (struct {
				Result string `json:"result"`
			}, error) {
				result, err := encodeText(input.Text, input.Operation)
				if err != nil {
					return struct {
						Result string `json:"result"`
					}{}, err
				}
				return struct {
					Result string `json:"result"`
				}{Result: result}, nil
			},
		),

		// 哈希计算
		tool.NewFunc(
			"text_hash",
			"计算文本哈希值 (MD5, SHA256)",
			func(ctx context.Context, input struct {
				Text      string `json:"text" description:"要计算哈希的文本"`
				Algorithm string `json:"algorithm" description:"哈希算法: md5, sha256"`
			}) (struct {
				Hash string `json:"hash"`
			}, error) {
				var hashValue string
				switch strings.ToLower(input.Algorithm) {
				case "md5":
					// 仅计算调用者明确指定的内容摘要，不得用于凭据、认证或签名。
					digest := md5.Sum([]byte(input.Text)) // #nosec G401 -- 公开工具明确请求非安全 MD5 内容摘要。
					hashValue = hex.EncodeToString(digest[:])
				case "sha256":
					// 复用 toolkit 的等价实现
					hashValue = hash.SHA256(input.Text)
				default:
					return struct {
						Hash string `json:"hash"`
					}{}, fmt.Errorf("unknown algorithm: %s", input.Algorithm)
				}
				return struct {
					Hash string `json:"hash"`
				}{Hash: hashValue}, nil
			},
		),

		// 相似度计算
		tool.NewFunc(
			"text_similarity",
			"计算两个文本的相似度",
			func(ctx context.Context, input struct {
				Text1  string `json:"text1" description:"第一个文本"`
				Text2  string `json:"text2" description:"第二个文本"`
				Method string `json:"method" description:"计算方法: levenshtein, jaccard, cosine"`
			}) (struct {
				Similarity float64 `json:"similarity"`
				Distance   int     `json:"distance,omitempty"`
			}, error) {
				sim, dist := calculateSimilarity(input.Text1, input.Text2, input.Method)
				return struct {
					Similarity float64 `json:"similarity"`
					Distance   int     `json:"distance,omitempty"`
				}{
					Similarity: sim,
					Distance:   dist,
				}, nil
			},
		),

		// 文本截取
		tool.NewFunc(
			"text_truncate",
			"截取文本到指定长度",
			func(ctx context.Context, input struct {
				Text   string `json:"text" description:"源文本"`
				Length int    `json:"length" description:"目标长度 (字符数)"`
				Suffix string `json:"suffix,omitempty" description:"截断后缀 (默认 '...')"`
			}) (struct {
				Result    string `json:"result"`
				Truncated bool   `json:"truncated"`
			}, error) {
				suffix := input.Suffix
				if suffix == "" {
					suffix = "..."
				}

				if utf8.RuneCountInString(input.Text) <= input.Length {
					return struct {
						Result    string `json:"result"`
						Truncated bool   `json:"truncated"`
					}{Result: input.Text, Truncated: false}, nil
				}

				runes := []rune(input.Text)
				truncated := string(runes[:input.Length]) + suffix
				return struct {
					Result    string `json:"result"`
					Truncated bool   `json:"truncated"`
				}{Result: truncated, Truncated: true}, nil
			},
		),

		// 提取实体
		tool.NewFunc(
			"text_extract_entities",
			"提取文本中的常见实体 (邮箱、URL、电话、日期等)",
			func(ctx context.Context, input struct {
				Text string `json:"text" description:"源文本"`
				Type string `json:"type" description:"实体类型: email, url, phone, date, ip, hashtag, mention, all"`
			}) (struct {
				Entities []string `json:"entities"`
				Count    int      `json:"count"`
			}, error) {
				entities := extractEntities(input.Text, input.Type)
				return struct {
					Entities []string `json:"entities"`
					Count    int      `json:"count"`
				}{
					Entities: entities,
					Count:    len(entities),
				}, nil
			},
		),
	}
}

// TextAnalysis 文本分析结果
type TextAnalysis struct {
	CharCount        int            `json:"char_count"`
	CharCountNoSpace int            `json:"char_count_no_space"`
	WordCount        int            `json:"word_count"`
	SentenceCount    int            `json:"sentence_count"`
	ParagraphCount   int            `json:"paragraph_count"`
	LineCount        int            `json:"line_count"`
	AvgWordLength    float64        `json:"avg_word_length"`
	UniqueWords      int            `json:"unique_words"`
	TopWords         map[string]int `json:"top_words,omitempty"`
}

// analyzeText 分析文本
func analyzeText(text string) TextAnalysis {
	// 字符统计
	charCount := utf8.RuneCountInString(text)
	charCountNoSpace := utf8.RuneCountInString(strings.ReplaceAll(text, " ", ""))

	// 行数
	lines := strings.Split(text, "\n")
	lineCount := len(lines)

	// 段落数（以空行分隔）
	paragraphCount := 1
	prevEmpty := false
	for _, line := range lines {
		empty := strings.TrimSpace(line) == ""
		if empty && !prevEmpty {
			paragraphCount++
		}
		prevEmpty = empty
	}

	// 词统计
	words := strings.Fields(text)
	wordCount := len(words)

	// 唯一词和词频
	wordFreq := make(map[string]int)
	totalWordLen := 0
	for _, word := range words {
		word = strings.ToLower(strings.Trim(word, ".,!?;:\"'()[]{}"))
		if word != "" {
			wordFreq[word]++
			totalWordLen += utf8.RuneCountInString(word)
		}
	}
	uniqueWords := len(wordFreq)

	// 平均词长
	var avgWordLen float64
	if wordCount > 0 {
		avgWordLen = float64(totalWordLen) / float64(wordCount)
	}

	// 句子数（简单估算）
	sentenceCount := 0
	for _, r := range text {
		if r == '.' || r == '!' || r == '?' || r == '。' || r == '！' || r == '？' {
			sentenceCount++
		}
	}
	if sentenceCount == 0 && len(text) > 0 {
		sentenceCount = 1
	}

	return TextAnalysis{
		CharCount:        charCount,
		CharCountNoSpace: charCountNoSpace,
		WordCount:        wordCount,
		SentenceCount:    sentenceCount,
		ParagraphCount:   paragraphCount,
		LineCount:        lineCount,
		AvgWordLength:    avgWordLen,
		UniqueWords:      uniqueWords,
		TopWords:         getTopWords(wordFreq, 10),
	}
}

// getTopWords 获取词频最高的词
func getTopWords(freq map[string]int, n int) map[string]int {
	if len(freq) <= n {
		return freq
	}

	// 简单实现：返回前 n 个
	result := make(map[string]int)
	count := 0
	for word, cnt := range freq {
		if count >= n {
			break
		}
		result[word] = cnt
		count++
	}
	return result
}

// transformText 转换文本
func transformText(text, transform string) string {
	switch strings.ToLower(transform) {
	case "uppercase":
		return strings.ToUpper(text)
	case "lowercase":
		return strings.ToLower(text)
	case "capitalize":
		if len(text) == 0 {
			return text
		}
		runes := []rune(text)
		runes[0] = unicode.ToUpper(runes[0])
		return string(runes)
	case "title":
		return strings.Title(text)
	case "reverse":
		runes := []rune(text)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return string(runes)
	case "trim":
		return strings.TrimSpace(text)
	case "slug":
		// 转为 URL 友好的格式
		result := strings.ToLower(text)
		result = strings.ReplaceAll(result, " ", "-")
		result = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(result, "")
		result = regexp.MustCompile(`-+`).ReplaceAllString(result, "-")
		return strings.Trim(result, "-")
	default:
		return text
	}
}

// splitText 分割文本
func splitText(text, separator string, limit int) []string {
	var parts []string

	switch separator {
	case "newline":
		parts = strings.Split(text, "\n")
	case "paragraph":
		parts = regexp.MustCompile(`\n\s*\n`).Split(text, -1)
	case "sentence":
		parts = regexp.MustCompile(`[.!?。！？]+`).Split(text, -1)
	case "word":
		parts = strings.Fields(text)
	case "char":
		for _, r := range text {
			parts = append(parts, string(r))
		}
	default:
		parts = strings.Split(text, separator)
	}

	// 清理空项
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}

	// 应用限制
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}

	return result
}

// encodeText 编码/解码文本
func encodeText(text, operation string) (string, error) {
	switch operation {
	case "base64_encode":
		return base64.StdEncoding.EncodeToString([]byte(text)), nil
	case "base64_decode":
		decoded, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	case "url_encode":
		return strings.ReplaceAll(strings.ReplaceAll(text, " ", "%20"), "&", "%26"), nil
	case "url_decode":
		return strings.ReplaceAll(strings.ReplaceAll(text, "%20", " "), "%26", "&"), nil
	case "html_escape":
		text = strings.ReplaceAll(text, "&", "&amp;")
		text = strings.ReplaceAll(text, "<", "&lt;")
		text = strings.ReplaceAll(text, ">", "&gt;")
		text = strings.ReplaceAll(text, "\"", "&quot;")
		return text, nil
	case "html_unescape":
		text = strings.ReplaceAll(text, "&quot;", "\"")
		text = strings.ReplaceAll(text, "&gt;", ">")
		text = strings.ReplaceAll(text, "&lt;", "<")
		text = strings.ReplaceAll(text, "&amp;", "&")
		return text, nil
	default:
		return "", fmt.Errorf("unknown operation: %s", operation)
	}
}

// calculateSimilarity 计算相似度
func calculateSimilarity(text1, text2, method string) (float64, int) {
	switch method {
	case "levenshtein":
		dist := levenshteinDistance(text1, text2)
		maxLen := max(len(text1), len(text2))
		if maxLen == 0 {
			return 1.0, 0
		}
		return 1.0 - float64(dist)/float64(maxLen), dist
	case "jaccard":
		words1 := strings.Fields(strings.ToLower(text1))
		words2 := strings.Fields(strings.ToLower(text2))
		set1 := make(map[string]bool)
		for _, w := range words1 {
			set1[w] = true
		}
		set2 := make(map[string]bool)
		for _, w := range words2 {
			set2[w] = true
		}

		intersection := 0
		for w := range set1 {
			if set2[w] {
				intersection++
			}
		}

		union := len(set1) + len(set2) - intersection
		if union == 0 {
			return 1.0, 0
		}
		return float64(intersection) / float64(union), 0
	default:
		// 默认使用 Jaccard
		return calculateSimilarity(text1, text2, "jaccard")
	}
}

// levenshteinDistance 计算编辑距离
func levenshteinDistance(s1, s2 string) int {
	if len(s1) == 0 {
		return len(s2)
	}
	if len(s2) == 0 {
		return len(s1)
	}

	r1 := []rune(s1)
	r2 := []rune(s2)
	len1, len2 := len(r1), len(r2)

	// 使用两行滚动数组优化空间
	prev := make([]int, len2+1)
	curr := make([]int, len2+1)

	for j := 0; j <= len2; j++ {
		prev[j] = j
	}

	for i := 1; i <= len1; i++ {
		curr[0] = i
		for j := 1; j <= len2; j++ {
			cost := 0
			if r1[i-1] != r2[j-1] {
				cost = 1
			}
			curr[j] = min(
				prev[j]+1,      // 删除
				curr[j-1]+1,    // 插入
				prev[j-1]+cost, // 替换
			)
		}
		prev, curr = curr, prev
	}

	return prev[len2]
}

// extractEntities 提取实体
func extractEntities(text, entityType string) []string {
	patterns := map[string]string{
		"email":   `[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`,
		"url":     `https?://[^\s<>"]+|www\.[^\s<>"]+`,
		"phone":   `\+?[0-9]{1,3}[-.\s]?[\(]?[0-9]{1,3}[\)]?[-.\s]?[0-9]{3,4}[-.\s]?[0-9]{4}`,
		"ip":      `\b(?:\d{1,3}\.){3}\d{1,3}\b`,
		"hashtag": `#[a-zA-Z0-9_]+`,
		"mention": `@[a-zA-Z0-9_]+`,
		"date":    `\d{4}[-/]\d{1,2}[-/]\d{1,2}|\d{1,2}[-/]\d{1,2}[-/]\d{4}`,
	}

	if entityType == "all" {
		var all []string
		for _, pattern := range patterns {
			re := regexp.MustCompile(pattern)
			matches := re.FindAllString(text, -1)
			all = append(all, matches...)
		}
		return all
	}

	pattern, ok := patterns[entityType]
	if !ok {
		return nil
	}

	re := regexp.MustCompile(pattern)
	return re.FindAllString(text, -1)
}

func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
