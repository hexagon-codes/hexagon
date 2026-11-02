package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

type taskReceiptPayload struct {
	Result TaskResult `json:"result"`
}

// archiveTaskResult 独立归档已经发生的结果，不借新执行权复活取消/失租的 Run。
// 私有结果仍留在作用域内；公开事件只发布投影，不展开协议续传或宿主句柄。
func (s *runSession) archiveTaskResult(ctx context.Context, result TaskResult, unsafe bool) error {
	archive, ok := s.store.(ReceiptStore)
	if !ok {
		return nil
	}
	data, err := json.Marshal(taskReceiptPayload{Result: result})
	if err != nil {
		return err
	}
	s.mu.Lock()
	scope, runID := s.record.Scope, s.record.RunID
	s.mu.Unlock()
	status := OperationSucceeded
	if result.Status == TaskWaiting || unsafe {
		status = OperationUnknown
	} else if result.Status != TaskSucceeded {
		status = OperationFailed
	}
	_, err = archive.ArchiveReceipt(ctx, OperationReceipt{Scope: scope, RunID: runID, OperationID: result.TaskID, ReceiptID: result.TaskID + "/" + result.AttemptID + "/result", Provider: "runtime.task", Status: status, Payload: data})
	return err
}

// lookupTaskReceiptLocked 只核对原激活，不能把另一物理尝试的回执当作当前完成。
func (s *runSession) lookupTaskReceiptLocked(ctx context.Context, id string, activation TaskActivation) (*TaskResult, error) {
	archive, ok := s.store.(ReceiptStore)
	if !ok {
		return nil, nil
	}
	receipts, err := archive.ListReceipts(ctx, s.record.Scope, id)
	if err != nil {
		return nil, err
	}
	for _, receipt := range receipts {
		if receipt.RunID != s.record.RunID || receipt.Provider != "runtime.task" {
			continue
		}
		var payload taskReceiptPayload
		decoder := json.NewDecoder(bytes.NewReader(receipt.Payload))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			return nil, fmt.Errorf("runtime: invalid task receipt: %w", err)
		}
		result := payload.Result
		if result.TaskID != id || result.AttemptID != activation.AttemptID || result.Activation != activation.Activation || result.InputRevision != activation.InputRevision {
			continue
		}
		return &result, nil
	}
	return nil, nil
}

func (s *runSession) archiveModelFact(ctx context.Context, fact ModelAttempt) error {
	if !fact.ProviderCalled {
		return nil
	}
	archive, ok := s.store.(ReceiptStore)
	if !ok {
		return nil
	}
	s.mu.Lock()
	scope, runID := s.record.Scope, s.record.RunID
	s.mu.Unlock()
	payload, err := json.Marshal(struct {
		Attempt ModelAttempt `json:"attempt"`
		Error   string       `json:"error,omitempty"`
	}{fact, errorText(fact.Error)})
	if err != nil {
		return err
	}
	operationID := fact.ID
	if fact.ParentAttemptID != "" {
		operationID = fact.ParentAttemptID
	}
	status := OperationSucceeded
	if fact.Outcome == "failed" && !fact.ProviderSucceeded {
		status = OperationFailed
	}
	provider := fact.Provider
	if provider == "" {
		provider = "unspecified"
	}
	_, err = archive.ArchiveReceipt(ctx, OperationReceipt{Scope: scope, RunID: runID, OperationID: "model/" + operationID, ReceiptID: "model/" + fact.ID + "/final", Provider: provider, ProviderTaskID: fact.ResponseID, Status: status, Payload: payload})
	return err
}
