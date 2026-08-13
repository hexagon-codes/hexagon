// Package agent 提供 AI Agent 核心功能
//
// 本文件实现 Agent 能力协商功能：
//
//   - 能力声明：Agent 声明自己的能力
//
//   - 能力查询：查询 Agent 的能力
//
//   - 能力匹配：匹配任务需求与 Agent 能力
//
//   - 能力协商：多 Agent 能力协商
//
//   - A2A Protocol: Agent 能力协商
//
//   - OpenAPI: 接口描述规范
//
//   - WSDL: 服务描述语言
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// ============== 能力定义 ==============

// CapabilitySpec 能力规格
type CapabilitySpec struct {
	// Name 能力名称
	Name string `json:"name"`

	// Version 能力版本
	Version string `json:"version,omitempty"`

	// Description 能力描述
	Description string `json:"description,omitempty"`

	// Category 能力分类
	Category string `json:"category,omitempty"`

	// InputSchema 输入 Schema
	InputSchema map[string]any `json:"input_schema,omitempty"`

	// OutputSchema 输出 Schema
	OutputSchema map[string]any `json:"output_schema,omitempty"`

	// Parameters 参数定义
	Parameters []ParameterSpec `json:"parameters,omitempty"`

	// Constraints 约束条件
	Constraints []Constraint `json:"constraints,omitempty"`

	// Dependencies 依赖的能力
	Dependencies []string `json:"dependencies,omitempty"`

	// Cost 能力成本估算
	Cost *CostEstimate `json:"cost,omitempty"`

	// SLA 服务级别协议
	SLA *SLASpec `json:"sla,omitempty"`
}

// ParameterSpec 参数规格
type ParameterSpec struct {
	// Name 参数名称
	Name string `json:"name"`

	// Type 参数类型
	Type string `json:"type"`

	// Required 是否必需
	Required bool `json:"required,omitempty"`

	// Description 参数描述
	Description string `json:"description,omitempty"`

	// Default 默认值
	Default any `json:"default,omitempty"`

	// Enum 枚举值
	Enum []any `json:"enum,omitempty"`

	// Min 最小值
	Min *float64 `json:"min,omitempty"`

	// Max 最大值
	Max *float64 `json:"max,omitempty"`
}

// Constraint 约束条件
type Constraint struct {
	// Type 约束类型
	Type ConstraintType `json:"type"`

	// Expression 约束表达式
	Expression string `json:"expression,omitempty"`

	// Value 约束值
	Value any `json:"value,omitempty"`

	// Description 约束描述
	Description string `json:"description,omitempty"`
}

// ConstraintType 约束类型
type ConstraintType string

const (
	// ConstraintMaxTokens 最大 token 数
	ConstraintMaxTokens ConstraintType = "max_tokens"

	// ConstraintMaxConcurrency 最大并发数
	ConstraintMaxConcurrency ConstraintType = "max_concurrency"

	// ConstraintRateLimit 速率限制
	ConstraintRateLimit ConstraintType = "rate_limit"

	// ConstraintTimeout 超时限制
	ConstraintTimeout ConstraintType = "timeout"

	// ConstraintLanguage 语言限制
	ConstraintLanguage ConstraintType = "language"

	// ConstraintRegion 地区限制
	ConstraintRegion ConstraintType = "region"

	// ConstraintCustom 自定义约束
	ConstraintCustom ConstraintType = "custom"
)

// CostEstimate 成本估算
type CostEstimate struct {
	// PerRequest 每请求成本
	PerRequest float64 `json:"per_request,omitempty"`

	// PerToken 每 token 成本
	PerToken float64 `json:"per_token,omitempty"`

	// PerMinute 每分钟成本
	PerMinute float64 `json:"per_minute,omitempty"`

	// Currency 货币单位
	Currency string `json:"currency,omitempty"`
}

// SLASpec 服务级别协议
type SLASpec struct {
	// Availability 可用性（百分比）
	Availability float64 `json:"availability,omitempty"`

	// ResponseTime 响应时间（毫秒）
	ResponseTime int `json:"response_time,omitempty"`

	// Throughput 吞吐量（请求/秒）
	Throughput int `json:"throughput,omitempty"`

	// ErrorRate 错误率（百分比）
	ErrorRate float64 `json:"error_rate,omitempty"`
}

// ============== 能力协商器 ==============

