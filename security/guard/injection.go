package guard

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// PromptInjectionGuard Prompt 注入检测守卫
type PromptInjectionGuard struct {
	config   *GuardConfig
	patterns []*injectionPattern
	enabled  bool
}

// injectionPattern 注入模式
type injectionPattern struct {
	name     string
	pattern  *regexp.Regexp
	severity string
	score    float64
}

// NewPromptInjectionGuard 创建 Prompt 注入守卫
func NewPromptInjectionGuard(opts ...PromptInjectionOption) *PromptInjectionGuard {
	g := &PromptInjectionGuard{
		config:  DefaultConfig(),
		enabled: true,
	}

	// 默认模式
	g.patterns = defaultInjectionPatterns()

	for _, opt := range opts {
		opt(g)
	}

	return g
}

// PromptInjectionOption 配置选项
type PromptInjectionOption func(*PromptInjectionGuard)

// WithInjectionConfig 设置配置
func WithInjectionConfig(cfg *GuardConfig) PromptInjectionOption {
	return func(g *PromptInjectionGuard) {
		g.config = cfg
	}
}

// WithCustomPatterns 添加自定义模式
func WithCustomPatterns(patterns map[string]string) PromptInjectionOption {
	return func(g *PromptInjectionGuard) {
		for name, pattern := range patterns {
			if re, err := regexp.Compile(pattern); err == nil {
				g.patterns = append(g.patterns, &injectionPattern{
					name:     name,
					pattern:  re,
					severity: "high",
					score:    0.8,
				})
			}
		}
	}
}

// Name 返回名称
func (g *PromptInjectionGuard) Name() string {
	return "prompt_injection"
}

// Enabled 返回是否启用
func (g *PromptInjectionGuard) Enabled() bool {
	return g.enabled && g.config.Enabled
}

// Check 执行检查
func (g *PromptInjectionGuard) Check(ctx context.Context, input string) (*CheckResult, error) {
	if !g.Enabled() {
		return &CheckResult{Passed: true}, nil
	}

	var findings []Finding
	var maxScore float64 = 0

	// 直接在原始 input 上进行匹配
	// 所有正则模式都已使用 (?i) 标志实现不区分大小写匹配，
	// 避免 ToLower 后索引与原始字符串字节偏移不一致的 Unicode 安全问题
	for _, p := range g.patterns {
		matches := p.pattern.FindAllStringIndex(input, -1)
		for _, match := range matches {
			findings = append(findings, Finding{
				Type:     p.name,
				Text:     input[match[0]:match[1]],
				Position: Position{Start: match[0], End: match[1]},
				Severity: p.severity,
			})
			if p.score > maxScore {
				maxScore = p.score
			}
		}
	}

	// 额外检查：启发式规则（使用小写文本进行关键词检查，此处不需要索引）
	lowerInput := strings.ToLower(input)
	heuristicScore := g.checkHeuristics(lowerInput)
	if heuristicScore > maxScore {
		maxScore = heuristicScore
	}

	passed := maxScore < g.config.Threshold

	result := &CheckResult{
		Passed:   passed,
		Score:    maxScore,
		Category: "prompt_injection",
		Findings: findings,
	}

	if !passed {
		result.Reason = "Potential prompt injection detected"
	}

	return result, nil
}

// checkHeuristics 启发式检查
func (g *PromptInjectionGuard) checkHeuristics(input string) float64 {
	var score float64 = 0

	// 检查可疑关键词密度
	suspiciousKeywords := []string{
		"ignore", "forget", "disregard", "override",
		"system prompt", "new instructions", "act as",
		"pretend", "roleplay", "jailbreak",
		"you are now", "you must", "bypass",
	}

	keywordCount := 0
	for _, kw := range suspiciousKeywords {
		if strings.Contains(input, kw) {
			keywordCount++
		}
	}

	if keywordCount > 0 {
		score = float64(keywordCount) * 0.15
		if score > 0.9 {
			score = 0.9
		}
	}

	// 检查特殊字符模式
	if strings.Contains(input, "```") && strings.Contains(input, "system") {
		score += 0.3
	}

	// 检查换行符滥用
	newlineCount := strings.Count(input, "\n")
	if newlineCount > 10 && len(input) < 500 {
		score += 0.2
	}

	if score > 1.0 {
		score = 1.0
	}

	return score
}

