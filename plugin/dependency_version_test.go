package plugin

import "testing"

func registerVersioned(t *testing.T, r *Registry, name, version string) {
	t.Helper()
	if err := r.Register(NewBasePlugin(PluginInfo{Name: name, Version: version, Type: PluginTypeTool})); err != nil {
		t.Fatalf("register %s@%s: %v", name, version, err)
	}
}

// TestCheckDependencies_VersionConstraints 验证版本约束闭环：依赖声明 "name@constraint"
// 经 ParseVersionConstraint+CheckVersion 对已注册依赖的实际版本做校验。
func TestCheckDependencies_VersionConstraints(t *testing.T) {
	tests := []struct {
		name       string
		depVersion string // 已注册依赖的实际版本
		depSpec    string // 插件声明的依赖（可含约束）
		wantErr    bool
	}{
		{"满足 >=", "1.2.0", "logger@>=1.0.0", false},
		{"满足 >= 等于边界", "1.0.0", "logger@>=1.0.0", false},
		{"不满足 >=", "0.9.0", "logger@>=1.0.0", true},
		{"满足 <", "1.9.0", "logger@<2.0.0", false},
		{"不满足 <", "2.0.0", "logger@<2.0.0", true},
		{"精确匹配", "1.0.0", "logger@1.0.0", false},
		{"精确不匹配", "1.0.1", "logger@1.0.0", true},
		{"纯名字无约束（向后兼容）", "0.0.1", "logger", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			registerVersioned(t, r, "logger", tt.depVersion)
			resolver := NewDependencyResolver(r)
			err := resolver.CheckDependencies(PluginInfo{
				Name:         "app",
				Version:      "1.0.0",
				Dependencies: []string{tt.depSpec},
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckDependencies(dep=%s, 实际版本=%s) err=%v, wantErr=%v", tt.depSpec, tt.depVersion, err, tt.wantErr)
			}
		})
	}
}

// TestCheckDependencies_MissingDependency 缺失依赖仍报"not found"（含约束亦然，解析出名字查找）。
func TestCheckDependencies_MissingDependency(t *testing.T) {
	resolver := NewDependencyResolver(NewRegistry())
	err := resolver.CheckDependencies(PluginInfo{Name: "app", Dependencies: []string{"missing@>=1.0.0"}})
	if err == nil {
		t.Fatal("缺失依赖应报错")
	}
}

// TestCheckDependencies_DepHasNoVersion 约束已指定但依赖无版本号 → 无法满足，报错。
func TestCheckDependencies_DepHasNoVersion(t *testing.T) {
	r := NewRegistry()
	registerVersioned(t, r, "logger", "") // 无版本
	resolver := NewDependencyResolver(r)
	err := resolver.CheckDependencies(PluginInfo{Name: "app", Dependencies: []string{"logger@>=1.0.0"}})
	if err == nil {
		t.Fatal("约束指定但依赖无版本应报错")
	}
}

func TestParseDependencySpec(t *testing.T) {
	cases := []struct{ spec, name, constraint string }{
		{"logger", "logger", ""},
		{"logger@>=1.2.0", "logger", ">=1.2.0"},
		{"db@^1.0.0", "db", "^1.0.0"},
		{"  cache @ ~>1.0 ", "cache", "~>1.0"},
	}
	for _, c := range cases {
		n, con := parseDependencySpec(c.spec)
		if n != c.name || con != c.constraint {
			t.Errorf("parseDependencySpec(%q) = (%q,%q), want (%q,%q)", c.spec, n, con, c.name, c.constraint)
		}
	}
}
