// Package milvus 提供 Milvus 向量数据库集成（HTTP REST API v2）
//
// 本包通过 Milvus 的 RESTful API v2 实现向量存储，无需额外的 gRPC 依赖。
// 所有操作通过 HTTP POST 请求完成，支持标准的 Milvus 2.4+ 服务器。
//
// Milvus 是一个开源的云原生向量数据库，专为海量向量数据的存储、索引和管理而设计。
//
// 特性：
//   - 支持十亿级向量检索
//   - 多种索引类型 (FLAT, IVF_FLAT, HNSW)
//   - 分布式架构
//   - 无 gRPC 依赖，仅使用 HTTP REST API
//
// 使用示例:
//
//	store, err := milvus.NewStore(ctx,
//	    milvus.WithAddress("http://localhost:19530"),
//	    milvus.WithCollection("documents"),
//	    milvus.WithDimension(1536),
//	)
//	defer store.Close()
//
//	docs := []vector.Document{
//	    {ID: "1", Content: "Hello", Embedding: embedding},
//	}
//	store.Add(ctx, docs)
package milvus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// DefaultDimension 默认向量维度
const DefaultDimension = 1536

// DefaultAddress 默认 Milvus HTTP 地址
const DefaultAddress = "http://localhost:19530"

// DefaultCollection 默认集合名称
const DefaultCollection = "hexagon_documents"

// DefaultIndexType 默认索引类型
const DefaultIndexType = "HNSW"

// DefaultMetricType 默认距离度量类型
const DefaultMetricType = "COSINE"

// Store Milvus 向量存储（HTTP REST API 实现）
//
// 通过 Milvus RESTful API v2 (POST /v2/vectordb/...) 实现完整的向量存储功能。
// 线程安全，所有方法均可并发调用。
type Store struct {
	// baseURL Milvus HTTP 服务地址（如 http://localhost:19530）
	baseURL string

	// collection 集合名称
	collection string

	// dimension 向量维度
	dimension int

	// token 认证令牌（格式: "username:password"）
	token string

	// httpClient HTTP 客户端（可注入自定义实现用于测试）
	httpClient *http.Client

	// 索引配置
	indexType  string // FLAT, IVF_FLAT, HNSW
	metricType string // L2, IP, COSINE

	// 连接状态
	connected bool
	mu        sync.RWMutex
}

// Option Store 配置选项
type Option func(*Store)

// WithAddress 设置 Milvus HTTP 地址
//
// 地址格式应为 "http://host:port" 或 "https://host:port"。
// 如果省略 scheme，自动添加 "http://"。
func WithAddress(address string) Option {
	return func(s *Store) {
		s.baseURL = address
	}
}

// WithCollection 设置集合名称
func WithCollection(collection string) Option {
	return func(s *Store) {
		s.collection = collection
	}
}

// WithDimension 设置向量维度
func WithDimension(dim int) Option {
	return func(s *Store) {
		s.dimension = dim
	}
}

// WithAuth 设置认证信息
//
// Milvus REST API 使用 Bearer Token 认证，
// Token 格式为 "username:password"。
func WithAuth(username, password string) Option {
	return func(s *Store) {
		s.token = username + ":" + password
	}
}

// WithIndexType 设置索引类型
//
// 支持: FLAT, IVF_FLAT, IVF_SQ8, HNSW
func WithIndexType(indexType string) Option {
	return func(s *Store) {
		s.indexType = indexType
	}
}

// WithMetricType 设置距离度量类型
//
// 支持: L2, IP (内积), COSINE (余弦)
func WithMetricType(metricType string) Option {
	return func(s *Store) {
		s.metricType = metricType
	}
}

// WithHTTPClient 设置自定义 HTTP 客户端
//
// 主要用于测试场景注入 mock 客户端。
func WithHTTPClient(client *http.Client) Option {
	return func(s *Store) {
		s.httpClient = client
	}
}

