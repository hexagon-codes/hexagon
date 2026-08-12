package cost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const removedSessionTokenOption = "WithMaxTokensPer" + "Session"

func TestSecurityGuidesUseCurrentControllerAPI(t *testing.T) {
	for _, name := range []string{"security.md", "security.en.md"} {
		t.Run(name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("..", "..", "docs", "guides", name))
			if err != nil {
				t.Fatalf("read security guide: %v", err)
			}
			guide := string(content)
			for _, want := range []string{
				"controller, err := cost.NewController(",
				"cost.WithBudget(100.0)",
				"cost.WithMaxTokensTotal(1_000_000)",
				"cost.WithRequestsPerMinute(100)",
			} {
				if !strings.Contains(guide, want) {
					t.Errorf("security guide does not contain current controller API %q", want)
				}
			}
			for _, stale := range []string{"cost.WithDailyBudget", "cost.WithTokenLimit", "cost.WithRateLimit"} {
				if strings.Contains(guide, stale) {
					t.Errorf("security guide still contains removed API %q", stale)
				}
			}
		})
	}
}

func TestBreakingAPIExamplesAreMigratedTogether(t *testing.T) {
	type contractCase struct {
		path    string
		want    []string
		removed []string
	}
	tests := make([]contractCase, 0, 10)
	tests = append(tests,
		contractCase{
			path: filepath.Join("docs", "guides", "observability.md"),
			want: []string{
				"tracing, err := otel.SetupTracing(",
				"if err := tracing.SetExporter(ctx, exporter); err != nil",
				"exporter, err := prometheus.NewExporter(",
			},
			removed: []string{"otel.WithTracerEndpoint", "exporter := prometheus.NewExporter("},
		},
		contractCase{
			path: filepath.Join("docs", "guides", "observability.en.md"),
			want: []string{
				"tracing, err := otel.SetupTracing(",
				"if err := tracing.SetExporter(ctx, exporter); err != nil",
				"exporter, err := prometheus.NewExporter(",
			},
			removed: []string{"otel.WithTracerEndpoint", "exporter := prometheus.NewExporter("},
		},
		contractCase{
			path:    filepath.Join("observe", "prometheus", "wrapper.go"),
			want:    []string{"exporter, err := prometheus.NewExporter("},
			removed: []string{"exporter := prometheus.NewExporter("},
		},
		contractCase{
			path:    "deprecated.go",
			want:    []string{"controller, err := hexagon.NewCostController("},
			removed: []string{"controller := hexagon.NewCostController(", removedSessionTokenOption + " ="},
		},
	)
	for _, name := range []string{"API.md", "API.en.md"} {
		tests = append(tests, contractCase{
			path:    filepath.Join("docs", name),
			want:    []string{"WithMaxTokensTotal"},
			removed: []string{removedSessionTokenOption},
		})
	}
	for _, name := range []string{"QUICKSTART.md", "QUICKSTART.en.md", "DESIGN.md", "DESIGN.en.md"} {
		tests = append(tests, contractCase{
			path:    filepath.Join("docs", name),
			want:    []string{"controller, err := cost.NewController("},
			removed: []string{"controller := hexagon.NewCostController(", "controller, err := hexagon.NewCostController("},
		})
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("..", "..", test.path))
			if err != nil {
				t.Fatalf("read documentation contract source: %v", err)
			}
			text := string(content)
			for _, want := range test.want {
				if !strings.Contains(text, want) {
					t.Errorf("documentation contract source does not contain %q", want)
				}
			}
			for _, removed := range test.removed {
				if strings.Contains(text, removed) {
					t.Errorf("documentation contract source still contains removed API %q", removed)
				}
			}
		})
	}
}
