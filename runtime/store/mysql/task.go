package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hexagon-codes/hexagon/runtime"
)

func digest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Store) accepted(ctx context.Context, scope, inputID, wantDigest string) (runtime.RunRecord, bool, error) {
	var runID, previousDigest string
	err := s.db.QueryRowContext(ctx, `SELECT run_id,digest FROM hexagon_inputs WHERE scope=? AND input_id=?`, scope, inputID).Scan(&runID, &previousDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.RunRecord{}, false, nil
	}
	if err != nil {
		return runtime.RunRecord{}, false, err
	}
	if previousDigest != wantDigest {
		return runtime.RunRecord{}, false, runtime.ErrCommitConflict
	}
	record, ok, err := s.GetRun(ctx, scope, runID)
	if err != nil {
		return runtime.RunRecord{}, false, err
	}
	if !ok {
		return runtime.RunRecord{}, false, fmt.Errorf("mysql: accepted input run is missing")
	}
	return record, true, nil
}

// Submit 在接受事务提交后才返回。同一入站身份重试返回原 Run，输入变化明确冲突。
func (s *Store) Submit(ctx context.Context, request runtime.SubmitRequest) (runtime.RunRecord, error) {
	if request.Scope == "" || request.InputID == "" || request.RunID == "" {
		return runtime.RunRecord{}, fmt.Errorf("mysql: submission identity is required")
	}
	if err := request.Spec.Validate(); err != nil {
		return runtime.RunRecord{}, err
	}
	if request.Spec.RunID != request.RunID {
		return runtime.RunRecord{}, runtime.ErrUnrecoverableSpec
	}
	requestDigest, err := digest(request)
	if err != nil {
		return runtime.RunRecord{}, err
	}
	if record, ok, err := s.accepted(ctx, request.Scope, request.InputID, requestDigest); err != nil {
		return runtime.RunRecord{}, err
	} else if ok {
		return record, nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return runtime.RunRecord{}, err
	}
	defer tx.Rollback()
	record := runtime.RunRecord{Scope: request.Scope, RunID: request.RunID, Spec: request.Spec, Status: runtime.RunQueued,
		Inputs: map[string]runtime.InboundInput{request.InputID: {ID: request.InputID, Digest: requestDigest, Payload: request.Input}}}
	if err = writeRun(ctx, tx, record, nil); err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO hexagon_inputs(scope,input_id,run_id,digest) VALUES(?,?,?,?)`, request.Scope, request.InputID, request.RunID, requestDigest)
	}
	unknown := false
	if err == nil {
		err = tx.Commit()
		unknown = err != nil
	}
	if err != nil {
		tx.Rollback()
		if previous, ok, readErr := s.accepted(ctx, request.Scope, request.InputID, requestDigest); readErr != nil {
			if errors.Is(readErr, runtime.ErrCommitConflict) {
				return runtime.RunRecord{}, readErr
			}
			return runtime.RunRecord{}, fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, readErr)
		} else if ok {
			return previous, nil
		}
		if unknown {
			return runtime.RunRecord{}, fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, err)
		}
		return runtime.RunRecord{}, err
	}
	return record, nil
}

func (s *Store) Signal(ctx context.Context, scope, runID string, input runtime.InboundInput) (runtime.InboundInput, error) {
	if input.ID == "" || input.ConsumedRevision != 0 {
		return runtime.InboundInput{}, fmt.Errorf("mysql: invalid inbound input")
	}
	wantDigest, err := digest(struct {
		RunID   string
		Target  string
		Payload json.RawMessage
	}{runID, input.Target, input.Payload})
	if err != nil {
		return runtime.InboundInput{}, err
	}
	if input.Digest != "" && input.Digest != wantDigest {
		return runtime.InboundInput{}, runtime.ErrCommitConflict
	}
	input.Digest = wantDigest
	lookup := func() (runtime.InboundInput, bool, error) {
		record, found, err := s.accepted(ctx, scope, input.ID, wantDigest)
		if err != nil || !found {
			return runtime.InboundInput{}, found, err
		}
		if record.RunID != runID {
			return runtime.InboundInput{}, false, runtime.ErrCommitConflict
		}
		old, exists := record.Inputs[input.ID]
		if !exists {
			return runtime.InboundInput{}, false, fmt.Errorf("mysql: accepted signal is missing")
		}
		return old, true, nil
	}
	if accepted, found, err := lookup(); err != nil {
		return runtime.InboundInput{}, err
	} else if found {
		return accepted, nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return runtime.InboundInput{}, err
	}
	defer tx.Rollback()
	record, exists, err := loadRun(ctx, tx, scope, runID, true)
	if err != nil {
		return runtime.InboundInput{}, err
	}
	if !exists {
		return runtime.InboundInput{}, runtime.ErrRunNotFound
	}
	if old, found := record.Inputs[input.ID]; found {
		if old.Digest != input.Digest {
			return runtime.InboundInput{}, runtime.ErrCommitConflict
		}
		return old, nil
	}
	if record.Status.Terminal() {
		return runtime.InboundInput{}, runtime.ErrRunTerminal
	}
	if record.Inputs == nil {
		record.Inputs = make(map[string]runtime.InboundInput)
	}
	record.Inputs[input.ID] = input
	if record.Status == runtime.RunWaiting {
		record.Status, record.WakeAt = runtime.RunQueued, time.Time{}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO hexagon_inputs(scope,input_id,run_id,digest) VALUES(?,?,?,?)`, scope, input.ID, runID, wantDigest)
	if err == nil {
		err = writeRun(ctx, tx, record, &record.Revision)
	}
	unknown := false
	if err == nil {
		err = tx.Commit()
		unknown = err != nil
	}
	if err != nil {
		tx.Rollback()
		if accepted, found, lookupErr := lookup(); lookupErr != nil {
			if errors.Is(lookupErr, runtime.ErrCommitConflict) {
				return runtime.InboundInput{}, lookupErr
			}
			return runtime.InboundInput{}, fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, lookupErr)
		} else if found {
			return accepted, nil
		}
		if unknown {
			return runtime.InboundInput{}, fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, err)
		}
		return runtime.InboundInput{}, err
	}
	return input, nil
}

