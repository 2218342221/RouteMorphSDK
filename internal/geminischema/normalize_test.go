package geminischema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeNestedSchema(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"object",
		"title":"lookup",
		"properties":{
			"query":{"type":["string","null"],"minLength":1},
			"limit":{"type":"integer","enum":[1,2,2]},
			"filters":{"type":"array","items":{"type":"boolean"}}
		},
		"required":["query"],
		"propertyOrdering":["query","limit","filters"],
		"additionalProperties":false
	}`)

	got, err := Normalize(raw, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"properties":{"filters":{"items":{"type":"BOOLEAN"},"type":"ARRAY"},"limit":{"enum":["1","2"],"type":"INTEGER"},"query":{"minLength":1,"nullable":true,"type":"STRING"}},"propertyOrdering":["query","limit","filters"],"required":["query"],"title":"lookup","type":"OBJECT"}`
	if string(got) != want {
		t.Fatalf("Normalize() = %s\nwant        = %s", got, want)
	}
	if strings.Contains(string(got), "additionalProperties") {
		t.Fatalf("closed-object compatibility marker leaked into Gemini schema: %s", got)
	}
}

func TestNormalizeTypeUnionAndAnyOfNull(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "multi type",
			raw:  `{"type":["string","number","null"]}`,
			want: `{"anyOf":[{"type":"STRING"},{"type":"NUMBER"}],"nullable":true}`,
		},
		{
			name: "null anyOf branch",
			raw:  `{"anyOf":[{"type":"string"},{"type":"null"}]}`,
			want: `{"anyOf":[{"type":"STRING"}],"nullable":true}`,
		},
		{
			name: "pure null type",
			raw:  `{"type":"null"}`,
			want: `{"type":"NULL"}`,
		},
		{
			name: "fractional numeric bound",
			raw:  `{"type":"number","minimum":-1.5,"maximum":2.25}`,
			want: `{"maximum":2.25,"minimum":-1.5,"type":"NUMBER"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Normalize(json.RawMessage(test.raw), Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("Normalize() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestNormalizeRejectsUnsupportedOrUnsafeShapes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		kind error
	}{
		{name: "unknown keyword", raw: `{"type":"string","oneOf":[]}`, kind: ErrUnsupportedKeyword},
		{name: "open object", raw: `{"type":"object","additionalProperties":true}`, kind: ErrUnsupportedKeyword},
		{name: "schema valued extras", raw: `{"type":"object","additionalProperties":{"type":"string"}}`, kind: ErrUnsupportedKeyword},
		{name: "tuple items", raw: `{"type":"array","items":[{"type":"string"}]}`, kind: ErrUnsupportedKeyword},
		{name: "null properties", raw: `{"type":"object","properties":null}`, kind: ErrInvalidSchema},
		{name: "null anyOf", raw: `{"anyOf":null}`, kind: ErrInvalidSchema},
		{name: "required undeclared", raw: `{"type":"object","properties":{},"required":["missing"]}`, kind: ErrInvalidSchema},
		{name: "non object root", raw: `true`, kind: ErrInvalidSchema},
		{name: "trailing document", raw: `{} {}`, kind: ErrInvalidSchema},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Normalize(json.RawMessage(test.raw), Limits{})
			if !errors.Is(err, test.kind) {
				t.Fatalf("Normalize() error = %v, want %v", err, test.kind)
			}
		})
	}
}

func TestNormalizeEnforcesBudgets(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		limits Limits
	}{
		{name: "bytes", raw: `{"type":"string"}`, limits: Limits{MaxBytes: 5}},
		{name: "depth", raw: `{"type":"array","items":{"type":"array","items":{"type":"string"}}}`, limits: Limits{MaxDepth: 3}},
		{name: "nodes", raw: `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}`, limits: Limits{MaxNodes: 5}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Normalize(json.RawMessage(test.raw), test.limits)
			if !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("Normalize() error = %v, want ErrLimitExceeded", err)
			}
		})
	}
}

func TestNormalizeRejectsInvalidLimits(t *testing.T) {
	_, err := Normalize(json.RawMessage(`{}`), Limits{MaxNodes: -1})
	if !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("Normalize() error = %v, want ErrInvalidSchema", err)
	}
}

func TestNormalizeReportsPreciseViolationPath(t *testing.T) {
	_, err := Normalize(json.RawMessage(`{"type":"object","properties":{"value":{"oneOf":[]}}}`), Limits{})
	var violation *Violation
	if !errors.As(err, &violation) {
		t.Fatalf("Normalize() error = %v, want *Violation", err)
	}
	if violation.Path != "$.properties.value.oneOf" || violation.Reason == "" || !errors.Is(violation, ErrUnsupportedKeyword) {
		t.Fatalf("violation = %#v", violation)
	}
}

func TestNormalizeParametersOmitsEmptySchemas(t *testing.T) {
	for _, raw := range []string{"", "null", `{}`, `{"type":"object"}`, `{"type":"object","properties":{}}`} {
		got, err := NormalizeParameters(json.RawMessage(raw), Limits{})
		if err != nil {
			t.Fatalf("NormalizeParameters(%q): %v", raw, err)
		}
		if got != nil {
			t.Fatalf("NormalizeParameters(%q) = %s, want nil", raw, got)
		}
	}
	got, err := NormalizeParameters(json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"properties":{"city":{"type":"STRING"}},"type":"OBJECT"}` {
		t.Fatalf("NormalizeParameters() = %s", got)
	}
}
