// Package indexer 提供文档索引功能
//
// 本文件实现 SQL 索引：
//   - 结构化数据索引
//   - SQL 查询接口
//   - 表结构自动推断
//   - 查询优化
//
// 设计借鉴：
//   - LlamaIndex: SQL Index
//   - LangChain: SQL Database
//   - DuckDB: 嵌入式分析
package indexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// ============== SQL Index ==============

// SQLIndex SQL 索引
type SQLIndex struct {
	// db 数据库连接
	db *sql.DB

	// tables 表信息
	tables map[string]*TableInfo

	// config 配置
	config SQLIndexConfig

	mu sync.RWMutex
}

// SQLIndexConfig SQL 索引配置
type SQLIndexConfig struct {
	// Driver 数据库驱动
	Driver string

	// DSN 数据源名称
	DSN string

	// MaxOpenConns 最大连接数
	MaxOpenConns int

	// MaxIdleConns 最大空闲连接数
	MaxIdleConns int

	// TablePrefix 表前缀
	TablePrefix string

	// AutoMigrate 自动迁移
	AutoMigrate bool
}

// TableInfo 表信息
type TableInfo struct {
	// Name 表名
	Name string `json:"name"`

	// Columns 列信息
	Columns []*ColumnInfo `json:"columns"`

	// PrimaryKey 主键
	PrimaryKey string `json:"primary_key,omitempty"`

	// Indexes 索引
	Indexes []*IndexInfo `json:"indexes,omitempty"`

	// Description 表描述
	Description string `json:"description,omitempty"`

	// RowCount 行数（估算）
	RowCount int64 `json:"row_count,omitempty"`
}

// ColumnInfo 列信息
type ColumnInfo struct {
	// Name 列名
	Name string `json:"name"`

	// Type 数据类型
	Type string `json:"type"`

	// Nullable 是否可空
	Nullable bool `json:"nullable"`

	// Default 默认值
	Default *string `json:"default,omitempty"`

	// Description 列描述
	Description string `json:"description,omitempty"`

	// IsPrimaryKey 是否主键
	IsPrimaryKey bool `json:"is_primary_key,omitempty"`

	// ForeignKey 外键引用
	ForeignKey *ForeignKeyInfo `json:"foreign_key,omitempty"`
}

// ForeignKeyInfo 外键信息
type ForeignKeyInfo struct {
	// Table 引用表
	Table string `json:"table"`

	// Column 引用列
	Column string `json:"column"`
}

// IndexInfo 索引信息
type IndexInfo struct {
	// Name 索引名
	Name string `json:"name"`

	// Columns 索引列
	Columns []string `json:"columns"`

	// Unique 是否唯一
	Unique bool `json:"unique"`
}

// NewSQLIndex 创建 SQL 索引
func NewSQLIndex(db *sql.DB, config ...SQLIndexConfig) *SQLIndex {
	cfg := SQLIndexConfig{}
	if len(config) > 0 {
		cfg = config[0]
	}

	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}

	return &SQLIndex{
		db:     db,
		tables: make(map[string]*TableInfo),
		config: cfg,
	}
}

// LoadSchema 加载数据库 Schema
func (idx *SQLIndex) LoadSchema(ctx context.Context) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// 获取所有表
	tables, err := idx.getTables(ctx)
	if err != nil {
		return err
	}

	for _, tableName := range tables {
		info, err := idx.getTableInfo(ctx, tableName)
		if err != nil {
			continue // 跳过无法获取信息的表
		}
		idx.tables[tableName] = info
	}

	return nil
}

// getTables 获取所有表名
func (idx *SQLIndex) getTables(ctx context.Context) ([]string, error) {
	// 通用 SQL，可能需要根据数据库类型调整
	query := `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		AND table_type = 'BASE TABLE'
	`

	rows, err := idx.db.QueryContext(ctx, query)
	if err != nil {
		// 尝试 SQLite 格式
		query = `SELECT name FROM sqlite_master WHERE type='table'`
		rows, err = idx.db.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		tables = append(tables, name)
	}

	return tables, nil
}

// getTableInfo 获取表信息
func (idx *SQLIndex) getTableInfo(ctx context.Context, tableName string) (*TableInfo, error) {
	info := &TableInfo{
		Name:    tableName,
		Columns: make([]*ColumnInfo, 0),
	}

	// 获取列信息
	query := fmt.Sprintf(`
		SELECT column_name, data_type, is_nullable, column_default, column_key
		FROM information_schema.columns
		WHERE table_name = '%s'
		ORDER BY ordinal_position
	`, tableName)

	rows, err := idx.db.QueryContext(ctx, query)
	if err != nil {
		// 尝试 PRAGMA for SQLite
		query = fmt.Sprintf(`PRAGMA table_info(%s)`, tableName)
		rows, err = idx.db.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}

		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, colType string
			var notNull, pk int
			var dfltValue sql.NullString

			if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
				continue
			}

			col := &ColumnInfo{
				Name:         name,
				Type:         colType,
				Nullable:     notNull == 0,
				IsPrimaryKey: pk == 1,
			}
			if dfltValue.Valid {
				col.Default = &dfltValue.String
			}
			info.Columns = append(info.Columns, col)

			if pk == 1 {
				info.PrimaryKey = name
			}
		}

		return info, nil
	}

	defer rows.Close()
	for rows.Next() {
		var name, dataType, nullable string
		var dfltValue sql.NullString
		var columnKey string

		if err := rows.Scan(&name, &dataType, &nullable, &dfltValue, &columnKey); err != nil {
			continue
		}

		col := &ColumnInfo{
			Name:         name,
			Type:         dataType,
			Nullable:     nullable == "YES",
			IsPrimaryKey: columnKey == "PRI",
		}
		if dfltValue.Valid {
			col.Default = &dfltValue.String
		}
		info.Columns = append(info.Columns, col)

		if columnKey == "PRI" {
			info.PrimaryKey = name
		}
	}

	return info, nil
}

