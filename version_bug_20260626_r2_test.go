package hexagon

import (
	"runtime/debug"
	"testing"
)

// BUG-20260626 R2：左下角「Hexagon engine」显示成 **hexclaw 的版本**（0.4.6）而非 hexagon 的 0.5.4。
//
// 根因（R1 修不全）：resolveVersionFromBuildInfo 命中 hexagon 依赖但版本无效（go.work/dirty 上报 "(devel)"）
// 时只 break，随后**无条件**落到 info.Main.Version —— 而装机 sidecar 的主模块是 hexclaw，VCS 戳记为
// "v0.4.6+dirty"（非 "(devel)"，能通过守卫）→ 返回 "0.4.6+dirty" → 把 Hexagon engine 谎报成 hexclaw 版本。
// R1 的测试用例里 Main.Version 恰好是 "(devel)" 所以漏掉了这条真实路径。
//
// 不变量：只要 hexagon 是**依赖**（hexclaw sidecar 永远如此），就绝不能用主模块版本——
// 依赖版本无效时一律回退 devFallbackVersion（= 当前 hexagon 框架版本）。
func TestResolveVersionFromBuildInfo_BUG_20260626_R2(t *testing.T) {
	// 真实复现：主模块 = hexclaw 的真实 VCS 版本，hexagon 依赖 = (devel)
	info := &debug.BuildInfo{
		Main: debug.Module{Path: "github.com/hexagon-codes/hexclaw", Version: "v0.4.6+dirty"},
		Deps: []*debug.Module{{Path: hexagonModulePath, Version: "(devel)"}},
	}
	got := resolveVersionFromBuildInfo(info, true)
	if got != devFallbackVersion {
		t.Fatalf("BUG-20260626 R2: 命中 hexagon 依赖但版本无效时返回了主模块(hexclaw)版本 %q，"+
			"应回退 hexagon fallback %q（绝不显示 hexclaw 版本）", got, devFallbackVersion)
	}

	// 反例：主模块是 hexclaw 真实版本、但 hexagon 依赖是真实版本 → 用依赖版本（发布构建不受影响）
	info2 := &debug.BuildInfo{
		Main: debug.Module{Path: "github.com/hexagon-codes/hexclaw", Version: "v0.4.6"},
		Deps: []*debug.Module{{Path: hexagonModulePath, Version: "v0.5.4"}},
	}
	if got := resolveVersionFromBuildInfo(info2, true); got != "0.5.4" {
		t.Fatalf("hexagon 依赖真实版本应优先：得到 %q 期望 0.5.4", got)
	}
}
