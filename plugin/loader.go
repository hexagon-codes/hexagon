// Package plugin 提供 Hexagon AI Agent 框架的插件系统
package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Loader 插件加载器
type Loader struct {
	registry    *Registry
	lifecycle   *Lifecycle
	searchPaths []string
}

// LoaderOption Loader 选项
type LoaderOption func(*Loader)

// WithSearchPaths 设置搜索路径
func WithSearchPaths(paths ...string) LoaderOption {
	return func(l *Loader) {
		l.searchPaths = paths
	}
}

// NewLoader 创建插件加载器
func NewLoader(registry *Registry, lifecycle *Lifecycle, opts ...LoaderOption) *Loader {
	l := &Loader{
		registry:  registry,
		lifecycle: lifecycle,
		searchPaths: []string{
			"./plugins",
			"~/.hexagon/plugins",
			"/etc/hexagon/plugins",
		},
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// PluginsConfig 插件配置文件结构
type PluginsConfig struct {
	Plugins []PluginConfig `yaml:"plugins"`
}

// LoadFromConfig 从配置加载插件
func (l *Loader) LoadFromConfig(ctx context.Context, configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	var config PluginsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// 按优先级排序
	sorted := SortPluginsByPriority(config.Plugins)

	// 加载启用的插件
	for _, pluginConfig := range sorted {
		if !pluginConfig.Enabled {
			continue
		}

		// 从工厂创建插件
		plugin, err := l.registry.CreateFromFactory(pluginConfig.Name)
		if err != nil {
			return fmt.Errorf("failed to create plugin %s: %w", pluginConfig.Name, err)
		}

		// 注册插件
		if err := l.registry.Register(plugin); err != nil {
			return fmt.Errorf("failed to register plugin %s: %w", pluginConfig.Name, err)
		}

		// 初始化插件
		if err := l.lifecycle.Init(ctx, pluginConfig.Name, pluginConfig.Config); err != nil {
			return fmt.Errorf("failed to init plugin %s: %w", pluginConfig.Name, err)
		}
	}

	return nil
}

// ReplaceFromConfig 只构造目标插件的独立候选，成功后原子替换其路由。
// 同文件内其他插件不被停止、初始化或重新注册。
func (l *Loader) ReplaceFromConfig(ctx context.Context, name, configPath string) (*Retirement, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var config PluginsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	for _, item := range config.Plugins {
		if item.Name != name {
			continue
		}
		if !item.Enabled {
			return nil, fmt.Errorf("plugin %s is disabled in candidate config", name)
		}
		candidate, err := l.registry.CreateFromFactory(name)
		if err != nil {
			return nil, err
		}
		manager := &PluginManager{Registry: l.registry, Lifecycle: l.lifecycle}
		return manager.ReplaceCandidate(ctx, name, candidate, item.Config)
	}
	return nil, fmt.Errorf("plugin %s not found in candidate config", name)
}

// ReplaceAllFromConfig 将配置中的已启用插件作为候选集整体替换。
// 未出现在候选集中的路由不变；新增插件仍通过正常 Register/Init 入口装配。
func (l *Loader) ReplaceAllFromConfig(ctx context.Context, configPath string) (*Retirement, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var config PluginsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	candidates := make([]Candidate, 0, len(config.Plugins))
	for _, item := range SortPluginsByPriority(config.Plugins) {
		if !item.Enabled {
			continue
		}
		candidate, err := l.registry.CreateFromFactory(item.Name)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, Candidate{Plugin: candidate, Config: item.Config})
	}
	manager := &PluginManager{Registry: l.registry, Lifecycle: l.lifecycle}
	return manager.ReplaceCandidates(ctx, candidates)
}

// LoadFromDirectory 从目录加载插件配置
func (l *Loader) LoadFromDirectory(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		configPath := filepath.Join(dir, name)
		if err := l.LoadFromConfig(ctx, configPath); err != nil {
			return fmt.Errorf("failed to load config %s: %w", configPath, err)
		}
	}

	return nil
}

