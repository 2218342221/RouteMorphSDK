package schema

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNormalizeFunctionParameters(t *testing.T) {
	for _, input := range []json.RawMessage{nil, json.RawMessage(`null`)} {
		got, err := NormalizeFunctionParameters(input)
		if err != nil || string(got) != `{"type":"object","properties":{}}` {
			t.Fatalf("got=%s error=%v", got, err)
		}
	}
	got, err := NormalizeFunctionParameters(json.RawMessage(`{"additionalProperties":false}`))
	if err != nil || string(got) != `{"additionalProperties":false,"properties":{},"type":"object"}` {
		t.Fatalf("got=%s error=%v", got, err)
	}
	if _, err := NormalizeFunctionParameters(json.RawMessage(`[]`)); !errors.Is(err, ErrObjectRequired) {
		t.Fatalf("error = %v", err)
	}
}
