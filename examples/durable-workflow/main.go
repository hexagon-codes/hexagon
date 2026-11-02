// durable-workflow 演示宿主显式装配 SQL、版本化定义和带续租的执行器。
// 设置 HEXAGON_EXAMPLE_MYSQL_DSN 后首次使用 -migrate；相同 scope/run-id 可重复启动。
// 示例仅执行本地纯函数，不调用模型或业务服务，也不自动清理持久记录。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/hexagon-codes/hexagon/orchestration/workflow"
	hr "github.com/hexagon-codes/hexagon/runtime"
	mysqlstore "github.com/hexagon-codes/hexagon/runtime/store/mysql"
)

func main() {
	scope := flag.String("scope", "durable-workflow-example", "Persistent example scope")
	runID := flag.String("run-id", "example-run", "Stable run identity to submit or resume")
	migrate := flag.Bool("migrate", false, "Explicitly create the store tables")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dsn := os.Getenv("HEXAGON_EXAMPLE_MYSQL_DSN")
	if err := run(ctx, dsn, *scope, *runID, *migrate); err != nil {
		message := err.Error()
		if dsn != "" {
			message = strings.ReplaceAll(message, dsn, "[redacted]")
		}
		fmt.Fprintln(os.Stderr, message)
		os.Exit(1)
	}
}

func run(ctx context.Context, dsn, scope, runID string, migrate bool) error {
	if dsn == "" || scope == "" || runID == "" {
		return errors.New("MYSQL DSN, scope and run-id are required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return errors.New("open database failed")
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("database unavailable: %w", err)
	}
	store, err := mysqlstore.New(db)
	if err != nil {
		return err
	}
	// 建表是宿主显式动作；一般启动不隐式迁移或启动其他服务。
	if migrate {
		if err := store.Migrate(ctx); err != nil {
			return err
		}
	}
	wf, err := workflow.New("durable-example").WithID("durable-example").
		AddFunc("prepare", "Prepare", func(_ context.Context, input workflow.StepInput) (*workflow.StepOutput, error) {
			fmt.Println("prepare executed")
			return &workflow.StepOutput{Data: input.Data}, nil
		}).
		AddFunc("finish", "Finish", func(_ context.Context, input workflow.StepInput) (*workflow.StepOutput, error) {
			fmt.Println("finish executed")
			return &workflow.StepOutput{Data: map[string]any{"prepared": input.Data, "result": "completed"}}, nil
		}).Build()
	if err != nil {
		return err
	}
	// 定义引用标识宿主实现；修改步骤语义时必须使用新版本，不能恢复时偷换闭包。
	wf.Version = "1"
	definition := json.RawMessage(`{"implementation":"durable-workflow-example-v1"}`)
	digest, err := hr.ContentDigest(definition)
	if err != nil {
		return err
	}
	wf.DefinitionReference = &hr.ContentReference{Kind: workflow.WorkflowDefinitionKind, ID: wf.ID, Revision: "1", Digest: digest, Content: definition}
	input := json.RawMessage(`{"source":"local-example"}`)
	spec, err := wf.FreezeRunSpec(runID, workflow.WorkflowInput{Data: map[string]any{"source": "local-example"}})
	if err != nil {
		return err
	}
	if _, err := store.Submit(ctx, hr.SubmitRequest{Scope: scope, InputID: "initial-input", RunID: runID, Spec: spec, Input: input}); err != nil {
		return err
	}
	runner := workflow.NewDurableWorkflowRunner(store, store)
	// Process 取得本 Run 的执行权、续租并在返回前等待续租协程退出。
	// 外部副作用应另接原操作核对，不能用可恢复步骤冒称供应商 exactly-once。
	worker := hr.Worker{Store: store, Scope: scope, Owner: "example-worker", Lease: 30 * time.Second,
		Execute: func(execution context.Context, record hr.RunRecord) error {
			_, err := runner.Run(execution, scope, record.RunID, wf, "initial-input")
			return err
		},
	}
	if err := worker.Process(ctx, runID); err != nil {
		return err
	}
	record, found, err := store.GetRun(ctx, scope, runID)
	if err != nil {
		return err
	}
	if !found {
		return hr.ErrRunNotFound
	}
	fmt.Printf("run=%s status=%s revision=%d\n", record.RunID, record.Status, record.Revision)
	if record.Status != hr.RunSucceeded {
		return fmt.Errorf("run is not completed: %s", record.Status)
	}
	return nil
}