// GetTableInfo 获取表信息
func (idx *SQLIndex) GetTableInfo(tableName string) (*TableInfo, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	info, ok := idx.tables[tableName]
	return info, ok
}

// GetAllTables 获取所有表
func (idx *SQLIndex) GetAllTables() []*TableInfo {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	tables := make([]*TableInfo, 0, len(idx.tables))
	for _, info := range idx.tables {
		tables = append(tables, info)
	}
	return tables
}

// ============== 查询执行 ==============

// QueryResult 查询结果
type QueryResult struct {
	// Columns 列名
	Columns []string `json:"columns"`

	// Rows 行数据
	Rows []map[string]any `json:"rows"`

	// RowCount 行数
	RowCount int `json:"row_count"`

	// Query 执行的查询
	Query string `json:"query,omitempty"`
}

// Query 执行查询
func (idx *SQLIndex) Query(ctx context.Context, query string, args ...any) (*QueryResult, error) {
	rows, err := idx.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	// 获取列信息
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	result := &QueryResult{
		Columns: columns,
		Rows:    make([]map[string]any, 0),
		Query:   query,
	}

	// 读取行
	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			continue
		}

		row := make(map[string]any)
		for i, col := range columns {
			row[col] = values[i]
		}
		result.Rows = append(result.Rows, row)
	}

	result.RowCount = len(result.Rows)
	return result, nil
}

// Execute 执行非查询语句
func (idx *SQLIndex) Execute(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return idx.db.ExecContext(ctx, query, args...)
}

// ============== 自然语言查询 ==============

// NLQuery 自然语言查询接口
type NLQuery interface {
	// ToSQL 转换为 SQL
	ToSQL(ctx context.Context, question string, tables []*TableInfo) (string, error)
}

// QueryBuilder 查询构建器
type QueryBuilder struct {
	// table 目标表
	table string

	// columns 选择的列
	columns []string

	// conditions WHERE 条件
	conditions []string

	// orderBy 排序
	orderBy string

	// limit 限制
	limit int

	// offset 偏移
	offset int

	// joins JOIN 子句
	joins []string

	// groupBy GROUP BY
	groupBy string

	// having HAVING
	having string
}

// NewQueryBuilder 创建查询构建器
func NewQueryBuilder(table string) *QueryBuilder {
	return &QueryBuilder{
		table:   table,
		columns: []string{"*"},
	}
}

// Select 选择列
//
// 不传任何列名时退回默认的 "*", 避免 Build 生成非法 SQL "SELECT  FROM t"。
func (b *QueryBuilder) Select(columns ...string) *QueryBuilder {
	if len(columns) == 0 {
		b.columns = []string{"*"}
		return b
	}
	b.columns = columns
	return b
}

// Where 添加条件
func (b *QueryBuilder) Where(condition string) *QueryBuilder {
	b.conditions = append(b.conditions, condition)
	return b
}

// OrderBy 排序
func (b *QueryBuilder) OrderBy(order string) *QueryBuilder {
	b.orderBy = order
	return b
}

// Limit 限制数量
func (b *QueryBuilder) Limit(limit int) *QueryBuilder {
	b.limit = limit
	return b
}

// Offset 偏移
func (b *QueryBuilder) Offset(offset int) *QueryBuilder {
	b.offset = offset
	return b
}

// Join 添加 JOIN
func (b *QueryBuilder) Join(join string) *QueryBuilder {
	b.joins = append(b.joins, join)
	return b
}

// GroupBy 分组
func (b *QueryBuilder) GroupBy(group string) *QueryBuilder {
	b.groupBy = group
	return b
}

// Having HAVING 条件
func (b *QueryBuilder) Having(having string) *QueryBuilder {
	b.having = having
	return b
}

