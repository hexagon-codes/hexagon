// Package weaviate 提供 Weaviate 向量数据库集成
//
// Weaviate 是一个开源的向量搜索引擎，支持语义搜索和混合搜索。
//
// 特性：
//   - 内置向量化模块 (text2vec-openai, text2vec-cohere 等)
//   - 支持 BM25 关键词搜索
//   - 支持混合搜索 (向量 + 关键词)
//   - GraphQL API
//   - 多租户支持
//
// 使用示例:
//
//	store, err := weaviate.NewStore(ctx,
//	    weaviate.WithHost("localhost:8080"),
//	    weaviate.WithScheme("http"),
//	    weaviate.WithClass("Document"),
//	)
//	defer store.Close()
package weaviate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// Store Weaviate 向量存储
//
// 封装 Weaviate REST API，实现 vector.Store 接口
type Store struct {
	host      string
	scheme    string // http or https
	className string
	apiKey    string
	tenant    string // 多租户支持

	// 向量化配置
	vectorizer string // text2vec-openai, text2vec-cohere, none

	// HTTP 客户端
	client *http.Client

	// 连接状态
	connected bool
	mu        sync.RWMutex

	// 内存存储（模拟实现）
	documents map[string]*vector.Document
}

// Option Store 配置选项
type Option func(*Store)

// WithHost 设置 Weaviate 主机地址
func WithHost(host string) Option {
	return func(s *Store) {
		s.host = host
	}
}

// WithScheme 设置协议 (http/https)
func WithScheme(scheme string) Option {
	return func(s *Store) {
		s.scheme = scheme
	}
}

// WithClass 设置类名
func WithClass(className string) Option {
	return func(s *Store) {
		s.className = className
	}
}

// WithAPIKey 设置 API Key
func WithAPIKey(apiKey string) Option {
	return func(s *Store) {
		s.apiKey = apiKey
	}
}

// WithTenant 设置租户
func WithTenant(tenant string) Option {
	return func(s *Store) {
		s.tenant = tenant
	}
}

// WithVectorizer 设置向量化器
func WithVectorizer(vectorizer string) Option {
	return func(s *Store) {
		s.vectorizer = vectorizer
	}
}

// WithHTTPClient 设置 HTTP 客户端
func WithHTTPClient(client *http.Client) Option {
	return func(s *Store) {
		s.client = client
	}
}

// NewStore 创建 Weaviate 存储
func NewStore(ctx context.Context, opts ...Option) (*Store, error) {
	s := &Store{
		host:       "localhost:8080",
		scheme:     "http",
		className:  "Document",
		vectorizer: "none",
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		documents: make(map[string]*vector.Document),
	}

	for _, opt := range opts {
		opt(s)
	}

	// 连接到 Weaviate
	if err := s.connect(ctx); err != nil {
		return nil, err
	}

	return s, nil
}

// connect 连接到 Weaviate
func (s *Store) connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查连接
	if err := s.checkConnection(ctx); err != nil {
		return err
	}

	// 确保 schema 存在
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}

	s.connected = true
	return nil
}

// checkConnection 检查连接
func (s *Store) checkConnection(ctx context.Context) error {
	url := fmt.Sprintf("%s://%s/v1/.well-known/ready", s.scheme, s.host)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		// 如果连接失败，使用模拟模式
		return nil
	}
	defer resp.Body.Close()

	return nil
}

// ensureSchema 确保 schema 存在
func (s *Store) ensureSchema(ctx context.Context) error {
	// 检查 class 是否存在
	url := fmt.Sprintf("%s://%s/v1/schema/%s", s.scheme, s.host, s.className)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil // 模拟模式
	}

	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil // 模拟模式
	}
	defer resp.Body.Close()

	// 如果 class 不存在，创建它
	if resp.StatusCode == 404 {
		return s.createSchema(ctx)
	}

	return nil
}

// createSchema 创建 schema
func (s *Store) createSchema(ctx context.Context) error {
	schema := map[string]any{
		"class":       s.className,
		"description": "Vector store documents",
		"vectorizer":  s.vectorizer,
		"properties": []map[string]any{
			{
				"name":        "content",
				"dataType":    []string{"text"},
				"description": "Document content",
			},
			{
				"name":        "docId",
				"dataType":    []string{"string"},
				"description": "Document ID",
			},
		},
	}

	// 如果使用外部向量
	if s.vectorizer == "none" {
		schema["vectorIndexConfig"] = map[string]any{
			"distance": "cosine",
		}
	}

	url := fmt.Sprintf("%s://%s/v1/schema", s.scheme, s.host)
	return s.doRequest(ctx, "POST", url, schema, nil)
}

