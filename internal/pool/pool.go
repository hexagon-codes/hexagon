// Package pool 提供 Hexagon 框架的协程池和对象池封装
//
// 本包封装了 toolkit/util/poolx，提供：
//   - 全局协程池管理
//   - 并发任务执行
//   - 对象池复用
//
// 使用示例：
//
//	// 使用全局协程池
//	pool.Submit(func() {
//	    // 任务逻辑
//	})
//
//	// 并行 Map
//	results, err := pool.Map(ctx, items, func(item T) (R, error) {
//	    return process(item), nil
//	})
package pool

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/hexagon-codes/toolkit/util/poolx"
)

// ============== 全局协程池 ==============

var (
	// globalPool 全局协程池
	globalPool *poolx.Pool
	poolOnce   sync.Once
)

// initGlobalPool 初始化全局协程池
func initGlobalPool() {
	poolOnce.Do(func() {
		numCPU := int32(runtime.NumCPU())
		globalPool = poolx.New("hexagon-global",
			poolx.WithMinWorkers(numCPU),
			poolx.WithMaxWorkers(numCPU*8),
			poolx.WithQueueSize(numCPU*32),
			poolx.WithAutoScale(true),
			poolx.WithWorkStealing(true),
			poolx.WithPanicHandler(func(v any) {
				// 静默处理 panic，避免影响其他任务
			}),
		)
	})
}

// GlobalPool 获取全局协程池
func GlobalPool() *poolx.Pool {
	initGlobalPool()
	return globalPool
}

// Submit 提交任务到全局协程池
func Submit(fn func()) error {
	initGlobalPool()
	return globalPool.Submit(fn)
}

// TrySubmit 尝试提交任务（非阻塞）
func TrySubmit(fn func()) bool {
	initGlobalPool()
	return globalPool.TrySubmit(fn)
}

// SubmitWait 提交任务并等待完成
func SubmitWait(fn func()) error {
	initGlobalPool()
	return globalPool.SubmitWait(fn)
}

// SubmitWithContext 带 context 提交任务
func SubmitWithContext(ctx context.Context, fn func()) error {
	initGlobalPool()
	return globalPool.SubmitWithContext(ctx, func(_ context.Context) { fn() })
}

// ============== 并行执行工具 ==============

// BatchConfig 批量执行配置
type BatchConfig struct {
	// MaxConcurrency 最大并发数（0 表示使用 CPU 核心数）
	MaxConcurrency int

	// StopOnError 遇到错误时是否停止
	StopOnError bool
}

// DefaultBatchConfig 默认批量配置
func DefaultBatchConfig() *BatchConfig {
	return &BatchConfig{
		MaxConcurrency: 0,
		StopOnError:    true,
	}
}

// BatchOption 批量执行选项
type BatchOption func(*BatchConfig)

// WithMaxConcurrency 设置最大并发数
func WithMaxConcurrency(n int) BatchOption {
	return func(c *BatchConfig) {
		c.MaxConcurrency = n
	}
}

// WithStopOnError 设置遇到错误时是否停止
func WithStopOnError(stop bool) BatchOption {
	return func(c *BatchConfig) {
		c.StopOnError = stop
	}
}

// Map 并行 Map 操作
// 使用协程池并发执行，结果保持输入顺序
func Map[T, R any](ctx context.Context, items []T, fn func(T) (R, error), opts ...BatchOption) ([]R, error) {
	if len(items) == 0 {
		return nil, nil
	}

	config := DefaultBatchConfig()
	for _, opt := range opts {
		opt(config)
	}

	maxConcurrency := config.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = runtime.NumCPU()
	}

	// 单个输入直接执行
	if len(items) == 1 {
		result, err := fn(items[0])
		if err != nil {
			return nil, err
		}
		return []R{result}, nil
	}

	// 使用 toolkit 的 poolx.Map
	return poolx.Map(ctx, items, maxConcurrency, fn)
}

// ForEach 并行 ForEach 操作
func ForEach[T any](ctx context.Context, items []T, fn func(T) error, opts ...BatchOption) error {
	if len(items) == 0 {
		return nil
	}

	config := DefaultBatchConfig()
	for _, opt := range opts {
		opt(config)
	}

	maxConcurrency := config.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = runtime.NumCPU()
	}

	return poolx.ForEach(ctx, items, maxConcurrency, fn)
}

