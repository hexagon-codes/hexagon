// Package agent 提供 AI Agent 核心功能
//
// 本文件实现 Agent 发现与注册功能：
//
//   - Agent 注册表：中央注册和管理 Agent
//
//   - 服务发现：动态发现可用 Agent
//
//   - 健康检查：监控 Agent 状态
//
//   - 负载均衡：智能路由请求
//
//   - Consul: 服务注册与发现
//
//   - Kubernetes: Service Discovery
//
//   - gRPC: 服务发现机制
package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/idgen"
)

// ============== Agent 注册表 ==============

// Registry Agent 注册表
//
// 功能：
//   - 注册和注销 Agent
//   - 按标签/能力查询 Agent
//   - 健康检查和心跳
//   - 负载均衡选择
//
// 使用示例：
//
//	registry := NewRegistry()
//
//	// 注册 Agent
//	registry.Register(&AgentInfo{
//	    ID:   "agent-1",
//	    Name: "assistant",
//	    Tags: []string{"chat", "qa"},
//	})
//
//	// 发现 Agent
//	agents := registry.Discover(WithTag("chat"))
type Registry struct {
	// agents 已注册的 Agent
	agents map[string]*AgentInfo

	// watches 监听器
	watches map[string][]WatchCallback

	// healthChecker 健康检查器
	healthChecker *HealthChecker

	// loadBalancer 负载均衡器
	loadBalancer LoadBalancer

	// 配置
	config RegistryConfig

	mu sync.RWMutex
}