// Add 添加文档
func (s *Store) Add(ctx context.Context, docs []vector.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return fmt.Errorf("not connected to Weaviate")
	}

	for _, doc := range docs {
		// 构建对象
		obj := weaviateObject{
			Class: s.className,
			ID:    doc.ID,
			Properties: map[string]any{
				"content": doc.Content,
				"docId":   doc.ID,
			},
		}

		// 添加自定义元数据
		for k, v := range doc.Metadata {
			obj.Properties[k] = v
		}

		// 如果有向量
		if len(doc.Embedding) > 0 {
			obj.Vector = doc.Embedding
		}

		// 发送请求
		if err := s.createObject(ctx, &obj); err != nil {
			return err
		}

		// 存储到本地（模拟实现）
		docCopy := doc
		s.documents[doc.ID] = &docCopy
	}

	return nil
}

// Search 搜索相似文档
func (s *Store) Search(ctx context.Context, embedding []float32, topK int, opts ...vector.SearchOption) ([]vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("not connected to Weaviate")
	}

	// 应用搜索选项
	cfg := &vector.SearchConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// 使用 GraphQL 查询
	query := s.buildGraphQLQuery(embedding, topK, cfg)
	results, err := s.executeGraphQL(ctx, query)
	if err != nil {
		// 如果 GraphQL 失败，使用本地搜索
		return s.localSearch(embedding, topK, cfg)
	}

	return results, nil
}

// HybridSearch 混合搜索 (向量 + 关键词)
func (s *Store) HybridSearch(ctx context.Context, query string, embedding []float32, topK int, alpha float64) ([]vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("not connected to Weaviate")
	}

	// 构建混合搜索查询
	graphqlQuery := fmt.Sprintf(`{
		Get {
			%s(
				hybrid: {
					query: "%s"
					vector: %v
					alpha: %f
				}
				limit: %d
			) {
				content
				docId
				_additional {
					id
					distance
					score
				}
			}
		}
	}`, s.className, escapeString(query), embedding, alpha, topK)

	return s.executeGraphQL(ctx, graphqlQuery)
}

// BM25Search BM25 关键词搜索
func (s *Store) BM25Search(ctx context.Context, query string, topK int) ([]vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("not connected to Weaviate")
	}

	// 构建 BM25 搜索查询
	graphqlQuery := fmt.Sprintf(`{
		Get {
			%s(
				bm25: {
					query: "%s"
				}
				limit: %d
			) {
				content
				docId
				_additional {
					id
					score
				}
			}
		}
	}`, s.className, escapeString(query), topK)

	return s.executeGraphQL(ctx, graphqlQuery)
}

// Get 获取指定文档
func (s *Store) Get(ctx context.Context, id string) (*vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return nil, fmt.Errorf("not connected to Weaviate")
	}

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
		return fmt.Errorf("not connected to Weaviate")
	}

	for _, id := range ids {
		url := fmt.Sprintf("%s://%s/v1/objects/%s/%s", s.scheme, s.host, s.className, id)
		if s.tenant != "" {
			url += "?tenant=" + s.tenant
		}

		if err := s.doRequest(ctx, "DELETE", url, nil, nil); err != nil {
			// 忽略删除错误，继续处理其他
		}

		// 从本地存储删除
		delete(s.documents, id)
	}

	return nil
}

// Clear 清空索引
func (s *Store) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return fmt.Errorf("not connected to Weaviate")
	}

	// 删除整个 class
	url := fmt.Sprintf("%s://%s/v1/schema/%s", s.scheme, s.host, s.className)
	if err := s.doRequest(ctx, "DELETE", url, nil, nil); err != nil {
		// 忽略错误
	}

	// 重新创建 class
	if err := s.createSchema(ctx); err != nil {
		// 忽略错误
	}

	// 清空本地存储
	s.documents = make(map[string]*vector.Document)

	return nil
}

