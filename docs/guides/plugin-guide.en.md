<div align="right">Language: <a href="plugin-guide.md">中文</a> | English</div>

# Plugin Development Guide

This guide follows the current public API of `github.com/hexagon-codes/hexagon/plugin` and covers plugin definitions, registration, lifecycle management, configuration loading, dependency validation, and health checks.

## Scope

Hexagon plugins are components compiled into a Go program and registered explicitly. The `Loader` reads YAML configuration and creates plugins through previously registered `PluginFactory` values; it does not dynamically load Go binaries from a directory.

Values such as `PluginTypeProvider`, `PluginTypeTool`, and `PluginTypeMiddleware` are metadata classifications only. They do not automatically register Providers, Tools, or Middleware with another global registry. Wire those objects through the constructors or registries provided by their actual consumers.

## Plugin Interface

A plugin provides metadata, lifecycle methods, and its current health status:

```go
type Plugin interface {
    Info() PluginInfo
    Init(ctx context.Context, config map[string]any) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Health() HealthStatus
}
```

`Info().Name` is the unique key in a registry. `PluginInfo.Dependencies` declares dependencies on other plugins.

### Using BasePlugin

`BasePlugin` supplies default implementations of `Info`, `Init`, `Start`, `Stop`, `Health`, and configuration accessors. Embed it and override only the behavior your plugin needs:

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

By default, a new `BasePlugin` reports `unknown`, reports `healthy` after `Start`, and returns to `unknown` after `Stop`. Override `Health()` and return `plugin.HealthStatus` when finer-grained status is required.

## Plugin Registration

### Global Registry

Small programs can use the package-level registration functions:

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

Registering another plugin with the same name returns an error.

### Isolated Registry

Use an independent `Registry` to isolate instances or tests:

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

A registered instance starts in the `loaded` state. A running plugin cannot be unregistered directly; stop it through the lifecycle manager first.

## Lifecycle Management

Construct `Lifecycle` with the same `Registry` that holds the plugins. `Init` takes the plugin name and configuration, while `Start` and `Stop` also operate by name:

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

For batches, use `InitAll(ctx, configs)`, `StartAll(ctx)`, and `StopAll(ctx)`. `StartAll` starts plugins according to their dependencies. `StopAll` stops them in reverse start order and joins stop errors.

`PluginManager` combines a registry and lifecycle and provides `Load`, `Enable`, `Disable`, and `Unload`:

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

## Loading from Configuration

Configuration loading depends on plugin factories. Register each factory with the same `Registry` before constructing a `Loader`:

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

`plugins.yaml` maps to `PluginsConfig` and `PluginConfig`:

```yaml
plugins:
  - name: my-plugin
    enabled: true
    priority: 10
    config:
      endpoint: https://service.example.com
```

`LoadFromConfig` processes enabled plugins by ascending `priority`, creates each one through its factory, registers it, and initializes it. Starting still requires an explicit call to `Start` or `StartAll`.

## Plugin Manifest

The top-level YAML fields of `PluginManifest` are `info`, `config`, `config_schema`, and optional `hooks`. This minimal manifest matches the current structure:

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

Parse a manifest with `ParseManifest(data)` or `LoadManifest(path)`:

```go
manifest, err := plugin.LoadManifest("plugin.yaml")
if err != nil {
    return err
}
fmt.Println(manifest.Info.Name)
```

Manifest parsing only returns structured metadata; it does not register, initialize, or start a plugin.

## Dependency Management

### Runtime Dependencies

For `Lifecycle.Start` and `StartAll`, put registered plugin names such as `database-plugin` in `PluginInfo.Dependencies`. A dependency must be in the `running` state before its dependent starts.

### Dependency Graph

Build a `DependencyGraph` with `AddNode`, then inspect it with `DetectCycle` and `TopologicalSort`:

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

`DependencyResolver.CheckDependencies` validates the existence and version constraints of registered plugins. Constraint specifications use `name@constraint` and support `=`, `>`, `>=`, `<`, `<=`, `~>`, and `^`:

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

Version validation and lifecycle startup are separate operations. The lifecycle resolves dependencies by plugin name, so the actual plugin metadata used by `Start` and `StartAll` should retain plain-name dependencies.

## Health Checks

Each plugin returns `HealthStatus` from the parameterless `Health()` method. `Lifecycle.HealthCheck(ctx)` aggregates all plugins; plugins that are not running report `unknown`:

```go
statuses := lifecycle.HealthCheck(ctx)
for name, status := range statuses {
    if status.Status != plugin.HealthStateHealthy {
        log.Printf("plugin %s is not healthy: %s", name, status.Message)
    }
}
```

For periodic checks, pass a cancelable `context.Context` to `StartHealthChecker(ctx)` and call `StopHealthChecker()` during shutdown.

## Practices

1. Validate configuration in `Init` and return clear, actionable errors.
2. Respect `context.Context` cancellation and deadlines in `Start` and `Stop`.
3. Report `healthy` only after startup succeeds, and set `HealthStatus.LastCheck` to the check time.
4. Validate dependency existence, versions, and cycles before starting plugins.
5. Use an isolated `Registry` in tests so global registrations cannot leak between test cases.
6. Always stop started plugins to release connections, goroutines, and other resources.
