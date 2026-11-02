// Package main 演示代码审查 Agent 场景
//
// 本示例展示如何使用 Hexagon 构建一个智能代码审查系统：
//
//   - 多 Agent 协作: Reviewer + SecurityAuditor + StyleChecker 并行审查
//   - Barrier 节点: 等待所有审查完成后汇总
//   - Functional API: 使用函数式工作流定义审查流程
//   - 结构化输出: 输出标准化的审查报告
//
// 运行方式:
//
//	go run ./examples/code-review
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/hexagon-codes/hexagon/orchestration/graph"
)

// ReviewState 代码审查状态
type ReviewState struct {
	// Code 要审查的代码
	Code string `json:"code"`

	// Language 编程语言
	Language string `json:"language"`

	// LogicIssues 逻辑问题
	LogicIssues []Issue `json:"logic_issues,omitempty"`

	// SecurityIssues 安全问题
	SecurityIssues []Issue `json:"security_issues,omitempty"`

	// StyleIssues 代码风格问题
	StyleIssues []Issue `json:"style_issues,omitempty"`

	// Summary 最终摘要
	Summary string `json:"summary,omitempty"`

	// Score 综合评分 (0-100)
	Score int `json:"score,omitempty"`
}

// Clone 克隆状态
func (s ReviewState) Clone() (graph.State, error) {
	clone := s
	clone.LogicIssues = append([]Issue{}, s.LogicIssues...)
	clone.SecurityIssues = append([]Issue{}, s.SecurityIssues...)
	clone.StyleIssues = append([]Issue{}, s.StyleIssues...)
	return clone, nil
}

// Issue 问题
type Issue struct {
	Severity    string `json:"severity"`    // critical, warning, info
	Line        int    `json:"line"`        // 行号
	Description string `json:"description"` // 描述
	Suggestion  string `json:"suggestion"`  // 建议
}

func main() {
	ctx := context.Background()

	// 构建代码审查图（使用并行+屏障模式）
	g, err := graph.NewGraph[ReviewState]("code-review").
		// 并行执行三种审查
		AddNodeWithBuilder(graph.ParallelNodeWithMerger[ReviewState](
			"review",
			mergeReviews,
			logicReview,
			securityReview,
			styleReview,
		)).
		// 汇总报告
		AddNode("summarize", summarize).
		// 边
		AddEdge(graph.START, "review").
		AddEdge("review", "summarize").
		AddEdge("summarize", graph.END).
		Build()

	if err != nil {
		fmt.Printf("构建审查图失败: %v\n", err)
		return
	}

	// 测试代码
	code := `package main

import (
    "database/sql"
    "fmt"
    "net/http"
)

func handler(w http.ResponseWriter, r *http.Request) {
    userID := r.URL.Query().Get("id")

    db, _ := sql.Open("mysql", "root:password@/mydb")

    query := "SELECT * FROM users WHERE id = " + userID
    rows, _ := db.Query(query)
    defer rows.Close()

    for rows.Next() {
        var name string
        rows.Scan(&name)
        fmt.Fprintf(w, "User: %s", name)
    }
}

func main() {
    http.HandleFunc("/user", handler)
    http.ListenAndServe(":8080", nil)
}`

	fmt.Println("=== 智能代码审查 Agent 演示 ===")
	fmt.Println()
	fmt.Println("📝 待审查代码:")
	fmt.Println(strings.Repeat("-", 50))
	lines := strings.Split(code, "\n")
	for i, line := range lines {
		fmt.Printf("  %3d | %s\n", i+1, line)
	}
	fmt.Println(strings.Repeat("-", 50))

	state := ReviewState{
		Code:     code,
		Language: "go",
	}

	result, err := g.Run(ctx, state)
	if err != nil {
		fmt.Printf("审查失败: %v\n", err)
		return
	}

	// 输出报告
	fmt.Printf("\n%s\n", result.Summary)
	fmt.Println("=== 演示结束 ===")
}

// ============== 审查函数 ==============

// logicReview 逻辑审查
func logicReview(_ context.Context, state ReviewState) (ReviewState, error) {
	var issues []Issue

	lines := strings.Split(state.Code, "\n")
	for i, line := range lines {
		// 检查忽略的错误
		if strings.Contains(line, ", _ :=") || strings.Contains(line, ", _:=") {
			issues = append(issues, Issue{
				Severity:    "warning",
				Line:        i + 1,
				Description: "忽略了错误返回值",
				Suggestion:  "应当检查并处理错误",
			})
		}

		// 检查资源泄漏
		if strings.Contains(line, "sql.Open") && !containsInRange(lines, i, i+3, "defer") {
			issues = append(issues, Issue{
				Severity:    "critical",
				Line:        i + 1,
				Description: "数据库连接未在函数退出时关闭",
				Suggestion:  "在 sql.Open 后立即 defer db.Close()",
			})
		}
	}

	state.LogicIssues = issues
	return state, nil
}

