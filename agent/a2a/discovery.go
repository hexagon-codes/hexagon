package a2a

import (
	"context"
	"sync"
	"time"

	"github.com/hexagon-codes/hexagon/agent"
)

// ============== Discovery 接口 ==============

// Discovery Agent 发现服务接口
// 用于发现和管理 A2A Agent。
type Discovery interface {
	// Discover 发现符合条件的 Agent
	Discover(ctx context.Context, filter *AgentFilter) ([]*AgentCard, error)

	// Register 注册 Agent
	Register(ctx context.Context, card *AgentCard) error

	// Deregister 注销 Agent
	Deregister(ctx context.Context, url string) error

	// Get 获取指定 Agent
	Get(ctx context.Context, url string) (*AgentCard, error)

	// Watch 监听 Agent 变化
	Watch(ctx context.Context, filter *AgentFilter) (<-chan DiscoveryEvent, error)
}

// AgentFilter Agent 过滤条件
type AgentFilter struct {
	// Name Agent 名称（支持通配符）
	Name string

	// Skills 技能过滤
	Skills []string

	// Tags 标签过滤
	Tags []string

	// Capabilities 能力过滤
	Capabilities *AgentCapabilities
}

// DiscoveryEvent 发现事件
type DiscoveryEvent struct {
	// Type 事件类型
	Type DiscoveryEventType

	// Card Agent Card
	Card *AgentCard

	// Timestamp 时间戳
	Timestamp time.Time
}

// DiscoveryEventType 发现事件类型
type DiscoveryEventType string

const (
	// EventTypeRegistered Agent 注册
	EventTypeRegistered DiscoveryEventType = "registered"

	// EventTypeDeregistered Agent 注销
	EventTypeDeregistered DiscoveryEventType = "deregistered"

	// EventTypeUpdated Agent 更新
	EventTypeUpdated DiscoveryEventType = "updated"
)

// ============== RegistryDiscovery ==============

// RegistryDiscovery 基于 Hexagon Registry 的 Agent 发现服务
// 将 Hexagon 的 Agent 注册表与 A2A 协议桥接。
type RegistryDiscovery struct {
	// registry Hexagon Agent 注册表
	registry *agent.Registry

	// a2aAgents A2A Agent 缓存 (url -> card)
	a2aAgents map[string]*AgentCard

	// baseURL 基础 URL（用于生成 Agent URL）
	baseURL string

	// watchers 监听器
	watchers []chan DiscoveryEvent

	mu sync.RWMutex
}

// NewRegistryDiscovery 创建基于 Registry 的发现服务
func NewRegistryDiscovery(registry *agent.Registry, baseURL string) *RegistryDiscovery {
	d := &RegistryDiscovery{
		registry:  registry,
		a2aAgents: make(map[string]*AgentCard),
		baseURL:   baseURL,
		watchers:  make([]chan DiscoveryEvent, 0),
	}

	// 监听 Registry 事件
	registry.Watch(func(event agent.WatchEvent) {
		d.handleRegistryEvent(event)
	})

	return d
}

// handleRegistryEvent 处理 Registry 事件
func (d *RegistryDiscovery) handleRegistryEvent(event agent.WatchEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var discEvent DiscoveryEvent
	discEvent.Timestamp = event.Timestamp

	switch event.Type {
	case agent.EventRegistered:
		// 转换为 A2A Card
		card := AgentInfoToCard(event.Agent, d.makeAgentURL(event.Agent.ID))
		d.a2aAgents[card.URL] = card
		discEvent.Type = EventTypeRegistered
		discEvent.Card = card

	case agent.EventDeregistered:
		url := d.makeAgentURL(event.Agent.ID)
		if card, exists := d.a2aAgents[url]; exists {
			delete(d.a2aAgents, url)
			discEvent.Type = EventTypeDeregistered
			discEvent.Card = card
		}

	case agent.EventUpdated, agent.EventHealthChanged:
		url := d.makeAgentURL(event.Agent.ID)
		card := AgentInfoToCard(event.Agent, url)
		d.a2aAgents[url] = card
		discEvent.Type = EventTypeUpdated
		discEvent.Card = card
	}

	// 通知监听器
	if discEvent.Card != nil {
		for _, watcher := range d.watchers {
			select {
			case watcher <- discEvent:
			default:
				// 通道已满，跳过
			}
		}
	}
}

// makeAgentURL 生成 Agent URL
func (d *RegistryDiscovery) makeAgentURL(agentID string) string {
	return d.baseURL + "/agents/" + agentID
}