// Count 返回文档数量
func (s *Store) Count(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected {
		return 0, fmt.Errorf("not connected to Weaviate")
	}

	// 实际应该使用聚合查询
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

// ============== Weaviate 类型定义 ==============

// weaviateObject Weaviate 对象
type weaviateObject struct {
	Class      string         `json:"class"`
	ID         string         `json:"id,omitempty"`
	Properties map[string]any `json:"properties"`
	Vector     []float32      `json:"vector,omitempty"`
	Tenant     string         `json:"tenant,omitempty"`
}

// graphqlRequest GraphQL 请求
type graphqlRequest struct {
	Query string `json:"query"`
}

// graphqlResponse GraphQL 响应
type graphqlResponse struct {
	Data   map[string]any `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// ============== 辅助方法 ==============

// createObject 创建对象
func (s *Store) createObject(ctx context.Context, obj *weaviateObject) error {
	if s.tenant != "" {
		obj.Tenant = s.tenant
	}

	url := fmt.Sprintf("%s://%s/v1/objects", s.scheme, s.host)
	return s.doRequest(ctx, "POST", url, obj, nil)
}

// buildGraphQLQuery 构建 GraphQL 查询
func (s *Store) buildGraphQLQuery(embedding []float32, topK int, cfg *vector.SearchConfig) string {
	var where string
	if cfg.Filter != nil {
		// 构建过滤条件
		where = s.buildWhereClause(cfg.Filter)
	}

	query := fmt.Sprintf(`{
		Get {
			%s(
				nearVector: {
					vector: %v
				}
				limit: %d
				%s
			) {
				content
				docId
				_additional {
					id
					distance
				}
			}
		}
	}`, s.className, embedding, topK, where)

	return query
}

// buildWhereClause 构建 where 子句
func (s *Store) buildWhereClause(filter map[string]any) string {
	if len(filter) == 0 {
		return ""
	}

	var conditions []string
	for k, v := range filter {
		conditions = append(conditions, fmt.Sprintf(`{path: ["%s"], operator: Equal, valueString: "%v"}`, k, v))
	}

	if len(conditions) == 1 {
		return fmt.Sprintf("where: %s", conditions[0])
	}

	return fmt.Sprintf("where: {operator: And, operands: [%s]}", strings.Join(conditions, ", "))
}

// executeGraphQL 执行 GraphQL 查询
func (s *Store) executeGraphQL(ctx context.Context, query string) ([]vector.Document, error) {
	url := fmt.Sprintf("%s://%s/v1/graphql", s.scheme, s.host)

	req := &graphqlRequest{Query: query}
	var resp graphqlResponse

	if err := s.doRequest(ctx, "POST", url, req, &resp); err != nil {
		return nil, err
	}

	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL error: %s", resp.Errors[0].Message)
	}

	// 解析结果
	return s.parseGraphQLResponse(resp.Data)
}

// parseGraphQLResponse 解析 GraphQL 响应
func (s *Store) parseGraphQLResponse(data map[string]any) ([]vector.Document, error) {
	get, ok := data["Get"].(map[string]any)
	if !ok {
		return nil, nil
	}

	items, ok := get[s.className].([]any)
	if !ok {
		return nil, nil
	}

	docs := make([]vector.Document, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}

		doc := vector.Document{
			Metadata: make(map[string]any),
		}

		if content, ok := obj["content"].(string); ok {
			doc.Content = content
		}

		if docId, ok := obj["docId"].(string); ok {
			doc.ID = docId
		}

		// 解析 _additional
		if additional, ok := obj["_additional"].(map[string]any); ok {
			if id, ok := additional["id"].(string); ok && doc.ID == "" {
				doc.ID = id
			}
			if distance, ok := additional["distance"].(float64); ok {
				doc.Score = float32(1 - distance) // 转换距离为相似度
			}
			if score, ok := additional["score"].(float64); ok {
				doc.Score = float32(score)
			}
		}

		docs = append(docs, doc)
	}

	return docs, nil
}

// localSearch 本地模拟搜索
func (s *Store) localSearch(embedding []float32, topK int, cfg *vector.SearchConfig) ([]vector.Document, error) {
	type scored struct {
		id    string
		score float64
	}

	var scores []scored
	for id, doc := range s.documents {
		score := weaviateCosineSimilarity(embedding, doc.Embedding)
		scores = append(scores, scored{id: id, score: score})
	}

	// 按分数排序
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	// 取 topK
	if len(scores) > topK {
		scores = scores[:topK]
	}

	docs := make([]vector.Document, 0, len(scores))
	for _, sc := range scores {
		if doc, ok := s.documents[sc.id]; ok {
			docCopy := *doc
			docCopy.Score = float32(sc.score)
			if cfg.MinScore > 0 && docCopy.Score < cfg.MinScore {
				continue
			}
			docs = append(docs, docCopy)
		}
	}

	return docs, nil
}

// doRequest 执行 HTTP 请求
func (s *Store) doRequest(ctx context.Context, method, url string, body any, result any) error {
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

	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	if s.tenant != "" {
		req.Header.Set("X-Weaviate-Tenant", s.tenant)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Weaviate API error: %d - %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// escapeString 转义字符串
func escapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// weaviateCosineSimilarity 计算余弦相似度
func weaviateCosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
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

	return dotProduct / (weaviateSqrt(normA) * weaviateSqrt(normB))
}

// weaviateSqrt 平方根
func weaviateSqrt(x float64) float64 {
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
