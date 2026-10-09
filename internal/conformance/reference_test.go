package conformance

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nslaughter/financial-data-api/internal/expected"
)

// testBodies returns bodies with step c's body: an export manifest.
func testBodies(t *testing.T) bodies {
	return bodies{
		"c": {value: jsonValue(t, `{
			"export_id": "exp_1",
			"position": 36,
			"coverage": {"period_start": null},
			"files": [{"url": "/v1/exports/exp_1/files/revisions.jsonl"}],
			"flag": true
		}`)},
		"file": {err: errors.New("invalid character")},
	}
}

func TestResolveString(t *testing.T) {
	b := testBodies(t)
	tests := []struct {
		in   string
		want any
	}{
		{"no references", "no references"},
		{"${c.export_id}", "exp_1"},
		// A string that is exactly one reference keeps the value's type.
		{"${c.position}", json.Number("36")},
		{"${c.coverage.period_start}", nil},
		{"${c.flag}", true},
		{"${c.files.0.url}", "/v1/exports/exp_1/files/revisions.jsonl"},
		// A reference in a longer string is inserted as text.
		{"/v1/exports/${c.export_id}", "/v1/exports/exp_1"},
		{"after ${c.position}", "after 36"},
		{"${c.export_id}/${c.position}", "exp_1/36"},
		// Text that is not a reference stays.
		{"${", "${"},
		{"$c.export_id", "$c.export_id"},
	}
	for _, tt := range tests {
		got, err := b.resolveString(tt.in)
		if err != nil {
			t.Errorf("%s: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: got %#v, want %#v", tt.in, got, tt.want)
		}
	}
}

func TestResolveStringErrors(t *testing.T) {
	b := testBodies(t)
	tests := []struct{ in, want string }{
		{"${d.export_id}", "names no step that has run"},
		{"${c.missing}", `the body of step c has no member "missing"`},
		{"${c.files.1.url}", "an array of 1 elements, has no element 1"},
		{"${c.files.01.url}", "has no element 01"},
		{"${c.export_id.x}", `c.export_id is a string, which has no member "x"`},
		{"${file.url}", "the body of step file is not JSON"},
		{"x${c.coverage}", "must name a string or a number, not an object"},
		{"x${c.coverage.period_start}", "not null"},
		{"${c}", "malformed reference"},
		{"${.export_id}", "malformed reference"},
		{"${c..export_id}", "malformed reference"},
	}
	for _, tt := range tests {
		_, err := b.resolveString(tt.in)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got error %v, want one containing %q", tt.in, err, tt.want)
		}
	}
}

func TestResolveLeavesTheValueUnchanged(t *testing.T) {
	b := testBodies(t)
	in := jsonValue(t, `{"id": "${c.export_id}", "list": ["${c.position}", 1], "${c.export_id}": "key"}`)
	got, err := b.resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if d := expected.Match("", jsonValue(t, `{"id": "exp_1", "list": [36, 1], "${c.export_id}": "key"}`), got); d != nil {
		t.Fatalf("resolved value differs at %s: expected %s, got %s", d.At, d.Expected, d.Actual)
	}
	if in.(map[string]any)["id"] != "${c.export_id}" {
		t.Fatal("resolve changed its argument")
	}
}
