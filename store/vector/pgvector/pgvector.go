// Package pgvector 提供 PostgreSQL pgvector 扩展的向量存储集成
//
// pgvector 是 PostgreSQL 的开源向量搜索扩展，适合已有 PostgreSQL 基础设施的团队。
//
// 特性：
//   - 基于成熟的 PostgreSQL 生态
//   - 支持 IVFFlat 和 HNSW 索引
//   - 精确和近似最近邻搜索
//   - 事务支持，ACID 保证
//   - 与现有 SQL 数据无缝结合
//
// 前置条件：
//   - PostgreSQL 15+
//   - pgvector 扩展 (CREATE EXTENSION vector)
//
// 使用示例：
//
//	store, err := pgvector.NewStore(ctx,
//	    pgvector.WithDSN("postgres://user:pass@localhost:5432/mydb"),
//	    pgvector.WithTable("embeddings"),
//	    pgvector.WithDimension(1536),
//	)
//	defer store.Close()
//
//	// 添加文档
//	store.Add(ctx, docs)
//
//	// 搜索
//	results, err := store.Search(ctx, queryEmbedding, 5)
package pgvector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// DistanceMetric 距离度量方式
type DistanceMetric string

const (
	// DistanceCosine 余弦距离（默认）
	DistanceCosine DistanceMetric = "cosine"
	// DistanceL2 欧几里得距离（L2）
	DistanceL2 DistanceMetric = "l2"
	// DistanceInnerProduct 内积距离
	DistanceInnerProduct DistanceMetric = "inner_product"
)

// IndexType 索引类型
type IndexType string

const (
	// IndexIVFFlat IVFFlat 索引（适合中小规模数据）
	IndexIVFFlat IndexType = "ivfflat"
	// IndexHNSW HNSW 索引（适合大规模数据，推荐）
	IndexHNSW IndexType = "hnsw"
	// IndexNone 不创建索引（精确搜索）
	IndexNone IndexType = "none"
)

// Store pgvector 向量存储
type Store struct {
	db        *sql.DB
	dsn       string
	table     string
	dimension int
	distance  DistanceMetric
	indexType IndexType

	mu     sync.RWMutex
	closed bool
}

// Option 配置选项
type Option func(*Store)

// WithDSN 设置 PostgreSQL 连接字符串
func WithDSN(dsn string) Option {
	return func(s *Store) {
		s.dsn = dsn
	}
}

// WithDB 设置已有的数据库连接
func WithDB(db *sql.DB) Option {
	return func(s *Store) {
		s.db = db
	}
}

// WithTable 设置表名
func WithTable(table string) Option {
	return func(s *Store) {
		s.table = table
	}
}

// WithDimension 设置向量维度
func WithDimension(dim int) Option {
	return func(s *Store) {
		s.dimension = dim
	}
}

// WithDistance 设置距离度量方式
func WithDistance(d DistanceMetric) Option {
	return func(s *Store) {
		s.distance = d
	}
}

// WithIndex 设置索引类型
func WithIndex(idx IndexType) Option {
	return func(s *Store) {
		s.indexType = idx
	}
}

// NewStore 创建 pgvector 向量存储
func NewStore(ctx context.Context, opts ...Option) (*Store, error) {
	s := &Store{
		table:     "vector_store",
		dimension: 1536,
		distance:  DistanceCosine,
		indexType: IndexHNSW,
	}

	for _, opt := range opts {
		opt(s)
	}

	// 如果没有传入已有连接，通过 DSN 创建
	if s.db == nil {
		if s.dsn == "" {
			return nil, fmt.Errorf("必须提供 DSN 或 DB 连接")
		}
		db, err := sql.Open("postgres", s.dsn)
		if err != nil {
			return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
		}
		if err := db.PingContext(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("PostgreSQL 连接测试失败: %w", err)
		}
		s.db = db
	}

	// 初始化表结构
	if err := s.initTable(ctx); err != nil {
		return nil, err
	}

	return s, nil
}