// Discover 发现符合条件的 Agent
func (d *RegistryDiscovery) Discover(_ context.Context, filter *AgentFilter) ([]*AgentCard, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*AgentCard

	for _, card := range d.a2aAgents {
		if d.matchFilter(card, filter) {
			result = append(result, card)
		}
	}

	return result, nil
}

// matchFilter 检查 Agent 是否匹配过滤条件
func (d *RegistryDiscovery) matchFilter(card *AgentCard, filter *AgentFilter) bool {
	if filter == nil {
		return true
	}

	// 名称过滤
	if filter.Name != "" && card.Name != filter.Name {
		// 简单通配符支持（* 匹配所有）
		if filter.Name != "*" {
			return false
		}
	}

	// 技能过滤
	if len(filter.Skills) > 0 {
		skillSet := make(map[string]bool)
		for _, skill := range card.Skills {
			skillSet[skill.ID] = true
		}
		for _, required := range filter.Skills {
			if !skillSet[required] {
				return false
			}
		}
	}

	// 标签过滤
	if len(filter.Tags) > 0 {
		tagSet := make(map[string]bool)
		for _, skill := range card.Skills {
			for _, tag := range skill.Tags {
				tagSet[tag] = true
			}
		}
		for _, required := range filter.Tags {
			if !tagSet[required] {
				return false
			}
		}
	}

	// 能力过滤
	if filter.Capabilities != nil {
		if filter.Capabilities.Streaming && !card.Capabilities.Streaming {
			return false
		}
		if filter.Capabilities.PushNotifications && !card.Capabilities.PushNotifications {
			return false
		}
	}

	return true
}

// Register 注册 Agent
func (d *RegistryDiscovery) Register(_ context.Context, card *AgentCard) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.a2aAgents[card.URL] = card

	// 通知监听器
	event := DiscoveryEvent{
		Type:      EventTypeRegistered,
		Card:      card,
		Timestamp: time.Now(),
	}
	for _, watcher := range d.watchers {
		select {
		case watcher <- event:
		default:
		}
	}

	return nil
}

// Deregister 注销 Agent
func (d *RegistryDiscovery) Deregister(_ context.Context, url string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	card, exists := d.a2aAgents[url]
	if !exists {
		return ErrTaskNotFound
	}

	delete(d.a2aAgents, url)

	// 通知监听器
	event := DiscoveryEvent{
		Type:      EventTypeDeregistered,
		Card:      card,
		Timestamp: time.Now(),
	}
	for _, watcher := range d.watchers {
		select {
		case watcher <- event:
		default:
		}
	}

	return nil
}

// Get 获取指定 Agent
func (d *RegistryDiscovery) Get(_ context.Context, url string) (*AgentCard, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	card, exists := d.a2aAgents[url]
	if !exists {
		return nil, ErrTaskNotFound
	}

	return card, nil
}

// Watch 监听 Agent 变化
func (d *RegistryDiscovery) Watch(_ context.Context, _ *AgentFilter) (<-chan DiscoveryEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	ch := make(chan DiscoveryEvent, 100)
	d.watchers = append(d.watchers, ch)

	return ch, nil
}

// ============== StaticDiscovery ==============

// StaticDiscovery 静态 Agent 发现服务
// 用于已知固定 Agent 列表的场景。
type StaticDiscovery struct {
	// agents Agent 列表 (url -> card)
	agents map[string]*AgentCard

	mu sync.RWMutex
}

// NewStaticDiscovery 创建静态发现服务
func NewStaticDiscovery(cards ...*AgentCard) *StaticDiscovery {
	d := &StaticDiscovery{
		agents: make(map[string]*AgentCard),
	}

	for _, card := range cards {
		d.agents[card.URL] = card
	}

	return d
}

// Discover 发现符合条件的 Agent
func (d *StaticDiscovery) Discover(_ context.Context, filter *AgentFilter) ([]*AgentCard, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*AgentCard
	for _, card := range d.agents {
		if d.matchFilter(card, filter) {
			result = append(result, card)
		}
	}

	return result, nil
}

// matchFilter 检查 Agent 是否匹配过滤条件
func (d *StaticDiscovery) matchFilter(card *AgentCard, filter *AgentFilter) bool {
	if filter == nil {
		return true
	}

	// 名称过滤
	if filter.Name != "" && filter.Name != "*" && card.Name != filter.Name {
		return false
	}

	// 技能过滤
	if len(filter.Skills) > 0 {
		skillSet := make(map[string]bool)
		for _, skill := range card.Skills {
			skillSet[skill.ID] = true
		}
		for _, required := range filter.Skills {
			if !skillSet[required] {
				return false
			}
		}
	}

	return true
}