// IsInputGuard 标记为输入守卫
func (g *PromptInjectionGuard) IsInputGuard() {}

// 确保实现了接口
var _ InputGuard = (*PromptInjectionGuard)(nil)

// defaultInjectionPatterns 默认注入模式
func defaultInjectionPatterns() []*injectionPattern {
	patterns := []struct {
		name     string
		pattern  string
		severity string
		score    float64
	}{
		// 直接指令覆盖
		{"direct_override", `(?i)(ignore|forget|disregard).{0,20}(previous|above|prior|all).{0,20}(instructions?|rules?|prompts?)`, "critical", 0.95},
		{"new_instructions", `(?i)(new|different|updated).{0,20}(instructions?|rules?|prompts?).{0,20}(are|is|:)`, "high", 0.85},

		// 角色扮演注入
		{"role_hijack", `(?i)(you are now|act as|pretend to be|roleplay as).{0,50}(assistant|ai|bot|system)`, "high", 0.85},
		{"identity_override", `(?i)(forget|ignore).{0,20}(you are|your role|your identity)`, "critical", 0.9},

		// 系统提示词提取
		{"prompt_leak", `(?i)(show|reveal|display|print|output).{0,30}(system|original|initial).{0,20}(prompt|instructions?)`, "high", 0.85},
		{"repeat_prompt", `(?i)(repeat|echo|say).{0,20}(everything|all).{0,20}(above|before|previous)`, "medium", 0.7},

		// 分隔符注入
		{"delimiter_injection", `(?i)(\[system\]|\[assistant\]|\[user\]|<\|im_start\|>|<\|im_end\|>)`, "critical", 0.95},
		{"markdown_injection", `(?i)(###|---).{0,10}(system|instructions?|new role)`, "high", 0.8},

		// DAN 类攻击
		{"jailbreak_attempt", `(?i)(jailbreak|dan|do anything now|developer mode|unleashed)`, "critical", 0.95},
		{"bypass_attempt", `(?i)(bypass|circumvent|workaround).{0,20}(safety|filter|restriction|rule)`, "high", 0.85},

		// 编码绕过
		{"encoding_bypass", `(?i)(base64|hex|rot13|unicode).{0,20}(decode|encode|convert)`, "medium", 0.7},

		// 虚假输出
		{"fake_output", `(?i)(output|response|answer).{0,10}(:|=).{0,20}(yes|allowed|permitted|successful)`, "high", 0.8},
	}

	result := make([]*injectionPattern, 0, len(patterns))
	for _, p := range patterns {
		if re, err := regexp.Compile(p.pattern); err == nil {
			result = append(result, &injectionPattern{
				name:     p.name,
				pattern:  re,
				severity: p.severity,
				score:    p.score,
			})
		}
	}

	return result
}

// PIIGuard PII 检测守卫
type PIIGuard struct {
	config   *GuardConfig
	patterns []*piiPattern
	enabled  bool
}

// piiPattern PII 模式
type piiPattern struct {
	name    string
	pattern *regexp.Regexp
	redact  func(string) string
	// valid 可选的有效性校验函数：仅当返回 true 时该匹配才被视为真正的 PII。
	// 用于卡号类模式做 Luhn 校验，确保 Check 与 Redact 共用同一套判定逻辑，
	// 避免"被判为 PII 却原样泄漏"的安全漏洞（回归: B11）。
	// 为 nil 时表示无需额外校验，匹配即命中。
	valid func(string) bool
}

// matchedRegion 单次正则命中的区间及其归属模式，用于跨模式重叠消解。
type matchedRegion struct {
	start   int
	end     int
	pattern *piiPattern
}