// initTable 初始化数据库表
func (s *Store) initTable(ctx context.Context) error {
	// 确保 pgvector 扩展已启用
	_, err := s.db.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		return fmt.Errorf("启用 pgvector 扩展失败: %w", err)
	}

	// 创建表
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			embedding vector(%d),
			metadata JSONB DEFAULT '{}',
			created_at TIMESTAMP DEFAULT NOW()
		)
	`, s.table, s.dimension)

	if _, err := s.db.ExecContext(ctx, createSQL); err != nil {
		return fmt.Errorf("创建表失败: %w", err)
	}

	// 创建索引
	if s.indexType != IndexNone {
		if err := s.createIndex(ctx); err != nil {
			return fmt.Errorf("创建索引失败: %w", err)
		}
	}

	return nil
}

// createIndex 创建向量索引
func (s *Store) createIndex(ctx context.Context) error {
	indexName := fmt.Sprintf("idx_%s_embedding", s.table)

	var distOp string
	switch s.distance {
	case DistanceCosine:
		distOp = "vector_cosine_ops"
	case DistanceL2:
		distOp = "vector_l2_ops"
	case DistanceInnerProduct:
		distOp = "vector_ip_ops"
	default:
		distOp = "vector_cosine_ops"
	}

	var indexSQL string
	switch s.indexType {
	case IndexHNSW:
		indexSQL = fmt.Sprintf(
			"CREATE INDEX IF NOT EXISTS %s ON %s USING hnsw (embedding %s)",
			indexName, s.table, distOp,
		)
	case IndexIVFFlat:
		indexSQL = fmt.Sprintf(
			"CREATE INDEX IF NOT EXISTS %s ON %s USING ivfflat (embedding %s) WITH (lists = 100)",
			indexName, s.table, distOp,
		)
	default:
		return nil
	}

	_, err := s.db.ExecContext(ctx, indexSQL)
	return err
}

// Add 添加文档
func (s *Store) Add(ctx context.Context, docs []vector.Document) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	if len(docs) == 0 {
		return nil
	}

	// 使用批量插入
	var valueStrings []string
	var args []any
	argIdx := 1

	for _, doc := range docs {
		metadata, _ := json.Marshal(doc.Metadata)
		embeddingStr := embeddingToString(doc.Embedding)

		valueStrings = append(valueStrings,
			fmt.Sprintf("($%d, $%d, $%d, $%d)", argIdx, argIdx+1, argIdx+2, argIdx+3))
		args = append(args, doc.ID, doc.Content, embeddingStr, string(metadata))
		argIdx += 4
	}

	insertSQL := fmt.Sprintf(`
		INSERT INTO %s (id, content, embedding, metadata)
		VALUES %s
		ON CONFLICT (id) DO UPDATE SET
			content = EXCLUDED.content,
			embedding = EXCLUDED.embedding,
			metadata = EXCLUDED.metadata
	`, s.table, strings.Join(valueStrings, ", "))

	_, err := s.db.ExecContext(ctx, insertSQL, args...)
	if err != nil {
		return fmt.Errorf("插入文档失败: %w", err)
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

	embeddingStr := embeddingToString(embedding)

	// 构建距离运算符
	var distOp string
	switch s.distance {
	case DistanceCosine:
		distOp = "<=>"
	case DistanceL2:
		distOp = "<->"
	case DistanceInnerProduct:
		distOp = "<#>"
	default:
		distOp = "<=>"
	}

	// 构建查询
	querySQL := fmt.Sprintf(`
		SELECT id, content, metadata, embedding %s $1 AS distance
		FROM %s
	`, distOp, s.table)

	// 添加过滤条件
	args := []any{embeddingStr}
	argIdx := 2
	if len(filter) > 0 {
		var conditions []string
		for key, value := range filter {
			conditions = append(conditions,
				fmt.Sprintf("metadata->>'%s' = $%d", key, argIdx))
			args = append(args, fmt.Sprintf("%v", value))
			argIdx++
		}
		querySQL += " WHERE " + strings.Join(conditions, " AND ")
	}

	querySQL += fmt.Sprintf(" ORDER BY embedding %s $1 LIMIT $%d", distOp, argIdx)
	args = append(args, topK)

	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("搜索失败: %w", err)
	}
	defer rows.Close()

	var docs []vector.Document
	for rows.Next() {
		var doc vector.Document
		var metadataStr string
		var distance float64

		if err := rows.Scan(&doc.ID, &doc.Content, &metadataStr, &distance); err != nil {
			return nil, fmt.Errorf("扫描结果失败: %w", err)
		}

		if metadataStr != "" {
			json.Unmarshal([]byte(metadataStr), &doc.Metadata)
		}

		// 将距离转换为相似度分数 (0-1)
		doc.Score = distanceToScore(distance, s.distance)
		docs = append(docs, doc)
	}

	return docs, rows.Err()
}

// Delete 删除文档
func (s *Store) Delete(ctx context.Context, ids []string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	if len(ids) == 0 {
		return nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE id IN (%s)",
		s.table, strings.Join(placeholders, ", "))

	_, err := s.db.ExecContext(ctx, deleteSQL, args...)
	return err
}

// Clear 清空存储
func (s *Store) Clear(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	_, err := s.db.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s", s.table))
	return err
}

// Count 返回文档数量
func (s *Store) Count(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return 0, fmt.Errorf("存储已关闭")
	}

	var count int
	err := s.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s", s.table)).Scan(&count)
	return count, err
}

// Close 关闭存储
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// ============== 辅助函数 ==============

// embeddingToString 将向量转换为 pgvector 格式字符串
func embeddingToString(embedding []float32) string {
	parts := make([]string, len(embedding))
	for i, v := range embedding {
		parts[i] = fmt.Sprintf("%f", v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// distanceToScore 将距离转换为相似度分数
func distanceToScore(distance float64, metric DistanceMetric) float32 {
	switch metric {
	case DistanceCosine:
		// 余弦距离范围 0-2，转换为相似度 1-(-1)
		return float32(1 - distance)
	case DistanceL2:
		// L2 距离越小越相似
		return float32(1 / (1 + distance))
	case DistanceInnerProduct:
		// 内积距离取负值
		return float32(-distance)
	default:
		return float32(1 - distance)
	}
}