// NewStore 创建 Milvus 向量存储
//
// 连接到 Milvus HTTP REST API，自动创建集合（如果不存在）并加载到内存。
//
// 参数:
//   - ctx: 上下文
//   - opts: 配置选项
//
// 返回:
//   - *Store: Milvus 存储实例
//   - error: 连接或初始化失败时返回错误
func NewStore(ctx context.Context, opts ...Option) (*Store, error) {
	s := &Store{
		baseURL:    DefaultAddress,
		collection: DefaultCollection,
		dimension:  DefaultDimension,
		indexType:  DefaultIndexType,
		metricType: DefaultMetricType,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	for _, opt := range opts {
		opt(s)
	}

	// 规范化地址：确保有 scheme
	if !strings.HasPrefix(s.baseURL, "http://") && !strings.HasPrefix(s.baseURL, "https://") {
		s.baseURL = "http://" + s.baseURL
	}
	// 去除尾部斜杠
	s.baseURL = strings.TrimRight(s.baseURL, "/")

	// 确保集合存在
	if err := s.ensureCollection(ctx); err != nil {
		return nil, fmt.Errorf("确保集合存在失败: %w", err)
	}

	s.connected = true
	return s, nil
}

// ensureCollection 确保集合存在，不存在则创建
func (s *Store) ensureCollection(ctx context.Context) error {
	// 检查集合是否存在（通过 describe）
	_, err := s.describeCollection(ctx)
	if err == nil {
		// 集合已存在，加载到内存
		return s.loadCollection(ctx)
	}

	// 集合不存在，创建它
	return s.createCollection(ctx)
}

// createCollection 创建集合（自定义 schema + 索引）
func (s *Store) createCollection(ctx context.Context) error {
	req := createCollectionRequest{
		CollectionName: s.collection,
		Schema: collectionSchema{
			AutoID:             false,
			EnableDynamicField: true,
			Fields: []fieldSchema{
				{
					FieldName:         "id",
					DataType:          "VarChar",
					IsPrimary:         true,
					ElementTypeParams: map[string]string{"max_length": "256"},
				},
				{
					FieldName: "vector",
					DataType:  "FloatVector",
					ElementTypeParams: map[string]string{
						"dim": fmt.Sprintf("%d", s.dimension),
					},
				},
				{
					FieldName:         "content",
					DataType:          "VarChar",
					ElementTypeParams: map[string]string{"max_length": "65535"},
				},
				{
					FieldName:         "metadata_json",
					DataType:          "VarChar",
					ElementTypeParams: map[string]string{"max_length": "65535"},
				},
			},
		},
		IndexParams: []indexParam{
			{
				FieldName:  "vector",
				MetricType: s.metricType,
				IndexName:  "vector_idx",
				Params: map[string]string{
					"index_type":     s.indexType,
					"M":              "16",
					"efConstruction": "256",
				},
			},
		},
	}

	_, err := s.doPost(ctx, "/v2/vectordb/collections/create", req)
	if err != nil {
		return fmt.Errorf("创建集合失败: %w", err)
	}

	// 加载集合到内存
	return s.loadCollection(ctx)
}

// describeCollection 获取集合描述信息
func (s *Store) describeCollection(ctx context.Context) (json.RawMessage, error) {
	req := map[string]string{"collectionName": s.collection}
	return s.doPost(ctx, "/v2/vectordb/collections/describe", req)
}

// loadCollection 加载集合到内存
func (s *Store) loadCollection(ctx context.Context) error {
	req := map[string]string{"collectionName": s.collection}
	_, err := s.doPost(ctx, "/v2/vectordb/collections/load", req)
	return err
}

// Add 添加文档
//
// 将文档批量插入 Milvus。每个文档必须包含与配置维度匹配的嵌入向量。
// 如果文档未提供 ID，将自动生成唯一 ID。
func (s *Store) Add(ctx context.Context, docs []vector.Document) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return fmt.Errorf("未连接到 Milvus")
	}

	if len(docs) == 0 {
		return nil
	}

	// 构建插入数据
	data := make([]map[string]any, len(docs))
	for i, doc := range docs {
		if len(doc.Embedding) != s.dimension {
			return fmt.Errorf("文档 %d 的向量维度不匹配: 期望 %d, 实际 %d",
				i, s.dimension, len(doc.Embedding))
		}

		id := doc.ID
		if id == "" {
			id = generateID()
		}

		// 序列化元数据为 JSON 字符串
		metadataJSON := "{}"
		if doc.Metadata != nil {
			b, err := json.Marshal(doc.Metadata)
			if err != nil {
				return fmt.Errorf("序列化文档 %d 的元数据失败: %w", i, err)
			}
			metadataJSON = string(b)
		}

		data[i] = map[string]any{
			"id":            id,
			"vector":        doc.Embedding,
			"content":       doc.Content,
			"metadata_json": metadataJSON,
		}
	}

	req := map[string]any{
		"collectionName": s.collection,
		"data":           data,
	}

	_, err := s.doPost(ctx, "/v2/vectordb/entities/insert", req)
	if err != nil {
		return fmt.Errorf("插入文档失败: %w", err)
	}

	return nil
}

