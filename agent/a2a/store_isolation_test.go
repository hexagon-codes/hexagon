package a2a

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"
)

type privateProtocolValue struct {
	value map[string]any
}

func (v privateProtocolValue) MarshalJSON() ([]byte, error) { return json.Marshal(v.value) }

func TestMemoryTaskStoreRejectsUnisolatableValueWithoutMutation(t *testing.T) {
	store := NewMemoryTaskStore(WithTaskTTL(0), WithMaxTasks(1))
	defer store.Close()
	original := NewTask("original")
	original.Status.State = TaskStateCompleted
	original.Metadata = map[string]any{"value": "original"}
	if err := store.Create(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	invalid := NewTask("unisolatable")
	invalid.Metadata = map[string]any{"private": privateProtocolValue{value: map[string]any{"value": "unshared"}}}
	if _, err := json.Marshal(invalid); err != nil {
		t.Fatalf("fixture must remain valid protocol JSON: %v", err)
	}
	if err := store.Create(t.Context(), invalid); err == nil {
		t.Fatal("unisolatable value was accepted")
	}
	if _, err := store.Get(t.Context(), invalid.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("failed create left a task: %v", err)
	}
	updated := *invalid
	updated.ID = original.ID
	if err := store.Update(t.Context(), &updated); err == nil {
		t.Fatal("unisolatable update was accepted")
	}
	stored, err := store.Get(t.Context(), original.ID)
	if err != nil || stored.Metadata["value"] != "original" {
		t.Fatalf("failed write changed or evicted original task: %+v %v", stored, err)
	}
	listed, err := store.List(t.Context(), &ListTasksRequest{})
	if err != nil || len(listed.Tasks) != 1 || listed.Tasks[0].ID != original.ID {
		t.Fatalf("failed write changed stored task list: %+v %v", listed, err)
	}
}

func TestMemoryTaskStoreIsolatesNestedProtocolData(t *testing.T) {
	fixture := func() *Task {
		task := NewTask("nested-task")
		message := NewAgentMessage("original answer")
		status := NewAgentMessage("original status")
		task.History = []Message{message}
		task.Status = TaskStatus{State: TaskStateCompleted, Message: &status}
		task.Metadata = map[string]any{"nested": map[string]any{"number": int64(9007199254740993)}}
		task.Artifacts = []Artifact{{ID: "media", Parts: []Part{
			&DataPart{Data: map[string]any{"bytes": []byte{1, 2, 3}}},
			&FilePart{File: FileContent{Bytes: "AQID", MimeType: "application/octet-stream"}},
		}}}
		return task
	}
	mutate := func(task *Task) {
		task.History[0].Parts[0].(*TextPart).Text = "changed answer"
		task.Status.Message.Parts[0].(*TextPart).Text = "changed status"
		task.Metadata["nested"].(map[string]any)["number"] = int64(0)
		task.Artifacts[0].Parts[0].(*DataPart).Data.(map[string]any)["bytes"].([]byte)[0] = 9
		task.Artifacts[0].Parts[1].(*FilePart).File.Bytes = "CQID"
	}
	assertOriginal := func(task *Task) {
		t.Helper()
		if task.History[0].GetTextContent() != "original answer" || task.Status.Message.GetTextContent() != "original status" {
			t.Fatal("message mutation escaped its copy")
		}
		if value := task.Metadata["nested"].(map[string]any)["number"]; value != int64(9007199254740993) {
			t.Fatalf("metadata type or precision changed: %T %v", value, value)
		}
		if task.Artifacts[0].Parts[0].(*DataPart).Data.(map[string]any)["bytes"].([]byte)[0] != 1 || task.Artifacts[0].Parts[1].(*FilePart).File.Bytes != "AQID" {
			t.Fatal("artifact media mutation escaped its copy")
		}
	}
	store := NewMemoryTaskStore(WithTaskTTL(0))
	defer store.Close()
	source := fixture()
	if err := store.Create(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	mutate(source)
	first, err := store.Get(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal(first)
	mutate(first)
	second, err := store.Get(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal(second)
	updated := fixture()
	if err := store.Update(t.Context(), updated); err != nil {
		t.Fatal(err)
	}
	mutate(updated)
	listed, err := store.List(t.Context(), &ListTasksRequest{})
	if err != nil || len(listed.Tasks) != 1 {
		t.Fatalf("list failed: %+v %v", listed, err)
	}
	assertOriginal(listed.Tasks[0])
	mutate(listed.Tasks[0])
	last, err := store.Get(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal(last)
}

func TestMemoryTaskStoreIsolatesStandardBigInteger(t *testing.T) {
	store := NewMemoryTaskStore(WithTaskTTL(0))
	defer store.Close()
	task := NewTask("integer-task")
	task.Metadata = map[string]any{"amount": big.NewInt(1000)}
	if err := store.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	task.Metadata["amount"].(*big.Int).SetInt64(1)
	read := func() *Task {
		t.Helper()
		got, err := store.Get(t.Context(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Metadata["amount"].(*big.Int).Cmp(big.NewInt(1000)) != 0 {
			t.Fatalf("stored number changed: %+v", got.Metadata)
		}
		return got
	}
	got := read()
	got.Metadata["amount"].(*big.Int).SetInt64(2)
	updated := read()
	if err := store.Update(t.Context(), updated); err != nil {
		t.Fatal(err)
	}
	updated.Metadata["amount"].(*big.Int).SetInt64(3)
	list, err := store.List(t.Context(), &ListTasksRequest{})
	if err != nil || len(list.Tasks) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if list.Tasks[0].Metadata["amount"].(*big.Int).Cmp(big.NewInt(1000)) != 0 {
		t.Fatal("update source changed stored number")
	}
	list.Tasks[0].Metadata["amount"].(*big.Int).SetInt64(4)
	read()
}
