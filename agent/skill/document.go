package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/hexagon-codes/toolkit/util/file"
	"github.com/hexagon-codes/toolkit/util/hash"
	"github.com/hexagon-codes/toolkit/util/reflectx"
)

// Kind 是技能的使用方式；读取文档本身不调用 Handler 或执行脚本。
type Kind string

const (
	KindHandler  Kind = "handler"
	KindDocument Kind = "document"
)

var (
	ErrDocumentNotExecutable = errors.New("skill: document skill is not executable")
	ErrRevisionUnavailable   = errors.New("skill: revision is unavailable")
	ErrScopeMismatch         = errors.New("skill: selection scope does not match")
)

// DocumentReference 是可持久化的不可变内容清单。Files 将相对路径绑定至 SHA-256。
// Root 是来源内资源根，Entry 是其内的正文路径。Revision 由清单生成，不能只填可变标签。
// 来源如何保存历史修订由宿主决定；框架不安装依赖、不运行 shell，也不授予业务权限。
type DocumentReference struct {
	Source   string            `json:"source"`
	Revision string            `json:"revision"`
	Root     string            `json:"root"`
	Entry    string            `json:"entry"`
	Files    map[string]string `json:"files"`
}

// NewDocumentReference 从宿主提供的文件摘要构造固定清单，不读取正文或资源。
// 历史修订应使用可重新定位的独立资源根；相同根被覆盖时读取将明确报告原修订不可用。
func NewDocumentReference(source, root, entry string, digests map[string]string) (DocumentReference, error) {
	ref := DocumentReference{Source: source, Root: root, Entry: entry, Files: make(map[string]string, len(digests))}
	for name, digest := range digests {
		ref.Files[name] = digest
	}
	ref.Revision = ref.digest()
	if err := ref.Validate(); err != nil {
		return DocumentReference{}, err
	}
	return ref, nil
}

// Clone 返回独立清单，避免调用者更改在途选择的资源摘要。
func (r DocumentReference) Clone() DocumentReference {
	files := make(map[string]string, len(r.Files))
	for name, digest := range r.Files {
		files[name] = digest
	}
	r.Files = files
	return r
}

func (r DocumentReference) digest() string {
	r.Revision = ""
	data, _ := json.Marshal(r)
	return hash.SHA256Bytes(data)
}

// Validate 验证固定清单的结构及修订标识，不做网络请求或新增业务策略校验。
func (r DocumentReference) Validate() error {
	if r.Source == "" || r.Root == "" || !relativeResource(r.Entry) || r.Files[r.Entry] == "" || r.Revision != r.digest() {
		return fmt.Errorf("skill: invalid immutable document reference")
	}
	for name, digest := range r.Files {
		if !relativeResource(name) || len(digest) != 64 {
			return fmt.Errorf("skill: invalid resource digest for %s", name)
		}
		for _, c := range digest {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return fmt.Errorf("skill: invalid resource digest for %s", name)
			}
		}
	}
	return nil
}

func relativeResource(name string) bool {
	clean := path.Clean(name)
	return name != "" && !path.IsAbs(name) && clean != ".." && !strings.HasPrefix(clean, "../") && !strings.Contains(name, "\\")
}

// DocumentSource 是宿主装配的内容读取端口，必须按给定来源与资源根读取。
// 框架在每次返回内容前校验清单摘要，不会退回最新版本。
type DocumentSource interface {
	Read(context.Context, DocumentReference, string) ([]byte, error)
}

// DirectorySource 从宿主配置的本地目录读取；文件读取与摘要复用 Toolkit。
// Root 可为来源目录内相对路径或宿主显式提供的绝对资源根，不附加业务白名单。
type DirectorySource struct{ Directory string }