// securityReview 安全审查
func securityReview(_ context.Context, state ReviewState) (ReviewState, error) {
	var issues []Issue

	lines := strings.Split(state.Code, "\n")
	for i, line := range lines {
		// SQL 注入检测
		if strings.Contains(line, "SELECT") && strings.Contains(line, "+") {
			issues = append(issues, Issue{
				Severity:    "critical",
				Line:        i + 1,
				Description: "SQL 注入风险：使用字符串拼接构造 SQL 查询",
				Suggestion:  "使用参数化查询: db.Query(\"SELECT * FROM users WHERE id = ?\", userID)",
			})
		}

		// 硬编码密码
		if strings.Contains(line, "password") && strings.Contains(line, ":") && strings.Contains(line, "@") {
			issues = append(issues, Issue{
				Severity:    "critical",
				Line:        i + 1,
				Description: "数据库密码硬编码在源码中",
				Suggestion:  "使用环境变量或配置文件管理敏感信息",
			})
		}

		// 未设置超时
		if strings.Contains(line, "ListenAndServe") && !strings.Contains(state.Code, "ReadTimeout") {
			issues = append(issues, Issue{
				Severity:    "warning",
				Line:        i + 1,
				Description: "HTTP 服务器未设置超时",
				Suggestion:  "使用 http.Server 结构体配置 ReadTimeout/WriteTimeout",
			})
		}
	}

	state.SecurityIssues = issues
	return state, nil
}

// styleReview 代码风格审查
func styleReview(_ context.Context, state ReviewState) (ReviewState, error) {
	var issues []Issue

	lines := strings.Split(state.Code, "\n")
	for i, line := range lines {
		// 检查行长度
		if len(line) > 120 {
			issues = append(issues, Issue{
				Severity:    "info",
				Line:        i + 1,
				Description: fmt.Sprintf("行过长 (%d 字符)", len(line)),
				Suggestion:  "建议每行不超过 120 字符",
			})
		}

		// 检查函数注释
		if strings.HasPrefix(strings.TrimSpace(line), "func ") && i > 0 {
			prevLine := strings.TrimSpace(lines[i-1])
			if !strings.HasPrefix(prevLine, "//") {
				funcName := extractFuncName(line)
				issues = append(issues, Issue{
					Severity:    "info",
					Line:        i + 1,
					Description: fmt.Sprintf("函数 %s 缺少注释", funcName),
					Suggestion:  "Go 规范要求导出函数必须有文档注释",
				})
			}
		}
	}

	state.StyleIssues = issues
	return state, nil
}

// mergeReviews 合并所有审查结果
func mergeReviews(original ReviewState, results []ReviewState) ReviewState {
	merged := original
	for _, r := range results {
		merged.LogicIssues = append(merged.LogicIssues, r.LogicIssues...)
		merged.SecurityIssues = append(merged.SecurityIssues, r.SecurityIssues...)
		merged.StyleIssues = append(merged.StyleIssues, r.StyleIssues...)
	}
	return merged
}

// summarize 生成汇总报告
func summarize(_ context.Context, state ReviewState) (ReviewState, error) {
	var report strings.Builder

	totalIssues := len(state.LogicIssues) + len(state.SecurityIssues) + len(state.StyleIssues)
	criticalCount := 0
	warningCount := 0
	infoCount := 0

	allIssues := append(append(state.LogicIssues, state.SecurityIssues...), state.StyleIssues...)
	for _, issue := range allIssues {
		switch issue.Severity {
		case "critical":
			criticalCount++
		case "warning":
			warningCount++
		case "info":
			infoCount++
		}
	}

	// 评分
	score := 100
	score -= criticalCount * 20
	score -= warningCount * 10
	score -= infoCount * 2
	if score < 0 {
		score = 0
	}
	state.Score = score

	report.WriteString("📋 代码审查报告\n")
	report.WriteString(strings.Repeat("═", 50) + "\n\n")
	report.WriteString(fmt.Sprintf("综合评分: %d/100\n", score))
	report.WriteString(fmt.Sprintf("问题总数: %d (🔴 严重: %d  🟡 警告: %d  🔵 建议: %d)\n\n",
		totalIssues, criticalCount, warningCount, infoCount))

	if len(state.SecurityIssues) > 0 {
		report.WriteString("🔒 安全问题:\n")
		for _, issue := range state.SecurityIssues {
			report.WriteString(fmt.Sprintf("  %s [行 %d] %s\n    💡 %s\n",
				severityIcon(issue.Severity), issue.Line, issue.Description, issue.Suggestion))
		}
		report.WriteString("\n")
	}

	if len(state.LogicIssues) > 0 {
		report.WriteString("🧠 逻辑问题:\n")
		for _, issue := range state.LogicIssues {
			report.WriteString(fmt.Sprintf("  %s [行 %d] %s\n    💡 %s\n",
				severityIcon(issue.Severity), issue.Line, issue.Description, issue.Suggestion))
		}
		report.WriteString("\n")
	}

	if len(state.StyleIssues) > 0 {
		report.WriteString("🎨 代码风格:\n")
		for _, issue := range state.StyleIssues {
			report.WriteString(fmt.Sprintf("  %s [行 %d] %s\n    💡 %s\n",
				severityIcon(issue.Severity), issue.Line, issue.Description, issue.Suggestion))
		}
	}

	state.Summary = report.String()
	return state, nil
}

// ============== 辅助函数 ==============

func severityIcon(severity string) string {
	switch severity {
	case "critical":
		return "🔴"
	case "warning":
		return "🟡"
	case "info":
		return "🔵"
	default:
		return "⚪"
	}
}

func containsInRange(lines []string, start, end int, substr string) bool {
	if end > len(lines) {
		end = len(lines)
	}
	for i := start; i < end; i++ {
		if strings.Contains(lines[i], substr) {
			return true
		}
	}
	return false
}

func extractFuncName(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "func ")
	if idx := strings.Index(line, "("); idx > 0 {
		return line[:idx]
	}
	return line
}
