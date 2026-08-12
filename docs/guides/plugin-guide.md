<div align="right">语言: 中文 | <a href="plugin-guide.en.md">English</a></div>

# 插件开发指南

本指南基于 `github.com/hexagon-codes/hexagon/plugin` 当前公开 API，介绍插件定义、注册、生命周期、配置加载、依赖校验和健康检查。

## 能力边界

Hexagon 插件是编译进 Go 程序并显式注册的组件。`Loader` 读取 YAML 配置后，通过预先注册的 `PluginFactory` 创建插件；它不会从目录动态加载 Go 二进制。

`PluginTypeProvider`、`PluginTypeTool`、`PluginTypeMiddleware` 等值只表示插件元数据分类，不会自动把 Provider、Tool 或 Middleware 注册到其他全局注册器。应用应通过实际使用方提供的构造参数或注册表完成这些对象的接线。

## Plugin 接口

插件必须提供元信息、生命周期方法和当前健康状态：

```go
type Plugin interface {
    Info() PluginInfo
    Init(ctx context.Context, config map[string]any) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Health() HealthStatus
}
```

`Info().Name` 是注册表中的唯一标识。`PluginInfo.Dependencies` 用于声明其他插件依赖。

### 使用 BasePlugin

`BasePlugin` 已提供 `Info`、`Init`、`Start`、`Stop`、`Health` 和配置读取的默认实现。插件可以嵌入它，只覆盖自身需要的行为：

```go
package myplugin

import (
    "context"
    "errors"

    "github.com/hexagon-codes/hexagon/plugin"
)

type MyPlugin struct {
    *plugin.BasePlugin
    endpoint string
}

func New() *MyPlugin {
    return &MyPlugin{
        BasePlugin: plugin.NewBasePlugin(plugin.PluginInfo{
            Name:         "my-plugin",
            Version:      "1.0.0",
            Type:         plugin.PluginTypeExtension,
            Description:  "My application extension",
        }),
    }
}

func (p *MyPlugin) Init(ctx context.Context, config map[string]any) error {
    endpoint, _ := config["endpoint"].(string)
    if endpoint == "" {
        return errors.New("endpoint is required")
    }
    p.endpoint = endpoint
    return p.BasePlugin.Init(ctx, config)
}

var _ plugin.Plugin = (*MyPlugin)(nil)
```

默认 `BasePlugin` 在创建后返回 `unknown`，`Start` 后返回 `healthy`，`Stop` 后恢复为 `unknown`。需要更细粒度状态时，可以覆盖 `Health()` 并返回 `plugin.HealthStatus`。

## 插件注册

### 全局注册表

简单程序可以使用包级注册函数：

```go
if err := plugin.RegisterPlugin(myplugin.New()); err != nil {
    return err
}

p, err := plugin.GetPlugin("my-plugin")
if err != nil {
    return err
}
_ = p
```

同名插件重复注册会返回错误。

### 独立注册表

需要隔离实例或进行测试时，使用独立 `Registry`：

```go
registry := plugin.NewRegistry()

if err := registry.Register(myplugin.New()); err != nil {
    return err
}

p, err := registry.Get("my-plugin")
if err != nil {
    return err
}
_ = p
```

注册后实例状态为 `loaded`。运行中的插件不能直接 `Unregister`，应先通过生命周期管理器停止。

## 生命周期管理

`Lifecycle` 必须与承载插件的同一个 `Registry` 一起构造。`Init` 接收插件名称和配置，`Start`、`Stop` 也按名称操作：

```go
registry := plugin.NewRegistry()
lifecycle := plugin.NewLifecycle(
    registry,
    plugin.WithHealthCheckInterval(30*time.Second),
)

p := myplugin.New()
if err := registry.Register(p); err != nil {
    return err
}
if err := lifecycle.Init(ctx, p.Info().Name, map[string]any{
    "endpoint": "https://service.example.com",
}); err != nil {
    return err
}
if err := lifecycle.Start(ctx, p.Info().Name); err != nil {
    return err
}
if err := lifecycle.Stop(ctx, p.Info().Name); err != nil {
    return err
}
```

批量场景可使用 `InitAll(ctx, configs)`、`StartAll(ctx)` 和 `StopAll(ctx)`。`StartAll` 按插件依赖启动，`StopAll` 按启动顺序的逆序停止，并聚合停止错误。

`PluginManager` 把注册表和生命周期组合在一起，提供 `Load`、`Enable`、`Disable` 和 `Unload`：

```go
manager := plugin.NewPluginManager()
if err := manager.Load(myplugin.New()); err != nil {
    return err
}
if err := manager.Enable(ctx, "my-plugin", map[string]any{
    "endpoint": "https://service.example.com",
}); err != nil {
    return err
}
defer manager.Unload(ctx, "my-plugin")
```

## 从配置加载

配置加载依赖插件工厂。必须先在同一个 `Registry` 注册工厂，再构造 `Loader`：

