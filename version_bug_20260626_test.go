package hexagon

import (
	"runtime/debug"
	"testing"
)

// BUG-20260626：hexclaw-desktop 侧栏左下角「Hexagon engine」版本号不再显示。
//
// 根因：resolveVersionFromBuildInfo 的「依赖分支」无条件返回 dep.Version——
// go.work / 装机开发构建里 hexagon 作为依赖被上报为 "(devel)"，于是返回 "(devel)"，
// 前端 Sidebar.pickEngineVersion 又会过滤掉 "(devel)" → 版本号消失。
// 「主模块分支」(info.Main.Version) 本就有 "(devel)" 守卫，依赖分支缺失同样守卫 = 不对称即 bug。
// 2026-06-23 hexagon 框架首次下沉为引擎依赖后该路径才被走到，故"不显示了"。
func TestResolveVersionFromBuildInfo_BUG_20260626(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{
			name: "BUG-20260626: hexagon 依赖上报 (devel) 应回退 fallback 而非泄露 (devel)",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "github.com/hexagon-codes/hexclaw", Version: "(devel)"},
				Deps: []*debug.Module{{Path: hexagonModulePath, Version: "(devel)"}},
			},
			ok:   true,
			want: devFallbackVersion,
		},
		{
			name: "hexagon 依赖空版本 → 回退 fallback（不能显示空串）",
			info: &debug.BuildInfo{
				Deps: []*debug.Module{{Path: hexagonModulePath, Version: ""}},
			},
			ok:   true,
			want: devFallbackVersion,
		},
		{
			name: "hexagon 依赖真实版本 v0.5.3 → 0.5.3（正常发布构建不受影响）",
			info: &debug.BuildInfo{
				Deps: []*debug.Module{{Path: hexagonModulePath, Version: "v0.5.3"}},
			},
			ok:   true,
			want: "0.5.3",
		},
		{
			name: "hexagon 为主模块且真实版本 v1.2.3 → 1.2.3",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: hexagonModulePath, Version: "v1.2.3"},
			},
			ok:   true,
			want: "1.2.3",
		},
		{
			name: "hexagon 为主模块且 (devel) → 回退 fallback",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: hexagonModulePath, Version: "(devel)"},
			},
			ok:   true,
			want: devFallbackVersion,
		},
		{
			name: "无 build info → 回退 fallback",
			info: nil,
			ok:   false,
			want: devFallbackVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveVersionFromBuildInfo(tt.info, tt.ok)
			if got != tt.want {
				t.Fatalf("resolveVersionFromBuildInfo() = %q, 期望 %q", got, tt.want)
			}
		})
	}
}