// collectPIIRegions 在 input 上运行所有 PII 模式，收集通过有效性校验的命中区间，
// 并消解重叠：同一字符区间只允许一个模式生效。
//
// 重叠消解规则（回归: B9/B10）：
//   - 优先保留更长的匹配（更长通常意味着更精确，如 id_card_cn 比内部的 phone_cn 长）；
//   - 长度相同时，保留 patterns 切片中靠前的模式（声明顺序即优先级）。
//
// 这样可避免 phone_cn/ssn_us 把身份证号切碎，也避免多个模式对同一区间重复替换。
// 返回的区间按 start 升序排列且互不重叠，可直接用于一次性脱敏或检测。
func (g *PIIGuard) collectPIIRegions(input string) []matchedRegion {
	var regions []matchedRegion

	// 第一步：收集所有通过有效性校验的命中。
	for i := range g.patterns {
		p := g.patterns[i]
		matches := p.pattern.FindAllStringIndex(input, -1)
		for _, m := range matches {
			// 卡号等模式可能携带 Luhn 校验，校验失败则不视为 PII，
			// 保证 Check 与 Redact 判定一致。
			if p.valid != nil && !p.valid(input[m[0]:m[1]]) {
				continue
			}
			regions = append(regions, matchedRegion{start: m[0], end: m[1], pattern: p})
		}
	}

	// 第二步：消解重叠。按"起点升序、长度降序、声明顺序靠前"排序，
	// 然后贪心选择互不重叠的区间。
	patternRank := make(map[*piiPattern]int, len(g.patterns))
	for i := range g.patterns {
		patternRank[g.patterns[i]] = i
	}
	sort.SliceStable(regions, func(a, b int) bool {
		ra, rb := regions[a], regions[b]
		if ra.start != rb.start {
			return ra.start < rb.start
		}
		la, lb := ra.end-ra.start, rb.end-rb.start
		if la != lb {
			return la > lb // 更长的优先
		}
		return patternRank[ra.pattern] < patternRank[rb.pattern] // 声明靠前的优先
	})

	var resolved []matchedRegion
	lastEnd := -1
	for _, r := range regions {
		if r.start < lastEnd {
			// 与已选区间重叠：但排序保证此处被跳过的是更短/更低优先级的匹配。
			// 仍需处理"起点不同但区间交叉"的情况，统一以 lastEnd 为界跳过。
			continue
		}
		resolved = append(resolved, r)
		lastEnd = r.end
	}
	return resolved
}

// NewPIIGuard 创建 PII 守卫
func NewPIIGuard(opts ...PIIOption) *PIIGuard {
	g := &PIIGuard{
		config:  DefaultConfig(),
		enabled: true,
	}

	g.patterns = defaultPIIPatterns()

	for _, opt := range opts {
		opt(g)
	}

	return g
}

// PIIOption 配置选项
type PIIOption func(*PIIGuard)

// WithPIIConfig 设置配置
func WithPIIConfig(cfg *GuardConfig) PIIOption {
	return func(g *PIIGuard) {
		g.config = cfg
	}
}

// Name 返回名称
func (g *PIIGuard) Name() string {
	return "pii_detection"
}

// Enabled 返回是否启用
func (g *PIIGuard) Enabled() bool {
	return g.enabled && g.config.Enabled
}

// Check 执行检查
func (g *PIIGuard) Check(ctx context.Context, input string) (*CheckResult, error) {
	if !g.Enabled() {
		return &CheckResult{Passed: true}, nil
	}

	var findings []Finding
	var maxScore float64 = 0

	// 使用与 Redact 相同的区间收集逻辑：
	//   - 卡号类模式经过 Luhn 校验，未通过则不计入 PII（回归: B11，保证检测与脱敏一致）；
	//   - 重叠区间已消解，避免同一片敏感串被多个模式重复上报（回归: B9/B10）。
	for _, region := range g.collectPIIRegions(input) {
		findings = append(findings, Finding{
			Type:     region.pattern.name,
			Text:     "[REDACTED]", // 不输出实际 PII
			Position: Position{Start: region.start, End: region.end},
			Severity: "high",
		})
		maxScore = 0.9
	}

	passed := maxScore < g.config.Threshold

	result := &CheckResult{
		Passed:   passed,
		Score:    maxScore,
		Category: "pii",
		Findings: findings,
	}

	if !passed {
		result.Reason = "PII detected in input"
	}

	return result, nil
}