// Negotiator 能力协商器
type Negotiator struct {
	// registry Agent 注册表
	registry *Registry

	// matchers 能力匹配器
	matchers []CapabilityMatcher

	// scorer 能力评分器
	scorer CapabilityScorer

	mu sync.RWMutex
}

// NegotiatorOption 协商器选项
type NegotiatorOption func(*Negotiator)

// WithMatcher 添加匹配器
func WithMatcher(matcher CapabilityMatcher) NegotiatorOption {
	return func(n *Negotiator) {
		n.matchers = append(n.matchers, matcher)
	}
}

// WithCapabilityScorer 设置评分器
func WithCapabilityScorer(scorer CapabilityScorer) NegotiatorOption {
	return func(n *Negotiator) {
		n.scorer = scorer
	}
}

// NewNegotiator 创建协商器
func NewNegotiator(registry *Registry, opts ...NegotiatorOption) *Negotiator {
	n := &Negotiator{
		registry: registry,
		matchers: []CapabilityMatcher{&DefaultCapabilityMatcher{}},
		scorer:   &DefaultCapabilityScorer{},
	}

	for _, opt := range opts {
		opt(n)
	}

	return n
}

// ============== 能力匹配 ==============

// CapabilityMatcher 能力匹配器接口
type CapabilityMatcher interface {
	// Match 检查 Agent 能力是否满足需求
	Match(requirement *CapabilityRequirement, capability *CapabilitySpec) bool
}

// CapabilityRequirement 能力需求
type CapabilityRequirement struct {
	// Name 需要的能力名称
	Name string `json:"name"`

	// Version 需要的版本（可选）
	Version string `json:"version,omitempty"`

	// Parameters 参数要求
	Parameters map[string]any `json:"parameters,omitempty"`

	// Constraints 约束要求
	Constraints []Constraint `json:"constraints,omitempty"`

	// Priority 优先级（1-10）
	Priority int `json:"priority,omitempty"`

	// Optional 是否可选
	Optional bool `json:"optional,omitempty"`
}

// DefaultCapabilityMatcher 默认能力匹配器
type DefaultCapabilityMatcher struct{}