func (s DirectorySource) Read(ctx context.Context, ref DocumentReference, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := ref.Root
	if !filepath.IsAbs(root) {
		root = filepath.Join(s.Directory, filepath.FromSlash(root))
	}
	data, err := file.Read(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

// Metadata 是初始目录视图，不含正文、相对资源内容或 Handler。
type Metadata struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Version     string         `json:"version"`
	Kind        Kind           `json:"kind"`
	Scope       string         `json:"scope,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
	Revision    string         `json:"revision"`
}

// Selection 保存 Run 选中的原定义，不携带文件句柄、连接或执行权限。
type Selection struct {
	Metadata Metadata           `json:"metadata"`
	Document *DocumentReference `json:"document,omitempty"`
}

// Selected 是本次 Run 的固定使用视图；正文仅在 Select/Restore 后加载。
// 运行交接只序列化 Reference()，恢复重新解析其原修订。
type Selected struct {
	selection  Selection
	body       string
	definition *Skill
	source     DocumentSource
}

// Reference 返回可持久化选择的独立副本，宿主需保留其原来源与资源根。
func (s *Selected) Reference() Selection { return cloneSelection(s.selection) }

// Content 返回已选中文档的正文；Handler 选择的正文为空。
func (s *Selected) Content() string { return s.body }

// ReadResource 相对选中正文解析资源，且只读取该修订清单绑定的内容。
func (s *Selected) ReadResource(ctx context.Context, name string) ([]byte, error) {
	if s.selection.Document == nil {
		return nil, fmt.Errorf("skill: selected handler has no document resources")
	}
	ref := s.selection.Document.Clone()
	resource := path.Join(path.Dir(ref.Entry), name)
	if name == "" || path.IsAbs(name) || strings.Contains(name, "\\") || !relativeResource(resource) {
		return nil, fmt.Errorf("skill: invalid relative resource %s", name)
	}
	return readDocumentResource(ctx, s.source, ref, resource)
}

// Execute 只调用原 Handler 定义。加载文档不会自动转为可执行技能。
func (s *Selected) Execute(ctx context.Context, input map[string]any) (map[string]any, error) {
	if s.definition == nil || s.definition.Handler == nil {
		return nil, ErrDocumentNotExecutable
	}
	return s.definition.Handler(ctx, input)
}

// RegisterSource 显式绑定内容来源；同名来源不可原位替换，以免在途定义改变读取语义。
func (r *Registry) RegisterSource(name string, source DocumentSource) error {
	if name == "" || source == nil {
		return fmt.Errorf("skill: document source is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sources[name]; exists {
		return fmt.Errorf("skill: source %s already registered", name)
	}
	r.sources[name] = source
	return nil
}

// Catalog 返回作用域内可见元数据，局部同名定义覆盖共享定义；不加载任何正文。
// 作用域只是宿主传入的选择范围，不替代宿主的用户或业务鉴权。
func (r *Registry) Catalog(scope string) []Metadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	visible := make(map[string]*Skill)
	for _, s := range r.skills {
		if s.Enabled && s.Scope == "" {
			visible[s.Name] = s
		}
	}
	if scope != "" {
		for _, s := range r.skills {
			if s.Enabled && s.Scope == scope {
				visible[s.Name] = s
			}
		}
	}
	result := make([]Metadata, 0, len(visible))
	for _, s := range visible {
		result = append(result, metadata(s))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// Replace 发布新修订，保留旧定义供已选择的 Run 和恢复使用；不修改旧定义。
// Handler 更新须改变 Version；文档修订由内容清单确定。
func (r *Registry) Replace(s *Skill) error {
	if err := validateSkill(s); err != nil {
		return err
	}
	frozen, err := freezeSkill(s)
	if err != nil {
		return err
	}
	r.mu.Lock()
	key := skillKey(frozen.Scope, frozen.Name)
	old, ok := r.skills[key]
	if !ok {
		r.mu.Unlock()
		return ErrSkillNotFound
	}
	if _, exists := r.revisions[revisionKey(frozen)]; exists {
		r.mu.Unlock()
		return fmt.Errorf("skill: revision already exists")
	}
	frozen.Enabled = old.Enabled
	if frozen.CreatedAt.IsZero() {
		frozen.CreatedAt = old.CreatedAt
	}
	r.skills[key] = frozen
	r.revisions[revisionKey(frozen)] = copySkill(frozen)
	hooks := append([]Hook(nil), r.hooks...)
	r.mu.Unlock()
	notifyHooksUnlocked(hooks, EventRegistered, copySkill(frozen))
	return nil
}

// Select 原子选择当前可见定义，锁外加载选中正文。之后更新目录不改变本次选择。
func (r *Registry) Select(ctx context.Context, scope, name string) (*Selected, error) {
	r.mu.RLock()
	s, exists := r.skills[skillKey(scope, name)]
	if !exists {
		s, exists = r.skills[skillKey("", name)]
	}
	if !exists {
		r.mu.RUnlock()
		return nil, ErrSkillNotFound
	}
	if !s.Enabled {
		r.mu.RUnlock()
		return nil, ErrSkillDisabled
	}
	selection := Selection{Metadata: metadata(s)}
	if s.Document != nil {
		ref := s.Document.Clone()
		selection.Document = &ref
	}
	r.mu.RUnlock()
	return r.Restore(ctx, scope, selection)
}

// Restore 按保存的修订重建选择。原版本缺失或清单解码失败均明确报错，不读取最新版。
func (r *Registry) Restore(ctx context.Context, scope string, selection Selection) (*Selected, error) {
	selection = cloneSelection(selection)
	if selection.Metadata.Scope != "" && selection.Metadata.Scope != scope {
		return nil, ErrScopeMismatch
	}
	if selection.Metadata.Name == "" || selection.Metadata.Revision == "" {
		return nil, fmt.Errorf("skill: invalid selection")
	}
	selected := &Selected{selection: selection}
	if selection.Metadata.Kind == KindDocument {
		if selection.Document == nil {
			return nil, fmt.Errorf("skill: document selection is missing its reference")
		}
		ref := selection.Document.Clone()
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		if ref.Revision != selection.Metadata.Revision {
			return nil, fmt.Errorf("skill: document selection revision does not match")
		}
		r.mu.RLock()
		source, ok := r.sources[ref.Source]
		r.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("%w: source %s", ErrRevisionUnavailable, ref.Source)
		}
		data, err := readDocumentResource(ctx, source, ref, ref.Entry)
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(data) {
			return nil, fmt.Errorf("skill: document content is not valid UTF-8")
		}
		selected.body = string(data)
		selected.source = source
		return selected, nil
	}
	if selection.Metadata.Kind != KindHandler {
		return nil, fmt.Errorf("skill: unknown selection kind %s", selection.Metadata.Kind)
	}
	r.mu.RLock()
	definition, ok := r.revisions[skillKey(selection.Metadata.Scope, selection.Metadata.Name)+"\x00"+selection.Metadata.Revision]
	if ok {
		definition = copySkill(definition)
	}
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: handler %s", ErrRevisionUnavailable, selection.Metadata.Name)
	}
	selected.definition = definition
	return selected, nil
}

// DecodeSelection 解码持久化选择并验证身份结构；正文和资源仍留待 Restore 加载。
func DecodeSelection(data []byte) (Selection, error) {
	var selection Selection
	if err := json.Unmarshal(data, &selection); err != nil {
		return Selection{}, fmt.Errorf("skill: failed to decode selection: %w", err)
	}
	if selection.Metadata.Name == "" || selection.Metadata.Revision == "" {
		return Selection{}, fmt.Errorf("skill: invalid selection")
	}
	if selection.Metadata.Kind == KindDocument {
		if selection.Document == nil {
			return Selection{}, fmt.Errorf("skill: missing document reference")
		}
		if err := selection.Document.Validate(); err != nil {
			return Selection{}, err
		}
		if selection.Metadata.Revision != selection.Document.Revision {
			return Selection{}, fmt.Errorf("skill: selection revision does not match")
		}
	} else if selection.Metadata.Kind != KindHandler {
		return Selection{}, fmt.Errorf("skill: unknown selection kind")
	}
	return selection, nil
}

func readDocumentResource(ctx context.Context, source DocumentSource, ref DocumentReference, name string) ([]byte, error) {
	expected, ok := ref.Files[name]
	if !ok {
		return nil, fmt.Errorf("%w: resource %s is absent from revision %s", ErrRevisionUnavailable, name, ref.Revision)
	}
	data, err := source.Read(ctx, ref.Clone(), name)
	if err != nil {
		return nil, fmt.Errorf("%w: resource %s: %w", ErrRevisionUnavailable, name, err)
	}
	if hash.SHA256Bytes(data) != expected {
		return nil, fmt.Errorf("%w: resource %s no longer matches revision %s", ErrRevisionUnavailable, name, ref.Revision)
	}
	return append([]byte(nil), data...), nil
}

func validateSkill(s *Skill) error {
	if s == nil || s.Name == "" {
		return ErrInvalidSkill
	}
	switch s.Kind {
	case "", KindHandler:
		if s.Handler == nil || s.Document != nil {
			return ErrInvalidSkill
		}
	case KindDocument:
		if s.Handler != nil || s.Document == nil {
			return ErrInvalidSkill
		}
		return s.Document.Validate()
	default:
		return ErrInvalidSkill
	}
	return nil
}

func freezeSkill(s *Skill) (*Skill, error) {
	c := copySkill(s)
	var err error
	c.Schema, err = reflectx.DeepCopyChecked(s.Schema)
	if err != nil {
		return nil, fmt.Errorf("skill: schema cannot be snapshotted: %w", err)
	}
	if c.Kind == "" {
		c.Kind = KindHandler
	}
	return c, nil
}

func skillKey(scope, name string) string {
	return fmt.Sprintf("%d:%s%d:%s", len(scope), scope, len(name), name)
}
func revisionKey(s *Skill) string { return skillKey(s.Scope, s.Name) + "\x00" + skillRevision(s) }

func skillRevision(s *Skill) string {
	if s.Document != nil {
		return s.Document.Revision
	}
	if s.Version != "" {
		return s.Version
	}
	data, _ := json.Marshal(struct {
		Name, Description string
		Schema            map[string]any
	}{s.Name, s.Description, s.Schema})
	return hash.SHA256Bytes(data)
}

func metadata(s *Skill) Metadata {
	return Metadata{Name: s.Name, Description: s.Description, Version: s.Version, Kind: s.Kind, Scope: s.Scope, Tags: append([]string(nil), s.Tags...), Schema: reflectx.DeepCopy(s.Schema), Revision: skillRevision(s)}
}
func cloneSelection(s Selection) Selection {
	s.Metadata.Tags = append([]string(nil), s.Metadata.Tags...)
	s.Metadata.Schema = reflectx.DeepCopy(s.Metadata.Schema)
	if s.Document != nil {
		ref := s.Document.Clone()
		s.Document = &ref
	}
	return s
}
