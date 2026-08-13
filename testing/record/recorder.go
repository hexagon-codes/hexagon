// Package record 提供测试的录制和回放功能
//
// Record 系统允许录制和回放 LLM 调用，便于测试和调试：
//   - Recorder: 录制 LLM 调用
//   - Replayer: 回放录制的调用
//   - Cassette: 存储录制的会话
package record

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/toolkit/util/idgen"
)

// Interaction 表示一次 LLM 交互
type Interaction struct {
	// ID 交互 ID
	ID string `json:"id"`

	// Request 请求
	Request llm.CompletionRequest `json:"request"`

	// Response 响应
	Response *llm.CompletionResponse `json:"response,omitempty"`

	// Error 错误信息
	Error string `json:"error,omitempty"`

	// Duration 耗时
	Duration time.Duration `json:"duration"`

	// Timestamp 时间戳
	Timestamp time.Time `json:"timestamp"`

	// RequestHash 请求哈希（用于匹配）
	RequestHash string `json:"request_hash"`
}

// Cassette 表示一个录制会话
type Cassette struct {
	// Name 会话名称
	Name string `json:"name"`

	// Description 描述
	Description string `json:"description,omitempty"`

	// Interactions 交互列表
	Interactions []Interaction `json:"interactions"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt 更新时间
	UpdatedAt time.Time `json:"updated_at"`
}

// NewCassette 创建新的录制会话
func NewCassette(name string) *Cassette {
	now := time.Now()
	return &Cassette{
		Name:         name,
		Interactions: make([]Interaction, 0),
		Metadata:     make(map[string]any),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// AddInteraction 添加交互
func (c *Cassette) AddInteraction(interaction Interaction) {
	c.Interactions = append(c.Interactions, interaction)
	c.UpdatedAt = time.Now()
}

// FindByHash 通过请求哈希查找交互
func (c *Cassette) FindByHash(hash string) *Interaction {
	for _, i := range c.Interactions {
		if i.RequestHash == hash {
			return &i
		}
	}
	return nil
}

// Save 保存到文件
func (c *Cassette) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cassette: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// Load 从文件加载
func LoadCassette(path string) (*Cassette, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read cassette file: %w", err)
	}

	var cassette Cassette
	if err := json.Unmarshal(data, &cassette); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cassette: %w", err)
	}

	return &cassette, nil
}

// ============== Recorder ==============

// Recorder 录制 LLM 调用
type Recorder struct {
	provider llm.Provider
	cassette *Cassette
	mu       sync.Mutex
}

// NewRecorder 创建录制器
func NewRecorder(provider llm.Provider, cassetteName string) *Recorder {
	return &Recorder{
		provider: provider,
		cassette: NewCassette(cassetteName),
	}
}

// Name 返回 Provider 名称
func (r *Recorder) Name() string {
	return r.provider.Name() + "_recorder"
}

// Complete 执行并录制请求
func (r *Recorder) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	start := time.Now()

	// 执行实际请求
	resp, err := r.provider.Complete(ctx, req)

	// 录制交互
	interaction := Interaction{
		ID:          fmt.Sprintf("int_%s", idgen.NanoID()),
		Request:     req,
		Duration:    time.Since(start),
		Timestamp:   start,
		RequestHash: hashRequest(req),
	}

	if err != nil {
		interaction.Error = err.Error()
	} else {
		interaction.Response = resp
	}

	r.mu.Lock()
	r.cassette.AddInteraction(interaction)
	r.mu.Unlock()

	return resp, err
}

// Stream 执行并录制流式请求
func (r *Recorder) Stream(ctx context.Context, req llm.CompletionRequest) (*llm.Stream, error) {
	// 简化处理：流式请求不录制，直接代理
	return r.provider.Stream(ctx, req)
}

// Models 返回支持的模型列表
func (r *Recorder) Models() []llm.ModelInfo {
	return r.provider.Models()
}

// CountTokens 计算 Token 数
func (r *Recorder) CountTokens(messages []llm.Message) (int, error) {
	return r.provider.CountTokens(messages)
}

// CountTokensContext 使用底层 Provider 的可取消能力计算 Token 数量。
// 若底层仅支持旧接口，则快速返回不支持错误，不执行无法中途取消的同步降级。
func (r *Recorder) CountTokensContext(ctx context.Context, messages []llm.Message) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	counter, ok := r.provider.(llm.ContextTokenCounter)
	if !ok {
		return 0, llm.ErrContextTokenCountingUnsupported
	}
	return counter.CountTokensContext(ctx, messages)
}

// Cassette 返回录制的会话
func (r *Recorder) Cassette() *Cassette {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cassette
}

// Save 保存录制
func (r *Recorder) Save(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cassette.Save(path)
}

var (
	_ llm.Provider            = (*Recorder)(nil)
	_ llm.ContextTokenCounter = (*Recorder)(nil)
)

// ============== Replayer ==============

// ReplayMode 回放模式
type ReplayMode string

const (
	// ReplayModeStrict 严格模式：必须找到匹配的录制
	ReplayModeStrict ReplayMode = "strict"

	// ReplayModeFallback 回退模式：找不到时使用真实 Provider
	ReplayModeFallback ReplayMode = "fallback"
)

// Replayer 回放录制的 LLM 调用
type Replayer struct {
	cassette    *Cassette
	fallback    llm.Provider
	mode        ReplayMode
	mu          sync.Mutex
	missCount   int
	hitCount    int
	usedIndices map[int]bool
}

// ReplayerOption Replayer 选项
type ReplayerOption func(*Replayer)

// WithReplayMode 设置回放模式
func WithReplayMode(mode ReplayMode) ReplayerOption {
	return func(r *Replayer) {
		r.mode = mode
	}
}

// WithFallbackProvider 设置回退 Provider
func WithFallbackProvider(provider llm.Provider) ReplayerOption {
	return func(r *Replayer) {
		r.fallback = provider
	}
}

// NewReplayer 创建回放器
func NewReplayer(cassette *Cassette, opts ...ReplayerOption) *Replayer {
	r := &Replayer{
		cassette:    cassette,
		mode:        ReplayModeStrict,
		usedIndices: make(map[int]bool),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Name 返回 Provider 名称
func (r *Replayer) Name() string {
	return "replayer"
}

// Complete 回放或执行请求
func (r *Replayer) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 计算请求哈希
	hash := hashRequest(req)

	// 查找匹配的交互
	interaction := r.cassette.FindByHash(hash)

	if interaction != nil {
		r.hitCount++

		if interaction.Error != "" {
			return nil, fmt.Errorf("%s", interaction.Error)
		}
		return interaction.Response, nil
	}

	// 未找到匹配
	r.missCount++

	if r.mode == ReplayModeFallback && r.fallback != nil {
		return r.fallback.Complete(ctx, req)
	}

	return nil, fmt.Errorf("no matching recording found for request (hash: %s)", hash)
}

// Stream 回放流式请求
func (r *Replayer) Stream(ctx context.Context, req llm.CompletionRequest) (*llm.Stream, error) {
	// 简化处理：使用回退 Provider
	if r.fallback != nil {
		return r.fallback.Stream(ctx, req)
	}
	return nil, fmt.Errorf("streaming not supported in replay mode")
}

// Models 返回支持的模型列表
func (r *Replayer) Models() []llm.ModelInfo {
	return []llm.ModelInfo{
		{
			ID:          "replay-model",
			Name:        "Replay Model",
			Description: "Replayed from cassette",
		},
	}
}

// CountTokens 计算 Token 数
func (r *Replayer) CountTokens(messages []llm.Message) (int, error) {
	if r.fallback != nil {
		return r.fallback.CountTokens(messages)
	}
	// 简单估算
	total := 0
	for _, msg := range messages {
		total += len(msg.Content) / 4
	}
	return total, nil
}

// CountTokensContext 使用回退 Provider 的可取消能力或本地估算计算 Token 数量。
// 回退 Provider 仅支持旧接口时快速返回不支持错误，避免伪装调用期间可取消。
func (r *Replayer) CountTokensContext(ctx context.Context, messages []llm.Message) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if r.fallback != nil {
		counter, ok := r.fallback.(llm.ContextTokenCounter)
		if !ok {
			return 0, llm.ErrContextTokenCountingUnsupported
		}
		return counter.CountTokensContext(ctx, messages)
	}

	total := 0
	for _, msg := range messages {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		total += len(msg.Content) / 4
	}
	return total, nil
}

// Stats 返回统计信息
func (r *Replayer) Stats() (hits, misses int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hitCount, r.missCount
}

var (
	_ llm.Provider            = (*Replayer)(nil)
	_ llm.ContextTokenCounter = (*Replayer)(nil)
)

// ============== 辅助函数 ==============

// hashRequest 计算请求的哈希值
func hashRequest(req llm.CompletionRequest) string {
	// 只使用消息内容和模型来计算哈希
	data := struct {
		Messages []llm.Message
		Model    string
	}{
		Messages: req.Messages,
		Model:    req.Model,
	}

	b, _ := json.Marshal(data)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8]) // 只取前 8 字节
}