// Match 检查能力匹配
func (m *DefaultCapabilityMatcher) Match(req *CapabilityRequirement, cap *CapabilitySpec) bool {
	// 名称匹配
	if req.Name != cap.Name {
		return false
	}

	// 版本匹配（如果指定）
	if req.Version != "" && cap.Version != "" {
		if !m.versionMatch(req.Version, cap.Version) {
			return false
		}
	}

	// 约束匹配
	for _, reqConstraint := range req.Constraints {
		matched := false
		for _, capConstraint := range cap.Constraints {
			if m.constraintMatch(reqConstraint, capConstraint) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

// versionMatch 版本匹配（简化实现）
func (m *DefaultCapabilityMatcher) versionMatch(required, provided string) bool {
	// 简化实现：完全匹配或前缀匹配
	return required == provided || (len(required) < len(provided) && provided[:len(required)] == required)
}

// constraintMatch 约束匹配
func (m *DefaultCapabilityMatcher) constraintMatch(req, cap Constraint) bool {
	if req.Type != cap.Type {
		return false
	}

	// 简化实现：检查约束值是否满足
	switch req.Type {
	case ConstraintMaxTokens, ConstraintMaxConcurrency, ConstraintRateLimit:
		reqVal, reqOk := req.Value.(float64)
		capVal, capOk := cap.Value.(float64)
		if reqOk && capOk {
			return capVal >= reqVal
		}
	case ConstraintTimeout:
		reqVal, reqOk := req.Value.(float64)
		capVal, capOk := cap.Value.(float64)
		if reqOk && capOk {
			return capVal <= reqVal // 超时应该更小
		}
	}

	return true
}

// ============== 能力评分 ==============

// CapabilityScorer 能力评分器接口
type CapabilityScorer interface {
	// Score 计算能力匹配分数
	Score(requirement *CapabilityRequirement, capability *CapabilitySpec) float64
}

// DefaultCapabilityScorer 默认评分器
type DefaultCapabilityScorer struct {
	// Weights 各维度权重
	Weights ScoreWeights
}

// ScoreWeights 评分权重
type ScoreWeights struct {
	// VersionMatch 版本匹配权重
	VersionMatch float64

	// ConstraintMatch 约束匹配权重
	ConstraintMatch float64

	// CostWeight 成本权重
	CostWeight float64

	// SLAWeight SLA 权重
	SLAWeight float64
}

// DefaultScoreWeights 默认权重
var DefaultScoreWeights = ScoreWeights{
	VersionMatch:    0.2,
	ConstraintMatch: 0.3,
	CostWeight:      0.2,
	SLAWeight:       0.3,
}

// Score 计算分数
func (s *DefaultCapabilityScorer) Score(req *CapabilityRequirement, cap *CapabilitySpec) float64 {
	weights := s.Weights
	if weights.VersionMatch == 0 {
		weights = DefaultScoreWeights
	}

	var score float64

	// 版本匹配分数
	if req.Version != "" && cap.Version != "" {
		if req.Version == cap.Version {
			score += weights.VersionMatch
		} else if len(req.Version) < len(cap.Version) {
			score += weights.VersionMatch * 0.5
		}
	} else {
		score += weights.VersionMatch
	}

	// 约束匹配分数
	if len(req.Constraints) > 0 {
		matchedConstraints := 0
		for _, reqConstraint := range req.Constraints {
			for _, capConstraint := range cap.Constraints {
				if reqConstraint.Type == capConstraint.Type {
					matchedConstraints++
					break
				}
			}
		}
		score += weights.ConstraintMatch * float64(matchedConstraints) / float64(len(req.Constraints))
	} else {
		score += weights.ConstraintMatch
	}

	// 成本分数（成本越低分数越高）
	if cap.Cost != nil {
		// 简化：假设成本在合理范围内
		score += weights.CostWeight * 0.8
	} else {
		score += weights.CostWeight
	}

	// SLA 分数
	if cap.SLA != nil {
		slaScore := 0.0
		if cap.SLA.Availability >= 99.9 {
			slaScore += 0.5
		}
		if cap.SLA.ResponseTime <= 100 {
			slaScore += 0.5
		}
		score += weights.SLAWeight * slaScore
	} else {
		score += weights.SLAWeight * 0.5
	}

	return score
}

// ============== 协商流程 ==============

// NegotiationRequest 协商请求
type NegotiationRequest struct {
	// Requirements 能力需求列表
	Requirements []*CapabilityRequirement `json:"requirements"`

	// Preferences 偏好设置
	Preferences *NegotiationPreferences `json:"preferences,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// NegotiationPreferences 协商偏好
type NegotiationPreferences struct {
	// PreferredAgents 偏好的 Agent ID
	PreferredAgents []string `json:"preferred_agents,omitempty"`

	// ExcludedAgents 排除的 Agent ID
	ExcludedAgents []string `json:"excluded_agents,omitempty"`

	// MaxCost 最大成本
	MaxCost *float64 `json:"max_cost,omitempty"`

	// MinAvailability 最低可用性
	MinAvailability *float64 `json:"min_availability,omitempty"`

	// MaxResponseTime 最大响应时间（毫秒）
	MaxResponseTime *int `json:"max_response_time,omitempty"`
}

// NegotiationResult 协商结果
type NegotiationResult struct {
	// Success 是否成功
	Success bool `json:"success"`

	// Assignments 能力分配
	Assignments []*CapabilityAssignment `json:"assignments"`

	// UnmetRequirements 未满足的需求
	UnmetRequirements []*CapabilityRequirement `json:"unmet_requirements,omitempty"`

	// Score 总分
	Score float64 `json:"score"`

	// Error 错误信息
	Error string `json:"error,omitempty"`
}

// CapabilityAssignment 能力分配
type CapabilityAssignment struct {
	// Requirement 需求
	Requirement *CapabilityRequirement `json:"requirement"`

	// Agent 分配的 Agent
	Agent *AgentInfo `json:"agent"`

	// Capability 匹配的能力
	Capability *CapabilitySpec `json:"capability"`

	// Score 匹配分数
	Score float64 `json:"score"`
}

// Negotiate 执行能力协商
func (n *Negotiator) Negotiate(ctx context.Context, req *NegotiationRequest) (*NegotiationResult, error) {
	if len(req.Requirements) == 0 {
		return nil, fmt.Errorf("no requirements specified")
	}

	result := &NegotiationResult{
		Assignments:       make([]*CapabilityAssignment, 0),
		UnmetRequirements: make([]*CapabilityRequirement, 0),
	}

	// 获取所有可用的 Agent
	agents := n.registry.Discover(WithStatus(StatusHealthy))

	// 过滤 Agent
	if req.Preferences != nil {
		agents = n.filterAgents(agents, req.Preferences)
	}

	// 为每个需求找到最佳匹配
	for _, requirement := range req.Requirements {
		assignment := n.findBestMatch(requirement, agents)
		if assignment != nil {
			result.Assignments = append(result.Assignments, assignment)
			result.Score += assignment.Score
		} else if !requirement.Optional {
			result.UnmetRequirements = append(result.UnmetRequirements, requirement)
		}
	}

	// 判断是否成功
	result.Success = len(result.UnmetRequirements) == 0

	// 计算平均分数
	if len(result.Assignments) > 0 {
		result.Score /= float64(len(result.Assignments))
	}

	return result, nil
}

// filterAgents 过滤 Agent
func (n *Negotiator) filterAgents(agents []*AgentInfo, prefs *NegotiationPreferences) []*AgentInfo {
	var filtered []*AgentInfo

	excludeSet := make(map[string]bool)
	for _, id := range prefs.ExcludedAgents {
		excludeSet[id] = true
	}

	preferSet := make(map[string]bool)
	for _, id := range prefs.PreferredAgents {
		preferSet[id] = true
	}

	// 先添加偏好的 Agent
	for _, agent := range agents {
		if excludeSet[agent.ID] {
			continue
		}
		if preferSet[agent.ID] {
			filtered = append(filtered, agent)
		}
	}

	// 再添加其他 Agent
	for _, agent := range agents {
		if excludeSet[agent.ID] || preferSet[agent.ID] {
			continue
		}
		filtered = append(filtered, agent)
	}

	return filtered
}

// findBestMatch 查找最佳匹配
func (n *Negotiator) findBestMatch(requirement *CapabilityRequirement, agents []*AgentInfo) *CapabilityAssignment {
	var bestAssignment *CapabilityAssignment
	var bestScore float64 = -1

	for _, agent := range agents {
		for _, capability := range agent.Capabilities {
			// 转换为 CapabilitySpec
			capSpec := &CapabilitySpec{
				Name:        capability.Name,
				Version:     capability.Version,
				Description: capability.Description,
			}

			// 检查是否匹配
			matched := false
			for _, matcher := range n.matchers {
				if matcher.Match(requirement, capSpec) {
					matched = true
					break
				}
			}

			if !matched {
				continue
			}

			// 计算分数
			score := n.scorer.Score(requirement, capSpec)
			if score > bestScore {
				bestScore = score
				bestAssignment = &CapabilityAssignment{
					Requirement: requirement,
					Agent:       agent,
					Capability:  capSpec,
					Score:       score,
				}
			}
		}
	}

	return bestAssignment
}

// ============== 能力声明 ==============

// DeclareCapability 声明能力
func (n *Negotiator) DeclareCapability(agentID string, capability CapabilitySpec) error {
	agent, exists := n.registry.Get(agentID)
	if !exists {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	// 添加能力
	agent.Capabilities = append(agent.Capabilities, Capability{
		Name:        capability.Name,
		Description: capability.Description,
		Version:     capability.Version,
	})

	return nil
}

// QueryCapabilities 查询能力
func (n *Negotiator) QueryCapabilities(agentID string) ([]CapabilitySpec, error) {
	agent, exists := n.registry.Get(agentID)
	if !exists {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}

	specs := make([]CapabilitySpec, len(agent.Capabilities))
	for i, cap := range agent.Capabilities {
		specs[i] = CapabilitySpec{
			Name:        cap.Name,
			Description: cap.Description,
			Version:     cap.Version,
		}
	}

	return specs, nil
}

// ============== 能力描述导出 ==============

// ExportCapabilities 导出能力描述（JSON 格式）
func (n *Negotiator) ExportCapabilities(agentID string) ([]byte, error) {
	caps, err := n.QueryCapabilities(agentID)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(caps, "", "  ")
}

// ImportCapabilities 导入能力描述
func (n *Negotiator) ImportCapabilities(agentID string, data []byte) error {
	var caps []CapabilitySpec
	if err := json.Unmarshal(data, &caps); err != nil {
		return err
	}

	for _, cap := range caps {
		if err := n.DeclareCapability(agentID, cap); err != nil {
			return err
		}
	}

	return nil
}
