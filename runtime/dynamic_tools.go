package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/hexagon-codes/ai-core/llm"
)

// ToolBinding 将已固定资源上的执行器与同代定义绑定；不是全局工具注册表。
// Executor 和 References 必须来自宿主同一次解析/pin，不能按名称另查当前路由。
type ToolBinding struct {
	Definition llm.ToolDefinition
	Executor   ToolExecutor
	References []ContentReference
}

// ToolBindingBatch 的 Release 归成功接纳它的 Run 视图持有；全部重复或失败时立即释放。
// Initial 的资源仍归装配宿主，不能把同一共享资源的 Stop 同时交给两处执行。
type ToolBindingBatch struct {
	Bindings []ToolBinding
	Release  func()
}

// ToolBindingSpec 只持久化定义和可恢复引用，不序列化执行器或连接。
type ToolBindingSpec struct {
	Definition llm.ToolDefinition `json:"definition"`
	References []ContentReference `json:"references,omitempty"`
}

// ToolSnapshot 固定本 Run 已公布的定义及恢复引用，不携带资源句柄。
type ToolSnapshot struct {
	Version    int               `json:"version"`
	RunID      string            `json:"run_id"`
	SearchName string            `json:"search_name"`
	Bindings   []ToolBindingSpec `json:"bindings,omitempty"`
}

// ToolDefinitions 返回模型本轮实际使用的定义副本，不暴露未选择的目录条目。
func (s ToolSnapshot) ToolDefinitions() ([]llm.ToolDefinition, error) {
	if s.Version != 1 || s.SearchName == "" {
		return nil, fmt.Errorf("runtime: unsupported dynamic tool snapshot")
	}
	defs := []llm.ToolDefinition{dynamicSearchDefinition(s.SearchName)}
	for _, binding := range s.Bindings {
		defs = append(defs, binding.Definition)
	}
	return llm.CloneToolDefinitions(defs)
}

// 三个可选端口只影响主动装配的动态视图，普通 ToolExecutor 不需要实现。
type ToolSnapshotter interface {
	SnapshotTools(context.Context) (ToolSnapshot, error)
}
type ToolSnapshotRestorer interface {
	RestoreTools(context.Context, ToolSnapshot) error
}
type ToolRunBinder interface{ BindRun(string) error }

// DynamicToolConfig 的发现源可读取既有 Registry 或 MCP Session；每次返回的
// Batch 必须持有本次独立取得的使用权，不能重复交还另一 Batch 已拥有的同一 Lease。
type DynamicToolConfig struct {
	SearchName string
	Discover   func(context.Context, string) (ToolBindingBatch, error)
	Restore    func(context.Context, ToolSnapshot) (ToolBindingBatch, error)
	Initial    []ToolBinding
}

// DynamicTools 是每 Run 的只增绑定视图。搜索不授予业务权限，已有 Permission
// 检查仍在 Runner 调度处执行。热更新不会替换本视图中已经公布的同名绑定。
type DynamicTools struct {
	mu       sync.Mutex
	config   DynamicToolConfig
	runID    string
	bindings map[string]ToolBinding
	releases []func()
	active   int
	closed   bool
}

// NewDynamicTools 创建尚未绑定 Run 的选择视图，不启动任务或建立外部连接。
// Initial 立即可见；其他工具只在 Discover 返回完整绑定后加入。
func NewDynamicTools(config DynamicToolConfig) (*DynamicTools, error) {
	if config.SearchName == "" {
		config.SearchName = "search_tools"
	}
	if config.Discover == nil {
		return nil, fmt.Errorf("runtime: dynamic tool discovery is missing")
	}
	initial, err := freezeToolBindings(config.Initial, config.SearchName)
	if err != nil {
		return nil, err
	}
	config.Initial = nil
	view := &DynamicTools{config: config, bindings: make(map[string]ToolBinding)}
	for _, binding := range initial {
		view.bindings[binding.Definition.Function.Name] = binding
	}
	return view, nil
}

// BindRun 防止同一个可变选择视图污染不同 Run；同一 Run 恢复可装配新的独立视图。
func (d *DynamicTools) BindRun(runID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("runtime: dynamic tool view is released")
	}
	if runID == "" {
		return fmt.Errorf("runtime: dynamic tool view requires a run identity")
	}
	if d.runID != "" && d.runID != runID {
		return fmt.Errorf("runtime: dynamic tool view belongs to another run")
	}
	d.runID = runID
	return nil
}