// Search 搜索相似文档
//
// 使用向量相似度在 Milvus 中搜索最近邻文档。
// 返回结果按相似度降序排列。
func (s *Store) Search(ctx context.Context, query []float32, k int, opts ...vector.SearchOption) ([]vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("未连接到 Milvus")
	}

	if len(query) != s.dimension {
		return nil, fmt.Errorf("查询向量维度不匹配: 期望 %d, 实际 %d",
			s.dimension, len(query))
	}

	// 应用搜索选项
	cfg := &vector.SearchConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	outputFields := []string{"id", "content", "metadata_json"}
	if cfg.IncludeEmbedding {
		outputFields = append(outputFields, "vector")
	}

	req := map[string]any{
		"collectionName": s.collection,
		"data":           [][]float32{query},
		"annsField":      "vector",
		"limit":          k,
		"outputFields":   outputFields,
		"searchParams": map[string]any{
			"metricType": s.metricType,
			"params":     map[string]any{},
		},
	}

	respData, err := s.doPost(ctx, "/v2/vectordb/entities/search", req)
	if err != nil {
		return nil, fmt.Errorf("搜索失败: %w", err)
	}

	// 解析搜索结果
	var results []map[string]any
	if err := json.Unmarshal(respData, &results); err != nil {
		return nil, fmt.Errorf("解析搜索结果失败: %w", err)
	}

	var docs []vector.Document
	for _, item := range results {
		doc := vector.Document{}

		if id, ok := item["id"].(string); ok {
			doc.ID = id
		}
		if content, ok := item["content"].(string); ok {
			doc.Content = content
		}
		if dist, ok := item["distance"].(float64); ok {
			doc.Score = float32(dist)
		}
		if metaStr, ok := item["metadata_json"].(string); ok && metaStr != "" {
			var metadata map[string]any
			if err := json.Unmarshal([]byte(metaStr), &metadata); err == nil {
				doc.Metadata = metadata
			}
		}
		if cfg.IncludeEmbedding {
			if vec, ok := item["vector"].([]any); ok {
				embedding := make([]float32, len(vec))
				for i, v := range vec {
					if f, ok := v.(float64); ok {
						embedding[i] = float32(f)
					}
				}
				doc.Embedding = embedding
			}
		}

		// 应用最小分数过滤
		if doc.Score >= cfg.MinScore {
			docs = append(docs, doc)
		}
	}

	return docs, nil
}

