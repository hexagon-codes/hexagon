// Package plugin 提供插件依赖管理能力
package plugin

import (
	"fmt"
	"strings"
)

// DependencyResolver 依赖解析器
//
// 解析和验证插件依赖关系，确保：
//   - 依赖存在
//   - 版本兼容
//   - 无循环依赖
//   - 正确的加载顺序
type DependencyResolver struct {
	// registry 插件注册表
	registry *Registry
}

// NewDependencyResolver 创建依赖解析器
func NewDependencyResolver(registry *Registry) *DependencyResolver {
	return &DependencyResolver{
		registry: registry,
	}
}

// VersionConstraint 版本约束
type VersionConstraint struct {
	// Operator 操作符 (=, >, >=, <, <=, ~>, ^)
	Operator string

	// Version 版本号
	Version string
}

// ParseVersionConstraint 解析版本约束
//
// 支持的格式：
//   - "1.0.0" - 精确版本
//   - ">=1.0.0" - 大于等于
//   - "~>1.0" - 兼容版本（1.x）
//   - "^1.0.0" - 次版本兼容（1.x.x）
func ParseVersionConstraint(constraint string) (*VersionConstraint, error) {
	constraint = strings.TrimSpace(constraint)

	if constraint == "" {
		return nil, fmt.Errorf("empty version constraint")
	}

	// 检查操作符
	operators := []string{">=", "<=", "~>", "^", ">", "<", "="}
	for _, op := range operators {
		if strings.HasPrefix(constraint, op) {
			version := strings.TrimSpace(constraint[len(op):])
			return &VersionConstraint{
				Operator: op,
				Version:  version,
			}, nil
		}
	}

	// 无操作符，默认为精确匹配
	return &VersionConstraint{
		Operator: "=",
		Version:  constraint,
	}, nil
}

// parseDependencySpec 解析依赖声明，支持两种形式：
//   - "name"            —— 仅依赖名，不约束版本（任意版本满足）
//   - "name@constraint" —— 依赖名 + 版本约束，如 "logger@>=1.2.0"、"db@^1.0.0"
//
// 返回依赖名与版本约束字符串（无约束时 constraint 为空）。约束语法见 ParseVersionConstraint。
func parseDependencySpec(spec string) (name, constraint string) {
	spec = strings.TrimSpace(spec)
	if n, c, found := strings.Cut(spec, "@"); found {
		return strings.TrimSpace(n), strings.TrimSpace(c)
	}
	return spec, ""
}

// CheckVersion 检查版本是否满足约束
func (c *VersionConstraint) CheckVersion(version string) bool {
	switch c.Operator {
	case "=":
		return version == c.Version

	case ">":
		return compareVersions(version, c.Version) > 0

	case ">=":
		return compareVersions(version, c.Version) >= 0

	case "<":
		return compareVersions(version, c.Version) < 0

	case "<=":
		return compareVersions(version, c.Version) <= 0

	case "~>":
		// 兼容版本：1.0 允许 1.x
		return isCompatibleVersion(version, c.Version)

	case "^":
		// 次版本兼容：^1.2.3 允许 >=1.2.3 且 <2.0.0
		return isSemverCompatible(version, c.Version)

	default:
		return false
	}
}

// compareVersions 比较两个版本号
//
// 返回值：
//   - 0: 相等
//   - 1: v1 > v2
//   - -1: v1 < v2
func compareVersions(v1, v2 string) int {
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		p1 := 0
		p2 := 0

		if i < len(parts1) {
			fmt.Sscanf(parts1[i], "%d", &p1)
		}
		if i < len(parts2) {
			fmt.Sscanf(parts2[i], "%d", &p2)
		}

		if p1 > p2 {
			return 1
		}
		if p1 < p2 {
			return -1
		}
	}

	return 0
}

// isCompatibleVersion 检查兼容版本
//
// ~>1.0 匹配 1.x
// ~>1.2 匹配 1.2.x
func isCompatibleVersion(version, constraint string) bool {
	vParts := strings.Split(version, ".")
	cParts := strings.Split(constraint, ".")

	// 比较到约束指定的层级
	for i := 0; i < len(cParts); i++ {
		if i >= len(vParts) {
			return false
		}
		if vParts[i] != cParts[i] {
			return false
		}
	}

	return true
}

