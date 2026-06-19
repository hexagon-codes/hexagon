// Package chroma 提供 ChromaDB 向量数据库集成
//
// ChromaDB 是一个开源的嵌入式向量数据库，专为 AI 应用设计。
//
// 特性：
//   - 轻量级，易于嵌入
//   - 支持本地和服务器模式
//   - Python 友好
//   - 简单的 API
//
// 使用示例:
//
//	store, err := chroma.NewStore(ctx,
//	    chroma.WithHost("localhost"),
//	    chroma.WithPort(8000),
//	    chroma.WithCollection("documents"),
//	)
//	defer store.Close()
package chroma

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// Store ChromaDB 向量存储
//
// 通过 HTTP API 与 ChromaDB 服务器交互
type Store struct {
	host       string
	port       int
	collection string
	apiKey     string

	// HTTP 客户端
	httpClient *http.Client

	// 连接状态
	connected bool
	mu        sync.RWMutex

	// 配置
	distanceMetric string // l2, ip, cosine
}

// Option Store 配置选项
type Option func(*Store)

// WithHost 设置主机地址
func WithHost(host string) Option {
	return func(s *Store) {
		s.host = host
	}
}

// WithPort 设置端口
func WithPort(port int) Option {
	return func(s *Store) {
		s.port = port
	}
}

// WithCollection 设置集合名称
func WithCollection(collection string) Option {
	return func(s *Store) {
		s.collection = collection
	}
}

// WithAPIKey 设置 API Key
func WithAPIKey(apiKey string) Option {
	return func(s *Store) {
		s.apiKey = apiKey
	}
}

// WithDistanceMetric 设置距离度量
func WithDistanceMetric(metric string) Option {
	return func(s *Store) {
		s.distanceMetric = metric
	}
}

// NewStore 创建 ChromaDB 向量存储
func NewStore(ctx context.Context, opts ...Option) (*Store, error) {
	s := &Store{
		host:           "localhost",
		port:           8000,
		collection:     "hexagon_documents",
		distanceMetric: "cosine",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	for _, opt := range opts {
		opt(s)
	}

	// 测试连接
	if err := s.connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to ChromaDB: %w", err)
	}

	// 确保集合存在
	if err := s.ensureCollection(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure collection: %w", err)
	}

	s.connected = true
	return s, nil
}

// connect 测试连接
func (s *Store) connect(ctx context.Context) error {
	url := fmt.Sprintf("http://%s:%d/api/v1/heartbeat", s.host, s.port)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ChromaDB returned status %d", resp.StatusCode)
	}

	return nil
}

// ensureCollection 确保集合存在
func (s *Store) ensureCollection(ctx context.Context) error {
	// 检查集合是否存在
	url := fmt.Sprintf("http://%s:%d/api/v1/collections/%s", s.host, s.port, s.collection)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to check collection: %w", err)
	}
	defer resp.Body.Close()

	// 集合已存在
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	// 集合不存在，创建它
	if resp.StatusCode == http.StatusNotFound {
		return s.createCollection(ctx)
	}

	return fmt.Errorf("unexpected status code %d when checking collection", resp.StatusCode)
}

// createCollection 创建集合
func (s *Store) createCollection(ctx context.Context) error {
	type createRequest struct {
		Name     string         `json:"name"`
		Metadata map[string]any `json:"metadata,omitempty"`
	}

	req := createRequest{
		Name: s.collection,
		Metadata: map[string]any{
			"hnsw:space": s.distanceMetric,
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal create request: %w", err)
	}

	url := fmt.Sprintf("http://%s:%d/api/v1/collections", s.host, s.port)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create collection (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// Add 添加文档
func (s *Store) Add(ctx context.Context, docs []vector.Document) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return fmt.Errorf("not connected to ChromaDB")
	}

	// 准备请求数据
	type addRequest struct {
		IDs        []string         `json:"ids"`
		Embeddings [][]float32      `json:"embeddings"`
		Metadatas  []map[string]any `json:"metadatas"`
		Documents  []string         `json:"documents"`
	}

	req := addRequest{
		IDs:        make([]string, len(docs)),
		Embeddings: make([][]float32, len(docs)),
		Metadatas:  make([]map[string]any, len(docs)),
		Documents:  make([]string, len(docs)),
	}

	for i, doc := range docs {
		req.IDs[i] = doc.ID
		req.Embeddings[i] = doc.Embedding
		req.Metadatas[i] = doc.Metadata
		req.Documents[i] = doc.Content
	}

	// 发送请求
	url := fmt.Sprintf("http://%s:%d/api/v1/collections/%s/add",
		s.host, s.port, s.collection)

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ChromaDB returned status %d", resp.StatusCode)
	}

	return nil
}