func (d *DynamicTools) SnapshotTools(ctx context.Context) (ToolSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ToolSnapshot{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.runID == "" {
		return ToolSnapshot{}, fmt.Errorf("runtime: dynamic tool view is not active for a run")
	}
	return d.snapshotLocked()
}

func (d *DynamicTools) snapshotLocked() (ToolSnapshot, error) {
	names := make([]string, 0, len(d.bindings))
	for name := range d.bindings {
		names = append(names, name)
	}
	sort.Strings(names)
	snapshot := ToolSnapshot{Version: 1, RunID: d.runID, SearchName: d.config.SearchName}
	for _, name := range names {
		binding := d.bindings[name]
		defs, err := llm.CloneToolDefinitions([]llm.ToolDefinition{binding.Definition})
		if err != nil {
			return ToolSnapshot{}, err
		}
		snapshot.Bindings = append(snapshot.Bindings, ToolBindingSpec{Definition: defs[0], References: cloneReferences(binding.References)})
	}
	return snapshot, nil
}

// RestoreTools 只接受原定义与原引用；Resolver 必须返回已固定的资源，无法恢复时
// 明确失败。此路径不调用 Discover，也不把模型可见 schema 当作执行绑定。
func (d *DynamicTools) RestoreTools(ctx context.Context, snapshot ToolSnapshot) error {
	if snapshot.Version != 1 || snapshot.RunID == "" || snapshot.SearchName != d.config.SearchName {
		return fmt.Errorf("%w: dynamic tool snapshot identity differs", ErrUnrecoverableSpec)
	}
	if err := d.BindRun(snapshot.RunID); err != nil {
		return err
	}
	if err := d.begin(); err != nil {
		return err
	}
	defer d.finish()
	expected := make(map[string]ToolBindingSpec, len(snapshot.Bindings))
	for _, spec := range snapshot.Bindings {
		name := spec.Definition.Function.Name
		if name == "" || name == snapshot.SearchName {
			return fmt.Errorf("%w: invalid saved tool name", ErrUnrecoverableSpec)
		}
		if _, exists := expected[name]; exists {
			return fmt.Errorf("%w: duplicate saved tool %s", ErrUnrecoverableSpec, name)
		}
		if len(spec.References) == 0 {
			return fmt.Errorf("%w: tool %s has no fixed resource reference", ErrUnrecoverableSpec, name)
		}
		for _, ref := range spec.References {
			if err := validateToolReference(ref); err != nil {
				return err
			}
		}
		expected[name] = spec
	}
	d.mu.Lock()
	complete := len(d.bindings) == len(expected)
	for name, current := range d.bindings {
		wanted, exists := expected[name]
		if !exists || !sameToolSpec(current, wanted) {
			d.mu.Unlock()
			return fmt.Errorf("%w: prepared tool %s differs from saved binding", ErrUnrecoverableSpec, name)
		}
	}
	d.mu.Unlock()
	if complete {
		return nil
	}
	if d.config.Restore == nil {
		return fmt.Errorf("%w: dynamic tool resolver is unavailable", ErrUnrecoverableSpec)
	}
	copySnapshot, err := cloneToolSnapshot(snapshot)
	if err != nil {
		return err
	}
	batch, err := d.config.Restore(ctx, copySnapshot)
	if err != nil {
		releaseToolBatch(batch)
		return err
	}
	bindings, err := freezeToolBindings(batch.Bindings, d.config.SearchName)
	if err != nil {
		releaseToolBatch(batch)
		return err
	}
	if len(bindings) != len(expected) {
		releaseToolBatch(batch)
		return fmt.Errorf("%w: restored tool binding set differs", ErrUnrecoverableSpec)
	}
	for _, binding := range bindings {
		wanted, exists := expected[binding.Definition.Function.Name]
		if !exists || !sameToolSpec(binding, wanted) {
			releaseToolBatch(batch)
			return fmt.Errorf("%w: restored tool revision differs", ErrUnrecoverableSpec)
		}
	}
	_, err = d.install(bindings, batch.Release, expected)
	return err
}

func (d *DynamicTools) Execute(ctx context.Context, call llm.ToolCall) (ToolResult, error) {
	if err := d.begin(); err != nil {
		return ToolResult{}, err
	}
	defer d.finish()
	if call.Name == d.config.SearchName {
		var args struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			return ToolResult{}, fmt.Errorf("runtime: invalid tool search arguments: %w", err)
		}
		if strings.TrimSpace(args.Query) == "" {
			return ToolResult{}, fmt.Errorf("runtime: tool search query is required")
		}
		batch, err := d.config.Discover(ctx, args.Query)
		if err != nil {
			releaseToolBatch(batch)
			return ToolResult{}, err
		}
		bindings, err := freezeToolBindings(batch.Bindings, d.config.SearchName)
		if err != nil {
			releaseToolBatch(batch)
			return ToolResult{}, err
		}
		names, err := d.install(bindings, batch.Release, nil)
		if err != nil {
			return ToolResult{}, err
		}
		selected := struct {
			Selected []string `json:"selected"`
		}{Selected: names}
		content, _ := json.Marshal(selected)
		return ToolResult{Content: string(content), Raw: selected, Status: ToolStatusSuccess}, nil
	}
	d.mu.Lock()
	binding, ok := d.bindings[call.Name]
	d.mu.Unlock()
	if !ok {
		return ToolResult{}, fmt.Errorf("runtime: tool %q has no binding in this run", call.Name)
	}
	return binding.Executor.Execute(ctx, call)
}

// SideEffectOf 继承实际执行器的声明；搜索本身只改变本 Run 的可见集。
func (d *DynamicTools) SideEffectOf(call llm.ToolCall) ToolSideEffect {
	if call.Name == d.config.SearchName {
		return SideEffectReadOnly
	}
	d.mu.Lock()
	binding, ok := d.bindings[call.Name]
	d.mu.Unlock()
	if ok {
		if classifier, ok := binding.Executor.(SideEffectClassifier); ok {
			return classifier.SideEffectOf(call)
		}
	}
	return SideEffectUnsafe
}