// isSemverCompatible 检查语义版本兼容性
//
// ^1.2.3 匹配 >=1.2.3 且 <2.0.0
func isSemverCompatible(version, constraint string) bool {
	// 版本必须 >= 约束版本
	if compareVersions(version, constraint) < 0 {
		return false
	}

	// 主版本号必须相同
	vParts := strings.Split(version, ".")
	cParts := strings.Split(constraint, ".")

	if len(vParts) == 0 || len(cParts) == 0 {
		return false
	}

	return vParts[0] == cParts[0]
}

// Dependency 依赖项
type Dependency struct {
	// Name 插件名称
	Name string

	// Version 版本约束
	Version string

	// Optional 是否可选
	Optional bool
}

// DependencyGraph 依赖图
type DependencyGraph struct {
	// nodes 节点（插件）
	nodes map[string]*DependencyNode

	// edges 边（依赖关系）
	edges map[string][]string
}

// DependencyNode 依赖节点
type DependencyNode struct {
	// Name 插件名称
	Name string

	// Version 版本
	Version string

	// Dependencies 依赖列表
	Dependencies []Dependency
}

// NewDependencyGraph 创建依赖图
func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		nodes: make(map[string]*DependencyNode),
		edges: make(map[string][]string),
	}
}

// AddNode 添加节点
func (g *DependencyGraph) AddNode(name, version string, deps []Dependency) {
	node := &DependencyNode{
		Name:         name,
		Version:      version,
		Dependencies: deps,
	}

	g.nodes[name] = node

	// 构建边
	edges := make([]string, 0, len(deps))
	for _, dep := range deps {
		edges = append(edges, dep.Name)
	}
	g.edges[name] = edges
}

// Resolve 解析依赖顺序
//
// 返回按依赖顺序排列的插件列表（被依赖的在前）
func (r *DependencyResolver) Resolve(plugins []PluginInfo) ([]string, error) {
	// 构建依赖图
	graph := NewDependencyGraph()

	for _, plugin := range plugins {
		deps := make([]Dependency, len(plugin.Dependencies))
		for i, dep := range plugin.Dependencies {
			depName, _ := parseDependencySpec(dep)
			deps[i] = Dependency{
				Name:     depName,
				Optional: false,
			}
		}

		graph.AddNode(plugin.Name, plugin.Version, deps)
	}

	// 检查循环依赖
	if cycle := graph.DetectCycle(); cycle != nil {
		return nil, fmt.Errorf("circular dependency detected: %s", strings.Join(cycle, " -> "))
	}

	// 拓扑排序
	return graph.TopologicalSort()
}

// CheckDependencies 检查依赖是否满足（存在性 + 版本约束）。
//
// 依赖声明支持 "name" 或 "name@constraint"（见 parseDependencySpec）。指定了约束时，
// 用 ParseVersionConstraint 解析并校验已注册依赖的实际版本（VersionConstraint.CheckVersion）；
// 未指定约束时仅检查依赖存在（任意版本满足，保持向后兼容）。
func (r *DependencyResolver) CheckDependencies(plugin PluginInfo) error {
	for _, depSpec := range plugin.Dependencies {
		depName, constraint := parseDependencySpec(depSpec)

		// 检查依赖是否存在
		dep, err := r.registry.Get(depName)
		if err != nil {
			return fmt.Errorf("dependency %s not found for plugin %s", depName, plugin.Name)
		}

		// 无版本约束 → 任意版本满足
		if constraint == "" {
			continue
		}

		depInfo := dep.Info()
		if depInfo.Version == "" {
			return fmt.Errorf("plugin %s requires dependency %s %s, but %s declares no version",
				plugin.Name, depName, constraint, depName)
		}

		vc, err := ParseVersionConstraint(constraint)
		if err != nil {
			return fmt.Errorf("plugin %s: invalid version constraint %q for dependency %s: %w",
				plugin.Name, constraint, depName, err)
		}
		if !vc.CheckVersion(depInfo.Version) {
			return fmt.Errorf("plugin %s requires dependency %s %s, but found version %s",
				plugin.Name, depName, constraint, depInfo.Version)
		}
	}

	return nil
}

