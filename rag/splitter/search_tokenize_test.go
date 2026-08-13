package splitter

import (
	"testing"
)

func TestSearchTokenize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string // 结果中必须包含的 token
		excludes []string // 结果中不应包含的 token
	}{
		{
			name:     "纯中文 bigram",
			input:    "杭帮菜的正确吃法",
			contains: []string{"杭帮", "帮菜", "菜的", "的正", "正确", "确吃", "吃法", "杭帮菜的正确吃法"},
		},
		{
			name:     "短中文词（2字）保留原样",
			input:    "西湖",
			contains: []string{"西湖"},
		},
		{
			name:     "英文词保留原样",
			input:    "hello world",
			contains: []string{"hello", "world"},
		},
		{
			name:     "中英混合",
			input:    "hello 你好世界",
			contains: []string{"hello", "你好", "好世", "世界", "你好世界"},
		},
		{
			name:     "标点分隔",
			input:    "杭帮菜，西湖醋鱼",
			contains: []string{"杭帮", "帮菜", "西湖", "湖醋", "醋鱼"},
		},
		{
			name:     "单字过短被过滤",
			input:    "a b c",
			contains: []string{},
		},
		{
			name:     "空字符串",
			input:    "",
			contains: []string{},
		},
		{
			name:  "去重",
			input: "杭帮菜 杭帮菜",
			// "杭帮菜" bigram 出现两次，但去重后只保留一份
			contains: []string{"杭帮", "帮菜", "杭帮菜"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SearchTokenize(tt.input)
			resultSet := make(map[string]bool)
			for _, r := range result {
				resultSet[r] = true
			}

			for _, want := range tt.contains {
				if !resultSet[want] {
					t.Errorf("SearchTokenize(%q) should contain %q, got %v", tt.input, want, result)
				}
			}

			for _, exclude := range tt.excludes {
				if resultSet[exclude] {
					t.Errorf("SearchTokenize(%q) should NOT contain %q, got %v", tt.input, exclude, result)
				}
			}

			// 验证无重复
			seen := make(map[string]bool)
			for _, r := range result {
				if seen[r] {
					t.Errorf("SearchTokenize(%q) has duplicate token %q", tt.input, r)
				}
				seen[r] = true
			}
		})
	}
}

func TestIsCJK(t *testing.T) {
	tests := []struct {
		r    rune
		want bool
	}{
		{'杭', true},
		{'a', false},
		{'1', false},
		{'の', true}, // Hiragana
		{'カ', true}, // Katakana
		{' ', false},
		{'，', false},
	}

	for _, tt := range tests {
		if got := IsCJK(tt.r); got != tt.want {
			t.Errorf("IsCJK(%q) = %v, want %v", tt.r, got, tt.want)
		}
	}
}
