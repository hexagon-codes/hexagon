package mysql

import (
	"encoding/json"
	"testing"
)

func TestArtifactRecordKeepsNumbersAndRequiresSingleJSON(t *testing.T) {
	data := []byte(`{"id":"one","version":2,"metadata":{"large":9007199254740993,"nested":[1.2300]}}`)
	got, err := decodeArtifactRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || got.Metadata["large"].(json.Number).String() != "9007199254740993" || got.Metadata["nested"].([]any)[0].(json.Number).String() != "1.2300" {
		t.Fatalf("record values changed: %+v", got)
	}
	for _, suffix := range []string{` {}`, ` trailing`} {
		if _, err := decodeArtifactRecord(append(append([]byte(nil), data...), suffix...)); err == nil {
			t.Fatalf("accepted trailing data %q", suffix)
		}
	}
}
