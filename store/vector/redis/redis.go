// Package redis 提供 Redis Stack (RediSearch) 向量存储集成
//
// Redis Stack 通过 RediSearch 模块提供向量搜索能力，
// 适合需要低延迟、高吞吐量的实时向量检索场景。
//
// 特性：
//   - 亚毫秒级查询延迟
//   - 支持 FLAT 和 HNSW 索引
//   - 混合查询（向量 + 属性过滤）
//   - 实时索引更新
//   - 内存优先，可持久化
//
// 前置条件：
//   - Redis Stack 7.0+ (redis/redis-stack Docker 镜像)
//   - 或 Redis 7.0+ 带 RediSearch 模块
//
// 使用示例：
//
//	store, err := redis.NewStore(ctx,
//	    redis.WithAddr("localhost:6379"),
//	    redis.WithIndex("doc_index"),
//	    redis.WithDimension(1536),
//	)
//	defer store.Close()
//
//	// 添加文档
//	store.Add(ctx, docs)
//
//	// 搜索
//	results, err := store.Search(ctx, queryEmbedding, 5)
package redis

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// DistanceMetric 距离度量
type DistanceMetric string

const (
	// DistanceCosine 余弦距离
	DistanceCosine DistanceMetric = "COSINE"
	// DistanceL2 L2 距离
	DistanceL2 DistanceMetric = "L2"
	// DistanceIP 内积距离
	DistanceIP DistanceMetric = "IP"
)

// IndexAlgorithm 索引算法
type IndexAlgorithm string

const (
	// AlgorithmFlat 暴力搜索（精确）
	AlgorithmFlat IndexAlgorithm = "FLAT"
	// AlgorithmHNSW HNSW 索引（近似，推荐）
	AlgorithmHNSW IndexAlgorithm = "HNSW"
)

// Store Redis Stack 向量存储
type Store struct {
	client    *goredis.Client
	index     string
	prefix    string
	dimension int
	distance  DistanceMetric
	algorithm IndexAlgorithm

	mu     sync.RWMutex
	closed bool
}

// Option 配置选项
type Option func(*Store)

// WithAddr 设置 Redis 地址
func WithAddr(addr string) Option {
	return func(s *Store) {
		s.client = goredis.NewClient(&goredis.Options{
			Addr: addr,
		})
	}
}

// WithClient 设置已有的 Redis 客户端
func WithClient(client *goredis.Client) Option {
	return func(s *Store) {
		s.client = client
	}
}

// WithPassword 设置密码
func WithPassword(password string) Option {
	return func(s *Store) {
		if s.client != nil {
			// 重新创建带密码的客户端
			opts := s.client.Options()
			opts.Password = password
			s.client = goredis.NewClient(opts)
		}
	}
}

// WithIndex 设置索引名称
func WithIndex(index string) Option {
	return func(s *Store) {
		s.index = index
	}
}

// WithPrefix 设置 key 前缀
func WithPrefix(prefix string) Option {
	return func(s *Store) {
		s.prefix = prefix
	}
}

// WithDimension 设置向量维度
func WithDimension(dim int) Option {
	return func(s *Store) {
		s.dimension = dim
	}
}

// WithDistance 设置距离度量
func WithDistance(d DistanceMetric) Option {
	return func(s *Store) {
		s.distance = d
	}
}

// WithAlgorithm 设置索引算法
func WithAlgorithm(algo IndexAlgorithm) Option {
	return func(s *Store) {
		s.algorithm = algo
	}
}

// NewStore 创建 Redis Stack 向量存储
func NewStore(ctx context.Context, opts ...Option) (*Store, error) {
	s := &Store{
		index:     "hexagon_vectors",
		prefix:    "doc:",
		dimension: 1536,
		distance:  DistanceCosine,
		algorithm: AlgorithmHNSW,
	}

	for _, opt := range opts {
		opt(s)
	}

	if s.client == nil {
		s.client = goredis.NewClient(&goredis.Options{
			Addr: "localhost:6379",
		})
	}

	// 测试连接
	if err := s.client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("Redis 连接失败: %w", err)
	}

	// 创建索引
	if err := s.createIndex(ctx); err != nil {
		return nil, err
	}

	return s, nil
}

// createIndex 创建 RediSearch 索引
func (s *Store) createIndex(ctx context.Context) error {
	// 检查索引是否存在
	_, err := s.client.Do(ctx, "FT.INFO", s.index).Result()
	if err == nil {
		return nil // 索引已存在
	}

	// 创建索引
	// FT.CREATE idx ON HASH PREFIX 1 doc: SCHEMA
	//   content TEXT
	//   metadata TEXT
	//   embedding VECTOR HNSW 6 TYPE FLOAT32 DIM 1536 DISTANCE_METRIC COSINE
	args := []any{
		"FT.CREATE", s.index,
		"ON", "HASH",
		"PREFIX", "1", s.prefix,
		"SCHEMA",
		"content", "TEXT",
		"metadata", "TEXT",
		"embedding", "VECTOR", string(s.algorithm),
		"6",
		"TYPE", "FLOAT32",
		"DIM", s.dimension,
		"DISTANCE_METRIC", string(s.distance),
	}

	_, err = s.client.Do(ctx, args...).Result()
	if err != nil {
		return fmt.Errorf("创建 RediSearch 索引失败: %w", err)
	}

	return nil
}

