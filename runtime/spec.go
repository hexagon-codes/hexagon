package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

const RunSpecVersion = "runtime.runspec.v1"
const SnapshotCodecVersion = "runtime.snapshot.v1"

var ErrUnrecoverableSpec = errors.New("runtime: run specification cannot be restored")

// StateCodec 固定状态类型与编码版本，不使用进程内类型断言恢复 JSON map。
type StateCodec struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}

// ContentReference 保存不可变内容或稳定对象引用；摘要不能独立承担恢复。
// 内嵌 JSON 使用 ContentDigest 的稳定表示摘要；对象引用使用宿主保存的实际字节摘要。
type ContentReference struct {
	Kind      string          `json:"kind"`
	ID        string          `json:"id"`
	Revision  string          `json:"revision"`
	Digest    string          `json:"digest"`
	Content   json.RawMessage `json:"content,omitempty"`
	ObjectKey string          `json:"object_key,omitempty"`
}

// RunSpec 保存可读配置及资源版本，Request 不包含 Strategy/Provider 等 Go 资源句柄。
// 宿主按 Strategy 名称和资源引用重建依赖，无法取得原版本时显式失败。
type RunSpec struct {
	Version string `json:"version"`
	RunID   string `json:"run_id"`
	// IngressDigest 固定准备资源之前的调用输入，重复提交不重新绑定 latest。
	IngressDigest     string             `json:"ingress_digest,omitempty"`
	ParentRunID       string             `json:"parent_run_id,omitempty"`
	Request           json.RawMessage    `json:"request"`
	Strategy          string             `json:"strategy"`
	StrategyReference ContentReference   `json:"strategy_reference"`
	Codec             StateCodec         `json:"codec"`
	Resources         []ContentReference `json:"resources,omitempty"`
	// SelectedProvider 由持久宿主在接受前解析并冻结，不用请求别名冒充实际路由。
	SelectedProvider *ProviderRoute `json:"selected_provider,omitempty"`
}

type ProviderRoute struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}

func (s RunSpec) Validate() error {
	if s.Version != RunSpecVersion || s.RunID == "" || s.Codec.Type == "" || s.Codec.Version == "" || len(s.Request) == 0 || !json.Valid(s.Request) || s.Strategy == "" {
		return ErrUnrecoverableSpec
	}
	if s.StrategyReference.Kind != "strategy" || s.StrategyReference.ID != s.Strategy {
		return fmt.Errorf("%w: strategy version is missing", ErrUnrecoverableSpec)
	}
	for _, resource := range append([]ContentReference{s.StrategyReference}, s.Resources...) {
		if resource.Kind == "" || resource.ID == "" || resource.Revision == "" || resource.Digest == "" || (len(resource.Content) == 0 && resource.ObjectKey == "") {
			return fmt.Errorf("%w: incomplete content reference", ErrUnrecoverableSpec)
		}
		if len(resource.Content) > 0 {
			digest, err := ContentDigest(resource.Content)
			if err != nil || digest != resource.Digest {
				return fmt.Errorf("%w: content digest mismatch", ErrUnrecoverableSpec)
			}
		}
	}
	return nil
}

type frozenRequest struct {
	Request
	Strategy      any                          `json:"Strategy,omitempty"`
	Continuations []durableMessageContinuation `json:"message_continuations,omitempty"`
}

