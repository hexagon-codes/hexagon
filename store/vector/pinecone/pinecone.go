// Package pinecone 提供 Pinecone 向量数据库集成
//
// Pinecone 是一个托管的向量数据库服务，专为生产级向量搜索而设计。
//
// 特性：
//   - 完全托管，无需运维
//   - 亚毫秒级查询延迟
//   - 自动扩缩容
//   - 支持元数据过滤
//   - 支持命名空间隔离
//
// 使用示例:
//
//	store, err := pinecone.NewStore(ctx,
//	    pinecone.WithAPIKey("your-api-key"),
//	    pinecone.WithEnvironment("us-west1-gcp"),
//	    pinecone.WithIndex("my-index"),
//	)
//	defer store.Close()
package pinecone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// 默认配置常量
const (
	// DefaultDimension 默认向量维度（OpenAI text-embedding-ada-002）
	DefaultDimension = 1536

	// DefaultMetric 默认距离度量
	DefaultMetric = "cosine"

	// DefaultHTTPTimeout 默认 HTTP 超时时间
	DefaultHTTPTimeout = 30 * time.Second

	// DefaultMaxRetries 默认最大重试次数
	DefaultMaxRetries = 100

	// DefaultRetryDelay 默认重试延迟
	DefaultRetryDelay = 100 * time.Millisecond
)

// Store Pinecone 向量存储
//
// 封装 Pinecone REST API，实现 vector.Store 接口
type Store struct {
	apiKey      string
	environment string
	indexName   string
	namespace   string
	dimension   int
	metric      string // cosine, euclidean, dotproduct
	host        string // 索引的主机地址

	// HTTP 客户端
	client *http.Client

	// 连接状态
	connected bool
	mu        sync.RWMutex

	// 内存存储（模拟实现）
	// 生产环境应该替换为真实的 Pinecone API 调用
	documents map[string]*vector.Document
}

// Option Store 配置选项
type Option func(*Store)

// WithAPIKey 设置 API Key
func WithAPIKey(apiKey string) Option {
	return func(s *Store) {
		s.apiKey = apiKey
	}
}

// WithEnvironment 设置环境
func WithEnvironment(env string) Option {
	return func(s *Store) {
		s.environment = env
	}
}

// WithIndex 设置索引名称
func WithIndex(index string) Option {
	return func(s *Store) {
		s.indexName = index
	}
}

// WithNamespace 设置命名空间
func WithNamespace(namespace string) Option {
	return func(s *Store) {
		s.namespace = namespace
	}
}

// WithDimension 设置向量维度
func WithDimension(dim int) Option {
	return func(s *Store) {
		s.dimension = dim
	}
}

// WithMetric 设置距离度量
// 支持: cosine, euclidean, dotproduct
func WithMetric(metric string) Option {
	return func(s *Store) {
		s.metric = metric
	}
}

// WithHost 设置索引主机地址
func WithHost(host string) Option {
	return func(s *Store) {
		s.host = host
	}
}

// WithHTTPClient 设置 HTTP 客户端
func WithHTTPClient(client *http.Client) Option {
	return func(s *Store) {
		s.client = client
	}
}

// NewStore 创建 Pinecone 存储
func NewStore(ctx context.Context, opts ...Option) (*Store, error) {
	s := &Store{
		dimension: DefaultDimension,
		metric:    DefaultMetric,
		namespace: "",
		client: &http.Client{
			Timeout: DefaultHTTPTimeout,
		},
		documents: make(map[string]*vector.Document),
	}

	for _, opt := range opts {
		opt(s)
	}

	if s.apiKey == "" {
		return nil, fmt.Errorf("Pinecone API key is required")
	}

	if s.indexName == "" {
		return nil, fmt.Errorf("Pinecone index name is required")
	}

	// 连接到 Pinecone
	if err := s.connect(ctx); err != nil {
		return nil, err
	}

	return s, nil
}

