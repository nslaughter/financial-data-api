package conformance

import (
	"encoding/json"
	"testing"
)

// jsonValue decodes text as the runner decodes JSON, with json.Number.
func jsonValue(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := decodeJSON([]byte(text), &v); err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return v
}

func TestMatch(t *testing.T) {
	tests := []struct {
		name             string
		expected, actual string
		// at is where the first difference is, or "" when the values match.
		at               string
		wantExp, wantAct string
	}{
		{name: "equal strings", expected: `"102.0"`, actual: `"102.0"`},
		{name: "strings compare exactly", expected: `"102.0"`, actual: `"102"`, at: "v", wantExp: `"102.0"`, wantAct: `"102"`},
		{name: "a string does not match a number", expected: `"102"`, actual: `102`, at: "v", wantExp: `"102"`, wantAct: `102`},
		{name: "a number does not match a string", expected: `37`, actual: `"37"`, at: "v", wantExp: `37`, wantAct: `"37"`},
		{name: "numbers compare by value", expected: `37`, actual: `37.0`},
		{name: "numbers compare by value with an exponent", expected: `3.7e1`, actual: `37`},
		{name: "unequal numbers", expected: `37`, actual: `36`, at: "v", wantExp: `37`, wantAct: `36`},
		{name: "a boolean does not match a number", expected: `true`, actual: `1`, at: "v", wantExp: `true`, wantAct: `1`},
		{name: "null matches null", expected: `null`, actual: `null`},
		{name: "null does not match a string", expected: `null`, actual: `"null"`, at: "v", wantExp: `null`, wantAct: `"null"`},
		{name: "null does not match an absent member", expected: `{"value": null}`, actual: `{}`, at: "v.value", wantExp: `null`, wantAct: "no member"},
		{name: "a string does not match null", expected: `{"value": "1"}`, actual: `{"value": null}`, at: "v.value", wantExp: `"1"`, wantAct: `null`},
		{name: "unnamed members are not compared", expected: `{"a": 1}`, actual: `{"a": 1, "b": 2}`},
		{name: "an empty object matches any object", expected: `{}`, actual: `{"a": 1}`},
		{name: "an object does not match an array", expected: `{}`, actual: `[]`, at: "v", wantExp: `{}`, wantAct: `[]`},
		{name: "nested members", expected: `{"a": {"b": [1, {"c": "x"}]}}`, actual: `{"a": {"b": [1, {"c": "y", "d": 0}]}}`, at: "v.a.b[1].c", wantExp: `"x"`, wantAct: `"y"`},
		{name: "arrays match element by element", expected: `[{"id": 1}, {"id": 2}]`, actual: `[{"id": 1, "x": 0}, {"id": 2}]`},
		{name: "arrays are ordered", expected: `[1, 2]`, actual: `[2, 1]`, at: "v[0]", wantExp: `1`, wantAct: `2`},
		{name: "a longer actual array", expected: `[1]`, actual: `[1, 2]`, at: "v", wantExp: "1 elements", wantAct: "2 elements: [1,2]"},
		{name: "a shorter actual array", expected: `[1, 2]`, actual: `[1]`, at: "v", wantExp: "2 elements", wantAct: "1 elements: [1]"},
		{name: "an empty array matches only an empty array", expected: `[]`, actual: `[0]`, at: "v", wantExp: "0 elements", wantAct: "1 elements: [0]"},
		{name: "an element differs before the length", expected: `[1, 2]`, actual: `[3]`, at: "v[0]", wantExp: `1`, wantAct: `3`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := match("v", jsonValue(t, tt.expected), jsonValue(t, tt.actual))
			if tt.at == "" {
				if d != nil {
					t.Fatalf("got a difference at %s: expected %s, actual %s", d.at, d.expected, d.actual)
				}
				return
			}
			if d == nil {
				t.Fatalf("matched; want a difference at %s", tt.at)
			}
			if d.at != tt.at || d.expected != tt.wantExp || d.actual != tt.wantAct {
				t.Fatalf("got %s: expected %s, actual %s; want %s: expected %s, actual %s",
					d.at, d.expected, d.actual, tt.at, tt.wantExp, tt.wantAct)
			}
		})
	}
}

func TestMatchWithoutPrefix(t *testing.T) {
	d := match("", jsonValue(t, `{"a": 1}`), jsonValue(t, `{"a": 2}`))
	if d == nil || d.at != "a" {
		t.Fatalf("got %+v, want a difference at a", d)
	}
}

func TestRenderShortensLongValues(t *testing.T) {
	long := make([]any, 200)
	for i := range long {
		long[i] = "obs"
	}
	got := render(long)
	if len(got) > maxRendered+len("…") || got[len(got)-len("…"):] != "…" {
		t.Fatalf("render returned %d bytes ending %q", len(got), got[len(got)-5:])
	}
}

func TestDecimal(t *testing.T) {
	tests := []struct{ in, want string }{
		{"36", "36"},
		{"36.0", "36"},
		{"3.6e1", "36"},
		{"3.6E+1", "36"},
		{"3600e-2", "36"},
		{"0.1e1", "1"},
		{"1e3", "1000"},
		{"-0", "0"},
		{"-0.0e5", "0"},
		{"1.50", "1.5"},
		{"15e-1", "1.5"},
		{"1.5e-3", "0.0015"},
		{"-2.5E+0", "-2.5"},
		{"123456789012345678901234567890", "123456789012345678901234567890"},
	}
	for _, tt := range tests {
		got, err := decimal(json.Number(tt.in))
		if err != nil || got != tt.want {
			t.Errorf("decimal(%s) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	if _, err := decimal("1e2000000"); err == nil {
		t.Error("decimal(1e2000000): want an error")
	}
}

func TestInteger(t *testing.T) {
	for _, in := range []string{"36", "36.0", "3.6e1", "360e-1"} {
		if n, ok := integer(json.Number(in)); !ok || n.Int64() != 36 {
			t.Errorf("integer(%s) = %v, %v; want 36", in, n, ok)
		}
	}
	for _, in := range []string{"36.5", "3.65e1", ""} {
		if n, ok := integer(json.Number(in)); ok {
			t.Errorf("integer(%q) = %v; want no integer", in, n)
		}
	}
}