// AgentInfo Agent 信息
type AgentInfo struct {
	// ID Agent 唯一标识
	ID string `json:"id"`

	// Name Agent 名称
	Name string `json:"name"`

	// Description Agent 描述
	Description string `json:"description,omitempty"`

	// Version 版本号
	Version string `json:"version,omitempty"`

	// Tags 标签列表
	Tags []string `json:"tags,omitempty"`

	// Capabilities 能力列表
	Capabilities []Capability `json:"capabilities,omitempty"`

	// Endpoint 服务端点
	Endpoint string `json:"endpoint,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// Status 状态
	Status AgentStatus `json:"status"`

	// RegisteredAt 注册时间
	RegisteredAt time.Time `json:"registered_at"`

	// LastHeartbeat 最后心跳时间
	LastHeartbeat time.Time `json:"last_heartbeat"`

	// HealthCheck 健康检查配置
	HealthCheck *HealthCheckConfig `json:"health_check,omitempty"`

	// Weight 权重（用于负载均衡）
	Weight int `json:"weight,omitempty"`

	// MaxConcurrency 最大并发数
	MaxConcurrency int `json:"max_concurrency,omitempty"`

	// CurrentLoad 当前负载
	CurrentLoad int `json:"current_load,omitempty"`

	// instance Agent 实例引用
	instance Agent
}

// Capability Agent 能力定义
type Capability struct {
	// Name 能力名称
	Name string `json:"name"`

	// Description 能力描述
	Description string `json:"description,omitempty"`

	// Version 能力版本
	Version string `json:"version,omitempty"`

	// InputSchema 输入 Schema
	InputSchema map[string]any `json:"input_schema,omitempty"`

	// OutputSchema 输出 Schema
	OutputSchema map[string]any `json:"output_schema,omitempty"`

	// Constraints 约束条件
	Constraints map[string]any `json:"constraints,omitempty"`
}

// AgentStatus Agent 状态
type AgentStatus string

const (
	// StatusUnknown 未知状态
	StatusUnknown AgentStatus = "unknown"

	// StatusHealthy 健康状态
	StatusHealthy AgentStatus = "healthy"

	// StatusUnhealthy 不健康状态
	StatusUnhealthy AgentStatus = "unhealthy"

	// StatusDraining 正在排空
	StatusDraining AgentStatus = "draining"

	// StatusOffline 离线
	StatusOffline AgentStatus = "offline"
)

// RegistryConfig 注册表配置
type RegistryConfig struct {
	// HealthCheckInterval 健康检查间隔
	HealthCheckInterval time.Duration

	// HeartbeatTimeout 心跳超时时间
	HeartbeatTimeout time.Duration

	// DeregisterAfter 超时后自动注销时间
	DeregisterAfter time.Duration

	// EnableHealthCheck 启用健康检查
	EnableHealthCheck bool
}

// DefaultRegistryConfig 默认配置
var DefaultRegistryConfig = RegistryConfig{
	HealthCheckInterval: 10 * time.Second,
	HeartbeatTimeout:    30 * time.Second,
	DeregisterAfter:     60 * time.Second,
	EnableHealthCheck:   true,
}

// WatchCallback 监听回调
type WatchCallback func(event WatchEvent)

// WatchEvent 监听事件
type WatchEvent struct {
	// Type 事件类型
	Type WatchEventType `json:"type"`

	// Agent Agent 信息
	Agent *AgentInfo `json:"agent"`

	// Timestamp 事件时间
	Timestamp time.Time `json:"timestamp"`
}

// WatchEventType 事件类型
type WatchEventType string

const (
	// EventRegistered Agent 注册
	EventRegistered WatchEventType = "registered"

	// EventDeregistered Agent 注销
	EventDeregistered WatchEventType = "deregistered"

	// EventHealthChanged 健康状态变化
	EventHealthChanged WatchEventType = "health_changed"

	// EventUpdated Agent 更新
	EventUpdated WatchEventType = "updated"
)

// NewRegistry 创建注册表
func NewRegistry(config ...RegistryConfig) *Registry {
	cfg := DefaultRegistryConfig
	if len(config) > 0 {
		cfg = config[0]
	}

	r := &Registry{
		agents:       make(map[string]*AgentInfo),
		watches:      make(map[string][]WatchCallback),
		config:       cfg,
		loadBalancer: &RoundRobinBalancer{},
	}

	if cfg.EnableHealthCheck {
		r.healthChecker = NewHealthChecker(r, cfg.HealthCheckInterval)
		go r.healthChecker.Start()
	}

	return r
}

// Register 注册 Agent
func (r *Registry) Register(info *AgentInfo) error {
	if info.ID == "" {
		return fmt.Errorf("agent ID is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	info.RegisteredAt = time.Now()
	info.LastHeartbeat = time.Now()
	if info.Status == "" {
		info.Status = StatusHealthy
	}
	if info.Weight <= 0 {
		info.Weight = 1
	}

	r.agents[info.ID] = info

	// 触发事件
	r.notifyWatchers(WatchEvent{
		Type:      EventRegistered,
		Agent:     info,
		Timestamp: time.Now(),
	})

	return nil
}

// RegisterAgent 注册 Agent 实例
func (r *Registry) RegisterAgent(agent Agent, opts ...RegisterOption) error {
	info := &AgentInfo{
		ID:       agent.ID(),
		Name:     agent.Name(),
		instance: agent,
		Status:   StatusHealthy,
	}

	// 应用选项
	for _, opt := range opts {
		opt(info)
	}

	return r.Register(info)
}

// RegisterOption 注册选项
type RegisterOption func(*AgentInfo)

// WithTags 设置标签
func WithTags(tags ...string) RegisterOption {
	return func(info *AgentInfo) {
		info.Tags = append(info.Tags, tags...)
	}
}

// WithCapabilities 设置能力
func WithCapabilities(caps ...Capability) RegisterOption {
	return func(info *AgentInfo) {
		info.Capabilities = append(info.Capabilities, caps...)
	}
}

// WithEndpoint 设置端点
func WithEndpoint(endpoint string) RegisterOption {
	return func(info *AgentInfo) {
		info.Endpoint = endpoint
	}
}

// WithWeight 设置权重
func WithWeight(weight int) RegisterOption {
	return func(info *AgentInfo) {
		info.Weight = weight
	}
}

// WithMetadata 设置元数据
func WithMetadata(metadata map[string]any) RegisterOption {
	return func(info *AgentInfo) {
		info.Metadata = metadata
	}
}

// Deregister 注销 Agent
func (r *Registry) Deregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	info, exists := r.agents[id]
	if !exists {
		return fmt.Errorf("agent not found: %s", id)
	}

	delete(r.agents, id)

	// 触发事件
	r.notifyWatchers(WatchEvent{
		Type:      EventDeregistered,
		Agent:     info,
		Timestamp: time.Now(),
	})

	return nil
}

// Get 获取 Agent 信息
func (r *Registry) Get(id string) (*AgentInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, exists := r.agents[id]
	return info, exists
}

// GetAgent 获取 Agent 实例
func (r *Registry) GetAgent(id string) (Agent, bool) {
	info, exists := r.Get(id)
	if !exists || info.instance == nil {
		return nil, false
	}
	return info.instance, true
}

// List 列出所有 Agent
func (r *Registry) List() []*AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*AgentInfo, 0, len(r.agents))
	for _, info := range r.agents {
		result = append(result, info)
	}
	return result
}

// ============== 服务发现 ==============

// DiscoverOption 发现选项
type DiscoverOption func(*DiscoverQuery)

// DiscoverQuery 发现查询
type DiscoverQuery struct {
	Tags         []string
	Capabilities []string
	Status       AgentStatus
	Metadata     map[string]any
}

// WithTag 按标签过滤
func WithTag(tags ...string) DiscoverOption {
	return func(q *DiscoverQuery) {
		q.Tags = append(q.Tags, tags...)
	}
}

// WithCapability 按能力过滤
func WithCapability(caps ...string) DiscoverOption {
	return func(q *DiscoverQuery) {
		q.Capabilities = append(q.Capabilities, caps...)
	}
}

// WithStatus 按状态过滤
func WithStatus(status AgentStatus) DiscoverOption {
	return func(q *DiscoverQuery) {
		q.Status = status
	}
}

// Discover 发现 Agent
func (r *Registry) Discover(opts ...DiscoverOption) []*AgentInfo {
	query := &DiscoverQuery{}
	for _, opt := range opts {
		opt(query)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*AgentInfo
	for _, info := range r.agents {
		if r.matchQuery(info, query) {
			result = append(result, info)
		}
	}

	return result
}

// DiscoverOne 发现单个 Agent（负载均衡）
func (r *Registry) DiscoverOne(opts ...DiscoverOption) (*AgentInfo, error) {
	agents := r.Discover(opts...)
	if len(agents) == 0 {
		return nil, fmt.Errorf("no matching agents found")
	}

	// 使用负载均衡器选择
	return r.loadBalancer.Select(agents), nil
}

// matchQuery 匹配查询条件
func (r *Registry) matchQuery(info *AgentInfo, query *DiscoverQuery) bool {
	// 状态过滤
	if query.Status != "" && info.Status != query.Status {
		return false
	}

	// 默认只返回健康的 Agent
	if query.Status == "" && info.Status != StatusHealthy {
		return false
	}

	// 标签过滤
	if len(query.Tags) > 0 {
		if !r.hasAllTags(info.Tags, query.Tags) {
			return false
		}
	}

	// 能力过滤
	if len(query.Capabilities) > 0 {
		if !r.hasAllCapabilities(info.Capabilities, query.Capabilities) {
			return false
		}
	}

	return true
}

func (r *Registry) hasAllTags(have, want []string) bool {
	tagSet := make(map[string]bool)
	for _, t := range have {
		tagSet[t] = true
	}
	for _, t := range want {
		if !tagSet[t] {
			return false
		}
	}
	return true
}

func (r *Registry) hasAllCapabilities(have []Capability, want []string) bool {
	capSet := make(map[string]bool)
	for _, c := range have {
		capSet[c.Name] = true
	}
	for _, c := range want {
		if !capSet[c] {
			return false
		}
	}
	return true
}

// ============== 健康检查 ==============

// HealthCheckConfig 健康检查配置
type HealthCheckConfig struct {
	// Interval 检查间隔
	Interval time.Duration `json:"interval"`

	// Timeout 超时时间
	Timeout time.Duration `json:"timeout"`

	// HealthyThreshold 健康阈值
	HealthyThreshold int `json:"healthy_threshold"`

	// UnhealthyThreshold 不健康阈值
	UnhealthyThreshold int `json:"unhealthy_threshold"`

	// HTTPPath HTTP 检查路径
	HTTPPath string `json:"http_path,omitempty"`
}

// HealthChecker 健康检查器
type HealthChecker struct {
	registry *Registry
	interval time.Duration
	stopCh   chan struct{}
}

// NewHealthChecker 创建健康检查器
func NewHealthChecker(r *Registry, interval time.Duration) *HealthChecker {
	return &HealthChecker{
		registry: r,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start 启动健康检查
func (h *HealthChecker) Start() {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			h.checkAll()
		case <-h.stopCh:
			return
		}
	}
}

// Stop 停止健康检查
func (h *HealthChecker) Stop() {
	close(h.stopCh)
}

// checkAll 检查所有 Agent
func (h *HealthChecker) checkAll() {
	h.registry.mu.Lock()
	defer h.registry.mu.Unlock()

	now := time.Now()
	for _, info := range h.registry.agents {
		// 检查心跳超时
		if now.Sub(info.LastHeartbeat) > h.registry.config.HeartbeatTimeout {
			if info.Status == StatusHealthy {
				info.Status = StatusUnhealthy
				h.registry.notifyWatchers(WatchEvent{
					Type:      EventHealthChanged,
					Agent:     info,
					Timestamp: now,
				})
			}
		}

		// 检查是否需要自动注销
		if h.registry.config.DeregisterAfter > 0 &&
			now.Sub(info.LastHeartbeat) > h.registry.config.DeregisterAfter {
			delete(h.registry.agents, info.ID)
			h.registry.notifyWatchers(WatchEvent{
				Type:      EventDeregistered,
				Agent:     info,
				Timestamp: now,
			})
		}
	}
}

// Heartbeat 发送心跳
func (r *Registry) Heartbeat(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	info, exists := r.agents[id]
	if !exists {
		return fmt.Errorf("agent not found: %s", id)
	}

	info.LastHeartbeat = time.Now()

	// 如果之前不健康，恢复为健康
	if info.Status == StatusUnhealthy {
		info.Status = StatusHealthy
		r.notifyWatchers(WatchEvent{
			Type:      EventHealthChanged,
			Agent:     info,
			Timestamp: time.Now(),
		})
	}

	return nil
}

// ============== 监听器 ==============

// Watch 添加监听器
func (r *Registry) Watch(callback WatchCallback) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := fmt.Sprintf("watch_%s", idgen.NanoID())
	r.watches[id] = append(r.watches[id], callback)
	return id
}

// Unwatch 移除监听器
func (r *Registry) Unwatch(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.watches, id)
}

// notifyWatchers 通知所有监听器
func (r *Registry) notifyWatchers(event WatchEvent) {
	for _, callbacks := range r.watches {
		for _, callback := range callbacks {
			go callback(event)
		}
	}
}

// ============== 负载均衡 ==============

// LoadBalancer 负载均衡器接口
type LoadBalancer interface {
	Select(agents []*AgentInfo) *AgentInfo
}

// RoundRobinBalancer 轮询负载均衡器
type RoundRobinBalancer struct {
	counter uint64
	mu      sync.Mutex
}

// Select 轮询选择
func (b *RoundRobinBalancer) Select(agents []*AgentInfo) *AgentInfo {
	if len(agents) == 0 {
		return nil
	}

	b.mu.Lock()
	idx := b.counter % uint64(len(agents))
	b.counter++
	b.mu.Unlock()

	return agents[idx]
}

// WeightedBalancer 加权负载均衡器
type WeightedBalancer struct {
	mu sync.Mutex
}

// Select 加权选择
func (b *WeightedBalancer) Select(agents []*AgentInfo) *AgentInfo {
	if len(agents) == 0 {
		return nil
	}

	// 计算总权重
	totalWeight := 0
	for _, agent := range agents {
		totalWeight += agent.Weight
	}

	// 简单实现：按权重比例选择
	// 实际生产中应使用更复杂的算法
	b.mu.Lock()
	defer b.mu.Unlock()

	// 选择负载最小的
	var selected *AgentInfo
	minLoad := -1
	for _, agent := range agents {
		if agent.MaxConcurrency > 0 && agent.CurrentLoad >= agent.MaxConcurrency {
			continue // 跳过已满载的
		}
		load := agent.CurrentLoad * 100 / agent.Weight
		if minLoad < 0 || load < minLoad {
			minLoad = load
			selected = agent
		}
	}

	return selected
}

// LeastConnectionsBalancer 最少连接负载均衡器
type LeastConnectionsBalancer struct{}

// Select 选择连接数最少的
func (b *LeastConnectionsBalancer) Select(agents []*AgentInfo) *AgentInfo {
	if len(agents) == 0 {
		return nil
	}

	var selected *AgentInfo
	minLoad := -1
	for _, agent := range agents {
		if minLoad < 0 || agent.CurrentLoad < minLoad {
			minLoad = agent.CurrentLoad
			selected = agent
		}
	}

	return selected
}

// SetLoadBalancer 设置负载均衡器
func (r *Registry) SetLoadBalancer(lb LoadBalancer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loadBalancer = lb
}

// ============== 全局注册表 ==============

var (
	globalRegistry     *Registry
	globalRegistryOnce sync.Once
)

// GlobalRegistry 获取全局注册表
func GlobalRegistry() *Registry {
	globalRegistryOnce.Do(func() {
		globalRegistry = NewRegistry()
	})
	return globalRegistry
}

// ============== 便捷函数 ==============

// RegisterGlobal 注册到全局注册表
func RegisterGlobal(agent Agent, opts ...RegisterOption) error {
	return GlobalRegistry().RegisterAgent(agent, opts...)
}

// DiscoverGlobal 从全局注册表发现
func DiscoverGlobal(opts ...DiscoverOption) []*AgentInfo {
	return GlobalRegistry().Discover(opts...)
}

// DiscoverOneGlobal 从全局注册表发现单个
func DiscoverOneGlobal(opts ...DiscoverOption) (*AgentInfo, error) {
	return GlobalRegistry().DiscoverOne(opts...)
}
