// Package plugin 提供 Hexagon AI Agent 框架的插件系统
//
// Plugin 系统允许动态扩展框架功能：
//   - Plugin: 插件接口
//   - Registry: 插件注册表
//   - Loader: 插件加载器
//   - Lifecycle: 插件生命周期管理
//
// 插件类型：
//   - ProviderPlugin: LLM Provider 插件
//   - ToolPlugin: 工具插件
//   - MemoryPlugin: 记忆插件
//   - RetrieverPlugin: 检索器插件
//   - EvaluatorPlugin: 评估器插件
package plugin

import (
	"context"
	"time"
)

// Plugin 是插件接口
type Plugin interface {
	// Info 返回插件信息
	Info() PluginInfo

	// Init 初始化插件
	Init(ctx context.Context, config map[string]any) error

	// Start 启动插件
	Start(ctx context.Context) error

	// Stop 停止插件
	Stop(ctx context.Context) error

	// Health 返回健康状态
	Health() HealthStatus
}

// PluginInfo 插件信息
type PluginInfo struct {
	// Name 插件名称（唯一标识）
	Name string `json:"name"`

	// Version 插件版本
	Version string `json:"version"`

	// Type 插件类型
	Type PluginType `json:"type"`

	// Description 插件描述
	Description string `json:"description"`

	// Author 作者
	Author string `json:"author,omitempty"`

	// License 许可证
	License string `json:"license,omitempty"`

	// Homepage 主页
	Homepage string `json:"homepage,omitempty"`

	// Dependencies 依赖的其他插件
	Dependencies []string `json:"dependencies,omitempty"`

	// Tags 标签
	Tags []string `json:"tags,omitempty"`

	// Metadata 额外元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// PluginType 插件类型
type PluginType string

const (
	// PluginTypeProvider LLM Provider 插件
	PluginTypeProvider PluginType = "provider"

	// PluginTypeTool 工具插件
	PluginTypeTool PluginType = "tool"

	// PluginTypeMemory 记忆插件
	PluginTypeMemory PluginType = "memory"

	// PluginTypeRetriever 检索器插件
	PluginTypeRetriever PluginType = "retriever"

	// PluginTypeEvaluator 评估器插件
	PluginTypeEvaluator PluginType = "evaluator"

	// PluginTypeAgent Agent 插件
	PluginTypeAgent PluginType = "agent"

	// PluginTypeMiddleware 中间件插件
	PluginTypeMiddleware PluginType = "middleware"

	// PluginTypeExtension 扩展插件
	PluginTypeExtension PluginType = "extension"
)

// HealthStatus 健康状态
type HealthStatus struct {
	// Status 状态
	Status HealthState `json:"status"`

	// Message 状态消息
	Message string `json:"message,omitempty"`

	// Details 详细信息
	Details map[string]any `json:"details,omitempty"`

	// LastCheck 最后检查时间
	LastCheck time.Time `json:"last_check"`
}

// HealthState 健康状态枚举
type HealthState string

const (
	// HealthStateHealthy 健康
	HealthStateHealthy HealthState = "healthy"

	// HealthStateDegraded 降级
	HealthStateDegraded HealthState = "degraded"

	// HealthStateUnhealthy 不健康
	HealthStateUnhealthy HealthState = "unhealthy"

	// HealthStateUnknown 未知
	HealthStateUnknown HealthState = "unknown"
)

// PluginState 插件状态
type PluginState string

const (
	// PluginStateUnloaded 未加载
	PluginStateUnloaded PluginState = "unloaded"

	// PluginStateLoaded 已加载
	PluginStateLoaded PluginState = "loaded"

	// PluginStateInitialized 已初始化
	PluginStateInitialized PluginState = "initialized"

	// PluginStateRunning 运行中
	PluginStateRunning PluginState = "running"

	// PluginStateStopped 已停止
	PluginStateStopped PluginState = "stopped"

	// PluginStateError 错误
	PluginStateError PluginState = "error"
)

// PluginInstance 插件实例
type PluginInstance struct {
	generation *generation

	// Plugin 插件实例
	Plugin Plugin

	// State 当前状态
	State PluginState

	// Config 配置
	Config map[string]any

	// LoadedAt 加载时间
	LoadedAt time.Time

	// StartedAt 启动时间
	StartedAt *time.Time

	// Error 错误信息
	Error error
}

// PluginConfig 插件配置
type PluginConfig struct {
	// Name 插件名称
	Name string `json:"name" yaml:"name"`

	// Enabled 是否启用
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Config 插件配置
	Config map[string]any `json:"config" yaml:"config"`

	// Priority 优先级（越小越优先）
	Priority int `json:"priority" yaml:"priority"`
}

// PluginFactory 插件工厂函数
type PluginFactory func() Plugin

// ============== BasePlugin ==============

// BasePlugin 基础插件实现
// 提供默认实现，方便扩展
type BasePlugin struct {
	info   PluginInfo
	config map[string]any
	state  HealthState
}

// NewBasePlugin 创建基础插件
func NewBasePlugin(info PluginInfo) *BasePlugin {
	return &BasePlugin{
		info:  info,
		state: HealthStateUnknown,
	}
}

// Info 返回插件信息
func (p *BasePlugin) Info() PluginInfo {
	return p.info
}

// Init 初始化插件
func (p *BasePlugin) Init(ctx context.Context, config map[string]any) error {
	p.config = config
	return nil
}

// Start 启动插件
func (p *BasePlugin) Start(ctx context.Context) error {
	p.state = HealthStateHealthy
	return nil
}

// Stop 停止插件
func (p *BasePlugin) Stop(ctx context.Context) error {
	p.state = HealthStateUnknown
	return nil
}

// Health 返回健康状态
func (p *BasePlugin) Health() HealthStatus {
	return HealthStatus{
		Status:    p.state,
		LastCheck: time.Now(),
	}
}

// Config 获取配置
func (p *BasePlugin) Config() map[string]any {
	return p.config
}

// GetConfigString 获取字符串配置
func (p *BasePlugin) GetConfigString(key string, defaultVal string) string {
	if v, ok := p.config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return defaultVal
}

// GetConfigInt 获取整数配置
func (p *BasePlugin) GetConfigInt(key string, defaultVal int) int {
	if v, ok := p.config[key]; ok {
		switch val := v.(type) {
		case int:
			return val
		case int64:
			return int(val)
		case float64:
			return int(val)
		}
	}
	return defaultVal
}

// GetConfigBool 获取布尔配置
func (p *BasePlugin) GetConfigBool(key string, defaultVal bool) bool {
	if v, ok := p.config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return defaultVal
}

var _ Plugin = (*BasePlugin)(nil)

// ============== Hooks ==============

// PluginHooks 插件钩子
type PluginHooks interface {
	// OnLoad 加载时调用
	OnLoad() error

	// OnUnload 卸载时调用
	OnUnload() error

	// OnConfigChange 配置变更时调用
	OnConfigChange(oldConfig, newConfig map[string]any) error
}

// PluginWithHooks 带钩子的插件
type PluginWithHooks interface {
	Plugin
	PluginHooks
}

// ============== Events ==============

// PluginEvent 插件事件
type PluginEvent struct {
	// Type 事件类型
	Type PluginEventType `json:"type"`

	// Plugin 插件名称
	Plugin string `json:"plugin"`

	// Timestamp 时间戳
	Timestamp time.Time `json:"timestamp"`

	// Data 事件数据
	Data map[string]any `json:"data,omitempty"`
}

// PluginEventType 插件事件类型
type PluginEventType string

const (
	PluginEventLoaded      PluginEventType = "loaded"
	PluginEventUnloaded    PluginEventType = "unloaded"
	PluginEventInitialized PluginEventType = "initialized"
	PluginEventStarted     PluginEventType = "started"
	PluginEventStopped     PluginEventType = "stopped"
	PluginEventError       PluginEventType = "error"
	PluginEventHealthCheck PluginEventType = "health_check"
)

// PluginEventHandler 插件事件处理器
type PluginEventHandler func(event PluginEvent)
