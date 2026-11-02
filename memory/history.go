package memory

import (
	"context"
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
)

// HistoryAnchor 定位固定原文版本中的消息，不把压缩摘要当成原文或最新业务事实。
type HistoryAnchor struct {
	Scope     Scope  `json:"scope"`
	Source    string `json:"source"`
	HistoryID string `json:"history_id"`
	MessageID string `json:"message_id"`
	Revision  string `json:"revision"`
}

type historyDocument struct {
	Scope     Scope           `json:"scope"`
	Source    string          `json:"source"`
	HistoryID string          `json:"history_id"`
	Messages  []coremem.Entry `json:"messages"`
}

// HistoryHit 是发现结果，不自动将摘要或过期事实采用进 Run 上下文。
type HistoryHit struct {
	Anchor   HistoryAnchor `json:"anchor"`
	EntryID  string        `json:"entry_id"`
	Revision uint64        `json:"revision"`
	Summary  string        `json:"summary"`
	Stale    bool          `json:"stale"`
}

// SaveHistory 保存宿主提供的原文版本，复用 Store 的独立不可变键。
// 它只是来源证据副本，不写业务真相或执行进度；摘要登记是另一个显式写入。
func (m *ScopedMemory) SaveHistory(ctx context.Context, id, source, messageID string, messages []coremem.Entry) (HistoryAnchor, error) {
	conditional, ok := m.store.(store.ConditionalStore)
	if !ok {
		return HistoryAnchor{}, fmt.Errorf("memory: store does not support conditional updates")
	}
	if id == "" || source == "" || messageID == "" {
		return HistoryAnchor{}, fmt.Errorf("memory: history identity and source are required")
	}
	seen := make(map[string]bool, len(messages))
	for _, message := range messages {
		if message.ID == "" || seen[message.ID] {
			return HistoryAnchor{}, fmt.Errorf("memory: history message identity is missing or duplicated")
		}
		seen[message.ID] = true
	}
	if !seen[messageID] {
		return HistoryAnchor{}, fmt.Errorf("memory: anchor message is not in history")
	}
	document := historyDocument{Scope: m.scope.clone(), Source: source, HistoryID: id, Messages: messages}
	data, err := json.Marshal(document)
	if err != nil {
		return HistoryAnchor{}, fmt.Errorf("memory: history cannot be encoded: %w", err)
	}
	revision := hash.SHA256Bytes(data)
	anchor := HistoryAnchor{Scope: m.scope.clone(), Source: source, HistoryID: id, MessageID: messageID, Revision: revision}
	value := map[string]any{"document": string(data), "revision": revision}
	_, err = conditional.PutIfRevision(ctx, m.namespace("history"), historyKey(anchor), value, 0)
	if errors.Is(err, store.ErrRevisionConflict) {
		existing, getErr := m.store.Get(ctx, m.namespace("history"), historyKey(anchor))
		if getErr != nil {
			return HistoryAnchor{}, getErr
		}
		if existing == nil || existing.Value["document"] != string(data) {
			return HistoryAnchor{}, ErrRevisionUnavailable
		}
		err = nil
	}
	return anchor, err
}

// DiscoverHistory 只搜索本作用域的摘要与锚点，原文窗口需显式 ReadWindow。
// 过时结果仍可用于追溯，但 Stale 会被标出，Capture 不会自动采用它们。
func (m *ScopedMemory) DiscoverHistory(ctx context.Context, query string, limit int) ([]HistoryHit, error) {
	records, err := m.records(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Entry.UpdatedAt.Equal(records[j].Entry.UpdatedAt) {
			return records[i].Entry.ID < records[j].Entry.ID
		}
		return records[i].Entry.UpdatedAt.After(records[j].Entry.UpdatedAt)
	})
	now := time.Now()
	var hits []HistoryHit
	for _, record := range records {
		if record.Anchor == nil || !strings.Contains(strings.ToLower(record.Entry.Content), strings.ToLower(query)) {
			continue
		}
		anchor := *record.Anchor
		anchor.Scope = anchor.Scope.clone()
		stale := record.Entry.Stale || record.ValidUntil != nil && !record.ValidUntil.After(now)
		hits = append(hits, HistoryHit{Anchor: anchor, EntryID: record.Entry.ID, Revision: record.Revision, Summary: record.Entry.Content, Stale: stale})
		if limit > 0 && len(hits) >= limit {
			break
		}
	}
	return hits, nil
}

// ReadWindow 按 Scope 与原修订回读锚点前后消息，缺失或被覆盖明确报错。
func (m *ScopedMemory) ReadWindow(ctx context.Context, anchor HistoryAnchor, before, after int) ([]coremem.Entry, error) {
	if !anchor.Scope.equal(m.scope) {
		return nil, ErrScopeMismatch
	}
	if before < 0 || after < 0 {
		return nil, fmt.Errorf("memory: history window cannot be negative")
	}
	item, err := m.store.Get(ctx, m.namespace("history"), historyKey(anchor))
	if err != nil {
		return nil, err
	}
	if item == nil || !slices.Equal(item.Namespace, m.namespace("history")) {
		return nil, ErrRevisionUnavailable
	}
	data, ok := item.Value["document"].(string)
	if !ok || hash.SHA256(data) != anchor.Revision {
		return nil, ErrRevisionUnavailable
	}
	var document historyDocument
	if err := json.Unmarshal([]byte(data), &document); err != nil {
		return nil, fmt.Errorf("memory: history cannot be decoded: %w", err)
	}
	if !document.Scope.equal(m.scope) {
		return nil, ErrScopeMismatch
	}
	if document.HistoryID != anchor.HistoryID || document.Source != anchor.Source {
		return nil, ErrRevisionUnavailable
	}
	for i, message := range document.Messages {
		if message.ID == anchor.MessageID {
			start := max(0, i-before)
			end := len(document.Messages)
			if after < len(document.Messages)-i-1 {
				end = i + after + 1
			}
			return document.Messages[start:end], nil
		}
	}
	return nil, ErrRevisionUnavailable
}

func historyKey(anchor HistoryAnchor) string {
	return "history-" + hash.SHA256(anchor.HistoryID) + "-" + anchor.Revision
}
