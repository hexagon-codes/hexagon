// Package config 提供配置版本管理能力
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/hash"
	"gopkg.in/yaml.v3"
)

// Version 配置版本
//
// 记录配置的版本信息，包括内容哈希、时间戳、变更描述等。
type Version struct {
	// ID 版本 ID（时间戳 + 哈希前8位）
	ID string `json:"id" yaml:"id"`

	// Hash 配置内容哈希（SHA256）
	Hash string `json:"hash" yaml:"hash"`

	// Timestamp 创建时间戳
	Timestamp time.Time `json:"timestamp" yaml:"timestamp"`

	// Description 版本描述
	Description string `json:"description" yaml:"description"`

	// Tags 版本标签（如 production, staging, test）
	Tags []string `json:"tags,omitempty" yaml:"tags,omitempty"`

	// Config 配置内容（JSON 格式）
	Config string `json:"config" yaml:"config"`

	// ConfigType 配置类型（agent/team/workflow）
	ConfigType string `json:"config_type" yaml:"config_type"`
}

// VersionManager 版本管理器
//
// 管理配置的版本历史，支持：
//   - 版本创建和保存
//   - 版本回滚
//   - 版本对比
//   - 版本查询
//
// 线程安全：所有方法都是并发安全的
type VersionManager struct {
	// storePath 版本存储路径
	storePath string

	// versions 版本列表（按时间倒序）
	versions []*Version

	// mu 互斥锁
	mu sync.RWMutex
}

// VersionManagerOption 版本管理器选项
type VersionManagerOption func(*VersionManager)

// WithStorePath 设置版本存储路径
func WithStorePath(path string) VersionManagerOption {
	return func(m *VersionManager) {
		m.storePath = path
	}
}

// NewVersionManager 创建版本管理器
//
// 参数：
//   - opts: 可选配置
//
// 返回值：
//   - *VersionManager: 版本管理器实例
//   - error: 错误（如果有）
func NewVersionManager(opts ...VersionManagerOption) (*VersionManager, error) {
	m := &VersionManager{
		storePath: ".hexagon/versions", // 默认存储路径
		versions:  make([]*Version, 0),
	}

	for _, opt := range opts {
		opt(m)
	}

	// 确保存储目录存在
	if err := os.MkdirAll(m.storePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create version store directory: %w", err)
	}

	// 加载历史版本
	if err := m.loadVersions(); err != nil {
		return nil, err
	}

	return m, nil
}

// SaveVersion 保存配置版本
//
// 参数：
//   - config: 配置对象（*AgentConfig/*TeamConfig/*WorkflowConfig）
//   - configType: 配置类型
//   - description: 版本描述
//   - tags: 版本标签
//
// 返回值：
//   - *Version: 创建的版本
//   - error: 错误（如果有）
func (m *VersionManager) SaveVersion(config any, configType, description string, tags ...string) (*Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 序列化配置
	configJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	// 计算哈希
	hash := m.calculateHash(configJSON)

	// 检查是否有变化
	if len(m.versions) > 0 && m.versions[0].Hash == hash {
		return nil, fmt.Errorf("no changes detected")
	}

	// 创建版本
	now := time.Now()
	version := &Version{
		ID:          fmt.Sprintf("%d-%s", now.Unix(), hash[:8]),
		Hash:        hash,
		Timestamp:   now,
		Description: description,
		Tags:        tags,
		Config:      string(configJSON),
		ConfigType:  configType,
	}

	// 保存到文件
	if err := m.saveVersionToFile(version); err != nil {
		return nil, err
	}

	// 添加到列表（头部）
	m.versions = append([]*Version{version}, m.versions...)

	return version, nil
}

// GetVersion 获取指定版本
//
// 参数：
//   - id: 版本 ID
//
// 返回值：
//   - *Version: 版本信息
//   - error: 错误（如果有）
func (m *VersionManager) GetVersion(id string) (*Version, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, v := range m.versions {
		if v.ID == id {
			return v, nil
		}
	}

	return nil, fmt.Errorf("version %s not found", id)
}

// GetLatestVersion 获取最新版本
func (m *VersionManager) GetLatestVersion() (*Version, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.versions) == 0 {
		return nil, fmt.Errorf("no versions available")
	}

	return m.versions[0], nil
}

// ListVersions 列出所有版本
//
// 参数：
//   - limit: 最大返回数量（0 表示不限制）
//
// 返回值：
//   - []*Version: 版本列表（按时间倒序）
func (m *VersionManager) ListVersions(limit int) []*Version {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 || limit > len(m.versions) {
		limit = len(m.versions)
	}

	result := make([]*Version, limit)
	copy(result, m.versions[:limit])
	return result
}

// ListVersionsByTag 根据标签列出版本
//
// 参数：
//   - tag: 标签名称
//
// 返回值：
//   - []*Version: 版本列表
func (m *VersionManager) ListVersionsByTag(tag string) []*Version {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Version, 0)
	for _, v := range m.versions {
		for _, t := range v.Tags {
			if t == tag {
				result = append(result, v)
				break
			}
		}
	}
	return result
}