// Get 根据 ID 获取文档
func (s *Store) Get(ctx context.Context, id string) (*vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("未连接到 Milvus")
	}

	req := map[string]any{
		"collectionName": s.collection,
		"id":             []string{id},
		"outputFields":   []string{"id", "content", "vector", "metadata_json"},
	}

	respData, err := s.doPost(ctx, "/v2/vectordb/entities/get", req)
	if err != nil {
		return nil, fmt.Errorf("获取文档失败: %w", err)
	}

	var results []map[string]any
	if err := json.Unmarshal(respData, &results); err != nil {
		return nil, fmt.Errorf("解析结果失败: %w", err)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("文档未找到: %s", id)
	}

	item := results[0]
	doc := &vector.Document{}

	if docID, ok := item["id"].(string); ok {
		doc.ID = docID
	}
	if content, ok := item["content"].(string); ok {
		doc.Content = content
	}
	if metaStr, ok := item["metadata_json"].(string); ok && metaStr != "" {
		var metadata map[string]any
		if err := json.Unmarshal([]byte(metaStr), &metadata); err == nil {
			doc.Metadata = metadata
		}
	}
	if vec, ok := item["vector"].([]any); ok {
		embedding := make([]float32, len(vec))
		for i, v := range vec {
			if f, ok := v.(float64); ok {
				embedding[i] = float32(f)
			}
		}
		doc.Embedding = embedding
	}

	return doc, nil
}

// Delete 删除文档
func (s *Store) Delete(ctx context.Context, ids []string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return fmt.Errorf("未连接到 Milvus")
	}

	if len(ids) == 0 {
		return nil
	}

	// 构建 filter 表达式: id in ["id1", "id2", ...]
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	filter := fmt.Sprintf("id in [%s]", strings.Join(quoted, ","))

	req := map[string]any{
		"collectionName": s.collection,
		"filter":         filter,
	}

	_, err := s.doPost(ctx, "/v2/vectordb/entities/delete", req)
	if err != nil {
		return fmt.Errorf("删除文档失败: %w", err)
	}

	return nil
}

// Update 更新文档
//
// Milvus 通过 upsert（插入或更新）实现文档更新。
func (s *Store) Update(ctx context.Context, docs []vector.Document) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return fmt.Errorf("未连接到 Milvus")
	}

	if len(docs) == 0 {
		return nil
	}

	// 构建 upsert 数据
	data := make([]map[string]any, len(docs))
	for i, doc := range docs {
		if len(doc.Embedding) != s.dimension {
			return fmt.Errorf("文档 %d 的向量维度不匹配: 期望 %d, 实际 %d",
				i, s.dimension, len(doc.Embedding))
		}

		metadataJSON := "{}"
		if doc.Metadata != nil {
			b, err := json.Marshal(doc.Metadata)
			if err != nil {
				return fmt.Errorf("序列化文档 %d 的元数据失败: %w", i, err)
			}
			metadataJSON = string(b)
		}

		data[i] = map[string]any{
			"id":            doc.ID,
			"vector":        doc.Embedding,
			"content":       doc.Content,
			"metadata_json": metadataJSON,
		}
	}

	req := map[string]any{
		"collectionName": s.collection,
		"data":           data,
	}

	_, err := s.doPost(ctx, "/v2/vectordb/entities/upsert", req)
	if err != nil {
		return fmt.Errorf("更新文档失败: %w", err)
	}

	return nil
}

// Count 统计文档数量
//
// 通过查询所有 ID 来统计文档总数。
func (s *Store) Count(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return 0, fmt.Errorf("未连接到 Milvus")
	}

	// 使用 query 统计数量（只返回 id 字段，最小化传输）
	req := map[string]any{
		"collectionName": s.collection,
		"filter":         "",
		"outputFields":   []string{"id"},
		"limit":          100000,
	}

	respData, err := s.doPost(ctx, "/v2/vectordb/entities/query", req)
	if err != nil {
		return 0, fmt.Errorf("查询文档数量失败: %w", err)
	}

	var results []map[string]any
	if err := json.Unmarshal(respData, &results); err != nil {
		return 0, nil
	}

	return len(results), nil
}

// Clear 清空所有文档
//
// 通过删除并重建集合实现清空操作。
func (s *Store) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return fmt.Errorf("未连接到 Milvus")
	}

	// 删除集合
	dropReq := map[string]string{"collectionName": s.collection}
	s.doPost(ctx, "/v2/vectordb/collections/drop", dropReq)

	// 重建集合
	return s.createCollection(ctx)
}