```go
registry := plugin.NewRegistry()
lifecycle := plugin.NewLifecycle(registry)

if err := registry.RegisterFactory("my-plugin", func() plugin.Plugin {
    return myplugin.New()
}); err != nil {
    return err
}

loader := plugin.NewLoader(
    registry,
    lifecycle,
    plugin.WithSearchPaths("./plugins"),
)
if err := loader.LoadFromConfig(ctx, "plugins.yaml"); err != nil {
    return err
}
if err := lifecycle.StartAll(ctx); err != nil {
    return err
}
defer lifecycle.StopAll(ctx)
```

`plugins.yaml` 对应 `PluginsConfig` 和 `PluginConfig`：

```yaml
plugins:
  - name: my-plugin
    enabled: true
    priority: 10
    config:
      endpoint: https://service.example.com
```

`LoadFromConfig` 会按 `priority` 从小到大处理已启用插件，并完成工厂创建、注册和初始化；启动仍需显式调用 `Start` 或 `StartAll`。

## 插件清单

`PluginManifest` 的 YAML 顶层字段为 `info`、`config`、`config_schema` 和可选的 `hooks`。下面是与当前结构一致的最小清单：

```yaml
info:
  name: my-plugin
  version: 1.0.0
  type: extension
  description: My application extension
  author: Hexagon Team
  license: Apache-2.0
  homepage: https://example.com/my-plugin
  dependencies:
    - database-plugin
  tags:
    - integration

config:
  endpoint: https://service.example.com

config_schema:
  type: object
  required:
    - endpoint
  properties:
    endpoint:
      type: string
```

使用 `ParseManifest(data)` 或 `LoadManifest(path)` 解析清单：

```go
manifest, err := plugin.LoadManifest("plugin.yaml")
if err != nil {
    return err
}
fmt.Println(manifest.Info.Name)
```

清单解析只返回结构化元数据，不会注册、初始化或启动插件。

## 依赖管理

### 运行时依赖

参与 `Lifecycle.Start` / `StartAll` 的 `PluginInfo.Dependencies` 应填写已经注册的插件名称，例如 `database-plugin`。被依赖插件必须先进入 `running` 状态。

### 依赖图

`DependencyGraph` 使用 `AddNode` 建图，并通过 `DetectCycle` 和 `TopologicalSort` 检查图结构：

```go
graph := plugin.NewDependencyGraph()
graph.AddNode("database-plugin", "1.2.0", nil)
graph.AddNode("my-plugin", "1.0.0", []plugin.Dependency{
    {Name: "database-plugin", Version: ">=1.0.0"},
})

if cycle := graph.DetectCycle(); cycle != nil {
    return fmt.Errorf("circular dependency: %v", cycle)
}

graphOrder, err := graph.TopologicalSort()
if err != nil {
    return err
}
_ = graphOrder
```

`DependencyResolver.CheckDependencies` 可针对已注册插件校验存在性和版本约束。约束写法为 `name@constraint`，支持 `=`、`>`、`>=`、`<`、`<=`、`~>` 和 `^`：

```go
dependencyRegistry := plugin.NewRegistry()
if err := dependencyRegistry.Register(plugin.NewBasePlugin(plugin.PluginInfo{
    Name:    "database-plugin",
    Version: "1.2.0",
    Type:    plugin.PluginTypeExtension,
})); err != nil {
    return err
}

resolver := plugin.NewDependencyResolver(dependencyRegistry)
err := resolver.CheckDependencies(plugin.PluginInfo{
    Name:         "my-plugin",
    Version:      "1.0.0",
    Dependencies: []string{"database-plugin@>=1.0.0"},
})
if err != nil {
    return err
}
```

版本约束校验与生命周期启动是两个步骤；生命周期按插件名称查找依赖，因此用于 `Start` / `StartAll` 的实际插件元信息应保留纯名称依赖。

## 健康检查

每个插件通过无参数的 `Health()` 返回 `HealthStatus`。`Lifecycle.HealthCheck(ctx)` 汇总所有插件；未运行的插件返回 `unknown`：

```go
statuses := lifecycle.HealthCheck(ctx)
for name, status := range statuses {
    if status.Status != plugin.HealthStateHealthy {
        log.Printf("plugin %s is not healthy: %s", name, status.Message)
    }
}
```

如需周期检查，可在传入可取消的 `context.Context` 后调用 `StartHealthChecker(ctx)`，并在退出时调用 `StopHealthChecker()`。

## 实践建议

1. 在 `Init` 中验证配置，错误信息保持明确且可操作。
2. 在 `Start` 和 `Stop` 中遵循 `context.Context` 的取消与超时。
3. 只在 `Start` 成功后报告 `healthy`，并在 `HealthStatus.LastCheck` 中记录检查时间。
4. 对依赖先做存在性、版本和循环校验，再启动插件。
5. 使用独立 `Registry` 隔离测试，避免全局注册表在测试之间互相影响。
6. 始终停止已启动的插件，释放连接、goroutine 和其他资源。
