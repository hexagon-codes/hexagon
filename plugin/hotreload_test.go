package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNewHotReloadManager 测试创建热更新管理器
func TestNewHotReloadManager(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		manager := NewPluginManager()
		hrm := NewHotReloadManager(manager)

		if hrm.manager != manager {
			t.Error("manager 应该正确设置")
		}
		if hrm.interval != 5*time.Second {
			t.Errorf("默认间隔应为 5s，实际为 %v", hrm.interval)
		}
		if hrm.running {
			t.Error("初始状态不应为运行中")
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		manager := NewPluginManager()

		hrm := NewHotReloadManager(manager,
			WithHotReloadInterval(10*time.Second),
			WithHotReloadCallback(func(name string, old, new *PluginVersion, err error) {
				// 回调函数用于测试
			}),
		)

		if hrm.interval != 10*time.Second {
			t.Errorf("间隔应为 10s，实际为 %v", hrm.interval)
		}
		if hrm.callback == nil {
			t.Error("callback 应该已设置")
		}
	})
}

// TestHotReloadManagerWatch 测试监控配置
func TestHotReloadManagerWatch(t *testing.T) {
	t.Run("监控存在的文件", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")

		// 创建配置文件
		if err := os.WriteFile(configPath, []byte("plugins: []"), 0644); err != nil {
			t.Fatalf("创建文件失败: %v", err)
		}

		manager := NewPluginManager()
		hrm := NewHotReloadManager(manager)

		err := hrm.Watch("test-plugin", configPath)
		if err != nil {
			t.Fatalf("Watch 失败: %v", err)
		}

		watched := hrm.GetWatchedPlugins()
		if len(watched) != 1 || watched[0] != "test-plugin" {
			t.Errorf("监控列表不正确: %v", watched)
		}
	})

	t.Run("监控不存在的文件", func(t *testing.T) {
		manager := NewPluginManager()
		hrm := NewHotReloadManager(manager)

		err := hrm.Watch("nonexistent", "/nonexistent/path.yaml")
		if err == nil {
			t.Error("应该返回错误")
		}
	})
}

// TestHotReloadManagerUnwatch 测试取消监控
func TestHotReloadManagerUnwatch(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(configPath, []byte("plugins: []"), 0644)

	manager := NewPluginManager()
	hrm := NewHotReloadManager(manager)

	hrm.Watch("test-plugin", configPath)
	hrm.Unwatch("test-plugin")

	watched := hrm.GetWatchedPlugins()
	if len(watched) != 0 {
		t.Errorf("取消监控后列表应为空: %v", watched)
	}
}

// TestHotReloadManagerStartStop 测试启动和停止
func TestHotReloadManagerStartStop(t *testing.T) {
	t.Run("启动和停止", func(t *testing.T) {
		manager := NewPluginManager()
		hrm := NewHotReloadManager(manager, WithHotReloadInterval(100*time.Millisecond))

		if err := hrm.Start(); err != nil {
			t.Fatalf("启动失败: %v", err)
		}

		if !hrm.IsRunning() {
			t.Error("应该处于运行状态")
		}

		// 等待一小段时间让监控 goroutine 运行
		time.Sleep(150 * time.Millisecond)

		hrm.Stop()

		if hrm.IsRunning() {
			t.Error("停止后不应处于运行状态")
		}
	})

	t.Run("重复启动", func(t *testing.T) {
		manager := NewPluginManager()
		hrm := NewHotReloadManager(manager)

		hrm.Start()
		defer hrm.Stop()

		err := hrm.Start()
		if err == nil {
			t.Error("重复启动应该返回错误")
		}
	})

	t.Run("重复停止", func(t *testing.T) {
		manager := NewPluginManager()
		hrm := NewHotReloadManager(manager)

		// 停止未运行的管理器不应 panic
		hrm.Stop()
	})
}

// TestHotReloadManagerVersionHistory 测试版本历史
func TestHotReloadManagerVersionHistory(t *testing.T) {
	manager := NewPluginManager()
	hrm := NewHotReloadManager(manager)

	// 保存版本
	hrm.saveVersion("test-plugin", map[string]any{"key": "value1"})
	hrm.saveVersion("test-plugin", map[string]any{"key": "value2"})

	history := hrm.GetVersionHistory("test-plugin")
	if len(history) != 2 {
		t.Errorf("应该有 2 个版本，实际有 %d 个", len(history))
	}

	// 不存在的插件
	history = hrm.GetVersionHistory("nonexistent")
	if history != nil {
		t.Error("不存在的插件版本历史应为 nil")
	}
}

// TestHotReloadManagerVersionLimit 测试版本数量限制
func TestHotReloadManagerVersionLimit(t *testing.T) {
	manager := NewPluginManager()
	hrm := NewHotReloadManager(manager)

	// 保存超过 10 个版本
	for i := 0; i < 15; i++ {
		hrm.saveVersion("test-plugin", map[string]any{"version": i})
	}

	history := hrm.GetVersionHistory("test-plugin")
	if len(history) != 10 {
		t.Errorf("最多保留 10 个版本，实际有 %d 个", len(history))
	}
}