// Close 关闭存储连接
//
// HTTP 连接是无状态的，此方法仅标记连接状态为已关闭。
// 关闭后的所有操作将返回错误。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.connected = false
	return nil
}

// Stats 获取统计信息
func (s *Store) Stats(ctx context.Context) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("未连接到 Milvus")
	}

	return map[string]any{
		"collection":  s.collection,
		"dimension":   s.dimension,
		"index_type":  s.indexType,
		"metric_type": s.metricType,
		"connected":   s.connected,
		"timestamp":   time.Now(),
	}, nil
}

// CreateIndex 创建向量索引
func (s *Store) CreateIndex(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return fmt.Errorf("未连接到 Milvus")
	}

	req := map[string]any{
		"collectionName": s.collection,
		"indexParams": []map[string]any{
			{
				"fieldName":  "vector",
				"metricType": s.metricType,
				"indexName":  "vector_idx",
				"params": map[string]string{
					"index_type":     s.indexType,
					"M":              "16",
					"efConstruction": "256",
				},
			},
		},
	}

	_, err := s.doPost(ctx, "/v2/vectordb/indexes/create", req)
	return err
}

// DropIndex 删除向量索引
func (s *Store) DropIndex(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return fmt.Errorf("未连接到 Milvus")
	}

	req := map[string]any{
		"collectionName": s.collection,
		"indexName":      "vector_idx",
	}

	_, err := s.doPost(ctx, "/v2/vectordb/indexes/drop", req)
	return err
}

// LoadCollection 加载集合到内存
func (s *Store) LoadCollection(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return fmt.Errorf("未连接到 Milvus")
	}

	return s.loadCollection(ctx)
}

// ReleaseCollection 释放集合
func (s *Store) ReleaseCollection(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return fmt.Errorf("未连接到 Milvus")
	}

	req := map[string]string{"collectionName": s.collection}
	_, err := s.doPost(ctx, "/v2/vectordb/collections/release", req)
	return err
}

// Flush 刷新数据到磁盘
func (s *Store) Flush(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return fmt.Errorf("未连接到 Milvus")
	}

	// Milvus REST API v2 没有独立的 flush 端点
	// 数据在插入后自动持久化
	return nil
}

// ============================================================
// HTTP 通信
// ============================================================

// doPost 发送 POST 请求到 Milvus REST API
//
// 所有 Milvus v2 REST API 均使用 POST 方法。
// 返回响应的 data 字段内容。
func (s *Store) doPost(ctx context.Context, path string, body any) (json.RawMessage, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var milvusResp milvusResponse
	if err := json.Unmarshal(respBody, &milvusResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if milvusResp.Code != 0 {
		return nil, fmt.Errorf("Milvus 错误 (code=%d): %s", milvusResp.Code, milvusResp.Message)
	}

	return milvusResp.Data, nil
}

// ============================================================
// Milvus REST API 请求/响应类型
// ============================================================

// milvusResponse Milvus REST API 通用响应
type milvusResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// createCollectionRequest 创建集合请求
type createCollectionRequest struct {
	CollectionName string           `json:"collectionName"`
	Schema         collectionSchema `json:"schema"`
	IndexParams    []indexParam     `json:"indexParams"`
}

// collectionSchema 集合 schema 定义
type collectionSchema struct {
	AutoID             bool          `json:"autoId"`
	EnableDynamicField bool          `json:"enabledDynamicField"`
	Fields             []fieldSchema `json:"fields"`
}

// fieldSchema 字段 schema 定义
type fieldSchema struct {
	FieldName         string            `json:"fieldName"`
	DataType          string            `json:"dataType"`
	IsPrimary         bool              `json:"isPrimary,omitempty"`
	ElementTypeParams map[string]string `json:"elementTypeParams,omitempty"`
}

// indexParam 索引参数
type indexParam struct {
	FieldName  string            `json:"fieldName"`
	MetricType string            `json:"metricType"`
	IndexName  string            `json:"indexName"`
	Params     map[string]string `json:"params"`
}

// 确保实现了 vector.Store 接口
var _ vector.Store = (*Store)(nil)
