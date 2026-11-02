package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func decodeRuntimeJSON(data []byte, target any) error {
	if !json.Valid(data) {
		return fmt.Errorf("runtime: invalid JSON document")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}
