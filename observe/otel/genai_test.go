package otel

import "testing"

func TestNewGenAIAttributes_CardinalitySplit(t *testing.T) {
	a := NewGenAIAttributes(GenAICall{
		System:        "anthropic",
		RequestModel:  "claude-3-sonnet",
		ResponseModel: "claude-3-sonnet-20240229",
		MaxTokens:     1024,
		InputTokens:   100,
		OutputTokens:  50,
		TotalTokens:   150,
	})

	// 低基数（metrics 标签安全）：system / operation / request.model
	if a.Low[AttrGenAISystem] != "anthropic" {
		t.Errorf("system 应在 Low；got %v", a.Low[AttrGenAISystem])
	}
	if a.Low[AttrGenAIOperationName] != OperationChat {
		t.Errorf("operation 默认 chat 应在 Low；got %v", a.Low[AttrGenAIOperationName])
	}
	if a.Low[AttrGenAIRequestModel] != "claude-3-sonnet" {
		t.Errorf("request.model 应在 Low")
	}

	// 高基数（仅 traces）：token 计数 / response.model
	if a.High[AttrGenAIUsageInputTokens] != 100 || a.High[AttrGenAIUsageOutputTokens] != 50 || a.High[AttrGenAIUsageTotalTokens] != 150 {
		t.Errorf("token 计数应在 High；got %+v", a.High)
	}
	if a.High[AttrGenAIResponseModel] != "claude-3-sonnet-20240229" {
		t.Errorf("response.model 应在 High")
	}
	// token 不应出现在低基数（避免 metrics 标签爆炸）
	if _, ok := a.Low[AttrGenAIUsageInputTokens]; ok {
		t.Error("token 计数不应进 Low（metrics 标签爆炸）")
	}

	all := a.All()
	if all[AttrGenAISystem] != "anthropic" || all[AttrGenAIUsageInputTokens] != 100 {
		t.Error("All() 应合并 Low+High")
	}
}

func TestNewGenAIAttributes_OmitsZero(t *testing.T) {
	a := NewGenAIAttributes(GenAICall{Operation: "chat"})
	// 未提供的字段不应出现（omit zero）
	if _, ok := a.Low[AttrGenAISystem]; ok {
		t.Error("空 system 不应出现")
	}
	if len(a.High) != 0 {
		t.Errorf("无 token/响应模型时 High 应为空；got %+v", a.High)
	}
}