// updateRun 在同一行锁内维护调度事实，不递增语义 revision；执行核提交仍核对原 revision。
func (s *Store) updateRun(ctx context.Context, scope, runID string, update func(*runtime.RunRecord, time.Time) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	record, exists, err := loadRun(ctx, tx, scope, runID, true)
	if err != nil {
		return err
	}
	if !exists {
		return runtime.ErrRunNotFound
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(6)`).Scan(&now); err != nil {
		return err
	}
	revision := record.Revision
	before, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := update(&record, now); err != nil {
		return err
	}
	after, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if string(before) != string(after) {
		if err := writeRun(ctx, tx, record, &revision); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, err)
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, scope, runID string, authority runtime.ExecutionAuthority, ttl time.Duration) (runtime.RunRecord, bool, error) {
	if authority.Owner == "" || authority.AttemptID == "" || ttl <= 0 {
		return runtime.RunRecord{}, false, fmt.Errorf("mysql: claim requires owner, attempt and positive lease")
	}
	var claimed runtime.RunRecord
	err := s.updateRun(ctx, scope, runID, func(record *runtime.RunRecord, now time.Time) error {
		if record.Status.Terminal() {
			return runtime.ErrRunTerminal
		}
		if (record.Status == runtime.RunWaiting && record.WakeAt.IsZero()) || record.WakeAt.After(now) || (record.Authority.Owner != "" && record.LeaseUntil.After(now)) {
			return runtime.ErrExecutionLost
		}
		authority.Fence = record.Authority.Fence + 1
		record.Authority, record.LeaseUntil, record.Status, record.WakeAt = authority, now.Add(ttl), runtime.RunRunning, time.Time{}
		claimed = *record
		return nil
	})
	if errors.Is(err, runtime.ErrExecutionLost) || errors.Is(err, runtime.ErrRunTerminal) {
		return runtime.RunRecord{}, false, nil
	}
	return claimed, err == nil, err
}

func (s *Store) Renew(ctx context.Context, scope, runID string, authority runtime.ExecutionAuthority, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("mysql: renewal requires positive lease")
	}
	return s.updateRun(ctx, scope, runID, func(record *runtime.RunRecord, now time.Time) error {
		if record.Status.Terminal() || record.Authority != authority || !record.LeaseUntil.After(now) {
			return runtime.ErrExecutionLost
		}
		record.LeaseUntil = now.Add(ttl)
		return nil
	})
}

func (s *Store) Release(ctx context.Context, scope, runID string, authority runtime.ExecutionAuthority) error {
	return s.updateRun(ctx, scope, runID, func(record *runtime.RunRecord, _ time.Time) error {
		if record.Authority != authority {
			return runtime.ErrExecutionLost
		}
		record.LeaseUntil = time.Time{}
		if record.Status == runtime.RunRunning {
			record.Status = runtime.RunQueued
		}
		return nil
	})
}

func (s *Store) Cancel(ctx context.Context, scope, runID, reason string) error {
	return s.updateRun(ctx, scope, runID, func(record *runtime.RunRecord, now time.Time) error {
		if record.Status.Terminal() {
			return nil
		}
		record.Status, record.CancelReason, record.LeaseUntil = runtime.RunCancelled, reason, time.Time{}
		record.Authority.Fence++
		record.Revision++
		record.NextSequence++
		record.Events = append(record.Events, runtime.StoredEvent{Scope: scope, RunID: runID, ID: fmt.Sprintf("%s/%d", runID, record.NextSequence), Sequence: record.NextSequence, Revision: record.Revision, Version: runtime.EventVersionV1, Type: runtime.EventType("run_cancelled"), Timestamp: now})
		return nil
	})
}

// Runnable 只列出可尝试 claim 的身份；并发排他仍由 Claim 的数据库条件保证。
func (s *Store) Runnable(ctx context.Context, scope string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run_id FROM hexagon_runs WHERE scope=? AND (status IN ('queued','running') OR (status='waiting' AND wake_at IS NOT NULL)) AND (lease_until IS NULL OR lease_until<=UTC_TIMESTAMP(6)) AND (wake_at IS NULL OR wake_at<=UTC_TIMESTAMP(6)) ORDER BY run_id LIMIT ?`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
