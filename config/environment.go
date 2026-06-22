// Package config 提供多环境配置管理能力
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Environment 环境类型
type Environment string

const (
	// EnvDevelopment 开发环境
	EnvDevelopment Environment = "development"

	// EnvTest 测试环境
	EnvTest Environment = "test"

	// EnvStaging 预发布环境
	EnvStaging Environment = "staging"

	// EnvProduction 生产环境
	EnvProduction Environment = "production"
)

// EnvironmentConfig 环境配置
//
// 支持多环境配置管理，包括：
//   - 基础配置（base）
//   - 环境特定配置（development/test/staging/production）
//   - 配置合并和覆盖
//   - 环境变量展开
type EnvironmentConfig struct {
	// CurrentEnv 当前环境
	CurrentEnv Environment `yaml:"current_env" json:"current_env"`

	// BaseDir 配置文件基础目录
	BaseDir string `yaml:"base_dir" json:"base_dir"`

	// Configs 各环境配置路径
	Configs map[Environment]string `yaml:"configs" json:"configs"`
}

// EnvironmentManager 环境配置管理器
//
// 管理多环境配置，支持：
//   - 环境切换
//   - 配置加载和合并
//   - 环境变量展开
//   - 配置验证
type EnvironmentManager struct {
	// baseDir 配置文件基础目录
	baseDir string

	// currentEnv 当前环境
	currentEnv Environment

	// configs 已加载的配置
	configs map[Environment]map[string]any
}

// EnvironmentManagerOption 环境管理器选项
type EnvironmentManagerOption func(*EnvironmentManager)

// WithBaseDir 设置配置文件基础目录
func WithEnvironmentBaseDir(dir string) EnvironmentManagerOption {
	return func(m *EnvironmentManager) {
		m.baseDir = dir
	}
}

// WithCurrentEnv 设置当前环境
func WithCurrentEnv(env Environment) EnvironmentManagerOption {
	return func(m *EnvironmentManager) {
		m.currentEnv = env
	}
}

// NewEnvironmentManager 创建环境配置管理器
//
// 参数：
//   - opts: 可选配置
//
// 返回值：
//   - *EnvironmentManager: 环境管理器实例
func NewEnvironmentManager(opts ...EnvironmentManagerOption) *EnvironmentManager {
	m := &EnvironmentManager{
		baseDir:    "./config",
		currentEnv: EnvDevelopment,
		configs:    make(map[Environment]map[string]any),
	}

	for _, opt := range opts {
		opt(m)
	}

	// 从环境变量读取当前环境
	if envStr := os.Getenv("HEXAGON_ENV"); envStr != "" {
		m.currentEnv = Environment(strings.ToLower(envStr))
	}

	return m
}

// LoadAgentConfig 加载 Agent 配置（带环境支持）
//
// 加载顺序：
//  1. 加载 base 配置（如果存在）
//  2. 加载环境特定配置
//  3. 合并配置（环境配置覆盖 base 配置）
//  4. 展开环境变量
//
// 参数：
//   - name: 配置名称
//
// 返回值：
//   - *AgentConfig: Agent 配置
//   - error: 错误（如果有）
func (m *EnvironmentManager) LoadAgentConfig(name string) (*AgentConfig, error) {
	if err := validateConfigName(name); err != nil {
		return nil, err
	}
	// 加载 base 配置
	basePath := filepath.Join(m.baseDir, "base", fmt.Sprintf("%s.yaml", name))
	var baseConfig *AgentConfig

	if _, err := os.Stat(basePath); err == nil {
		baseConfig, err = LoadAgentConfig(basePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load base config: %w", err)
		}
	}

	// 加载环境配置
	envPath := filepath.Join(m.baseDir, string(m.currentEnv), fmt.Sprintf("%s.yaml", name))
	envConfig, err := LoadAgentConfig(envPath)
	if err != nil {
		// 如果环境配置不存在但有 base 配置，使用 base 配置
		if baseConfig != nil && os.IsNotExist(err) {
			return baseConfig, nil
		}
		return nil, fmt.Errorf("failed to load env config: %w", err)
	}

	// 合并配置
	if baseConfig != nil {
		envConfig = MergeAgentConfigs(baseConfig, envConfig)
	}

	return envConfig, nil
}