// connect 连接到 Pinecone
func (s *Store) connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 如果没有指定 host，尝试从索引描述中获取
	if s.host == "" && s.environment != "" {
		// 实际实现应该调用 Pinecone API 获取索引信息
		s.host = fmt.Sprintf("%s-%s.svc.%s.pinecone.io", s.indexName, s.environment[:8], s.environment)
	}

	s.connected = true
	return nil
}

// Add 添加文档
func (s *Store) Add(ctx context.Context, docs []vector.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return fmt.Errorf("not connected to Pinecone")
	}

	// 构建 upsert 请求
	vectors := make([]pineconeVector, len(docs))
	for i, doc := range docs {
		vectors[i] = pineconeVector{
			ID:       doc.ID,
			Values:   doc.Embedding,
			Metadata: s.buildMetadata(doc),
		}
		// 存储到本地（模拟实现）
		docCopy := doc
		s.documents[doc.ID] = &docCopy
	}

	// 发送 upsert 请求
	return s.upsertVectors(ctx, vectors)
}

// Search 搜索相似文档
func (s *Store) Search(ctx context.Context, embedding []float32, topK int, opts ...vector.SearchOption) ([]vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("not connected to Pinecone")
	}

	// 应用搜索选项
	cfg := &vector.SearchConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// 构建查询请求
	queryReq := &queryRequest{
		Namespace:       s.namespace,
		Vector:          embedding,
		TopK:            topK,
		IncludeValues:   cfg.IncludeEmbedding,
		IncludeMetadata: true,
	}

	// 如果有过滤条件
	if cfg.Filter != nil {
		queryReq.Filter = cfg.Filter
	}

	// 发送查询请求
	results, err := s.queryVectors(ctx, queryReq)
	if err != nil {
		return nil, err
	}

	// 转换结果
	docs := make([]vector.Document, 0, len(results))
	for _, match := range results {
		// 从本地存储获取完整文档（模拟实现）
		if doc, ok := s.documents[match.ID]; ok {
			docCopy := *doc
			docCopy.Score = float32(match.Score)
			if match.Values != nil && cfg.IncludeEmbedding {
				docCopy.Embedding = match.Values
			}
			if cfg.MinScore > 0 && docCopy.Score < cfg.MinScore {
				continue
			}
			docs = append(docs, docCopy)
		}
	}

	return docs, nil
}

// Get 获取指定文档
func (s *Store) Get(ctx context.Context, id string) (*vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("not connected to Pinecone")
	}

	// 从本地存储获取（模拟实现）
	if doc, ok := s.documents[id]; ok {
		return doc, nil
	}

	return nil, fmt.Errorf("document not found: %s", id)
}

// Delete 删除文档
func (s *Store) Delete(ctx context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return fmt.Errorf("not connected to Pinecone")
	}

	// 构建删除请求
	deleteReq := &deleteRequest{
		Namespace: s.namespace,
		IDs:       ids,
	}

	// 发送删除请求
	if err := s.deleteVectors(ctx, deleteReq); err != nil {
		return err
	}

	// 从本地存储删除（模拟实现）
	for _, id := range ids {
		delete(s.documents, id)
	}

	return nil
}

// Clear 清空索引
func (s *Store) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return fmt.Errorf("not connected to Pinecone")
	}

	// 删除命名空间中的所有向量
	deleteReq := &deleteRequest{
		Namespace: s.namespace,
		DeleteAll: true,
	}

	if err := s.deleteVectors(ctx, deleteReq); err != nil {
		return err
	}

	// 清空本地存储（模拟实现）
	s.documents = make(map[string]*vector.Document)

	return nil
}

// Count 返回文档数量
func (s *Store) Count(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return 0, fmt.Errorf("not connected to Pinecone")
	}

	// 实际应该调用 describe_index_stats API
	return len(s.documents), nil
}

// Close 关闭连接
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.connected = false
	return nil
}

var _ vector.Store = (*Store)(nil)

// ============== Pinecone API 类型 ==============