// Build 构建 SQL
func (b *QueryBuilder) Build() string {
	var sb strings.Builder

	// SELECT
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(b.columns, ", "))

	// FROM
	sb.WriteString(" FROM ")
	sb.WriteString(b.table)

	// JOIN
	for _, join := range b.joins {
		sb.WriteString(" ")
		sb.WriteString(join)
	}

	// WHERE
	if len(b.conditions) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(b.conditions, " AND "))
	}

	// GROUP BY
	if b.groupBy != "" {
		sb.WriteString(" GROUP BY ")
		sb.WriteString(b.groupBy)
	}

	// HAVING
	if b.having != "" {
		sb.WriteString(" HAVING ")
		sb.WriteString(b.having)
	}

	// ORDER BY
	if b.orderBy != "" {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(b.orderBy)
	}

	// LIMIT
	if b.limit > 0 {
		sb.WriteString(fmt.Sprintf(" LIMIT %d", b.limit))
	}

	// OFFSET
	if b.offset > 0 {
		sb.WriteString(fmt.Sprintf(" OFFSET %d", b.offset))
	}

	return sb.String()
}

// ============== 数据导入 ==============

// ImportOptions 导入选项
type ImportOptions struct {
	// BatchSize 批量大小
	BatchSize int

	// CreateTable 自动创建表
	CreateTable bool

	// TruncateFirst 先清空表
	TruncateFirst bool

	// OnConflict 冲突处理
	OnConflict string // "ignore", "update", "error"
}

// DefaultImportOptions 默认导入选项
var DefaultImportOptions = ImportOptions{
	BatchSize:   1000,
	CreateTable: true,
	OnConflict:  "error",
}

// Import 导入数据
func (idx *SQLIndex) Import(ctx context.Context, tableName string, data []map[string]any, opts ...ImportOptions) error {
	if len(data) == 0 {
		return nil
	}

	opt := DefaultImportOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	// 推断表结构
	columns := inferColumns(data[0])

	// 创建表
	if opt.CreateTable {
		if err := idx.createTable(ctx, tableName, columns); err != nil {
			// 表可能已存在，继续
		}
	}

	// 清空表
	if opt.TruncateFirst {
		_, _ = idx.Execute(ctx, fmt.Sprintf("DELETE FROM %s", tableName))
	}

	// 批量插入
	colNames := make([]string, 0, len(columns))
	for col := range columns {
		colNames = append(colNames, col)
	}

	for i := 0; i < len(data); i += opt.BatchSize {
		end := i + opt.BatchSize
		if end > len(data) {
			end = len(data)
		}

		batch := data[i:end]
		if err := idx.insertBatch(ctx, tableName, colNames, batch); err != nil {
			return err
		}
	}

	return nil
}

// inferColumns 从数据推断列
func inferColumns(row map[string]any) map[string]string {
	columns := make(map[string]string)

	for col, val := range row {
		columns[col] = inferType(val)
	}

	return columns
}

// inferType 推断数据类型
func inferType(val any) string {
	if val == nil {
		return "TEXT"
	}

	v := reflect.ValueOf(val)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "INTEGER"
	case reflect.Float32, reflect.Float64:
		return "REAL"
	case reflect.Bool:
		return "BOOLEAN"
	default:
		return "TEXT"
	}
}

// createTable 创建表
func (idx *SQLIndex) createTable(ctx context.Context, tableName string, columns map[string]string) error {
	var colDefs []string
	for col, colType := range columns {
		colDefs = append(colDefs, fmt.Sprintf("%s %s", col, colType))
	}

	query := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", tableName, strings.Join(colDefs, ", "))
	_, err := idx.Execute(ctx, query)
	return err
}

// insertBatch 批量插入
func (idx *SQLIndex) insertBatch(ctx context.Context, tableName string, columns []string, data []map[string]any) error {
	if len(data) == 0 {
		return nil
	}

	// 构建 INSERT 语句
	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = "?"
	}
	rowPlaceholder := "(" + strings.Join(placeholders, ", ") + ")"

	rowPlaceholders := make([]string, len(data))
	var values []any

	for i, row := range data {
		rowPlaceholders[i] = rowPlaceholder
		for _, col := range columns {
			values = append(values, row[col])
		}
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES %s",
		tableName,
		strings.Join(columns, ", "),
		strings.Join(rowPlaceholders, ", "),
	)

	_, err := idx.Execute(ctx, query, values...)
	return err
}

// ============== Schema 描述生成 ==============

// GenerateSchemaDescription 生成 Schema 描述（用于 LLM）
func (idx *SQLIndex) GenerateSchemaDescription() string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("Database Schema:\n\n")

	for _, table := range idx.tables {
		sb.WriteString(fmt.Sprintf("Table: %s\n", table.Name))
		if table.Description != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", table.Description))
		}
		sb.WriteString("Columns:\n")

		for _, col := range table.Columns {
			nullable := ""
			if !col.Nullable {
				nullable = " NOT NULL"
			}
			pk := ""
			if col.IsPrimaryKey {
				pk = " PRIMARY KEY"
			}
			sb.WriteString(fmt.Sprintf("  - %s: %s%s%s\n", col.Name, col.Type, nullable, pk))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// ExportSchema 导出 Schema（JSON 格式）
func (idx *SQLIndex) ExportSchema() ([]byte, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return json.MarshalIndent(idx.tables, "", "  ")
}

// Close 关闭连接
func (idx *SQLIndex) Close() error {
	return idx.db.Close()
}
