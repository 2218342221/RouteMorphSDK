package jsonx

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDecodeOne(t *testing.T) {
	var value map[string]any
	if err := DecodeOne([]byte(`{"n":9007199254740993}`), &value); err != nil {
		t.Fatal(err)
	}
	if _, ok := value["n"].(json.Number); !ok {
		t.Fatalf("number type = %T", value["n"])
	}
	for _, data := range []string{"", "{} {}", "{"} {
		if err := DecodeOne([]byte(data), &value); err == nil {
			t.Fatalf("DecodeOne(%q) unexpectedly succeeded", data)
		}
	}
}

func TestObject(t *testing.T) {
	object, err := Object([]byte(`{"x":1}`))
	if err != nil || string(object["x"]) != "1" {
		t.Fatalf("object=%v error=%v", object, err)
	}
	if _, err := Object([]byte("null")); !errors.Is(err, ErrObjectRequired) {
		t.Fatalf("null error = %v", err)
	}
}

func TestNormalizeObject(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"", `{}`}, {"null", `{}`}, {`""`, `{}`}, {`"{\"x\":1}"`, `{"x":1}`}, {`{"x":1}`, `{"x":1}`},
	} {
		got, err := NormalizeObject(json.RawMessage(test.input))
		if err != nil || string(got) != test.want {
			t.Fatalf("NormalizeObject(%q)=%s,%v want %s", test.input, got, err, test.want)
		}
	}
	for _, input := range []string{`[]`, `"not-json"`} {
		if _, err := NormalizeObject(json.RawMessage(input)); err == nil {
			t.Fatalf("NormalizeObject(%q) unexpectedly succeeded", input)
		}
	}
}

func TestValueHelpers(t *testing.T) {
	if Present(nil) || Present(json.RawMessage(`null`)) || Present(json.RawMessage(`{}`)) || Present(json.RawMessage(`[]`)) || Present(json.RawMessage(`""`)) {
		t.Fatal("empty JSON values reported present")
	}
	if !Present(json.RawMessage(`0`)) || RawString(String("x")) != "x" || RawString(json.RawMessage(`{"x":1}`)) != `{"x":1}` {
		t.Fatal("value helper mismatch")
	}
}