// Redact 脱敏处理
//
// 采用"先收集互斥区间、再一次性替换"的方式，替代过去对每个模式独立做
// ReplaceAllStringFunc 的多遍替换。多遍替换会因模式相互遮蔽（如 phone_cn
// 先把身份证中间切碎）导致输出错乱（回归: B9/B10）。
// 卡号类模式的 Luhn 校验在区间收集阶段完成，与 Check 共用同一判定，
// 确保"被判为 PII 的串脱敏后绝不原样残留"（回归: B11）。
func (g *PIIGuard) Redact(input string) string {
	return g.redactRegions(input, g.collectPIIRegions(input))
}

// redactRegions 按给定的互不重叠区间（须按 start 升序）对 input 执行一次性脱敏。
func (g *PIIGuard) redactRegions(input string, regions []matchedRegion) string {
	if len(regions) == 0 {
		return input
	}
	var b strings.Builder
	last := 0
	for _, r := range regions {
		if r.start < last {
			// 防御性跳过：理论上区间已互斥，此处仅防止越界。
			continue
		}
		b.WriteString(input[last:r.start])
		b.WriteString(r.pattern.redact(input[r.start:r.end]))
		last = r.end
	}
	b.WriteString(input[last:])
	return b.String()
}

// IsInputGuard 标记为输入守卫
func (g *PIIGuard) IsInputGuard() {}

var _ InputGuard = (*PIIGuard)(nil)

// defaultPIIPatterns 默认 PII 模式
func defaultPIIPatterns() []*piiPattern {
	// 注意：模式声明顺序即重叠消解的优先级（长度相同时靠前者胜）。
	// id_card_cn 必须排在 phone_cn / ssn_us 之前，避免身份证号被这两个
	// 较短模式遮蔽切碎（回归: B9）。
	return []*piiPattern{
		// 邮箱
		{
			name:    "email",
			pattern: regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`),
			redact:  maskEmail,
		},
		// 身份证号（中国）—— 18 位，须先于 phone_cn/ssn_us 命中以免被切碎
		{
			name:    "id_card_cn",
			pattern: regexp.MustCompile(`[1-9]\d{5}(18|19|20)\d{2}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])\d{3}[\dXx]`),
			redact:  maskIDCard,
		},
		// 银行卡号（16-19 位纯数字，使用 Luhn 校验减少误报）
		// 须先于 credit_card 命中，使纯数字卡号走 bank_card 脱敏规则（回归: B10）。
		{
			name:    "bank_card",
			pattern: regexp.MustCompile(`\b\d{16,19}\b`),
			redact:  maskBankCard,
			valid:   func(s string) bool { return validateLuhn(extractDigits(s)) },
		},
		// 信用卡号（必须含至少一个分隔符的分组格式，使用 Luhn 校验）
		// 要求分隔符以与 bank_card 明确互斥：纯数字交由 bank_card 处理（回归: B10）。
		{
			name:    "credit_card",
			pattern: regexp.MustCompile(`\d{4}[\s-]\d{4}[\s-]\d{4}[\s-]\d{4}`),
			redact:  maskCreditCard,
			valid:   func(s string) bool { return validateLuhn(extractDigits(s)) },
		},
		// 手机号（中国）
		{
			name:    "phone_cn",
			pattern: regexp.MustCompile(`1[3-9]\d{9}`),
			redact:  maskPhone,
		},
		// 国际电话号码
		{
			name:    "phone_intl",
			pattern: regexp.MustCompile(`\+\d{1,3}[- ]?\d{6,14}`),
			redact:  maskPhone,
		},
		// IP 地址
		{
			name:    "ip_address",
			pattern: regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`),
			redact:  maskIPv4,
		},
		// 美国 SSN
		{
			name:    "ssn_us",
			pattern: regexp.MustCompile(`\b\d{3}[- ]?\d{2}[- ]?\d{4}\b`),
			redact:  func(s string) string { return "***-**-****" },
		},
		// 护照号（中国）
		{
			name:    "passport_cn",
			pattern: regexp.MustCompile(`[EeGgPp]\d{8}`),
			redact:  func(s string) string { return s[:1] + "********" },
		},
	}
}