// Search 搜索相似文档
func (s *Store) Search(ctx context.Context, embedding []float32, limit int, opts ...vector.SearchOption) ([]vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("not connected to ChromaDB")
	}

	// 应用搜索选项
	cfg := &vector.SearchConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// 准备请求
	type queryRequest struct {
		QueryEmbeddings [][]float32    `json:"query_embeddings"`
		NResults        int            `json:"n_results"`
		Where           map[string]any `json:"where,omitempty"`
	}

	req := queryRequest{
		QueryEmbeddings: [][]float32{embedding},
		NResults:        limit,
	}

	if cfg.Filter != nil {
		req.Where = cfg.Filter
	}

	// 发送请求
	url := fmt.Sprintf("http://%s:%d/api/v1/collections/%s/query",
		s.host, s.port, s.collection)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ChromaDB returned status %d", resp.StatusCode)
	}

	// 解析响应
	type queryResponse struct {
		IDs        [][]string         `json:"ids"`
		Embeddings [][][]float32      `json:"embeddings"`
		Documents  [][]string         `json:"documents"`
		Metadatas  [][]map[string]any `json:"metadatas"`
		Distances  [][]float32        `json:"distances"`
	}

	var result queryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// 转换为文档
	var docs []vector.Document
	if len(result.IDs) > 0 {
		for i := range result.IDs[0] {
			doc := vector.Document{
				ID:      result.IDs[0][i],
				Content: result.Documents[0][i],
			}

			if len(result.Metadatas[0]) > i {
				doc.Metadata = result.Metadatas[0][i]
			}

			if len(result.Embeddings[0]) > i && cfg.IncludeEmbedding {
				doc.Embedding = result.Embeddings[0][i]
			}

			// 转换距离为分数 (越小越好 -> 越大越好)
			if len(result.Distances[0]) > i {
				doc.Score = 1.0 / (1.0 + result.Distances[0][i])
			}

			// 过滤低分文档
			if doc.Score >= cfg.MinScore {
				docs = append(docs, doc)
			}
		}
	}

	return docs, nil
}

// Get 获取文档
func (s *Store) Get(ctx context.Context, id string) (*vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("not connected to ChromaDB")
	}

	// ChromaDB 的 get 端点
	type getRequest struct {
		IDs []string `json:"ids"`
	}

	req := getRequest{
		IDs: []string{id},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("http://%s:%d/api/v1/collections/%s/get", s.host, s.port, s.collection)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ChromaDB returned status %d: %s", resp.StatusCode, string(body))
	}

	// 解析响应
	type getResponse struct {
		IDs        []string         `json:"ids"`
		Embeddings [][]float32      `json:"embeddings"`
		Documents  []string         `json:"documents"`
		Metadatas  []map[string]any `json:"metadatas"`
	}

	var result getResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// 检查是否找到文档
	if len(result.IDs) == 0 {
		return nil, fmt.Errorf("document not found: %s", id)
	}

	doc := &vector.Document{
		ID:      result.IDs[0],
		Content: result.Documents[0],
	}

	if len(result.Embeddings) > 0 {
		doc.Embedding = result.Embeddings[0]
	}

	if len(result.Metadatas) > 0 {
		doc.Metadata = result.Metadatas[0]
	}

	return doc, nil
}

// Delete 删除文档
func (s *Store) Delete(ctx context.Context, ids []string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return fmt.Errorf("not connected to ChromaDB")
	}

	// 准备请求
	type deleteRequest struct {
		IDs []string `json:"ids"`
	}

	req := deleteRequest{
		IDs: ids,
	}

	// 发送请求
	url := fmt.Sprintf("http://%s:%d/api/v1/collections/%s/delete",
		s.host, s.port, s.collection)

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ChromaDB returned status %d", resp.StatusCode)
	}

	return nil
}

// Update 更新文档
func (s *Store) Update(ctx context.Context, docs []vector.Document) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return fmt.Errorf("not connected to ChromaDB")
	}

	// ChromaDB 的 update 需要完整的文档信息
	type updateRequest struct {
		IDs        []string         `json:"ids"`
		Embeddings [][]float32      `json:"embeddings"`
		Metadatas  []map[string]any `json:"metadatas"`
		Documents  []string         `json:"documents"`
	}

	req := updateRequest{
		IDs:        make([]string, len(docs)),
		Embeddings: make([][]float32, len(docs)),
		Metadatas:  make([]map[string]any, len(docs)),
		Documents:  make([]string, len(docs)),
	}

	for i, doc := range docs {
		req.IDs[i] = doc.ID
		req.Embeddings[i] = doc.Embedding
		req.Metadatas[i] = doc.Metadata
		req.Documents[i] = doc.Content
	}

	// 发送请求
	url := fmt.Sprintf("http://%s:%d/api/v1/collections/%s/update",
		s.host, s.port, s.collection)

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ChromaDB returned status %d", resp.StatusCode)
	}

	return nil
}

// Count 统计文档数量
func (s *Store) Count(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return 0, fmt.Errorf("not connected to ChromaDB")
	}

	// 获取集合信息
	url := fmt.Sprintf("http://%s:%d/api/v1/collections/%s",
		s.host, s.port, s.collection)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}

	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("ChromaDB returned status %d", resp.StatusCode)
	}

	// 解析响应
	var result struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Count, nil
}

// Clear 清空所有文档
func (s *Store) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return fmt.Errorf("not connected to ChromaDB")
	}

	// ChromaDB 没有直接清空集合的 API，需要删除并重建
	// 方案1：删除集合然后重建
	// 方案2：获取所有 ID 然后批量删除
	// 这里使用方案1（更高效）

	// 删除集合
	url := fmt.Sprintf("http://%s:%d/api/v1/collections/%s", s.host, s.port, s.collection)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete collection: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete collection (status %d): %s", resp.StatusCode, string(body))
	}

	// 重新创建集合
	if err := s.createCollection(ctx); err != nil {
		return fmt.Errorf("failed to recreate collection: %w", err)
	}

	return nil
}

// Close 关闭连接
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return nil
	}

	s.connected = false
	return nil
}

// Stats 获取统计信息
func (s *Store) Stats(ctx context.Context) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("not connected to ChromaDB")
	}

	count, err := s.Count(ctx)
	if err != nil {
		return nil, err
	}

	stats := map[string]any{
		"collection":      s.collection,
		"host":            s.host,
		"port":            s.port,
		"distance_metric": s.distanceMetric,
		"count":           count,
		"connected":       s.connected,
		"timestamp":       time.Now(),
	}

	return stats, nil
}

// 确保实现了 vector.Store 接口
var _ vector.Store = (*Store)(nil)