// Discover 发现插件
func (l *Loader) Discover() ([]PluginInfo, error) {
	var discovered []PluginInfo

	for _, path := range l.searchPaths {
		// 展开 home 目录
		if path[0] == '~' {
			home, err := os.UserHomeDir()
			if err != nil {
				continue
			}
			path = filepath.Join(home, path[1:])
		}

		// 检查目录是否存在
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}

		// 读取目录
		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			// 查找插件清单文件
			manifestPath := filepath.Join(path, entry.Name(), "plugin.yaml")
			if _, err := os.Stat(manifestPath); err != nil {
				continue
			}

			// 解析清单
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				continue
			}

			var manifest struct {
				PluginInfo `yaml:",inline"`
			}
			if err := yaml.Unmarshal(data, &manifest); err != nil {
				continue
			}

			discovered = append(discovered, manifest.PluginInfo)
		}
	}

	return discovered, nil
}

// LoadAll 加载所有发现的插件
func (l *Loader) LoadAll(ctx context.Context) error {
	// 从搜索路径加载配置
	for _, path := range l.searchPaths {
		// 展开 home 目录
		if path[0] == '~' {
			home, err := os.UserHomeDir()
			if err != nil {
				continue
			}
			path = filepath.Join(home, path[1:])
		}

		// 查找配置文件
		configPath := filepath.Join(path, "plugins.yaml")
		if _, err := os.Stat(configPath); err == nil {
			if err := l.LoadFromConfig(ctx, configPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// ============== PluginManifest ==============

// PluginManifest 插件清单
type PluginManifest struct {
	// Info 插件信息
	Info PluginInfo `yaml:"info"`

	// Config 默认配置
	Config map[string]any `yaml:"config,omitempty"`

	// ConfigSchema 配置 Schema
	ConfigSchema map[string]any `yaml:"config_schema,omitempty"`

	// Hooks 钩子配置
	Hooks map[string]string `yaml:"hooks,omitempty"`
}

// ParseManifest 解析插件清单
func ParseManifest(data []byte) (*PluginManifest, error) {
	var manifest PluginManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// LoadManifest 从文件加载清单
func LoadManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseManifest(data)
}

// ============== BuiltinPlugins ==============

// BuiltinPlugins 内置插件注册
type BuiltinPlugins struct {
	factories map[string]PluginFactory
}

// NewBuiltinPlugins 创建内置插件集合
func NewBuiltinPlugins() *BuiltinPlugins {
	return &BuiltinPlugins{
		factories: make(map[string]PluginFactory),
	}
}

// Add 添加内置插件工厂
func (b *BuiltinPlugins) Add(name string, factory PluginFactory) *BuiltinPlugins {
	b.factories[name] = factory
	return b
}

// RegisterAll 注册所有内置插件到注册表
func (b *BuiltinPlugins) RegisterAll(registry *Registry) error {
	for name, factory := range b.factories {
		if err := registry.RegisterFactory(name, factory); err != nil {
			return err
		}
	}
	return nil
}

// List 列出所有内置插件
func (b *BuiltinPlugins) List() []string {
	names := make([]string, 0, len(b.factories))
	for name := range b.factories {
		names = append(names, name)
	}
	return names
}

// ============== 便捷函数 ==============

// QuickLoad 快速加载插件
func QuickLoad(ctx context.Context, configs []PluginConfig) (*PluginManager, error) {
	manager := NewPluginManager()

	for _, config := range configs {
		if !config.Enabled {
			continue
		}

		// 从工厂创建
		plugin, err := manager.Registry.CreateFromFactory(config.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to create plugin %s: %w", config.Name, err)
		}

		// 加载并启用
		if err := manager.Load(plugin); err != nil {
			return nil, fmt.Errorf("failed to load plugin %s: %w", config.Name, err)
		}

		if err := manager.Enable(ctx, config.Name, config.Config); err != nil {
			return nil, fmt.Errorf("failed to enable plugin %s: %w", config.Name, err)
		}
	}

	return manager, nil
}
