package api

import (
	"math"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseQuery(t *testing.T) {
	const endpoint = "GET /v1/observations"
	allowed := []string{"series_id", "available_as_of", "page_size"}
	tests := []struct {
		raw string
		// want is the parameters, or nil when code is set.
		want      map[string]string
		code      string
		parameter string
	}{
		{raw: "", want: map[string]string{}},
		{raw: "series_id=activity-index", want: map[string]string{"series_id": "activity-index"}},
		{raw: "series_id=a&page_size=10&", want: map[string]string{"series_id": "a", "page_size": "10"}},
		{raw: "available_as_of=2026-09-04+00%3A00%3A00Z", want: map[string]string{"available_as_of": "2026-09-04 00:00:00Z"}},
		{raw: "series_%69d=a", want: map[string]string{"series_id": "a"}},
		{raw: "available_asof=x", code: "unknown_parameter", parameter: "available_asof"},
		{raw: "series_id=a&series_id=a", code: "invalid_parameter", parameter: "series_id"},
		{raw: "series_id=", code: "invalid_parameter", parameter: "series_id"},
		{raw: "series_id", code: "invalid_parameter", parameter: "series_id"},
		{raw: "series_id=%zz", code: "invalid_parameter", parameter: "series_id"},
		{raw: "%zz=a", code: "invalid_parameter", parameter: "%zz"},
		{raw: "series_id=a;page_size=10", want: map[string]string{"series_id": "a;page_size=10"}},
	}
	for _, tt := range tests {
		got, p := parseQuery(tt.raw, endpoint, allowed...)
		if tt.code != "" {
			if p == nil || p.Code != tt.code || p.Parameter == nil || *p.Parameter != tt.parameter {
				t.Errorf("%q: got %+v, want %s naming %s", tt.raw, p, tt.code, tt.parameter)
			}
			continue
		}
		if p != nil {
			t.Errorf("%q: %v", tt.raw, p)
			continue
		}
		if len(got) != len(tt.want) {
			t.Errorf("%q: got %v, want %v", tt.raw, got, tt.want)
		}
		for k, v := range tt.want {
			if got[k] != v {
				t.Errorf("%q: %s is %q, want %q", tt.raw, k, got[k], v)
			}
		}
	}
	if _, p := parseQuery("", endpoint, allowed...); p != nil {
		t.Error(p)
	}
	q, _ := parseQuery("page_size=10", endpoint, allowed...)
	if _, p := q.required("series_id", endpoint); p == nil || p.Code != "missing_parameter" || *p.Parameter != "series_id" {
		t.Errorf("required: got %+v", p)
	}
}

func TestParseTimestamp(t *testing.T) {
	valid := []string{"2026-09-04T00:00:00Z", "2024-02-29T23:59:59Z", "9999-12-31T23:59:59Z"}
	invalid := []string{
		"2026-09-04T00:00:00.000Z", "2026-09-04T00:00:00+00:00", "2026-09-04", "2026-09-04t00:00:00z",
		"2026-09-04 00:00:00Z", "2026-09-04T24:00:00Z", "2026-09-04T00:00:60Z", "2026-02-30T00:00:00Z",
		"2025-02-29T00:00:00Z", "yesterday", "",
	}
	for _, s := range valid {
		if _, p := parseTimestamp("available_as_of", s); p != nil {
			t.Errorf("%q: %v", s, p)
		}
	}
	for _, s := range invalid {
		if _, p := parseTimestamp("available_as_of", s); p == nil || p.Code != "invalid_parameter" || *p.Parameter != "available_as_of" {
			t.Errorf("%q: got %+v, want invalid_parameter naming available_as_of", s, p)
		}
	}
}

func TestParseDate(t *testing.T) {
	for _, s := range []string{"2026-08-01", "2024-02-29"} {
		if _, p := parseDate("period_start", s); p != nil {
			t.Errorf("%q: %v", s, p)
		}
	}
	for _, s := range []string{"2026-02-30", "2026-8-01", "2026-09-01T00:00:00Z", "20260801", ""} {
		if _, p := parseDate("period_start", s); p == nil || *p.Parameter != "period_start" {
			t.Errorf("%q: got %+v, want invalid_parameter naming period_start", s, p)
		}
	}
}

func TestParseInteger(t *testing.T) {
	for _, tt := range []struct {
		s    string
		want int64
	}{{"0", 0}, {"1", 1}, {"1000", 1000}} {
		if n, p := parseInteger("limit", tt.s, 0, 1000); p != nil || n != tt.want {
			t.Errorf("%q: got %d, %v", tt.s, n, p)
		}
	}
	for _, s := range []string{"-1", "+5", "010", "1.5", "ten", "1e3", " 1", "1001", "99999999999999999999"} {
		if _, p := parseInteger("limit", s, 0, 1000); p == nil || *p.Parameter != "limit" {
			t.Errorf("%q: got %+v, want invalid_parameter naming limit", s, p)
		}
	}
	if n, p := parseInteger("after", "9007199254740991", 0, math.MaxInt64); p != nil || n != 9007199254740991 {
		t.Errorf("a large integer: %d, %v", n, p)
	}
}

func TestParseBody(t *testing.T) {
	const endpoint = "PUT /test/credentials/{credential_id}"
	parse := func(body string) (bodyFields, *problem) {
		r := httptest.NewRequest("PUT", "/test/credentials/cred_research", strings.NewReader(body))
		return parseBody(r, endpoint, "active", "datasets")
	}
	for _, body := range []string{"", "{}", ` { "active": true } `, `{"datasets": ["a"], "active": false}`} {
		if _, p := parse(body); p != nil {
			t.Errorf("%q: %v", body, p)
		}
	}
	for _, tt := range []struct {
		body, code string
		parameter  *string
	}{
		{`["2026-10-01T00:00:00Z"]`, "invalid_parameter", nil},
		{`null`, "invalid_parameter", nil},
		{`"x"`, "invalid_parameter", nil},
		{` `, "invalid_parameter", nil},
		{`{"active": true`, "invalid_parameter", nil},
		{`{"active": true} {}`, "invalid_parameter", nil},
		{`{"active": true, "position": 10}`, "unknown_parameter", named("position")},
		{strings.Repeat(" ", maxBodyBytes) + "{}", "invalid_parameter", nil},
	} {
		_, p := parse(tt.body)
		if p == nil || p.Code != tt.code || (p.Parameter == nil) != (tt.parameter == nil) || (p.Parameter != nil && *p.Parameter != *tt.parameter) {
			t.Errorf("%.40q: got %+v, want %s", tt.body, p, tt.code)
		}
	}
}

func TestBodyFields(t *testing.T) {
	fields := bodyFields{
		"now":      []byte(`"2026-10-01T00:00:00Z"`),
		"late":     []byte(`"2026-10-01"`),
		"number":   []byte(`1`),
		"null":     []byte(`null`),
		"yes":      []byte(`true`),
		"list":     []byte(`["a", "b"]`),
		"empty":    []byte(`[]`),
		"withNull": []byte(`["a", null]`),
	}
	if tm, present, p := fields.timestamp("now"); !present || p != nil || formatTimestamp(tm) != "2026-10-01T00:00:00Z" {
		t.Errorf("timestamp: %v %v %v", tm, present, p)
	}
	if _, present, p := fields.timestamp("absent"); present || p != nil {
		t.Errorf("an absent timestamp: %v %v", present, p)
	}
	for _, name := range []string{"late", "number", "null", "yes"} {
		if _, _, p := fields.timestamp(name); p == nil || *p.Parameter != name {
			t.Errorf("timestamp %s: got %+v", name, p)
		}
	}
	if b, p := fields.boolean("yes"); p != nil || b == nil || !*b {
		t.Errorf("boolean: %v %v", b, p)
	}
	if b, p := fields.boolean("absent"); p != nil || b != nil {
		t.Errorf("an absent boolean: %v %v", b, p)
	}
	for _, name := range []string{"null", "number", "now"} {
		if _, p := fields.boolean(name); p == nil || *p.Parameter != name {
			t.Errorf("boolean %s: got %+v", name, p)
		}
	}
	if v, present, p := fields.stringArray("list"); !present || p != nil || strings.Join(v, ",") != "a,b" {
		t.Errorf("stringArray: %v %v %v", v, present, p)
	}
	if v, present, p := fields.stringArray("empty"); !present || p != nil || v == nil || len(v) != 0 {
		t.Errorf("an empty array: %#v %v %v", v, present, p)
	}
	for _, name := range []string{"withNull", "null", "now"} {
		if _, _, p := fields.stringArray(name); p == nil || *p.Parameter != name {
			t.Errorf("stringArray %s: got %+v", name, p)
		}
	}
}
