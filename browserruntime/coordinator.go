// Package browserruntime 在同一个 MCP 浏览器会话中协调任务、页面和执行权。
package browserruntime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	coretool "github.com/hexagon-codes/ai-core/tool"
)

var (
	// ErrTargetRequired 表示没有唯一可绑定的浏览器目标。
	ErrTargetRequired = errors.New("browser target is ambiguous; specify the existing session or page")
	// ErrSessionLost 表示原浏览器会话已经不可用。
	ErrSessionLost = errors.New("browser session is no longer available")
	// ErrStaleGeneration 表示租约引用的浏览器代次已经改变。
	ErrStaleGeneration = errors.New("browser session generation has changed")
	// ErrLeaseReleased 表示任务已经释放执行租约。
	ErrLeaseReleased = errors.New("browser task has released its execution lease")
	// ErrNotQuiescent 表示尚未确认浏览器动作停止。
	ErrNotQuiescent       = errors.New("browser action has not been confirmed stopped")
	errFileChooserPending = errors.New("browser modal_pending: handle the file chooser with browser_file_upload")
)

// TaskRef 只接受应用运行时提供的身份，不从模型参数生成用户归属。
type TaskRef struct {
	ID, OwnerID, SessionID, RequestID, ToolCallID string
}

// SessionRef 指向现有 MCP 管理器拥有的浏览器，不持有第二个浏览器进程。
type SessionRef struct {
	ID, ServerName, ProfileID, PageID string
	Generation                        uint64
	OwnsBrowser                       bool
	Extension                         bool
}

// Target 声明调用方要求绑定的现有浏览器目标。
type Target struct {
	ServerName, ProfileID, PageID string
	RequireExistingSession        bool
	UseCurrentPage                bool
}

// Page 描述实际浏览器页面身份。
type Page struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