// Register 注册 Agent
func (d *StaticDiscovery) Register(_ context.Context, card *AgentCard) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.agents[card.URL] = card
	return nil
}

// Deregister 注销 Agent
func (d *StaticDiscovery) Deregister(_ context.Context, url string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.agents, url)
	return nil
}

// Get 获取指定 Agent
func (d *StaticDiscovery) Get(_ context.Context, url string) (*AgentCard, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	card, exists := d.agents[url]
	if !exists {
		return nil, ErrTaskNotFound
	}

	return card, nil
}

// Watch 监听 Agent 变化（静态发现不支持监听）
func (d *StaticDiscovery) Watch(_ context.Context, _ *AgentFilter) (<-chan DiscoveryEvent, error) {
	// 静态发现不支持监听，返回空通道
	ch := make(chan DiscoveryEvent)
	close(ch)
	return ch, nil
}

// ============== RemoteDiscovery ==============

// RemoteDiscovery 远程 Agent 发现服务
// 通过 HTTP 获取远程 Agent 的 Card。
type RemoteDiscovery struct {
	// clients Agent 客户端缓存 (url -> client)
	clients map[string]*Client

	// cards Agent Card 缓存 (url -> card)
	cards map[string]*AgentCard

	// cacheTTL 缓存过期时间
	cacheTTL time.Duration

	// cacheTime 缓存时间 (url -> time)
	cacheTime map[string]time.Time

	mu sync.RWMutex
}

// NewRemoteDiscovery 创建远程发现服务
func NewRemoteDiscovery(cacheTTL time.Duration) *RemoteDiscovery {
	return &RemoteDiscovery{
		clients:   make(map[string]*Client),
		cards:     make(map[string]*AgentCard),
		cacheTTL:  cacheTTL,
		cacheTime: make(map[string]time.Time),
	}
}

// AddAgent 添加要发现的 Agent URL
func (d *RemoteDiscovery) AddAgent(url string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.clients[url]; !exists {
		d.clients[url] = NewClient(url)
	}
}

// Discover 发现符合条件的 Agent
func (d *RemoteDiscovery) Discover(ctx context.Context, filter *AgentFilter) ([]*AgentCard, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var result []*AgentCard

	for url, client := range d.clients {
		// 检查缓存
		if card, exists := d.cards[url]; exists {
			if time.Since(d.cacheTime[url]) < d.cacheTTL {
				if d.matchFilter(card, filter) {
					result = append(result, card)
				}
				continue
			}
		}

		// 获取 Agent Card
		card, err := client.GetAgentCard(ctx)
		if err != nil {
			continue
		}

		// 更新缓存
		d.cards[url] = card
		d.cacheTime[url] = time.Now()

		if d.matchFilter(card, filter) {
			result = append(result, card)
		}
	}

	return result, nil
}

// matchFilter 检查 Agent 是否匹配过滤条件
func (d *RemoteDiscovery) matchFilter(card *AgentCard, filter *AgentFilter) bool {
	if filter == nil {
		return true
	}

	if filter.Name != "" && filter.Name != "*" && card.Name != filter.Name {
		return false
	}

	return true
}

// Register 注册 Agent（添加到发现列表）
func (d *RemoteDiscovery) Register(_ context.Context, card *AgentCard) error {
	d.AddAgent(card.URL)
	return nil
}

// Deregister 注销 Agent
func (d *RemoteDiscovery) Deregister(_ context.Context, url string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if client, exists := d.clients[url]; exists {
		client.Close()
		delete(d.clients, url)
	}
	delete(d.cards, url)
	delete(d.cacheTime, url)

	return nil
}

// Get 获取指定 Agent
func (d *RemoteDiscovery) Get(ctx context.Context, url string) (*AgentCard, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 检查缓存
	if card, exists := d.cards[url]; exists {
		if time.Since(d.cacheTime[url]) < d.cacheTTL {
			return card, nil
		}
	}

	// 获取或创建客户端
	client, exists := d.clients[url]
	if !exists {
		client = NewClient(url)
		d.clients[url] = client
	}

	// 获取 Agent Card
	card, err := client.GetAgentCard(ctx)
	if err != nil {
		return nil, err
	}

	// 更新缓存
	d.cards[url] = card
	d.cacheTime[url] = time.Now()

	return card, nil
}

// Watch 监听 Agent 变化（远程发现不支持实时监听）
func (d *RemoteDiscovery) Watch(_ context.Context, _ *AgentFilter) (<-chan DiscoveryEvent, error) {
	ch := make(chan DiscoveryEvent)
	close(ch)
	return ch, nil
}
