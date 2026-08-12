package hexagon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRetryMultiplierRequiresExplicitExponentialBackoff(t *testing.T) {
	t.Parallel()

	for _, relativePath := range []string{
		"agent/middleware.go",
		"core/fallback.go",
		"mcp/reconnect.go",
		"tool/middleware.go",
	} {
		relativePath := relativePath
		t.Run(relativePath, func(t *testing.T) {
			t.Parallel()
			assertRetryMultiplierHasExplicitExponentialBackoff(t, relativePath)
		})
	}
}

// assertRetryMultiplierHasExplicitExponentialBackoff 校验倍数参数不会隐式改变固定延迟策略。
func assertRetryMultiplierHasExplicitExponentialBackoff(t *testing.T, relativePath string) {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate retry policy contract test")
	}
	path := filepath.Join(filepath.Dir(currentFile), filepath.FromSlash(relativePath))
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", relativePath, err)
	}

	multiplierCalls := 0
	ast.Inspect(file, func(node ast.Node) bool {
		arguments, ok := retryOptionGroup(node)
		if !ok {
			return true
		}

		multipliers := countRetryOption(arguments, "Multiplier", "")
		if multipliers == 0 {
			return true
		}
		multiplierCalls += multipliers
		exponentials := countRetryOption(arguments, "DelayType", "ExponentialBackoff")
		if exponentials != multipliers {
			t.Errorf("%s: each retry.Multiplier option must be paired with retry.DelayType(retry.ExponentialBackoff) in the same call", relativePath)
		}
		return true
	})

	if multiplierCalls == 0 {
		t.Errorf("%s: no retry.Multiplier option found", relativePath)
	}
}

func retryOptionGroup(node ast.Node) ([]ast.Expr, bool) {
	switch node := node.(type) {
	case *ast.CallExpr:
		return node.Args, true
	case *ast.CompositeLit:
		expressions := make([]ast.Expr, 0, len(node.Elts))
		expressions = append(expressions, node.Elts...)
		return expressions, true
	default:
		return nil, false
	}
}

func countRetryOption(arguments []ast.Expr, optionName, argumentName string) int {
	count := 0
	for _, argument := range arguments {
		call, ok := argument.(*ast.CallExpr)
		if !ok || !isRetrySelector(call.Fun, optionName) {
			continue
		}
		if argumentName != "" && (len(call.Args) != 1 || !isRetrySelector(call.Args[0], argumentName)) {
			continue
		}
		count++
	}
	return count
}

func isRetrySelector(expression ast.Expr, name string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "retry"
}
