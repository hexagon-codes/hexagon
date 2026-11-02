// Package memory 为宿主显式作用域提供固定的 Run 记忆视图与历史来源回读。
// 记忆只提供历史上下文，不替代业务数据库或权威服务中的当前事实。
package memory

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	coremem "github.com/hexagon-codes/ai-core/memory"
	"github.com/hexagon-codes/hexagon/memory/store"
	"github.com/hexagon-codes/toolkit/util/hash"
	"github.com/hexagon-codes/toolkit/util/idgen"
)

var (
	ErrScopeMismatch       = errors.New("memory: scope does not match")
	ErrRevisionUnavailable = errors.New("memory: original revision is unavailable")
)

// Scope 由宿主提供，不推断用户、项目或会话，不扩大既有无作用域端口的范围。
type Scope struct {
	Namespace []string `json:"namespace"`
}

func (s Scope) clone() Scope           { return Scope{Namespace: append([]string(nil), s.Namespace...)} }
func (s Scope) equal(other Scope) bool { return slices.Equal(s.Namespace, other.Namespace) }
func (s Scope) validate() error {
	if len(s.Namespace) == 0 {
		return fmt.Errorf("memory: an explicit namespace is required")
	}
	return nil
}

// Record 分开表达来源、持久记录修订、ai-core 的语义谱系版本和历史锚点。
// ValidUntil 只在新的 Capture 边界判断；已取得的 Run 视图不会随共享更新原位改变。
type Record struct {
	Scope    Scope  `json:"scope"`
	Source   string `json:"source"`
	Revision uint64 `json:"revision"`
	// Unversioned 显式标记旧 Memory 端口没有持久修订；快照仍由内容摘要固定原文。
	Unversioned bool           `json:"unversioned,omitempty"`
	Entry       coremem.Entry  `json:"entry"`
	Anchor      *HistoryAnchor `json:"anchor,omitempty"`
	ValidUntil  *time.Time     `json:"valid_until,omitempty"`
}

// ScopedMemory 复用注入的 Store，不创建后台工作，不拥有底层 Store 的关闭责任。
// 所有记录写入都经过 ConditionalStore；原 MemoryStore 的无条件 Put 不用于此命名空间。
type ScopedMemory struct {
	store store.MemoryStore
	scope Scope
}

// NewScopedMemory 只装配显式作用域和已有 Store，不隐式创建工作或外部连接。
func NewScopedMemory(storage store.MemoryStore, scope Scope) (*ScopedMemory, error) {
	if storage == nil {
		return nil, fmt.Errorf("memory: store is required")
	}
	if err := scope.validate(); err != nil {
		return nil, err
	}
	return &ScopedMemory{store: storage, scope: scope.clone()}, nil
}

// Scope 返回宿主作用域的独立副本。
func (m *ScopedMemory) Scope() Scope { return m.scope.clone() }

func (m *ScopedMemory) namespace(kind string) []string {
	data, _ := json.Marshal(m.scope.Namespace)
	return []string{"hexagon-memory", kind, base64.RawURLEncoding.EncodeToString(data)}
}

// Save 只在持久修订与 expected 一致时保存，0 表示仅创建新条目。
// 修订冲突不会自动读取最新版后覆盖。来源由宿主显式声明，不借元数据推断业务授权。
func (m *ScopedMemory) Save(ctx context.Context, record Record, expected uint64) (Record, error) {
	conditional, ok := m.store.(store.ConditionalStore)
	if !ok {
		return Record{}, fmt.Errorf("memory: store does not support conditional updates")
	}
	if !record.Scope.equal(m.scope) {
		return Record{}, ErrScopeMismatch
	}
	if record.Source == "" {
		return Record{}, fmt.Errorf("memory: record source is required")
	}
	if expected == ^uint64(0) {
		return Record{}, fmt.Errorf("memory: revision overflow")
	}
	if record.Anchor != nil && !record.Anchor.Scope.equal(m.scope) {
		return Record{}, ErrScopeMismatch
	}
	if record.Entry.ID == "" {
		record.Entry.ID = "mem-" + idgen.NanoID()
	}
	now := time.Now().UTC()
	if record.Entry.CreatedAt.IsZero() {
		record.Entry.CreatedAt = now
	}
	record.Entry.UpdatedAt = now
	record.Revision = expected + 1
	record.Unversioned = false
	value, err := encodeRecord(record)
	if err != nil {
		return Record{}, err
	}
	item, err := conditional.PutIfRevision(ctx, m.namespace("records"), recordKey(record.Entry.ID), value, expected)
	if err != nil {
		return Record{}, err
	}
	return decodeRecord(item, m.scope)
}

