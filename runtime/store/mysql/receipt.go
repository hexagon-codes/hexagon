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

// ArchiveReceipt 不读取或修改 Run 执行权，取消/失租后的真实回执仍可保存。
func (s *Store) ArchiveReceipt(ctx context.Context, receipt runtime.OperationReceipt) (runtime.OperationReceipt, error) {
	if receipt.Scope == "" || receipt.RunID == "" || receipt.OperationID == "" || receipt.ReceiptID == "" || receipt.Provider == "" || receipt.Status == "" {
		return runtime.OperationReceipt{}, fmt.Errorf("mysql: receipt identity and status are required")
	}
	identity := receipt
	identity.Digest, identity.ReceivedAt = "", time.Time{}
	wantDigest, err := digest(identity)
	if err != nil {
		return runtime.OperationReceipt{}, err
	}
	if receipt.Digest != "" && receipt.Digest != wantDigest {
		return runtime.OperationReceipt{}, runtime.ErrCommitConflict
	}
	receipt.Digest = wantDigest
	if old, ok, err := s.getReceipt(ctx, receipt.Scope, receipt.ReceiptID); err != nil {
		return runtime.OperationReceipt{}, err
	} else if ok {
		if old.Digest != wantDigest {
			return runtime.OperationReceipt{}, runtime.ErrCommitConflict
		}
		return old, nil
	}
	if receipt.ReceivedAt.IsZero() {
		receipt.ReceivedAt = time.Now().UTC()
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return runtime.OperationReceipt{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO hexagon_receipts(scope,receipt_id,operation_id,receipt) VALUES(?,?,?,?)`, receipt.Scope, receipt.ReceiptID, receipt.OperationID, data)
	if err != nil {
		if old, ok, lookupErr := s.getReceipt(ctx, receipt.Scope, receipt.ReceiptID); lookupErr != nil {
			return runtime.OperationReceipt{}, fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, lookupErr)
		} else if ok {
			if old.Digest != wantDigest {
				return runtime.OperationReceipt{}, runtime.ErrCommitConflict
			}
			return old, nil
		}
		return runtime.OperationReceipt{}, fmt.Errorf("%w: %w", runtime.ErrCommitUnknown, err)
	}
	return receipt, nil
}

func (s *Store) getReceipt(ctx context.Context, scope, id string) (runtime.OperationReceipt, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT receipt FROM hexagon_receipts WHERE scope=? AND receipt_id=?`, scope, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.OperationReceipt{}, false, nil
	}
	if err != nil {
		return runtime.OperationReceipt{}, false, err
	}
	var receipt runtime.OperationReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return runtime.OperationReceipt{}, false, err
	}
	return receipt, true, nil
}

func (s *Store) ListReceipts(ctx context.Context, scope, operationID string) ([]runtime.OperationReceipt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT receipt FROM hexagon_receipts WHERE scope=? AND operation_id=? ORDER BY receipt_id`, scope, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var receipts []runtime.OperationReceipt
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var receipt runtime.OperationReceipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}