// Parallel 并行执行多个任务
func Parallel(fns ...func()) {
	if len(fns) == 0 {
		return
	}
	initGlobalPool()
	poolx.Parallel(fns...)
}

// ============== 对象池 ==============

// ObjectPool 泛型对象池
// 封装 toolkit 的 ObjectPool
type ObjectPool[T any] struct {
	pool *poolx.ObjectPool[T]
}

// NewObjectPool 创建对象池
func NewObjectPool[T any](factory func() T, reset func(*T)) *ObjectPool[T] {
	pool, err := poolx.NewObjectPool(factory, reset)
	if err != nil {
		// 仅当 factory 为 nil 或返回 nil 值时出错，属于调用方编程错误，直接 panic 暴露。
		panic(fmt.Errorf("hexagon/pool: create object pool: %w", err))
	}
	return &ObjectPool[T]{pool: pool}
}

// Get 获取对象
func (p *ObjectPool[T]) Get() T {
	return p.pool.Get()
}

// Put 归还对象
func (p *ObjectPool[T]) Put(obj T) {
	p.pool.Put(obj)
}

// ============== 字节池 ==============

// BytePool 字节切片池
type BytePool struct {
	pool *poolx.ByteSlicePool
}

// NewBytePool 创建字节池
func NewBytePool(size int) *BytePool {
	return &BytePool{
		pool: poolx.NewByteSlicePool(size),
	}
}

// Get 获取字节切片
func (p *BytePool) Get() []byte {
	return p.pool.Get()
}

// Put 归还字节切片
func (p *BytePool) Put(b []byte) {
	p.pool.Put(b)
}

// ============== Buffer 池 ==============

// BufferPool 缓冲池
type BufferPool struct {
	pool *poolx.BufferPool
}

// NewBufferPool 创建缓冲池
func NewBufferPool(initialSize int) *BufferPool {
	return &BufferPool{
		pool: poolx.NewBufferPool(initialSize),
	}
}

// Get 获取缓冲
func (p *BufferPool) Get() []byte {
	return p.pool.Get()
}

// Put 归还缓冲
func (p *BufferPool) Put(b []byte) {
	p.pool.Put(b)
}

// ============== 预定义的常用池 ==============

var (
	// smallBufferPool 小缓冲池 (4KB)
	smallBufferPool *BufferPool
	smallBufferOnce sync.Once

	// mediumBufferPool 中缓冲池 (64KB)
	mediumBufferPool *BufferPool
	mediumBufferOnce sync.Once

	// largeBufferPool 大缓冲池 (1MB)
	largeBufferPool *BufferPool
	largeBufferOnce sync.Once
)

// SmallBuffer 获取小缓冲 (4KB)
func SmallBuffer() *BufferPool {
	smallBufferOnce.Do(func() {
		smallBufferPool = NewBufferPool(4 * 1024)
	})
	return smallBufferPool
}

// MediumBuffer 获取中缓冲 (64KB)
func MediumBuffer() *BufferPool {
	mediumBufferOnce.Do(func() {
		mediumBufferPool = NewBufferPool(64 * 1024)
	})
	return mediumBufferPool
}

// LargeBuffer 获取大缓冲 (1MB)
func LargeBuffer() *BufferPool {
	largeBufferOnce.Do(func() {
		largeBufferPool = NewBufferPool(1024 * 1024)
	})
	return largeBufferPool
}

// ============== 指标 ==============

// Metrics 获取协程池指标
func Metrics() poolx.MetricsSnapshot {
	initGlobalPool()
	return globalPool.Metrics()
}

// Running 获取正在运行的 worker 数量
func Running() int32 {
	initGlobalPool()
	return globalPool.Running()
}

// Waiting 获取等待中的任务数量
func Waiting() int32 {
	initGlobalPool()
	return globalPool.Waiting()
}

// Free 获取空闲的 worker 数量
func Free() int32 {
	initGlobalPool()
	return globalPool.Free()
}

// Cap 获取协程池容量
func Cap() int32 {
	initGlobalPool()
	return globalPool.Cap()
}