// Get 读取当前持久记录；已捕获的 Snapshot 不重新调用它。
func (m *ScopedMemory) Get(ctx context.Context, id string) (Record, error) {
	item, err := m.store.Get(ctx, m.namespace("records"), recordKey(id))
	if err != nil {
		return Record{}, err
	}
	if item == nil {
		return Record{}, coremem.ErrNotFound
	}
	return decodeRecord(item, m.scope)
}

type snapshotDocument struct {
	Format     int       `json:"format"`
	Scope      Scope     `json:"scope"`
	CapturedAt time.Time `json:"captured_at"`
	Records    []Record  `json:"records"`
	Revision   string    `json:"revision"`
}

// Snapshot 是选中输入的固定副本；共享存储更新与本次 Run 视图分离。
// 序列化包含选中原文与来源，不依赖恢复时重新搜索最新条目。
type Snapshot struct{ document snapshotDocument }

// Scope 返回固定输入的作用域副本。
func (s *Snapshot) Scope() Scope { return s.document.Scope.clone() }

// Revision 返回包含选中原文和来源的快照摘要。
func (s *Snapshot) Revision() string { return s.document.Revision }

// CapturedAt 返回本次输入冻结时间，不随共享更新改变。
func (s *Snapshot) CapturedAt() time.Time { return s.document.CapturedAt }

// Records 返回独立来源记录，修改返回值不改变冻结输入。
func (s *Snapshot) Records() []Record {
	data, _ := json.Marshal(s.document.Records)
	var records []Record
	_ = decodeJSONNumbers(data, &records)
	return records
}

// Entries 返回本次固定上下文，不自动刷新或采用共享存储中的新修订。
func (s *Snapshot) Entries() []coremem.Entry {
	records := s.Records()
	entries := make([]coremem.Entry, len(records))
	for i, record := range records {
		entries[i] = record.Entry
	}
	return entries
}

func (s *Snapshot) MarshalJSON() ([]byte, error) { return json.Marshal(s.document) }

// UnmarshalJSON 验证内容本身；宿主恢复运行时使用 DecodeSnapshot 核对预期 Scope。
func (s *Snapshot) UnmarshalJSON(data []byte) error {
	var document snapshotDocument
	if err := decodeJSONNumbers(data, &document); err != nil {
		return fmt.Errorf("memory: failed to decode snapshot: %w", err)
	}
	if err := validateSnapshot(document); err != nil {
		return err
	}
	s.document = document
	return nil
}

// FreezeEntries 在宿主声明的独占作用域内冻结旧 Memory 端口选中的输入。
// 该便捷适配不声称旧后端具备条件更新或跨用户隔离；记录明确标记 Unversioned。
func FreezeEntries(scope Scope, source string, entries []coremem.Entry) (*Snapshot, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if source == "" {
		return nil, fmt.Errorf("memory: snapshot source is required")
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.Stale {
			continue
		}
		records = append(records, Record{Scope: scope.clone(), Source: source, Entry: entry, Unversioned: true})
	}
	document := snapshotDocument{Format: 1, Scope: scope.clone(), CapturedAt: time.Now().UTC(), Records: records}
	document.Revision = snapshotRevision(document)
	data, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("memory: snapshot cannot be encoded: %w", err)
	}
	return DecodeSnapshot(data, scope)
}

// DecodeSnapshot 验证持久快照格式、作用域和内容修订，失败不回退到当前共享记忆。
func DecodeSnapshot(data []byte, scope Scope) (*Snapshot, error) {
	var document snapshotDocument
	if err := decodeJSONNumbers(data, &document); err != nil {
		return nil, fmt.Errorf("memory: failed to decode snapshot: %w", err)
	}
	if !document.Scope.equal(scope) {
		return nil, ErrScopeMismatch
	}
	if err := validateSnapshot(document); err != nil {
		return nil, err
	}
	return &Snapshot{document: document}, nil
}

// Capture 在明确边界选中作用域内的有效记录并冻结来源与修订。
// 这是选中条目的固定视图，不宣称不同键之间具有 SQL 全局快照隔离。
func (m *ScopedMemory) Capture(ctx context.Context, query coremem.SearchQuery) (*Snapshot, error) {
	records, err := m.records(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	selected := make([]Record, 0, len(records))
	for _, record := range records {
		if record.Entry.Stale || record.ValidUntil != nil && !record.ValidUntil.After(now) || !matchesRecord(record, query) {
			continue
		}
		selected = append(selected, record)
	}
	sort.Slice(selected, func(i, j int) bool {
		a, b := selected[i].Entry, selected[j].Entry
		less := a.CreatedAt.Before(b.CreatedAt)
		if a.CreatedAt.Equal(b.CreatedAt) {
			less = a.ID < b.ID
		}
		if query.OrderDesc {
			return !less && a.ID != b.ID
		}
		return less
	})
	start := max(0, query.Offset)
	if start > len(selected) {
		start = len(selected)
	}
	selected = selected[start:]
	if query.Limit > 0 && len(selected) > query.Limit {
		selected = selected[:query.Limit]
	}
	document := snapshotDocument{Format: 1, Scope: m.scope.clone(), CapturedAt: now, Records: selected}
	document.Revision = snapshotRevision(document)
	data, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("memory: snapshot cannot be encoded: %w", err)
	}
	return DecodeSnapshot(data, m.scope)
}