// DetectCycle 检测循环依赖
func (g *DependencyGraph) DetectCycle() []string {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	var path []string

	var dfs func(string) bool
	dfs = func(node string) bool {
		visited[node] = true
		recStack[node] = true
		path = append(path, node)

		for _, neighbor := range g.edges[node] {
			if !visited[neighbor] {
				if dfs(neighbor) {
					return true
				}
			} else if recStack[neighbor] {
				// 找到循环
				cycle := make([]string, 0)
				found := false
				for _, p := range path {
					if p == neighbor {
						found = true
					}
					if found {
						cycle = append(cycle, p)
					}
				}
				cycle = append(cycle, neighbor)
				return true
			}
		}

		recStack[node] = false
		path = path[:len(path)-1]
		return false
	}

	for node := range g.nodes {
		if !visited[node] {
			if dfs(node) {
				return path
			}
		}
	}

	return nil
}

// TopologicalSort 拓扑排序
func (g *DependencyGraph) TopologicalSort() ([]string, error) {
	// 计算入度
	inDegree := make(map[string]int)
	for node := range g.nodes {
		inDegree[node] = 0
	}

	for _, edges := range g.edges {
		for _, to := range edges {
			inDegree[to]++
		}
	}

	// 找出所有入度为 0 的节点
	queue := make([]string, 0)
	for node, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}

	// Kahn 算法
	var sorted []string
	for len(queue) > 0 {
		// 取出一个节点
		node := queue[0]
		queue = queue[1:]
		sorted = append(sorted, node)

		// 减少邻居的入度
		for _, neighbor := range g.edges[node] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	// 检查是否所有节点都已访问
	if len(sorted) != len(g.nodes) {
		return nil, fmt.Errorf("circular dependency detected")
	}

	return sorted, nil
}

// ValidateDependencies 验证所有插件的依赖
func (r *DependencyResolver) ValidateDependencies(plugins []PluginInfo) error {
	pluginMap := make(map[string]PluginInfo)
	for _, p := range plugins {
		pluginMap[p.Name] = p
	}

	for _, plugin := range plugins {
		for _, depSpec := range plugin.Dependencies {
			depName, _ := parseDependencySpec(depSpec)
			// 检查依赖是否存在
			if _, exists := pluginMap[depName]; !exists {
				return fmt.Errorf("plugin %s depends on %s which is not available", plugin.Name, depName)
			}
		}
	}

	return nil
}

// GetLoadOrder 获取插件加载顺序
func (r *DependencyResolver) GetLoadOrder(plugins []PluginInfo) ([]PluginInfo, error) {
	// 验证依赖
	if err := r.ValidateDependencies(plugins); err != nil {
		return nil, err
	}

	// 解析加载顺序
	order, err := r.Resolve(plugins)
	if err != nil {
		return nil, err
	}

	// 按顺序构建结果
	pluginMap := make(map[string]PluginInfo)
	for _, p := range plugins {
		pluginMap[p.Name] = p
	}

	ordered := make([]PluginInfo, 0, len(order))
	for _, name := range order {
		if plugin, ok := pluginMap[name]; ok {
			ordered = append(ordered, plugin)
		}
	}

	return ordered, nil
}

// DependencyTree 依赖树
type DependencyTree struct {
	// Plugin 插件信息
	Plugin PluginInfo

	// Dependencies 依赖子树
	Dependencies []*DependencyTree
}

// BuildDependencyTree 构建依赖树
func (r *DependencyResolver) BuildDependencyTree(pluginName string) (*DependencyTree, error) {
	plugin, err := r.registry.Get(pluginName)
	if err != nil {
		return nil, err
	}

	pluginInfo := plugin.Info()

	tree := &DependencyTree{
		Plugin:       pluginInfo,
		Dependencies: make([]*DependencyTree, 0),
	}

	// 递归构建子树
	for _, depSpec := range pluginInfo.Dependencies {
		depName, _ := parseDependencySpec(depSpec)
		depTree, err := r.BuildDependencyTree(depName)
		if err != nil {
			continue // 忽略错误，继续构建其他依赖
		}
		tree.Dependencies = append(tree.Dependencies, depTree)
	}

	return tree, nil
}

// String 格式化依赖树
func (t *DependencyTree) String() string {
	return t.format(0)
}

// format 递归格式化依赖树
func (t *DependencyTree) format(level int) string {
	indent := strings.Repeat("  ", level)
	result := fmt.Sprintf("%s- %s (v%s)\n", indent, t.Plugin.Name, t.Plugin.Version)

	for _, dep := range t.Dependencies {
		result += dep.format(level + 1)
	}

	return result
}