// RollbackToVersion 回滚到指定版本
//
// 参数：
//   - id: 版本 ID
//   - targetPath: 目标配置文件路径
//
// 返回值：
//   - any: 恢复的配置对象
//   - error: 错误（如果有）
func (m *VersionManager) RollbackToVersion(id, targetPath string) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 获取版本
	var version *Version
	for _, v := range m.versions {
		if v.ID == id {
			version = v
			break
		}
	}

	if version == nil {
		return nil, fmt.Errorf("version %s not found", id)
	}

	// 反序列化配置
	var config any
	var parseErr error

	switch version.ConfigType {
	case "agent":
		var agentConfig AgentConfig
		parseErr = json.Unmarshal([]byte(version.Config), &agentConfig)
		config = &agentConfig

	case "team":
		var teamConfig TeamConfig
		parseErr = json.Unmarshal([]byte(version.Config), &teamConfig)
		config = &teamConfig

	case "workflow":
		var workflowConfig WorkflowConfig
		parseErr = json.Unmarshal([]byte(version.Config), &workflowConfig)
		config = &workflowConfig

	default:
		return nil, fmt.Errorf("unknown config type: %s", version.ConfigType)
	}

	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse config: %w", parseErr)
	}

	// 写入文件
	yamlData, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config to YAML: %w", err)
	}

	if err := os.WriteFile(targetPath, yamlData, 0644); err != nil {
		return nil, fmt.Errorf("failed to write config file: %w", err)
	}

	return config, nil
}

// DeleteVersion 删除指定版本
//
// 参数：
//   - id: 版本 ID
//
// 返回值：
//   - error: 错误（如果有）
func (m *VersionManager) DeleteVersion(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 查找版本
	index := -1
	for i, v := range m.versions {
		if v.ID == id {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("version %s not found", id)
	}

	// 删除文件
	versionFile := filepath.Join(m.storePath, fmt.Sprintf("%s.json", id))
	if err := os.Remove(versionFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete version file: %w", err)
	}

	// 从列表中移除
	m.versions = append(m.versions[:index], m.versions[index+1:]...)

	return nil
}

// TagVersion 给版本添加标签
//
// 参数：
//   - id: 版本 ID
//   - tags: 标签列表
//
// 返回值：
//   - error: 错误（如果有）
func (m *VersionManager) TagVersion(id string, tags ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 查找版本
	var version *Version
	for _, v := range m.versions {
		if v.ID == id {
			version = v
			break
		}
	}

	if version == nil {
		return fmt.Errorf("version %s not found", id)
	}

	// 添加标签（去重）
	tagMap := make(map[string]bool)
	for _, t := range version.Tags {
		tagMap[t] = true
	}
	for _, t := range tags {
		tagMap[t] = true
	}

	version.Tags = make([]string, 0, len(tagMap))
	for t := range tagMap {
		version.Tags = append(version.Tags, t)
	}
	sort.Strings(version.Tags)

	// 更新文件
	return m.saveVersionToFile(version)
}

// calculateHash 计算配置哈希
func (m *VersionManager) calculateHash(data []byte) string {
	return hash.SHA256Bytes(data)
}

// saveVersionToFile 保存版本到文件
func (m *VersionManager) saveVersionToFile(version *Version) error {
	versionFile := filepath.Join(m.storePath, fmt.Sprintf("%s.json", version.ID))

	data, err := json.MarshalIndent(version, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal version: %w", err)
	}

	if err := os.WriteFile(versionFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write version file: %w", err)
	}

	return nil
}

// loadVersions 加载历史版本
func (m *VersionManager) loadVersions() error {
	// 查找所有版本文件
	files, err := filepath.Glob(filepath.Join(m.storePath, "*.json"))
	if err != nil {
		return fmt.Errorf("failed to glob version files: %w", err)
	}

	versions := make([]*Version, 0, len(files))

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		var version Version
		if err := json.Unmarshal(data, &version); err != nil {
			continue
		}

		versions = append(versions, &version)
	}

	// 按时间倒序排序
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Timestamp.After(versions[j].Timestamp)
	})

	m.versions = versions
	return nil
}

// Cleanup 清理旧版本
//
// 参数：
//   - keepCount: 保留的版本数量
//
// 返回值：
//   - int: 删除的版本数量
//   - error: 错误（如果有）
func (m *VersionManager) Cleanup(keepCount int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if keepCount <= 0 {
		return 0, fmt.Errorf("keepCount must be positive")
	}

	if len(m.versions) <= keepCount {
		return 0, nil
	}

	// 删除旧版本
	deleted := 0
	toDelete := m.versions[keepCount:]

	for _, v := range toDelete {
		versionFile := filepath.Join(m.storePath, fmt.Sprintf("%s.json", v.ID))
		if err := os.Remove(versionFile); err != nil && !os.IsNotExist(err) {
			continue
		}
		deleted++
	}

	// 更新列表
	m.versions = m.versions[:keepCount]

	return deleted, nil
}

// Count 返回版本数量
func (m *VersionManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.versions)
}