// LoadTeamConfig 加载 Team 配置（带环境支持）
func (m *EnvironmentManager) LoadTeamConfig(name string) (*TeamConfig, error) {
	if err := validateConfigName(name); err != nil {
		return nil, err
	}
	// 加载 base 配置
	basePath := filepath.Join(m.baseDir, "base", fmt.Sprintf("%s.yaml", name))
	var baseConfig *TeamConfig

	if _, err := os.Stat(basePath); err == nil {
		baseConfig, err = LoadTeamConfig(basePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load base config: %w", err)
		}
	}

	// 加载环境配置
	envPath := filepath.Join(m.baseDir, string(m.currentEnv), fmt.Sprintf("%s.yaml", name))
	envConfig, err := LoadTeamConfig(envPath)
	if err != nil {
		// 如果环境配置不存在但有 base 配置，使用 base 配置
		if baseConfig != nil && os.IsNotExist(err) {
			return baseConfig, nil
		}
		return nil, fmt.Errorf("failed to load env config: %w", err)
	}

	// 合并配置（简化版本，只覆盖顶层字段）
	if baseConfig != nil {
		envConfig = mergeTeamConfigs(baseConfig, envConfig)
	}

	return envConfig, nil
}

// LoadWorkflowConfig 加载 Workflow 配置（带环境支持）
func (m *EnvironmentManager) LoadWorkflowConfig(name string) (*WorkflowConfig, error) {
	if err := validateConfigName(name); err != nil {
		return nil, err
	}
	// 加载 base 配置
	basePath := filepath.Join(m.baseDir, "base", fmt.Sprintf("%s.yaml", name))
	var baseConfig *WorkflowConfig

	if _, err := os.Stat(basePath); err == nil {
		baseConfig, err = LoadWorkflowConfig(basePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load base config: %w", err)
		}
	}

	// 加载环境配置
	envPath := filepath.Join(m.baseDir, string(m.currentEnv), fmt.Sprintf("%s.yaml", name))
	envConfig, err := LoadWorkflowConfig(envPath)
	if err != nil {
		// 如果环境配置不存在但有 base 配置，使用 base 配置
		if baseConfig != nil && os.IsNotExist(err) {
			return baseConfig, nil
		}
		return nil, fmt.Errorf("failed to load env config: %w", err)
	}

	// 合并配置（简化版本，只覆盖顶层字段）
	if baseConfig != nil {
		envConfig = mergeWorkflowConfigs(baseConfig, envConfig)
	}

	return envConfig, nil
}

// SwitchEnvironment 切换环境
//
// 参数：
//   - env: 目标环境
//
// 返回值：
//   - error: 错误（如果有）
func (m *EnvironmentManager) SwitchEnvironment(env Environment) error {
	// 验证环境目录存在
	envDir := filepath.Join(m.baseDir, string(env))
	if _, err := os.Stat(envDir); os.IsNotExist(err) {
		return fmt.Errorf("environment %s directory not found: %s", env, envDir)
	}

	m.currentEnv = env
	return nil
}

// CurrentEnvironment 返回当前环境
func (m *EnvironmentManager) CurrentEnvironment() Environment {
	return m.currentEnv
}

// ListEnvironments 列出所有可用环境
//
// 返回值：
//   - []Environment: 环境列表
func (m *EnvironmentManager) ListEnvironments() ([]Environment, error) {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read config directory: %w", err)
	}

	envs := make([]Environment, 0)
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "base" {
			envs = append(envs, Environment(entry.Name()))
		}
	}

	return envs, nil
}

// InitializeEnvironments 初始化环境目录结构
//
// 创建标准的环境目录结构：
//
//	config/
//	  base/          # 基础配置
//	  development/   # 开发环境
//	  test/          # 测试环境
//	  staging/       # 预发布环境
//	  production/    # 生产环境
func (m *EnvironmentManager) InitializeEnvironments() error {
	envs := []string{
		"base",
		string(EnvDevelopment),
		string(EnvTest),
		string(EnvStaging),
		string(EnvProduction),
	}

	for _, env := range envs {
		dir := filepath.Join(m.baseDir, env)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create %s directory: %w", env, err)
		}
	}

	return nil
}

// LoadRawConfig 加载原始配置文件（不进行合并）
//
// 参数：
//   - env: 环境
//   - name: 配置名称
//
// 返回值：
//   - map[string]any: 原始配置数据
//   - error: 错误（如果有）
func (m *EnvironmentManager) LoadRawConfig(env Environment, name string) (map[string]any, error) {
	if err := validateConfigName(name); err != nil {
		return nil, err
	}
	path := filepath.Join(m.baseDir, string(env), fmt.Sprintf("%s.yaml", name))

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return config, nil
}

// SaveConfig 保存配置到指定环境
//
// 参数：
//   - env: 环境
//   - name: 配置名称
//   - config: 配置对象
//
// 返回值：
//   - error: 错误（如果有）
func (m *EnvironmentManager) SaveConfig(env Environment, name string, config any) error {
	if err := validateConfigName(name); err != nil {
		return err
	}
	path := filepath.Join(m.baseDir, string(env), fmt.Sprintf("%s.yaml", name))

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// CopyConfig 复制配置从一个环境到另一个环境
//
// 参数：
//   - name: 配置名称
//   - fromEnv: 源环境
//   - toEnv: 目标环境
//
// 返回值：
//   - error: 错误（如果有）
//
// validateConfigName 防路径穿越：配置名必须是简单标识符，不含 `..` / 路径分隔符 / 绝对路径，
// 否则 filepath.Join(baseDir, env, name+".yaml") 可逃逸 baseDir（gosec G703）。
func validateConfigName(name string) error {
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) || filepath.IsAbs(name) {
		return fmt.Errorf("invalid config name %q (must be a simple identifier)", name)
	}
	return nil
}

