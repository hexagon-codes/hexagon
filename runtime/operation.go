package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type OperationStatus string

const (
	OperationIntent     OperationStatus = "intent"
	OperationDispatched OperationStatus = "dispatched"
	OperationUnknown    OperationStatus = "unknown"
	OperationSucceeded  OperationStatus = "succeeded"
	OperationFailed     OperationStatus = "failed"
)

// Operation 的逻辑身份独立于模型 tool-call ID 和物理 attempt。
type Operation struct {
	ID             string          `json:"id"`
	InputDigest    string          `json:"input_digest"`
	Provider       string          `json:"provider"`
	IdempotencyKey string          `json:"idempotency_key"`
	ProviderTaskID string          `json:"provider_task_id,omitempty"`
	Status         OperationStatus `json:"status"`
	Result         json.RawMessage `json:"result,omitempty"`
}

func (o Operation) Validate() error {
	if o.ID == "" || o.InputDigest == "" || o.Provider == "" || o.IdempotencyKey == "" {
		return fmt.Errorf("runtime: operation requires stable identity, input digest and provider")
	}
	switch o.Status {
	case OperationIntent, OperationDispatched, OperationUnknown, OperationSucceeded, OperationFailed:
		return nil
	}
	return fmt.Errorf("runtime: invalid operation status")
}

// OperationReceipt 独立归档真实外部事实；归档不推进或复活 Run。
// 相同 ReceiptID 必须保留相同摘要，矛盾/迟到事实使用各自的供应商回执身份。
type OperationReceipt struct {
	Scope          string          `json:"scope"`
	RunID          string          `json:"run_id"`
	OperationID    string          `json:"operation_id"`
	ReceiptID      string          `json:"receipt_id"`
	Digest         string          `json:"digest"`
	Provider       string          `json:"provider"`
	ProviderTaskID string          `json:"provider_task_id,omitempty"`
	Status         OperationStatus `json:"status"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	ReceivedAt     time.Time       `json:"received_at"`
}

type ReceiptStore interface {
	ArchiveReceipt(context.Context, OperationReceipt) (OperationReceipt, error)
	ListReceipts(context.Context, string, string) ([]OperationReceipt, error)
}

var ErrOperationUnresolved = errors.New("runtime: original operation outcome is unresolved")

// OperationReconciler 只查询原操作，不负责新派发；not-found 不能自动解释成可重发。
type OperationReconciler interface {
	LookupOperation(context.Context, Operation) (Operation, bool, error)
}

// ResolveOperation 在恢复时优先复用确定结果，否则查询稳定原身份。
// 未知结果不调用执行器，供应商幂等重派策略由宿主另行明确实现。
func ResolveOperation(ctx context.Context, operation Operation, reconciler OperationReconciler) (Operation, error) {
	if err := operation.Validate(); err != nil {
		return operation, err
	}
	if operation.Status == OperationSucceeded || operation.Status == OperationFailed {
		return operation, nil
	}
	if reconciler == nil {
		return operation, ErrOperationUnresolved
	}
	found, ok, err := reconciler.LookupOperation(ctx, operation)
	if err != nil {
		return operation, fmt.Errorf("%w: %w", ErrOperationUnresolved, err)
	}
	if !ok {
		return operation, ErrOperationUnresolved
	}
	if found.ID != operation.ID || found.InputDigest != operation.InputDigest || found.Provider != operation.Provider || found.IdempotencyKey != operation.IdempotencyKey {
		return operation, ErrCommitConflict
	}
	if operation.ProviderTaskID != "" && found.ProviderTaskID != operation.ProviderTaskID {
		return operation, ErrCommitConflict
	}
	if found.Status != OperationSucceeded && found.Status != OperationFailed {
		return found, ErrOperationUnresolved
	}
	return found, nil
}

type ArtifactRegistrationStatus string

const (
	ArtifactBytesStored ArtifactRegistrationStatus = "bytes_stored"
	ArtifactRegistered  ArtifactRegistrationStatus = "registered"
	ArtifactDelivered   ArtifactRegistrationStatus = "delivered"
)

// ArtifactReference 只保存稳定对象引用，不以短期下载 URL 承担恢复。
type ArtifactReference struct {
	ID          string                     `json:"id"`
	TaskID      string                     `json:"task_id,omitempty"`
	OperationID string                     `json:"operation_id,omitempty"`
	ObjectKey   string                     `json:"object_key"`
	Digest      string                     `json:"digest"`
	Version     int                        `json:"version"`
	Status      ArtifactRegistrationStatus `json:"status"`
}

func (a ArtifactReference) Validate() error {
	if a.ID == "" || a.ObjectKey == "" || a.Digest == "" || a.Version < 1 {
		return fmt.Errorf("runtime: artifact requires stable object reference, digest and version")
	}
	switch a.Status {
	case ArtifactBytesStored, ArtifactRegistered, ArtifactDelivered:
		return nil
	}
	return fmt.Errorf("runtime: invalid artifact registration status")
}