func (d *DynamicTools) install(bindings []ToolBinding, release func(), expected map[string]ToolBindingSpec) ([]string, error) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		if release != nil {
			release()
		}
		return nil, fmt.Errorf("runtime: dynamic tool view is released")
	}
	// 解析器在锁外运行，安装恢复结果时再次核对，避免同时选择的新绑定偷换原修订。
	if expected != nil {
		for name, current := range d.bindings {
			wanted, exists := expected[name]
			if !exists || !sameToolSpec(current, wanted) {
				d.mu.Unlock()
				if release != nil {
					release()
				}
				return nil, fmt.Errorf("%w: tool bindings changed while restoring", ErrUnrecoverableSpec)
			}
		}
	}
	names := make([]string, 0, len(bindings))
	added := false
	for _, binding := range bindings {
		name := binding.Definition.Function.Name
		names = append(names, name)
		if _, exists := d.bindings[name]; !exists {
			d.bindings[name] = binding
			added = true
		}
	}
	if added && release != nil {
		d.releases = append(d.releases, release)
	}
	d.mu.Unlock()
	if !added && release != nil {
		release()
	}
	sort.Strings(names)
	return names, nil
}

func (d *DynamicTools) begin() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.runID == "" {
		return fmt.Errorf("runtime: dynamic tool view is not active for a run")
	}
	d.active++
	return nil
}
func (d *DynamicTools) finish() {
	d.mu.Lock()
	d.active--
	releases := d.takeReleasesLocked()
	d.mu.Unlock()
	for i := len(releases) - 1; i >= 0; i-- {
		releases[i]()
	}
}
func (d *DynamicTools) takeReleasesLocked() []func() {
	if !d.closed || d.active != 0 {
		return nil
	}
	releases := d.releases
	d.releases = nil
	return releases
}

// Release 幂等，仅归还本视图取得的 Batch lease。正在返回的调用结束后才归还依赖，
// 不擅自 Stop MCP Session 或插件；宿主把它串到 PreparedRun.Release 即可。
func (d *DynamicTools) Release() {
	d.mu.Lock()
	d.closed = true
	releases := d.takeReleasesLocked()
	d.mu.Unlock()
	for i := len(releases) - 1; i >= 0; i-- {
		releases[i]()
	}
}

func dynamicSearchDefinition(name string) llm.ToolDefinition {
	return llm.NewToolDefinition(name, "Find tools for a task and add their definitions to this run.", &llm.Schema{Type: "object", Properties: map[string]*llm.Schema{"query": {Type: "string", Description: "Describe the required tool capability"}}, Required: []string{"query"}})
}
func freezeToolBindings(bindings []ToolBinding, searchName string) ([]ToolBinding, error) {
	result := make([]ToolBinding, 0, len(bindings))
	seen := make(map[string]bool)
	for _, binding := range bindings {
		name := binding.Definition.Function.Name
		if name == "" || name == searchName || binding.Executor == nil {
			return nil, fmt.Errorf("runtime: tool binding requires a distinct name, schema and executor")
		}
		if seen[name] {
			return nil, fmt.Errorf("runtime: duplicate tool binding %q", name)
		}
		seen[name] = true
		if binding.Definition.Function.Parameters == nil {
			return nil, fmt.Errorf("runtime: tool %q schema is missing", name)
		}
		defs, err := llm.CloneToolDefinitions([]llm.ToolDefinition{binding.Definition})
		if err != nil {
			return nil, err
		}
		binding.Definition = defs[0]
		binding.References = cloneReferences(binding.References)
		for _, ref := range binding.References {
			if err := validateToolReference(ref); err != nil {
				return nil, err
			}
		}
		result = append(result, binding)
	}
	return result, nil
}
func validateToolReference(ref ContentReference) error {
	if ref.Kind == "" || ref.ID == "" || ref.Revision == "" || ref.Digest == "" || (len(ref.Content) == 0 && ref.ObjectKey == "") {
		return fmt.Errorf("%w: tool resource reference is incomplete", ErrUnrecoverableSpec)
	}
	if len(ref.Content) > 0 {
		digest, err := ContentDigest(ref.Content)
		if err != nil || digest != ref.Digest {
			return fmt.Errorf("%w: tool resource digest differs", ErrUnrecoverableSpec)
		}
	}
	return nil
}
func sameToolSpec(binding ToolBinding, spec ToolBindingSpec) bool {
	return sameJSON(ToolBindingSpec{Definition: binding.Definition, References: binding.References}, spec)
}
func cloneToolSnapshot(snapshot ToolSnapshot) (ToolSnapshot, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return ToolSnapshot{}, err
	}
	var clone ToolSnapshot
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	err = decoder.Decode(&clone)
	return clone, err
}
func releaseToolBatch(batch ToolBindingBatch) {
	if batch.Release != nil {
		batch.Release()
	}
}
