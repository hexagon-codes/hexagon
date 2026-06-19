package otel

// GenAI 语义约定属性键（对齐 OpenTelemetry GenAI semantic conventions, C1）。
//
// 这些是稳定的属性名常量，供 tracer/exporter 在 Span / metrics 上打标，使 hexagon 的
// LLM 调用可观测数据与 OTel 生态（Langfuse / Grafana / Tempo 等）的 GenAI 约定对齐。
const (
	AttrGenAISystem            = "gen_ai.system"              // Provider 系统名，如 "openai"/"anthropic"（低基数）
	AttrGenAIOperationName     = "gen_ai.operation.name"      // 操作名，如 "chat"/"text_completion"（低基数）
	AttrGenAIRequestModel      = "gen_ai.request.model"       // 请求模型（低基数）
	AttrGenAIResponseModel     = "gen_ai.response.model"      // 响应模型（高基数：可能含版本后缀）
	AttrGenAIRequestMaxTokens  = "gen_ai.request.max_tokens"  // 请求 max_tokens（高基数）
	AttrGenAIUsageInputTokens  = "gen_ai.usage.input_tokens"  // 输入 token 数（高基数，仅进 traces）
	AttrGenAIUsageOutputTokens = "gen_ai.usage.output_tokens" // 输出 token 数（高基数，仅进 traces）
	AttrGenAIUsageTotalTokens  = "gen_ai.usage.total_tokens"  // 总 token 数（高基数，仅进 traces）
)

// OperationChat 是聊天补全操作名（gen_ai.operation.name 的常见取值）。
const OperationChat = "chat"

// GenAIAttributes 是按基数分流后的 gen_ai.* 属性集合（C1：low/high cardinality 分流）。
//
//   - Low：低基数属性（system/operation/request.model），可安全作为 **metrics 标签**，
//     不会导致时间序列爆炸。
//   - High：高基数属性（token 计数、response.model、max_tokens 等），**只进 traces**
//     （进 metrics 标签会爆炸）。
//
// 这种分流是 OTel GenAI 可观测的核心纪律：模型/系统等枚举进指标标签，token 等数值
// 进 span 属性（或作为 metric 的 value 而非 label）。
type GenAIAttributes struct {
	Low  map[string]any
	High map[string]any
}

// GenAICall 是构造 GenAIAttributes 的输入（产品中立的原语，避免 observe 依赖 runtime）。
type GenAICall struct {
	System        string // Provider 系统名
	Operation     string // 操作名；空时默认 OperationChat
	RequestModel  string
	ResponseModel string
	MaxTokens     int
	InputTokens   int
	OutputTokens  int
	TotalTokens   int
}

// NewGenAIAttributes 把一次 LLM 调用映射为按基数分流的 gen_ai.* 属性。
func NewGenAIAttributes(c GenAICall) GenAIAttributes {
	op := c.Operation
	if op == "" {
		op = OperationChat
	}
	low := map[string]any{
		AttrGenAIOperationName: op,
	}
	if c.System != "" {
		low[AttrGenAISystem] = c.System
	}
	if c.RequestModel != "" {
		low[AttrGenAIRequestModel] = c.RequestModel
	}

	high := map[string]any{}
	if c.ResponseModel != "" {
		high[AttrGenAIResponseModel] = c.ResponseModel
	}
	if c.MaxTokens > 0 {
		high[AttrGenAIRequestMaxTokens] = c.MaxTokens
	}
	if c.InputTokens > 0 {
		high[AttrGenAIUsageInputTokens] = c.InputTokens
	}
	if c.OutputTokens > 0 {
		high[AttrGenAIUsageOutputTokens] = c.OutputTokens
	}
	if c.TotalTokens > 0 {
		high[AttrGenAIUsageTotalTokens] = c.TotalTokens
	}
	return GenAIAttributes{Low: low, High: high}
}

// All 返回 Low+High 合并后的扁平属性（供不区分基数的 exporter / span 直接使用）。
func (a GenAIAttributes) All() map[string]any {
	out := make(map[string]any, len(a.Low)+len(a.High))
	for k, v := range a.Low {
		out[k] = v
	}
	for k, v := range a.High {
		out[k] = v
	}
	return out
}