// TestHotReloadManagerGetCurrentVersion 测试获取当前版本
func TestHotReloadManagerGetCurrentVersion(t *testing.T) {
	t.Run("有版本", func(t *testing.T) {
		manager := NewPluginManager()
		hrm := NewHotReloadManager(manager)

		hrm.saveVersion("test-plugin", map[string]any{"key": "value"})

		version := hrm.getCurrentVersion("test-plugin")
		if version == nil {
			t.Fatal("应该返回版本")
		}
		if version.Config["key"] != "value" {
			t.Error("版本配置不正确")
		}
	})

	t.Run("无版本", func(t *testing.T) {
		manager := NewPluginManager()
		hrm := NewHotReloadManager(manager)

		version := hrm.getCurrentVersion("nonexistent")
		if version != nil {
			t.Error("不存在的插件应返回 nil")
		}
	})
}

// TestHotReloadManagerStats 测试统计信息
func TestHotReloadManagerStats(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(configPath, []byte("plugins: []"), 0644)

	manager := NewPluginManager()
	hrm := NewHotReloadManager(manager, WithHotReloadInterval(1*time.Second))

	hrm.Watch("plugin-1", configPath)
	hrm.saveVersion("plugin-1", nil)
	hrm.saveVersion("plugin-1", nil)

	stats := hrm.Stats()

	if stats["watched_plugins"].(int) != 1 {
		t.Errorf("watched_plugins 应为 1")
	}
	if stats["total_versions"].(int) != 2 {
		t.Errorf("total_versions 应为 2")
	}
	if stats["running"].(bool) != false {
		t.Error("running 应为 false")
	}
	if stats["interval"].(string) != "1s" {
		t.Errorf("interval 应为 1s")
	}
}

// TestPluginVersion 测试插件版本
func TestPluginVersion(t *testing.T) {
	version := PluginVersion{
		Version:  "20240101-120000",
		LoadedAt: time.Now(),
		Config:   map[string]any{"key": "value"},
		Hash:     "abc123",
	}

	if version.Version == "" {
		t.Error("Version 不应为空")
	}
	if version.Config["key"] != "value" {
		t.Error("Config 不正确")
	}
}

// TestUpdateOptions 测试更新选项
func TestUpdateOptions(t *testing.T) {
	t.Run("默认选项", func(t *testing.T) {
		opts := DefaultUpdateOptions()

		if opts.Strategy != UpdateStrategyImmediate {
			t.Errorf("默认策略应为 Immediate")
		}
		if opts.GracePeriod != 30*time.Second {
			t.Errorf("默认优雅期应为 30s")
		}
		if !opts.BackupConfig {
			t.Error("默认应备份配置")
		}
		if !opts.RollbackOnError {
			t.Error("默认应错误时回滚")
		}
	})
}

// TestPluginUpdateStrategy 测试更新策略常量
func TestPluginUpdateStrategy(t *testing.T) {
	strategies := []PluginUpdateStrategy{
		UpdateStrategyImmediate,
		UpdateStrategyGraceful,
		UpdateStrategyScheduled,
	}

	for _, s := range strategies {
		if s == "" {
			t.Error("策略不应为空")
		}
	}
}

// TestComputeConfigHash 测试配置哈希
func TestComputeConfigHash(t *testing.T) {
	config := map[string]any{"key": "value"}

	hash1 := computeConfigHash(config)
	hash2 := computeConfigHash(config)

	if len(hash1) != 64 {
		t.Fatalf("配置 SHA-256 长度 = %d，期望 64", len(hash1))
	}
	if hash1 != hash2 {
		t.Fatal("相同配置必须得到稳定指纹")
	}
	if hash1 == computeConfigHash(map[string]any{"key": "other"}) {
		t.Fatal("不同配置不得得到相同测试指纹")
	}
}

// TestHotReloadManagerRollbackToVersion 测试回滚到指定版本
func TestHotReloadManagerRollbackToVersion(t *testing.T) {
	t.Run("版本不存在", func(t *testing.T) {
		manager := NewPluginManager()
		hrm := NewHotReloadManager(manager)

		ctx := context.Background()
		err := hrm.RollbackToVersion(ctx, "test-plugin", "nonexistent")

		if err == nil {
			t.Error("应该返回错误")
		}
	})

	t.Run("插件无版本历史", func(t *testing.T) {
		manager := NewPluginManager()
		hrm := NewHotReloadManager(manager)

		ctx := context.Background()
		err := hrm.RollbackToVersion(ctx, "no-history", "1.0.0")

		if err == nil {
			t.Error("应该返回错误")
		}
	})
}

// TestHotReloadManagerUpdatePlugin 测试更新插件
func TestHotReloadManagerUpdatePlugin(t *testing.T) {
	t.Run("未知策略", func(t *testing.T) {
		manager := NewPluginManager()
		hrm := NewHotReloadManager(manager)

		ctx := context.Background()
		opts := UpdateOptions{
			Strategy: "unknown",
		}

		err := hrm.UpdatePlugin(ctx, "test", "/path", opts)
		if err == nil {
			t.Error("未知策略应该返回错误")
		}
	})
}