// Add 添加文档
func (s *Store) Add(ctx context.Context, docs []vector.Document) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	pipe := s.client.Pipeline()

	for _, doc := range docs {
		key := s.prefix + doc.ID

		metadata, _ := json.Marshal(doc.Metadata)
		embeddingBytes := float32SliceToBytes(doc.Embedding)

		pipe.HSet(ctx, key, map[string]any{
			"content":    doc.Content,
			"metadata":   string(metadata),
			"embedding":  embeddingBytes,
			"created_at": time.Now().Unix(),
		})
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("添加文档失败: %w", err)
	}

	return nil
}

// Search 搜索相似文档
func (s *Store) Search(ctx context.Context, embedding []float32, topK int, filter map[string]any) ([]vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, fmt.Errorf("存储已关闭")
	}

	embeddingBytes := float32SliceToBytes(embedding)

	// 构建查询
	// FT.SEARCH idx "(@filter:value)=>[KNN 5 @embedding $vec AS score]"
	// PARAMS 2 vec <bytes> SORTBY score LIMIT 0 5 DIALECT 2
	queryFilter := "*"
	if len(filter) > 0 {
		var parts []string
		for key, value := range filter {
			parts = append(parts, fmt.Sprintf("@metadata:{%s\\:%v}", key, value))
		}
		queryFilter = strings.Join(parts, " ")
	}

	query := fmt.Sprintf("(%s)=>[KNN %d @embedding $vec AS score]", queryFilter, topK)

	args := []any{
		"FT.SEARCH", s.index,
		query,
		"PARAMS", "2", "vec", embeddingBytes,
		"SORTBY", "score",
		"LIMIT", "0", topK,
		"DIALECT", "2",
	}

	result, err := s.client.Do(ctx, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("搜索失败: %w", err)
	}

	return s.parseSearchResult(result)
}

// parseSearchResult 解析 FT.SEARCH 结果
func (s *Store) parseSearchResult(result any) ([]vector.Document, error) {
	arr, ok := result.([]any)
	if !ok || len(arr) < 1 {
		return nil, nil
	}

	// 第一个元素是总数
	var docs []vector.Document

	// 后续元素是成对的 (key, fields)
	for i := 1; i < len(arr); i += 2 {
		if i+1 >= len(arr) {
			break
		}

		key, _ := arr[i].(string)
		fields, ok := arr[i+1].([]any)
		if !ok {
			continue
		}

		doc := vector.Document{
			ID: strings.TrimPrefix(key, s.prefix),
		}

		// 解析字段
		for j := 0; j < len(fields)-1; j += 2 {
			fieldName, _ := fields[j].(string)
			fieldValue := fields[j+1]

			switch fieldName {
			case "content":
				doc.Content, _ = fieldValue.(string)
			case "metadata":
				if metaStr, ok := fieldValue.(string); ok && metaStr != "" {
					json.Unmarshal([]byte(metaStr), &doc.Metadata)
				}
			case "score":
				if scoreStr, ok := fieldValue.(string); ok {
					if score, err := strconv.ParseFloat(scoreStr, 32); err == nil {
						// RediSearch 返回的是距离，转换为相似度
						doc.Score = float32(1 - score)
					}
				}
			}
		}

		docs = append(docs, doc)
	}

	return docs, nil
}

// Delete 删除文档
func (s *Store) Delete(ctx context.Context, ids []string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	pipe := s.client.Pipeline()
	for _, id := range ids {
		pipe.Del(ctx, s.prefix+id)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// Clear 清空存储
func (s *Store) Clear(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	// 删除索引（会同时删除所有文档）
	s.client.Do(ctx, "FT.DROPINDEX", s.index, "DD")

	// 重新创建索引
	return s.createIndex(ctx)
}

// Count 返回文档数量
func (s *Store) Count(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return 0, fmt.Errorf("存储已关闭")
	}

	// 使用 FT.INFO 获取文档数量
	result, err := s.client.Do(ctx, "FT.INFO", s.index).Result()
	if err != nil {
		return 0, err
	}

	// 解析 FT.INFO 结果
	arr, ok := result.([]any)
	if !ok {
		return 0, nil
	}

	for i := 0; i < len(arr)-1; i += 2 {
		key, _ := arr[i].(string)
		if key == "num_docs" {
			if countStr, ok := arr[i+1].(string); ok {
				count, _ := strconv.Atoi(countStr)
				return count, nil
			}
		}
	}

	return 0, nil
}

// Close 关闭存储
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// ============== 辅助函数 ==============

// float32SliceToBytes 将 float32 切片转换为字节数组（Little Endian）
func float32SliceToBytes(floats []float32) []byte {
	buf := make([]byte, len(floats)*4)
	for i, f := range floats {
		bits := math.Float32bits(f)
		binary.LittleEndian.PutUint32(buf[i*4:], bits)
	}
	return buf
}