// FreezeRunSpec 复制可序列化请求并排除运行期句柄，保存私有 continuation。
func FreezeRunSpec(request Request, resources []ContentReference) (RunSpec, error) {
	resources = append([]ContentReference(nil), resources...)
	for index := range resources {
		resource := &resources[index]
		if len(resource.Content) == 0 {
			continue
		}
		canonical, err := stableJSONContent(resource.Content)
		if err != nil {
			return RunSpec{}, fmt.Errorf("%w: invalid inline content", ErrUnrecoverableSpec)
		}
		rawSum, canonicalSum := sha256.Sum256(resource.Content), sha256.Sum256(canonical)
		digest := hex.EncodeToString(canonicalSum[:])
		if resource.Digest != digest && resource.Digest != hex.EncodeToString(rawSum[:]) {
			return RunSpec{}, fmt.Errorf("%w: content digest mismatch", ErrUnrecoverableSpec)
		}
		resource.Content, resource.Digest = canonical, digest
	}
	strategyValue := strategyOrDefault(request.Strategy)
	var strategy string
	if err := invokeExtension("strategy.name", func() error { strategy = strategyValue.Name(); return nil }); err != nil {
		return RunSpec{}, err
	}
	var strategyReference ContentReference
	switch strategyValue.(type) {
	case NoopStrategy, *NoopStrategy:
		strategyReference = builtinStrategyReference()
	default:
		for _, resource := range resources {
			if resource.Kind == "strategy" && resource.ID == strategy {
				strategyReference = resource
				break
			}
		}
		if strategyReference.ID == "" {
			return RunSpec{}, fmt.Errorf("%w: custom strategy requires immutable definition reference", ErrUnrecoverableSpec)
		}
	}
	request.Strategy = nil
	data, err := json.Marshal(frozenRequest{Request: request, Continuations: snapshotContinuations(request.Messages)})
	if err != nil {
		return RunSpec{}, fmt.Errorf("%w: %w", ErrUnrecoverableSpec, err)
	}
	spec := RunSpec{Version: RunSpecVersion, RunID: request.ID, Request: data, Strategy: strategy, StrategyReference: strategyReference, Codec: StateCodec{Type: "runtime.Snapshot", Version: SnapshotCodecVersion}, Resources: resources}
	if err := spec.Validate(); err != nil {
		return RunSpec{}, err
	}
	copyData, err := json.Marshal(spec)
	if err != nil {
		return RunSpec{}, err
	}
	var frozen RunSpec
	if err := json.Unmarshal(copyData, &frozen); err != nil {
		return RunSpec{}, err
	}
	return frozen, nil
}

// RestoreRequest 只还原配置。解析器必须按给定 revision/摘要加载原定义，
// 核对对象内容后才重建资源；原版本不可取得时返回错误，不能按名称使用最新策略。
func (s RunSpec) RestoreRequest(resolve func(ContentReference) (Strategy, error)) (Request, error) {
	if s.Codec != (StateCodec{Type: "runtime.Snapshot", Version: SnapshotCodecVersion}) {
		return Request{}, fmt.Errorf("%w: unsupported state codec", ErrUnrecoverableSpec)
	}
	return s.restoreRequestData(resolve)
}

// restoreRequestData 只重建冻结请求；持久宿主须先独立验证/迁移真实快照编码。
func (s RunSpec) restoreRequestData(resolve func(ContentReference) (Strategy, error)) (Request, error) {
	if err := s.Validate(); err != nil {
		return Request{}, err
	}
	var frozen frozenRequest
	decoder := json.NewDecoder(bytes.NewReader(s.Request))
	decoder.UseNumber()
	if err := decoder.Decode(&frozen); err != nil {
		return Request{}, fmt.Errorf("%w: %w", ErrUnrecoverableSpec, err)
	}
	if frozen.ID != s.RunID {
		return Request{}, fmt.Errorf("%w: run identity mismatch", ErrUnrecoverableSpec)
	}
	if err := restoreContinuations(frozen.Messages, frozen.Continuations); err != nil {
		return Request{}, fmt.Errorf("%w: %w", ErrUnrecoverableSpec, err)
	}
	if resolve == nil {
		if !sameJSON(s.StrategyReference, builtinStrategyReference()) {
			return Request{}, fmt.Errorf("%w: strategy resolver is required", ErrUnrecoverableSpec)
		}
		frozen.Request.Strategy = NoopStrategy{}
	} else {
		var strategy Strategy
		err := invokeExtension("strategy.resolve", func() error { var err error; strategy, err = resolve(s.StrategyReference); return err })
		if err != nil {
			return Request{}, fmt.Errorf("%w: %w", ErrUnrecoverableSpec, err)
		}
		name := ""
		if strategy != nil {
			if err := invokeExtension("strategy.name", func() error { name = strategy.Name(); return nil }); err != nil {
				return Request{}, err
			}
		}
		if strategy == nil || name != s.Strategy {
			return Request{}, fmt.Errorf("%w: strategy revision is unavailable", ErrUnrecoverableSpec)
		}
		frozen.Request.Strategy = strategy
	}
	return frozen.Request, nil
}