func (m *EnvironmentManager) CopyConfig(name string, fromEnv, toEnv Environment) error {
	if err := validateConfigName(name); err != nil {
		return err
	}
	// 读取源配置
	srcPath := filepath.Join(m.baseDir, string(fromEnv), fmt.Sprintf("%s.yaml", name))
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("failed to read source config: %w", err)
	}

	// 写入目标配置
	dstPath := filepath.Join(m.baseDir, string(toEnv), fmt.Sprintf("%s.yaml", name))
	if err := os.WriteFile(dstPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write dest config: %w", err)
	}

	return nil
}

// mergeTeamConfigs 合并 Team 配置
func mergeTeamConfigs(base, override *TeamConfig) *TeamConfig {
	result := *base

	if override.Name != "" {
		result.Name = override.Name
	}
	if override.Description != "" {
		result.Description = override.Description
	}
	if override.Mode != "" {
		result.Mode = override.Mode
	}
	if override.Manager != "" {
		result.Manager = override.Manager
	}
	if len(override.Agents) > 0 {
		result.Agents = override.Agents
	}
	if override.MaxRounds != 0 {
		result.MaxRounds = override.MaxRounds
	}

	return &result
}

// mergeWorkflowConfigs 合并 Workflow 配置
func mergeWorkflowConfigs(base, override *WorkflowConfig) *WorkflowConfig {
	result := *base

	if override.Name != "" {
		result.Name = override.Name
	}
	if override.Description != "" {
		result.Description = override.Description
	}
	if len(override.Nodes) > 0 {
		result.Nodes = override.Nodes
	}
	if len(override.Edges) > 0 {
		result.Edges = override.Edges
	}
	if override.EntryPoint != "" {
		result.EntryPoint = override.EntryPoint
	}

	return &result
}

// ValidateEnvironmentSetup 验证环境配置设置
//
// 检查：
//   - 环境目录是否存在
//   - 必需的配置文件是否存在
//   - 配置文件是否有效
//
// 参数：
//   - env: 环境
//
// 返回值：
//   - []string: 验证问题列表
//   - error: 错误（如果有）
func (m *EnvironmentManager) ValidateEnvironmentSetup(env Environment) ([]string, error) {
	issues := make([]string, 0)

	// 检查环境目录
	envDir := filepath.Join(m.baseDir, string(env))
	if _, err := os.Stat(envDir); os.IsNotExist(err) {
		issues = append(issues, fmt.Sprintf("Environment directory %s does not exist", envDir))
		return issues, nil
	}

	// 查找所有配置文件
	files, err := filepath.Glob(filepath.Join(envDir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("failed to glob config files: %w", err)
	}

	yamlFiles, err := filepath.Glob(filepath.Join(envDir, "*.yml"))
	if err != nil {
		return nil, fmt.Errorf("failed to glob config files: %w", err)
	}
	files = append(files, yamlFiles...)

	// 验证每个配置文件
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			issues = append(issues, fmt.Sprintf("Failed to read %s: %v", filepath.Base(file), err))
			continue
		}

		var doc map[string]any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			issues = append(issues, fmt.Sprintf("Invalid YAML in %s: %v", filepath.Base(file), err))
			continue
		}

		// 判断配置类型并验证
		validator := NewValidator()
		if _, ok := doc["agents"]; ok {
			var teamConfig TeamConfig
			if err := yaml.Unmarshal(data, &teamConfig); err == nil {
				if err := validator.ValidateTeamConfig(&teamConfig); err != nil {
					issues = append(issues, fmt.Sprintf("Invalid team config in %s: %v", filepath.Base(file), err))
				}
			}
		} else if _, ok := doc["nodes"]; ok {
			var workflowConfig WorkflowConfig
			if err := yaml.Unmarshal(data, &workflowConfig); err == nil {
				if err := validator.ValidateWorkflowConfig(&workflowConfig); err != nil {
					issues = append(issues, fmt.Sprintf("Invalid workflow config in %s: %v", filepath.Base(file), err))
				}
			}
		} else if _, ok := doc["llm"]; ok {
			var agentConfig AgentConfig
			if err := yaml.Unmarshal(data, &agentConfig); err == nil {
				if err := validator.ValidateAgentConfig(&agentConfig); err != nil {
					issues = append(issues, fmt.Sprintf("Invalid agent config in %s: %v", filepath.Base(file), err))
				}
			}
		}
	}

	return issues, nil
}