func (m *ScopedMemory) records(ctx context.Context) ([]Record, error) {
	namespace := m.namespace("records")
	items, err := m.store.List(ctx, namespace, store.WithListLimit(0))
	if err != nil {
		return nil, err
	}
	result := make([]Record, 0, len(items))
	for _, item := range items {
		if !slices.Equal(item.Namespace, namespace) {
			continue
		}
		record, err := decodeRecord(item, m.scope)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func encodeRecord(record Record) (map[string]any, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("memory: record cannot be encoded: %w", err)
	}
	return map[string]any{"record": string(data), "content": record.Entry.Content, "source": record.Source}, nil
}

func decodeRecord(item *store.Item, scope Scope) (Record, error) {
	if item == nil {
		return Record{}, fmt.Errorf("memory: store returned no saved record")
	}
	data, ok := item.Value["record"].(string)
	if !ok {
		return Record{}, fmt.Errorf("memory: record payload is missing")
	}
	var record Record
	if err := decodeJSONNumbers([]byte(data), &record); err != nil {
		return Record{}, fmt.Errorf("memory: record cannot be decoded: %w", err)
	}
	if !record.Scope.equal(scope) {
		return Record{}, ErrScopeMismatch
	}
	if record.Source == "" || record.Entry.ID == "" || item.Key != recordKey(record.Entry.ID) || record.Revision == 0 || record.Revision != item.Revision {
		return Record{}, fmt.Errorf("memory: invalid record revision or source")
	}
	if record.Anchor != nil && !record.Anchor.Scope.equal(scope) {
		return Record{}, ErrScopeMismatch
	}
	return record, nil
}

func recordKey(id string) string { return "record-" + hash.SHA256(id) }

// 解码动态元数据时保留数字字面值，避免记录和快照往返改变数值及内容摘要。
func decodeJSONNumbers(data []byte, target any) error {
	if !json.Valid(data) {
		return json.Unmarshal(data, target)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func snapshotRevision(document snapshotDocument) string {
	document.Revision = ""
	data, _ := json.Marshal(document)
	return hash.SHA256Bytes(data)
}

func validateSnapshot(document snapshotDocument) error {
	if err := document.Scope.validate(); err != nil {
		return err
	}
	if document.Format != 1 || document.CapturedAt.IsZero() || document.Revision == "" || document.Revision != snapshotRevision(document) {
		return fmt.Errorf("memory: invalid snapshot format or revision")
	}
	for _, record := range document.Records {
		if !record.Scope.equal(document.Scope) {
			return ErrScopeMismatch
		}
		if record.Source == "" || record.Entry.ID == "" || (record.Revision == 0) != record.Unversioned || record.Entry.Stale || record.ValidUntil != nil && !record.ValidUntil.After(document.CapturedAt) {
			return fmt.Errorf("memory: invalid selected record")
		}
		if record.Anchor != nil && !record.Anchor.Scope.equal(document.Scope) {
			return ErrScopeMismatch
		}
	}
	return nil
}

func matchesRecord(record Record, query coremem.SearchQuery) bool {
	entry := record.Entry
	if query.Query != "" && !strings.Contains(strings.ToLower(entry.Content), strings.ToLower(query.Query)) {
		return false
	}
	if len(query.Roles) > 0 && !slices.Contains(query.Roles, entry.Role) {
		return false
	}
	if query.Since != nil && entry.CreatedAt.Before(*query.Since) || query.Until != nil && entry.CreatedAt.After(*query.Until) {
		return false
	}
	if query.CauseBy != "" && query.CauseBy != entry.CauseBy {
		return false
	}
	for key, value := range query.Metadata {
		stored, exists := entry.Metadata[key]
		if !exists {
			return false
		}
		actual, actualErr := json.Marshal(stored)
		expected, expectedErr := json.Marshal(value)
		if actualErr != nil || expectedErr != nil || !bytes.Equal(actual, expected) {
			return false
		}
	}
	return true
}