// Observation 保存驱动观察到的页面与视口事实。
type Observation struct {
	Page      Page   `json:"page"`
	Pages     []Page `json:"pages"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	BrowserID string `json:"browser_id"`
}

// Point 表示同页CSS像素坐标。
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Action 的坐标始终采用同页 CSS 像素，不能使用完整页面截图的偏移坐标。
type Action struct {
	Type                      string `json:"type"`
	X, Y                      float64
	Button                    string
	Text                      string
	Keys                      []string
	Path                      []Point
	ScrollX, ScrollY          float64
	URL                       string
	PageID                    string
	Paths                     []string
	Seconds                   float64
	ToolName                  string
	Arguments                 map[string]any
	SourceWidth, SourceHeight int
}

// Image 保存一次实际截图的字节和像素尺寸。
type Image struct {
	MIME          string
	Data          []byte
	Width, Height int
}

// ActionResult 保存驱动返回值、截图及观察事实。
type ActionResult struct {
	Result      coretool.Result
	Images      []Image
	Observation *Observation
	Outcome     string
	Ready       *bool `json:"ready,omitempty"`
	// FileChooserPending 只表达实际成功响应确认的模态，不代表新的页面观察。
	FileChooserPending bool
}

// Driver 复用已连接的 MCP 服务；Quiesce 必须证明本地在途动作已收口。
type Driver interface {
	Bind(context.Context, string, string, bool) (Observation, error)
	Execute(context.Context, SessionRef, Action) (ActionResult, error)
	Quiesce(context.Context, string) error
	ClosePage(context.Context, string, string) (coretool.Result, error)
}

// Options 提供浏览器默认档案与应用授权回调。
type Options struct {
	DefaultProfile string
	// Authorize 在每次新动作派发前读取应用当前的有效策略。
	Authorize func(context.Context, SessionRef, Action) error
	Waiting   func(context.Context, TaskRef, SessionRef)
}

type profileState struct {
	ref                SessionRef
	gate               chan struct{}
	live               bool
	stopped            map[uint64]bool
	active             *Lease
	stranded           bool
	strandedGeneration uint64
	transition         chan struct{}
	fileChooser        *fileChooserState
	changed            chan struct{}
}

type fileChooserState struct {
	ref              SessionRef
	ownerID, taskKey string
}

func fileChooserMatches(chooser *fileChooserState, ref SessionRef, ownerID string) bool {
	return chooser != nil && chooser.ownerID == ownerID && chooser.ref.ProfileID == ref.ProfileID &&
		chooser.ref.ServerName == ref.ServerName && chooser.ref.Generation == ref.Generation && chooser.ref.PageID == ref.PageID
}

// Coordinator 协调已连接浏览器的代次、任务租约与页面归属。
type Coordinator struct {
	driver       Driver
	opts         Options
	mu           sync.Mutex
	profiles     map[string]*profileState
	bindings     map[string]SessionRef
	ownedPages   map[string]map[string]SessionRef
	unknownTasks map[string]bool
}

// New 创建使用既有驱动的浏览器协调器，不另行启动浏览器。
func New(driver Driver, opts Options) *Coordinator {
	return &Coordinator{driver: driver, opts: opts, profiles: make(map[string]*profileState), bindings: make(map[string]SessionRef), ownedPages: make(map[string]map[string]SessionRef), unknownTasks: make(map[string]bool)}
}

// Sessions 返回已登记浏览器会话的独立列表。
func (c *Coordinator) Sessions() []SessionRef {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sessions []SessionRef
	for _, state := range c.profiles {
		if state.live {
			sessions = append(sessions, state.ref)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ProfileID < sessions[j].ProfileID })
	return sessions
}

func newProfileState() *profileState {
	state := &profileState{gate: make(chan struct{}, 1), transition: make(chan struct{}, 1), stopped: make(map[uint64]bool), changed: make(chan struct{})}
	state.gate <- struct{}{}
	state.transition <- struct{}{}
	return state
}

// notifyChangedLocked 仅唤醒等待者重新读取原有状态，调用方持有 Coordinator.mu。
func (p *profileState) notifyChangedLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

// waitProfileGate 复用同一执行权；未知动作不会放行，也不能让后继请求永久等待。
func (c *Coordinator) waitProfileGate(ctx context.Context, state *profileState, ref SessionRef, onWait func()) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		if !state.live || state.ref.Generation != ref.Generation {
			c.mu.Unlock()
			return ErrStaleGeneration
		}
		uncertain := state.stranded && state.strandedGeneration == ref.Generation
		if active := state.active; active != nil && active.ref.Generation == ref.Generation {
			uncertain = uncertain || c.unknownTasks[bindingKey(active.task)]
		}
		changed := state.changed
		stopped := state.stopped[ref.Generation]
		c.mu.Unlock()
		if uncertain && !stopped {
			return ErrNotQuiescent
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-state.gate:
			return nil
		default:
		}
		if onWait != nil {
			onWait()
			onWait = nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-state.gate:
			return nil
		case <-changed:
		}
	}
}

// PrepareProfile 先登记受管配置，尚未连接时不把它暴露为可执行会话。
func (c *Coordinator) PrepareProfile(ref SessionRef) error {
	if ref.ServerName == "" || ref.ProfileID == "" || ref.ID == "" {
		return errors.New("browser session, server and profile identities are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if state := c.profiles[ref.ProfileID]; state != nil {
		if state.ref.ServerName != ref.ServerName {
			return errors.New("browser profile already belongs to another server")
		}
		return nil
	}
	state := newProfileState()
	state.ref = ref
	c.profiles[ref.ProfileID] = state
	return nil
}

// RegisterSession 注册已连接且身份已核定的服务；替换前必须先停止旧会话。
func (c *Coordinator) RegisterSession(ref SessionRef) error {
	if ref.ServerName == "" || ref.ProfileID == "" || ref.ID == "" {
		return errors.New("browser session, server and profile identities are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for profile, state := range c.profiles {
		if profile != ref.ProfileID && state.live && state.ref.ServerName == ref.ServerName {
			return errors.New("one browser server cannot own multiple active profiles")
		}
	}
	state := c.profiles[ref.ProfileID]
	if state == nil {
		state = newProfileState()
		c.profiles[ref.ProfileID] = state
	} else if state.live {
		if state.ref == ref {
			return nil
		}
		return errors.New("stop the previous browser session before replacing its profile")
	}
	if ref.Generation <= state.ref.Generation {
		ref.Generation = state.ref.Generation + 1
	}
	state.ref, state.live, state.fileChooser = ref, true, nil
	state.notifyChangedLocked()
	return nil
}

// BeginReplacement 将同 Profile 的 stop/connect/publish 包在一个替换租约内。
// 调用方无论连接成功与否都必须调用 finish，且只能在真实发布连接后传 connected=true。
func (c *Coordinator) BeginReplacement(ctx context.Context, profile string, closeOld func() error) (func(SessionRef, bool) error, error) {
	if closeOld == nil {
		return nil, errors.New("browser session stop callback is required")
	}
	c.mu.Lock()
	state := c.profiles[profile]
	c.mu.Unlock()
	if state == nil {
		return nil, ErrSessionLost
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-state.transition:
	}
	if err := c.CloseSession(ctx, profile, func(context.Context, SessionRef) error { return closeOld() }); err != nil {
		state.transition <- struct{}{}
		return nil, err
	}
	var once sync.Once
	var result error
	return func(next SessionRef, connected bool) error {
		once.Do(func() {
			defer func() { state.transition <- struct{}{} }()
			if connected {
				if next.ProfileID != profile {
					result = errors.New("replacement browser profile does not match its lease")
					return
				}
				result = c.RegisterSession(next)
			}
		})
		return result
	}, nil
}

// ConfirmSessionStopped 仅在管理器确认原连接/受管进程停止后调用。
func (c *Coordinator) ConfirmSessionStopped(profile string, generation uint64) {
	c.mu.Lock()
	if state := c.profiles[profile]; state != nil {
		state.stopped[generation] = true
		if state.fileChooser != nil && state.fileChooser.ref.Generation == generation {
			state.fileChooser = nil
		}
		if state.ref.Generation == generation {
			state.live = false
		}
		if state.stranded && state.strandedGeneration == generation {
			state.stranded = false
			state.gate <- struct{}{}
		}
		state.notifyChangedLocked()
	}
	c.mu.Unlock()
}

// CloseSession 是有状态服务的停止接缝。stop 由 MCP 管理器实现并确认旧进程/连接已关闭。
// 新连接只能在本方法返回后建立；扩展模式只断开连接，不终止用户浏览器。
func (c *Coordinator) CloseSession(ctx context.Context, profile string, stop func(context.Context, SessionRef) error) error {
	if stop == nil {
		return errors.New("browser session stop callback is required")
	}
	c.mu.Lock()
	state := c.profiles[profile]
	if state == nil {
		c.mu.Unlock()
		return ErrSessionLost
	}
	ref, active := state.ref, state.active
	state.live = false
	state.notifyChangedLocked()
	c.mu.Unlock()
	if active != nil {
		active.cancel()
	}
	if err := stop(ctx, ref); err != nil {
		return err
	}
	c.ConfirmSessionStopped(profile, ref.Generation)
	if active != nil {
		if err := active.Release(ctx, "session_stopped"); err != nil {
			return err
		}
	}
	// 等待可能仍在 Bind 的本地调用退出，避免新旧连接同时占用 Profile。
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-state.gate:
	}
	state.gate <- struct{}{}
	return nil
}

func bindingKey(task TaskRef) string { return task.OwnerID + "\x00" + task.ID }

// Inspect 以 Profile 执行权读取候选页面，不创建页面、选择活动页或建立任务绑定。
// 其他任务的同代次动作仍未知时返回 ErrNotQuiescent，不排队掩盖未确认状态。
func (c *Coordinator) Inspect(ctx context.Context, task TaskRef, target Target) (Observation, error) {
	if task.ID == "" || task.OwnerID == "" {
		return Observation{}, errors.New("trusted browser task and owner identities are required")
	}
	inspector, ok := c.driver.(interface {
		Inspect(context.Context, string) (Observation, error)
	})
	if !ok {
		return Observation{}, errors.New("browser page inspection is unavailable")
	}
	c.mu.Lock()
	var selected *profileState
	for profile, state := range c.profiles {
		if !state.live || (target.ProfileID != "" && target.ProfileID != profile) || (target.ServerName != "" && target.ServerName != state.ref.ServerName) || (target.RequireExistingSession && !state.ref.Extension) {
			continue
		}
		if selected != nil {
			c.mu.Unlock()
			return Observation{}, ErrTargetRequired
		}
		selected = state
	}
	if selected == nil {
		c.mu.Unlock()
		return Observation{}, ErrSessionLost
	}
	ref, active := selected.ref, selected.active
	c.mu.Unlock()
	if active != nil && bindingKey(active.task) == bindingKey(task) {
		if err := active.lockOperation(ctx); err != nil {
			return Observation{}, err
		}
		defer func() { active.operation <- struct{}{} }()
		if err := active.checkSession(); err != nil {
			return Observation{}, err
		}
		if active.fileChooserPending() {
			return Observation{}, errFileChooserPending
		}
		opCtx, cancel := active.operationContext(ctx)
		defer cancel()
		if f := c.opts.Authorize; f != nil {
			if err := f(opCtx, ref, Action{Type: "inspect"}); err != nil {
				return Observation{}, err
			}
		}
		observation, err := inspector.Inspect(opCtx, ref.ServerName)
		if err == nil {
			err = active.checkSession()
		}
		return observation, err
	}
	if err := c.waitProfileGate(ctx, selected, ref, nil); err != nil {
		return Observation{}, err
	}
	defer func() { selected.gate <- struct{}{} }()
	c.mu.Lock()
	live := selected.live && selected.ref.Generation == ref.Generation
	chooser := selected.fileChooser
	c.mu.Unlock()
	if !live {
		return Observation{}, ErrStaleGeneration
	}
	if chooser != nil {
		return Observation{}, errFileChooserPending
	}
	if f := c.opts.Authorize; f != nil {
		if err := f(ctx, ref, Action{Type: "inspect"}); err != nil {
			return Observation{}, err
		}
	}
	observation, err := inspector.Inspect(ctx, ref.ServerName)
	if err != nil {
		return Observation{}, err
	}
	c.mu.Lock()
	live = selected.live && selected.ref.Generation == ref.Generation
	c.mu.Unlock()
	if !live {
		return Observation{}, ErrStaleGeneration
	}
	return observation, nil
}

// Acquire 同一 Profile 串行排队；取消等待者不改变持有者或共享浏览器。
// 已确认的文件选择模态保留在原会话中，仅相同归属与页面的后续调用可以接续。
// 当前代次动作仍未知时返回 ErrNotQuiescent，不派发后续动作或永久排队。
func (c *Coordinator) Acquire(ctx context.Context, task TaskRef, target Target) (*Lease, error) {
	if c.driver == nil {
		return nil, errors.New("browser driver is unavailable")
	}
	if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.OwnerID) == "" {
		return nil, errors.New("trusted browser task and owner identities are required")
	}
	c.mu.Lock()
	previous, hasPrevious := c.bindings[bindingKey(task)]
	reuseBinding := target.PageID == "" && target.ServerName == "" && target.ProfileID == "" && hasPrevious && !target.UseCurrentPage
	if reuseBinding {
		target.ServerName, target.ProfileID, target.PageID = previous.ServerName, previous.ProfileID, previous.PageID
	}
	var selected *profileState
	for profile, candidate := range c.profiles {
		if !candidate.live || (target.ProfileID != "" && profile != target.ProfileID) || (target.ServerName != "" && candidate.ref.ServerName != target.ServerName) {
			continue
		}
		if target.RequireExistingSession && !candidate.ref.Extension {
			continue
		}
		if target.ProfileID == "" && target.ServerName == "" && !target.RequireExistingSession && c.opts.DefaultProfile != "" && profile != c.opts.DefaultProfile {
			continue
		}
		if selected != nil {
			c.mu.Unlock()
			return nil, ErrTargetRequired
		}
		selected = candidate
	}
	if selected == nil {
		c.mu.Unlock()
		return nil, ErrSessionLost
	}
	ref := selected.ref
	if reuseBinding && previous.Generation != ref.Generation {
		c.mu.Unlock()
		return nil, ErrStaleGeneration
	}
	c.mu.Unlock()
	if err := c.waitProfileGate(ctx, selected, ref, func() {
		if c.opts.Waiting != nil {
			c.opts.Waiting(ctx, task, ref)
		}
	}); err != nil {
		return nil, err
	}
	release := func() { selected.gate <- struct{}{} }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	c.mu.Lock()
	live := selected.live && selected.ref.Generation == ref.Generation
	chooser := selected.fileChooser
	c.mu.Unlock()
	if !live {
		release()
		return nil, ErrStaleGeneration
	}
	continuingChooser := false
	if chooser != nil {
		if target.UseCurrentPage && target.PageID == "" {
			target.PageID = chooser.ref.PageID
		}
		ref.PageID = target.PageID
		if !fileChooserMatches(chooser, ref, task.OwnerID) {
			release()
			return nil, errFileChooserPending
		}
		// 同会话的已确认模态跨手工调用保留；这里没有创建新的页面观察。
		continuingChooser = true
	}
	ref.PageID = target.PageID
	if c.opts.Authorize != nil {
		if err := c.opts.Authorize(ctx, ref, Action{Type: "bind"}); err != nil {
			release()
			return nil, err
		}
	}
	newPage := ref.OwnsBrowser && !ref.Extension && target.PageID == "" && !reuseBinding && !target.RequireExistingSession && !target.UseCurrentPage
	var observation Observation
	var err error
	if target.UseCurrentPage && target.PageID == "" {
		inspector, ok := c.driver.(interface {
			Inspect(context.Context, string) (Observation, error)
		})
		if !ok {
			release()
			return nil, errors.New("browser current-page inspection is unavailable")
		}
		observation, err = inspector.Inspect(ctx, ref.ServerName)
		if err == nil {
			target.PageID = observation.Page.ID
			if target.PageID == "" {
				err = errors.New("browser driver returned no current page identity")
			}
		}
	}
	if err == nil && !continuingChooser {
		observation, err = c.driver.Bind(ctx, ref.ServerName, target.PageID, newPage)
	}
	if err != nil {
		var unknown *UnknownCallError
		if errors.As(err, &unknown) {
			c.mu.Lock()
			stopped := selected.stopped[ref.Generation]
			if !stopped && selected.ref.Generation == ref.Generation {
				selected.stranded = true
				selected.strandedGeneration = ref.Generation
				selected.notifyChangedLocked()
			}
			c.mu.Unlock()
			if stopped {
				release()
			}
			return nil, err
		}
		release()
		return nil, err
	}
	if !continuingChooser {
		ref.PageID = observation.Page.ID
	}
	if ref.PageID == "" {
		release()
		return nil, errors.New("browser driver did not return a stable page identity")
	}
	taskCtx, taskCancel := context.WithCancel(ctx)
	lease := &Lease{coordinator: c, state: selected, ref: ref, task: task, taskCtx: taskCtx, cancel: taskCancel, operation: make(chan struct{}, 1)}
	lease.operation <- struct{}{}
	c.mu.Lock()
	live = selected.live && selected.ref.Generation == ref.Generation
	if live {
		c.bindings[bindingKey(task)] = ref
		selected.active = lease
		lease.uncertain = c.unknownTasks[bindingKey(task)]
		if newPage {
			c.ownPageLocked(task, ref)
		}
	}
	c.mu.Unlock()
	if !live {
		taskCancel()
		release()
		return nil, ErrStaleGeneration
	}
	return lease, nil
}

// Lease 表示同一任务独占的浏览器执行租约。
type Lease struct {
	coordinator *Coordinator
	state       *profileState
	ref         SessionRef
	task        TaskRef
	taskCtx     context.Context
	cancel      context.CancelFunc
	operation   chan struct{}
	mu          sync.Mutex
	released    bool
	uncertain   bool
}

// Ref 返回当前租约的浏览器身份副本。
func (l *Lease) Ref() SessionRef { l.mu.Lock(); defer l.mu.Unlock(); return l.ref }

func (l *Lease) fileChooserPending() bool {
	c := l.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	return fileChooserMatches(l.state.fileChooser, l.ref, l.task.OwnerID)
}

func (l *Lease) lockOperation(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.operation:
	}
	l.mu.Lock()
	released := l.released
	l.mu.Unlock()
	if released {
		l.operation <- struct{}{}
		return ErrLeaseReleased
	}
	return nil
}

func (l *Lease) checkSession() error {
	c := l.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	if !l.state.live {
		return ErrSessionLost
	}
	if l.state.ref.Generation != l.ref.Generation {
		return ErrStaleGeneration
	}
	return nil
}

func (l *Lease) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	opCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.taskCtx, cancel)
	if l.taskCtx.Err() != nil {
		cancel()
	}
	return opCtx, func() { stop(); cancel() }
}

// Observe 按稳定页面身份重新观察，不能用当前全局活动页替代绑定页。
func (l *Lease) Observe(ctx context.Context) (Observation, error) {
	if err := l.lockOperation(ctx); err != nil {
		return Observation{}, err
	}
	defer func() { l.operation <- struct{}{} }()
	if err := l.checkSession(); err != nil {
		return Observation{}, err
	}
	fileChooserPending := l.fileChooserPending()
	if fileChooserPending {
		return Observation{}, errFileChooserPending
	}
	opCtx, cancel := l.operationContext(ctx)
	defer cancel()
	if f := l.coordinator.opts.Authorize; f != nil {
		if err := f(opCtx, l.ref, Action{Type: "observe"}); err != nil {
			return Observation{}, err
		}
	}
	observed, err := l.coordinator.driver.Bind(opCtx, l.ref.ServerName, l.ref.PageID, false)
	l.recordUnknown(err)
	return observed, err
}

// Execute 每个动作重新核对页面和有效权限；派发后错误保留为未知而不自动重试。
func (l *Lease) Execute(ctx context.Context, action Action) (ActionResult, error) {
	if err := l.lockOperation(ctx); err != nil {
		return ActionResult{}, err
	}
	defer func() { l.operation <- struct{}{} }()
	if err := l.checkSession(); err != nil {
		return ActionResult{}, err
	}
	l.mu.Lock()
	uncertain := l.uncertain
	l.mu.Unlock()
	fileChooserPending := l.fileChooserPending()
	if uncertain {
		return ActionResult{}, ErrNotQuiescent
	}
	if fileChooserPending && action.Type != "upload" && (action.Type != "mcp" || action.ToolName != "browser_file_upload") {
		return ActionResult{Outcome: "observed"}, errFileChooserPending
	}
	opCtx, cancel := l.operationContext(ctx)
	defer cancel()
	if f := l.coordinator.opts.Authorize; f != nil {
		if err := f(opCtx, l.ref, Action{Type: "observe"}); err != nil {
			return ActionResult{}, err
		}
	}
	var observation Observation
	if !fileChooserPending {
		var err error
		observation, err = l.coordinator.driver.Bind(opCtx, l.ref.ServerName, l.ref.PageID, false)
		if err != nil {
			if action.Type != "login_ready" {
				l.recordUnknown(err)
			}
			return ActionResult{}, err
		}
		if observation.Page.ID != l.ref.PageID {
			return ActionResult{}, ErrSessionLost
		}
	}
	if action.SourceWidth != 0 || action.SourceHeight != 0 {
		if action.SourceWidth <= 0 || action.SourceHeight <= 0 || observation.Width <= 0 || observation.Height <= 0 {
			return ActionResult{}, errors.New("browser screenshot and viewport dimensions are required for coordinate conversion")
		}
		sx, sy := float64(observation.Width)/float64(action.SourceWidth), float64(observation.Height)/float64(action.SourceHeight)
		action.X, action.Y = action.X*sx, action.Y*sy
		action.ScrollX, action.ScrollY = action.ScrollX*sx, action.ScrollY*sy
		action.Path = append([]Point(nil), action.Path...)
		for i := range action.Path {
			action.Path[i].X *= sx
			action.Path[i].Y *= sy
		}
	}
	if f := l.coordinator.opts.Authorize; f != nil {
		if err := f(opCtx, l.ref, action); err != nil {
			return ActionResult{}, err
		}
	}
	if action.Type == "select_page" || action.Type == "new_page" {
		if action.Type == "select_page" && action.PageID == "" {
			return ActionResult{}, errors.New("stable browser page identity is required")
		}
		page, bindErr := l.coordinator.driver.Bind(opCtx, l.ref.ServerName, action.PageID, action.Type == "new_page")
		if bindErr != nil {
			l.recordUnknown(bindErr)
			out := ActionResult{}
			var unknown *UnknownCallError
			if errors.As(bindErr, &unknown) {
				out.Outcome = "unknown"
			}
			return out, bindErr
		}
		if err := l.checkSession(); err != nil {
			return ActionResult{}, err
		}
		l.setPage(page.Page.ID)
		if action.Type == "new_page" {
			l.coordinator.mu.Lock()
			l.coordinator.ownPageLocked(l.task, l.Ref())
			l.coordinator.mu.Unlock()
		}
		return ActionResult{Outcome: "observed", Observation: &page, Result: coretool.NewResult(page)}, nil
	}
	result, err := l.coordinator.driver.Execute(opCtx, l.ref, action)
	// 固定只读条件查询被取消不代表有业务写动作悬而未决；既有 uncertain 仍在入口拦截。
	if result.Outcome == "unknown" && action.Type != "login_ready" {
		l.markUnknown()
	}
	if err != nil {
		return result, err
	}
	if checkErr := l.checkSession(); checkErr != nil {
		return result, checkErr
	}
	c := l.coordinator
	c.mu.Lock()
	if result.FileChooserPending {
		l.state.fileChooser = &fileChooserState{ref: l.ref, ownerID: l.task.OwnerID, taskKey: bindingKey(l.task)}
	} else if fileChooserMatches(l.state.fileChooser, l.ref, l.task.OwnerID) {
		l.state.fileChooser = nil
	}
	c.mu.Unlock()
	if result.FileChooserPending {
		// 模态存在时官方仅允许文件上传；保留原响应，不虚构新的页面观察。
		return result, nil
	}
	if action.Type == "mcp" && action.ToolName == "browser_close" {
		// 原工具已明确关闭浏览器，不能为了补观察而再次触发惰性启动。
		l.coordinator.ConfirmSessionStopped(l.ref.ProfileID, l.ref.Generation)
		return result, nil
	}
	if result.Observation != nil && action.Type == "mcp" && action.ToolName == "browser_tabs" {
		l.setPage(result.Observation.Page.ID)
		if operation, ok := action.Arguments["action"].(string); ok && operation == "new" {
			l.coordinator.mu.Lock()
			l.coordinator.ownPageLocked(l.task, l.Ref())
			l.coordinator.mu.Unlock()
		}
		return result, nil
	}
	after, observeErr := l.coordinator.driver.Bind(opCtx, l.ref.ServerName, l.ref.PageID, false)
	if observeErr != nil {
		if action.Type != "login_ready" {
			l.recordUnknown(observeErr)
		}
		return result, fmt.Errorf("browser action completed but observation failed: %w", observeErr)
	}
	result.Observation = &after
	return result, nil
}

func (c *Coordinator) ownPageLocked(task TaskRef, ref SessionRef) {
	key := bindingKey(task)
	if c.ownedPages[key] == nil {
		c.ownedPages[key] = make(map[string]SessionRef)
	}
	c.ownedPages[key][ref.PageID] = ref
}

// RetainPage 在页面已交给用户时移除任务清理归属，页面仍可作为任务观察目标。
func (c *Coordinator) RetainPage(task TaskRef, pageID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.ownedPages[bindingKey(task)], pageID)
}

// FinishTask 只用于运行时已确认的任务终态，等待登录或仍在运行时不能调用。
func (c *Coordinator) FinishTask(ctx context.Context, task TaskRef, retainPageIDs ...string) error {
	c.mu.Lock()
	ref, exists := c.bindings[bindingKey(task)]
	state := c.profiles[ref.ProfileID]
	if !exists || state == nil {
		delete(c.bindings, bindingKey(task))
		delete(c.ownedPages, bindingKey(task))
		delete(c.unknownTasks, bindingKey(task))
		c.mu.Unlock()
		return nil
	}
	active := state.active
	if active != nil && bindingKey(active.task) == bindingKey(task) {
		c.mu.Unlock()
		finishErr := active.FinishTask(ctx, retainPageIDs...)
		releaseErr := active.Release(ctx, "task_finished")
		return errors.Join(finishErr, releaseErr)
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-state.gate:
	}
	taskCtx, taskCancel := context.WithCancel(ctx)
	lease := &Lease{coordinator: c, state: state, ref: ref, task: task, taskCtx: taskCtx, cancel: taskCancel, operation: make(chan struct{}, 1)}
	lease.operation <- struct{}{}
	c.mu.Lock()
	lease.uncertain = c.unknownTasks[bindingKey(task)]
	state.active = lease
	c.mu.Unlock()
	finishErr := lease.FinishTask(ctx, retainPageIDs...)
	releaseErr := lease.Release(ctx, "task_finished")
	return errors.Join(finishErr, releaseErr)
}

// FinishTask 清理本任务创建且未交给用户的页；从不关闭原有页、服务或浏览器。
// 关闭自有页前只取消本任务已确认的选择框，未知上传仍需先查证。
func (l *Lease) FinishTask(ctx context.Context, retainPageIDs ...string) error {
	if err := l.lockOperation(ctx); err != nil {
		return err
	}
	defer func() { l.operation <- struct{}{} }()
	c := l.coordinator
	c.mu.Lock()
	stopped := l.state.stopped[l.ref.Generation]
	owned := make(map[string]SessionRef, len(c.ownedPages[bindingKey(l.task)]))
	for id, ref := range c.ownedPages[bindingKey(l.task)] {
		owned[id] = ref
	}
	c.mu.Unlock()
	l.mu.Lock()
	uncertain := l.uncertain
	l.mu.Unlock()
	if uncertain && !stopped {
		return ErrNotQuiescent
	}
	retained := make(map[string]bool, len(retainPageIDs))
	for _, id := range retainPageIDs {
		retained[id] = true
	}
	ids := make([]string, 0, len(owned))
	for id := range owned {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	needsClose := false
	for _, id := range ids {
		ref := owned[id]
		needsClose = needsClose || !retained[id] && !stopped && ref.Generation == l.ref.Generation && ref.ServerName == l.ref.ServerName
	}
	c.mu.Lock()
	chooser := l.state.fileChooser
	c.mu.Unlock()
	if needsClose && chooser != nil {
		ref, exists := owned[chooser.ref.PageID]
		if !exists || retained[chooser.ref.PageID] || chooser.taskKey != bindingKey(l.task) || !fileChooserMatches(chooser, ref, l.task.OwnerID) {
			return errFileChooserPending
		}
		if err := l.checkSession(); err != nil {
			return err
		}
		if f := c.opts.Authorize; f != nil {
			if err := f(ctx, ref, Action{Type: "cleanup_page", PageID: ref.PageID}); err != nil {
				return err
			}
		}
		// 仅取消本任务自有待关闭页的已确认选择框，省略 paths 由官方协议完成取消。
		result, err := c.driver.Execute(ctx, ref, Action{Type: "mcp", ToolName: "browser_file_upload"})
		if result.Outcome == "unknown" {
			l.markUnknown()
			return errors.Join(ErrNotQuiescent, err)
		}
		if err != nil {
			l.recordUnknown(err)
			return err
		}
		if result.Outcome != "observed" || !result.Result.Success || result.FileChooserPending {
			return errFileChooserPending
		}
		c.mu.Lock()
		if l.state.fileChooser == chooser {
			l.state.fileChooser = nil
		}
		c.mu.Unlock()
	}
	for _, id := range ids {
		ref := owned[id]
		if !retained[id] && !stopped && ref.Generation == l.ref.Generation && ref.ServerName == l.ref.ServerName {
			if err := l.checkSession(); err != nil {
				return err
			}
			if f := c.opts.Authorize; f != nil {
				if err := f(ctx, ref, Action{Type: "cleanup_page", PageID: id}); err != nil {
					return err
				}
			}
			if _, err := c.driver.ClosePage(ctx, ref.ServerName, id); err != nil {
				l.recordUnknown(err)
				return err
			}
		}
		c.mu.Lock()
		delete(c.ownedPages[bindingKey(l.task)], id)
		c.mu.Unlock()
	}
	c.mu.Lock()
	delete(c.ownedPages, bindingKey(l.task))
	delete(c.bindings, bindingKey(l.task))
	delete(c.unknownTasks, bindingKey(l.task))
	c.mu.Unlock()
	return nil
}

func (l *Lease) recordUnknown(err error) {
	var unknown *UnknownCallError
	if errors.As(err, &unknown) {
		l.markUnknown()
	}
}

func (l *Lease) markUnknown() {
	l.mu.Lock()
	l.uncertain = true
	l.mu.Unlock()
	l.coordinator.mu.Lock()
	l.coordinator.unknownTasks[bindingKey(l.task)] = true
	l.state.notifyChangedLocked()
	l.coordinator.mu.Unlock()
}

func (l *Lease) setPage(pageID string) {
	l.mu.Lock()
	l.ref.PageID = pageID
	ref := l.ref
	l.mu.Unlock()
	l.coordinator.mu.Lock()
	l.coordinator.bindings[bindingKey(l.task)] = ref
	l.coordinator.mu.Unlock()
}

// Reconcile 仅在应用以原动作回执查证后解除未知状态，不根据截图自行重发动作。
func (l *Lease) Reconcile(ctx context.Context, verify func(context.Context, SessionRef, Observation) error) error {
	if verify == nil {
		return errors.New("browser action reconciliation callback is required")
	}
	if err := l.lockOperation(ctx); err != nil {
		return err
	}
	defer func() { l.operation <- struct{}{} }()
	if err := l.checkSession(); err != nil {
		return err
	}
	if err := l.coordinator.driver.Quiesce(ctx, l.ref.ServerName); err != nil {
		return err
	}
	observation, err := l.coordinator.driver.Bind(ctx, l.ref.ServerName, l.ref.PageID, false)
	if err != nil {
		return err
	}
	if verifyErr := verify(ctx, l.ref, observation); verifyErr != nil {
		return verifyErr
	}
	l.mu.Lock()
	l.uncertain = false
	l.mu.Unlock()
	l.coordinator.mu.Lock()
	delete(l.coordinator.unknownTasks, bindingKey(l.task))
	l.state.notifyChangedLocked()
	l.coordinator.mu.Unlock()
	return nil
}

// Release 不关闭共享服务、用户浏览器或原有页面；未知在途动作须先确认收口。
func (l *Lease) Release(ctx context.Context, reason string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.operation:
	}
	defer func() { l.operation <- struct{}{} }()
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return nil
	}
	uncertain := l.uncertain
	l.mu.Unlock()
	c := l.coordinator
	c.mu.Lock()
	stopped := l.state.stopped[l.ref.Generation]
	c.mu.Unlock()
	if uncertain && !stopped {
		if err := c.driver.Quiesce(ctx, l.ref.ServerName); err != nil {
			return fmt.Errorf("%w: %v", ErrNotQuiescent, err)
		}
	}
	l.mu.Lock()
	l.released = true
	l.mu.Unlock()
	l.cancel()
	c.mu.Lock()
	if l.state.active == l {
		l.state.active = nil
	}
	c.mu.Unlock()
	l.state.gate <- struct{}{}
	return nil
}
