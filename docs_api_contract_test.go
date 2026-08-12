package hexagon_test

import (
	"context"

	hexagon "github.com/hexagon-codes/hexagon"
	"github.com/hexagon-codes/hexagon/hooks"
	"github.com/hexagon-codes/hexagon/observe/otel"
	"github.com/hexagon-codes/hexagon/observe/prometheus"
)

func compileCurrentDocumentationAPIs(ctx context.Context, manager *hooks.Manager) error {
	controller, err := hexagon.NewCostController(
		hexagon.WithBudget(10),
		hexagon.WithMaxTokensTotal(100_000),
		hexagon.WithRequestsPerMinute(60),
	)
	if err != nil {
		return err
	}
	_ = controller

	tracing, err := otel.SetupTracing(manager, otel.WithTracerServiceName("docs-contract"))
	if err != nil {
		return err
	}
	exporter, err := otel.NewOTLPExporter("https://otel.example.com/v1/traces")
	if err != nil {
		return err
	}
	if err := tracing.SetExporter(ctx, exporter); err != nil {
		return err
	}

	metricsExporter, err := prometheus.NewExporter(prometheus.WithNamespace("hexagon"))
	if err != nil {
		return err
	}
	_ = metricsExporter.Handler()
	return nil
}

var _ = compileCurrentDocumentationAPIs
