// Package mysql 提供由宿主注入连接的 MySQL 8 事务参考路径。
// 构造函数不连接数据库、不建表、不启动 worker，也不关闭宿主的连接。
package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hexagon-codes/hexagon/runtime"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("mysql: database is required")
	}
	return &Store{db: db}, nil
}

// Schema 返回显式迁移语句，宿主决定迁移时机与权限；不在构造时执行。
func Schema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS hexagon_runs (scope VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, run_id VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, revision BIGINT UNSIGNED NOT NULL, status VARCHAR(32) NOT NULL, lease_until DATETIME(6) NULL, wake_at DATETIME(6) NULL, record LONGBLOB NOT NULL, PRIMARY KEY (scope, run_id), INDEX hexagon_runs_runnable (scope, status, lease_until, wake_at)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS hexagon_commits (scope VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, commit_id VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, run_id VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, receipt LONGBLOB NOT NULL, PRIMARY KEY (scope, commit_id), INDEX hexagon_commits_run (scope, run_id)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS hexagon_inputs (scope VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, input_id VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, run_id VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, digest CHAR(64) NOT NULL, PRIMARY KEY (scope, input_id)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS hexagon_receipts (scope VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, receipt_id VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, operation_id VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, receipt LONGBLOB NOT NULL, PRIMARY KEY (scope, receipt_id), INDEX hexagon_receipts_operation (scope, operation_id)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS hexagon_artifacts (scope VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, artifact_id VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, session_id VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, name VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, version BIGINT NOT NULL, record LONGBLOB NOT NULL, PRIMARY KEY (scope, artifact_id), INDEX hexagon_artifacts_versions (scope, session_id, name, version)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS hexagon_artifact_heads (scope VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, session_id VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, name VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, version BIGINT NOT NULL, PRIMARY KEY (scope, session_id, name)) ENGINE=InnoDB`,
	}
}

// Migrate 仅在宿主显式调用时建表；已有连接与迁移生命周期仍归宿主管理。
func (s *Store) Migrate(ctx context.Context) error {
	for _, statement := range Schema() {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("mysql: migrate: %w", err)
		}
	}
	return nil
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func lookupCommit(ctx context.Context, query queryer, scope, id string) (runtime.CommitReceipt, bool, error) {
	var data []byte
	err := query.QueryRowContext(ctx, `SELECT receipt FROM hexagon_commits WHERE scope=? AND commit_id=?`, scope, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.CommitReceipt{}, false, nil
	}
	if err != nil {
		return runtime.CommitReceipt{}, false, err
	}
	var receipt runtime.CommitReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return runtime.CommitReceipt{}, false, err
	}
	return receipt, true, nil
}

func (s *Store) LookupCommit(ctx context.Context, scope, id string) (runtime.CommitReceipt, bool, error) {
	return lookupCommit(ctx, s.db, scope, id)
}

func checkReceipt(receipt runtime.CommitReceipt, request runtime.CommitRequest) (runtime.CommitReceipt, error) {
	if receipt.RunID != request.RunID || receipt.Digest != request.Digest {
		return runtime.CommitReceipt{}, runtime.ErrCommitConflict
	}
	return receipt, nil
}

// Commit 首次读取和冲突后的查回均使用事务外的新视图。
// 事务提交返回错误时，即使即时查回未命中也保持未知，避免盲目重做外部动作。
func (s *Store) Commit(ctx context.Context, request runtime.CommitRequest) (runtime.CommitReceipt, error) {
	if err := runtime.ValidateCommit(request); err != nil {
		return runtime.CommitReceipt{}, err
	}
	if receipt, ok, err := s.LookupCommit(ctx, request.Scope, request.CommitID); err != nil {
		return runtime.CommitReceipt{}, fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, err)
	} else if ok {
		return checkReceipt(receipt, request)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return s.recoverReceipt(ctx, nil, request, err, true)
	}
	defer tx.Rollback()
	if receipt, ok, err := lookupCommit(ctx, tx, request.Scope, request.CommitID); err != nil {
		return runtime.CommitReceipt{}, fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, err)
	} else if ok {
		return checkReceipt(receipt, request)
	}
	current, ok, err := loadRun(ctx, tx, request.Scope, request.RunID, true)
	if err != nil {
		return s.recoverReceipt(ctx, tx, request, err, true)
	}
	if !ok {
		return s.recoverReceipt(ctx, tx, request, runtime.ErrRunNotFound, false)
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(6)`).Scan(&now); err != nil {
		return s.recoverReceipt(ctx, tx, request, err, true)
	}
	next, receipt, err := runtime.ApplyCommit(current, request, now)
	if err != nil {
		tx.Rollback()
		if existing, found, lookupErr := s.LookupCommit(ctx, request.Scope, request.CommitID); lookupErr != nil {
			return runtime.CommitReceipt{}, fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, lookupErr)
		} else if found {
			return checkReceipt(existing, request)
		}
		return runtime.CommitReceipt{}, err
	}
	if err := writeRun(ctx, tx, next, &current.Revision); err != nil {
		return s.recoverReceipt(ctx, tx, request, err, false)
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return runtime.CommitReceipt{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO hexagon_commits(scope,commit_id,run_id,receipt) VALUES(?,?,?,?)`, request.Scope, request.CommitID, request.RunID, data); err != nil {
		return s.recoverReceipt(ctx, tx, request, err, false)
	}
	if err := tx.Commit(); err != nil {
		return s.recoverReceipt(ctx, tx, request, err, true)
	}
	return receipt, nil
}

func (s *Store) recoverReceipt(ctx context.Context, tx *sql.Tx, request runtime.CommitRequest, cause error, unknown bool) (runtime.CommitReceipt, error) {
	if tx != nil {
		tx.Rollback()
	}
	receipt, exists, err := s.LookupCommit(ctx, request.Scope, request.CommitID)
	if err != nil {
		return runtime.CommitReceipt{}, fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, err)
	}
	if exists {
		return checkReceipt(receipt, request)
	}
	if unknown {
		return runtime.CommitReceipt{}, fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, cause)
	}
	return runtime.CommitReceipt{}, cause
}

func loadRun(ctx context.Context, query queryer, scope, runID string, lock bool) (runtime.RunRecord, bool, error) {
	statement := `SELECT record FROM hexagon_runs WHERE scope=? AND run_id=?`
	if lock {
		statement += ` FOR UPDATE`
	}
	var data []byte
	err := query.QueryRowContext(ctx, statement, scope, runID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.RunRecord{}, false, nil
	}
	if err != nil {
		return runtime.RunRecord{}, false, err
	}
	var record runtime.RunRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return runtime.RunRecord{}, false, err
	}
	if record.Scope != scope || record.RunID != runID {
		return runtime.RunRecord{}, false, fmt.Errorf("mysql: run identity mismatch")
	}
	return record, true, nil
}

func (s *Store) GetRun(ctx context.Context, scope, runID string) (runtime.RunRecord, bool, error) {
	return loadRun(ctx, s.db, scope, runID, false)
}

func sqlTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func writeRun(ctx context.Context, tx *sql.Tx, record runtime.RunRecord, revision *uint64) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if revision == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO hexagon_runs(scope,run_id,revision,status,lease_until,wake_at,record) VALUES(?,?,?,?,?,?,?)`, record.Scope, record.RunID, record.Revision, record.Status, sqlTime(record.LeaseUntil), sqlTime(record.WakeAt), data)
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE hexagon_runs SET revision=?,status=?,lease_until=?,wake_at=?,record=? WHERE scope=? AND run_id=? AND revision=?`, record.Revision, record.Status, sqlTime(record.LeaseUntil), sqlTime(record.WakeAt), data, record.Scope, record.RunID, *revision)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return runtime.ErrRevisionConflict
	}
	return nil
}

func (s *Store) ReadEvents(ctx context.Context, scope, runID string, after int64, limit int) ([]runtime.StoredEvent, error) {
	record, exists, err := s.GetRun(ctx, scope, runID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, runtime.ErrRunNotFound
	}
	if after < 0 || after > record.NextSequence {
		return nil, runtime.ErrEventCursor
	}
	if len(record.Events) > 0 && after < record.Events[0].Sequence-1 {
		return nil, runtime.ErrEventCursor
	}
	events := make([]runtime.StoredEvent, 0)
	for _, event := range record.Events {
		if event.Sequence <= after {
			continue
		}
		events = append(events, event)
		if limit > 0 && len(events) >= limit {
			break
		}
	}
	return events, nil
}

var _ runtime.Committer = (*Store)(nil)
var _ runtime.TaskStore = (*Store)(nil)
var _ runtime.ReceiptStore = (*Store)(nil)
var _ runtime.EventStore = (*Store)(nil)