// pineconeVector Pinecone 向量格式
type pineconeVector struct {
	ID       string         `json:"id"`
	Values   []float32      `json:"values"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// upsertRequest Upsert 请求
type upsertRequest struct {
	Vectors   []pineconeVector `json:"vectors"`
	Namespace string           `json:"namespace,omitempty"`
}

// queryRequest 查询请求
type queryRequest struct {
	Namespace       string         `json:"namespace,omitempty"`
	Vector          []float32      `json:"vector"`
	TopK            int            `json:"topK"`
	Filter          map[string]any `json:"filter,omitempty"`
	IncludeValues   bool           `json:"includeValues"`
	IncludeMetadata bool           `json:"includeMetadata"`
}

// queryMatch 查询匹配结果
type queryMatch struct {
	ID       string         `json:"id"`
	Score    float64        `json:"score"`
	Values   []float32      `json:"values,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// queryResponse 查询响应
type queryResponse struct {
	Matches   []queryMatch `json:"matches"`
	Namespace string       `json:"namespace"`
}

// deleteRequest 删除请求
type deleteRequest struct {
	Namespace string   `json:"namespace,omitempty"`
	IDs       []string `json:"ids,omitempty"`
	DeleteAll bool     `json:"deleteAll,omitempty"`
}

// ============== API 调用方法 ==============

// buildMetadata 构建元数据
func (s *Store) buildMetadata(doc vector.Document) map[string]any {
	metadata := make(map[string]any)
	if doc.Content != "" {
		metadata["content"] = doc.Content
	}
	for k, v := range doc.Metadata {
		metadata[k] = v
	}
	return metadata
}

// upsertVectors 上传向量
func (s *Store) upsertVectors(ctx context.Context, vectors []pineconeVector) error {
	if s.host == "" {
		// 模拟实现：直接返回成功
		return nil
	}

	body := &upsertRequest{
		Vectors:   vectors,
		Namespace: s.namespace,
	}

	return s.doRequest(ctx, "POST", "/vectors/upsert", body, nil)
}

// queryVectors 查询向量
func (s *Store) queryVectors(ctx context.Context, req *queryRequest) ([]queryMatch, error) {
	if s.host == "" {
		// 模拟实现：使用本地搜索
		return s.localSearch(req)
	}

	var resp queryResponse
	if err := s.doRequest(ctx, "POST", "/query", req, &resp); err != nil {
		return nil, err
	}

	return resp.Matches, nil
}

// deleteVectors 删除向量
func (s *Store) deleteVectors(ctx context.Context, req *deleteRequest) error {
	if s.host == "" {
		// 模拟实现：直接返回成功
		return nil
	}

	return s.doRequest(ctx, "POST", "/vectors/delete", req, nil)
}

// doRequest 执行 HTTP 请求
func (s *Store) doRequest(ctx context.Context, method, path string, body any, result any) error {
	url := fmt.Sprintf("https://%s%s", s.host, path)

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Api-Key", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Pinecone API error: %d - %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// localSearch 本地模拟搜索
func (s *Store) localSearch(req *queryRequest) ([]queryMatch, error) {
	type scored struct {
		id    string
		score float64
	}

	var scores []scored
	for id, doc := range s.documents {
		score := cosineSimilarity(req.Vector, doc.Embedding)
		scores = append(scores, scored{id: id, score: score})
	}

	// 按分数排序
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	// 取 topK
	if len(scores) > req.TopK {
		scores = scores[:req.TopK]
	}

	matches := make([]queryMatch, len(scores))
	for i, sc := range scores {
		doc := s.documents[sc.id]
		matches[i] = queryMatch{
			ID:       sc.id,
			Score:    sc.score,
			Metadata: doc.Metadata,
		}
		if req.IncludeValues {
			matches[i].Values = doc.Embedding
		}
	}

	return matches, nil
}

// cosineSimilarity 计算余弦相似度
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (sqrt(normA) * sqrt(normB))
}

// sqrt 平方根
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 100; i++ {
		z = (z + x/z) / 2
		if (z*z - x) < 1e-10 {
			break
		}
	}
	return z
}
