package a2a

import "testing"

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