// ============== 智能脱敏函数 ==============
// 保留部分信息以便识别，同时隐藏敏感部分

func maskEmail(s string) string {
	at := strings.Index(s, "@")
	if at <= 0 {
		return "***@***"
	}
	name := s[:at]
	domain := s[at+1:]
	if len(name) == 0 {
		return "***@" + domain
	}
	if len(name) <= 2 {
		return name[:1] + "***@" + domain
	}
	return name[:2] + "***@" + domain
}

func maskPhone(s string) string {
	clean := strings.ReplaceAll(strings.ReplaceAll(s, "-", ""), " ", "")
	if len(clean) < 7 {
		return "***"
	}
	return clean[:3] + "****" + clean[len(clean)-4:]
}

func maskIDCard(s string) string {
	if len(s) < 10 {
		return "***"
	}
	return s[:6] + "********" + s[len(s)-4:]
}

func maskCreditCard(s string) string {
	digits := extractDigits(s)
	if !validateLuhn(digits) {
		return s
	}
	if len(digits) < 4 {
		return "****"
	}
	return "****-****-****-" + digits[len(digits)-4:]
}

func maskBankCard(s string) string {
	if !validateLuhn(s) {
		return s
	}
	if len(s) < 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

func maskIPv4(s string) string {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return "*.*.*.*"
	}
	return parts[0] + ".*.*." + parts[3]
}

// extractDigits 从字符串中提取所有数字
func extractDigits(s string) string {
	var digits []byte
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			digits = append(digits, s[i])
		}
	}
	return string(digits)
}

// validateLuhn 使用 Luhn 算法验证卡号
// Luhn 算法（又称模 10 算法）用于验证银行卡号的有效性
// 算法步骤：
//  1. 从右向左，对奇数位数字直接相加
//  2. 从右向左，对偶数位数字乘以 2，如果结果大于 9 则减去 9
//  3. 将所有结果相加，如果总和能被 10 整除则有效
func validateLuhn(number string) bool {
	// 至少需要 13 位（最短的有效卡号）
	if len(number) < 13 || len(number) > 19 {
		return false
	}

	// 确保全是数字
	for i := 0; i < len(number); i++ {
		if number[i] < '0' || number[i] > '9' {
			return false
		}
	}

	var sum int
	alt := false
	for i := len(number) - 1; i >= 0; i-- {
		n := int(number[i] - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}

// ============== PII 便捷函数 ==============

// DetectPII 检测文本中的 PII
// 如果检测过程出错，返回空列表
func DetectPII(text string) []Finding {
	guard := NewPIIGuard()
	result, err := guard.Check(context.Background(), text)
	if err != nil {
		// 检测失败时返回空列表，而不是静默忽略错误
		// 调用方如需处理错误，应直接使用 NewPIIGuard().Check()
		return nil
	}
	return result.Findings
}

// DetectPIIWithError 检测文本中的 PII，返回错误信息
// 这是 DetectPII 的安全版本，提供完整的错误处理
func DetectPIIWithError(text string) ([]Finding, error) {
	guard := NewPIIGuard()
	result, err := guard.Check(context.Background(), text)
	if err != nil {
		return nil, err
	}
	return result.Findings, nil
}

// RedactPII 脱敏文本中的所有 PII
func RedactPII(text string) string {
	guard := NewPIIGuard()
	return guard.Redact(text)
}

// RedactPIISelective 选择性脱敏
// 只脱敏指定类型的 PII
func RedactPIISelective(text string, types ...string) string {
	guard := NewPIIGuard()
	typeSet := make(map[string]bool)
	for _, t := range types {
		typeSet[t] = true
	}

	// 复用统一的区间收集（含 Luhn 校验与重叠消解），再按类型过滤，
	// 保证选择性脱敏与全量脱敏使用同一套判定逻辑（回归: B9/B10/B11）。
	regions := guard.collectPIIRegions(text)
	if len(typeSet) != 0 {
		filtered := regions[:0]
		for _, r := range regions {
			if typeSet[r.pattern.name] {
				filtered = append(filtered, r)
			}
		}
		regions = filtered
	}
	return guard.redactRegions(text, regions)
}