func builtinStrategyReference() ContentReference {
	content := json.RawMessage(`{"name":"react","version":"1"}`)
	sum := sha256.Sum256(content)
	return ContentReference{Kind: "strategy", ID: "react", Revision: "1", Digest: hex.EncodeToString(sum[:]), Content: content}
}

// ContentDigest 为内嵌 JSON 生成跨序列化稳定的摘要；它不是 JCS 实现。
// 解码保留数值字面值，再按 Go JSON 编码规则固定键顺序及转义。
func ContentDigest(content json.RawMessage) (string, error) {
	canonical, err := stableJSONContent(content)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func stableJSONContent(content json.RawMessage) (json.RawMessage, error) {
	if !json.Valid(content) {
		return nil, fmt.Errorf("runtime: invalid inline JSON content")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// Fork 创建重新执行的规格与 lineage，不继承原 Run 的进度、操作或提交身份。
// 此方法只产生值，不创建任务或执行任何外部动作。
func (s RunSpec) Fork(newRunID string) (RunSpec, error) {
	if err := s.Validate(); err != nil {
		return RunSpec{}, err
	}
	if newRunID == "" || newRunID == s.RunID {
		return RunSpec{}, fmt.Errorf("runtime: fork requires a new run identity")
	}
	data, err := json.Marshal(s)
	if err != nil {
		return RunSpec{}, err
	}
	var fork RunSpec
	if err := json.Unmarshal(data, &fork); err != nil {
		return RunSpec{}, err
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(fork.Request, &request); err != nil {
		return RunSpec{}, ErrUnrecoverableSpec
	}
	identity, err := json.Marshal(newRunID)
	if err != nil {
		return RunSpec{}, err
	}
	request["ID"] = identity
	fork.Request, err = json.Marshal(request)
	if err != nil {
		return RunSpec{}, err
	}
	fork.ParentRunID, fork.RunID = s.RunID, newRunID
	return fork, nil
}

// SnapshotMigration 只转换传入副本；失败不会改写已存原记录。
type SnapshotMigration func([]byte) ([]byte, error)

// SnapshotCodecs 管理显式版本迁移；注册在装配期完成，可并发读取。
type SnapshotCodecs struct {
	mu         sync.RWMutex
	migrations map[StateCodec]SnapshotMigration
}

func NewSnapshotCodecs() *SnapshotCodecs {
	return &SnapshotCodecs{migrations: make(map[StateCodec]SnapshotMigration)}
}

func (c *SnapshotCodecs) Register(source StateCodec, migrate SnapshotMigration) error {
	if source.Type == "" || source.Version == "" || migrate == nil {
		return ErrUnrecoverableSpec
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.migrations[source]; exists {
		return fmt.Errorf("runtime: snapshot migration already registered")
	}
	c.migrations[source] = migrate
	return nil
}

// Decode 先迁移版本，再校验快照身份；不触发 Provider、工具或持久写入。
func (c *SnapshotCodecs) Decode(data []byte, codec StateCodec, runID string) (Snapshot, error) {
	var metadata struct {
		Codec StateCodec `json:"codec"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrUnrecoverableSpec, err)
	}
	if metadata.Codec != (StateCodec{}) {
		codec = metadata.Codec
	}
	if codec != (StateCodec{Type: "runtime.Snapshot", Version: SnapshotCodecVersion}) {
		if c == nil {
			return Snapshot{}, ErrUnrecoverableSpec
		}
		c.mu.RLock()
		migrate := c.migrations[codec]
		c.mu.RUnlock()
		if migrate == nil {
			return Snapshot{}, ErrUnrecoverableSpec
		}
		err := invokeExtension("snapshot_migration", func() error { var err error; data, err = migrate(append([]byte(nil), data...)); return err })
		if err != nil {
			return Snapshot{}, fmt.Errorf("%w: %w", ErrUnrecoverableSpec, err)
		}
	}
	snapshot, err := DecodeSnapshot(data)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrUnrecoverableSpec, err)
	}
	if snapshot.RunID != runID {
		return Snapshot{}, fmt.Errorf("%w: snapshot identity mismatch", ErrUnrecoverableSpec)
	}
	return snapshot, nil
}
