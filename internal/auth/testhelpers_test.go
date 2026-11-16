package auth_test

import (
	"bytes"
	"encoding/json"
	"testing"
)

// mustMarshal serializes v to JSON and returns a *bytes.Buffer. Fails the test on error.
func mustMarshal(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustMarshal: %v", err)
	}
	return bytes.NewBuffer(b)
}
