package hexagon

import (
	"runtime/debug"
	"testing"
)

// 方案 A（2026-07-13）：编译期 ldflags 注入让 git tag 成为版本号唯一真相源。
//
// 背景：本机 go.work / 装机构建里 hexagon 作为依赖被上报 "(devel)"，
// resolveVersionFromBuildInfo 只能回退写死的 devFallbackVersion——该常量需人工跟 tag
// 同步，实测已漂移（tag 已 v0.5.9，常量停在旧值）。方案 A 在 sidecar 编译时用
// `git describe` 注入 hexagon.injectedVersion，resolveVersion 优先采用它，
// 使"重编即显真实 git 版本"，无需再手改常量、漂移物理上不可能。
//
// 契约：注入值（去空白 + 去 v 前缀，非空）> build info > devFallbackVersion 兜底。
func TestResolveVersionWithInjection_20260713(t *testing.T) {
	develDep := &debug.BuildInfo{
		Main: debug.Module{Path: "github.com/hexagon-codes/hexclaw", Version: "(devel)"},
		Deps: []*debug.Module{{Path: hexagonModulePath, Version: "(devel)"}},
	}
	realDep := &debug.BuildInfo{
		Deps: []*debug.Module{{Path: hexagonModulePath, Version: "v0.5.3"}},
	}

	tests := []struct {
		name     string
		injected string
		info     *debug.BuildInfo
		ok       bool
		want     string
	}{
		{
			name:     "注入 v0.5.9 覆盖 (devel) 依赖 → 0.5.9（方案 A 核心路径：go.work 构建）",
			injected: "v0.5.9",
			info:     develDep,
			ok:       true,
			want:     "0.5.9",
		},
		{
			name:     "注入不带 v 前缀 0.5.9 → 0.5.9",
			injected: "0.5.9",
			info:     develDep,
			ok:       true,
			want:     "0.5.9",
		},
		{
			name:     "注入带前后空白 + dirty 描述 → 去空白后透传（HEAD 超前 tag 时更诚实）",
			injected: "  v0.5.9-3-gabc123-dirty  ",
			info:     develDep,
			ok:       true,
			want:     "0.5.9-3-gabc123-dirty",
		},
		{
			name:     "注入为空 → 落回 build info 真实依赖版本（正式发布构建不受影响）",
			injected: "",
			info:     realDep,
			ok:       true,
			want:     "0.5.3",
		},
		{
			name:     "注入为空 + (devel) 依赖 → 回退 devFallbackVersion 兜底",
			injected: "",
			info:     develDep,
			ok:       true,
			want:     devFallbackVersion,
		},
		{
			name:     "注入仅空白 → 视为未注入，落回 build info",
			injected: "   ",
			info:     realDep,
			ok:       true,
			want:     "0.5.3",
		},
		{
			name:     "注入为空 + 无 build info → 回退 devFallbackVersion",
			injected: "",
			info:     nil,
			ok:       false,
			want:     devFallbackVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveVersionWithInjection(tt.injected, tt.info, tt.ok)
			if got != tt.want {
				t.Fatalf("resolveVersionWithInjection(%q, ...) = %q, 期望 %q", tt.injected, got, tt.want)
			}
		})
	}
}
