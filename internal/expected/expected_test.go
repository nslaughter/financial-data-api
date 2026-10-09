package expected

import (
	"slices"
	"strings"
	"testing"
)

func loadExpected(t *testing.T) []*File {
	t.Helper()
	files, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func fileNamed(t *testing.T, files []*File, name string) *File {
	t.Helper()
	for _, f := range files {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no file %s", name)
	return nil
}

// TestLoadExpected decodes every file of expected/ strictly.
func TestLoadExpected(t *testing.T) {
	files := loadExpected(t)
	if len(files) != 18 {
		t.Errorf("loaded %d files, want 18", len(files))
	}
	full := fileNamed(t, files, "full-history")
	if len(full.QueryChecks) != 2 || len(full.Pages) != 4 {
		t.Errorf("full-history: %d query checks and %d pages, want 2 and 4", len(full.QueryChecks), len(full.Pages))
	}
	stream := fileNamed(t, files, "change-stream")
	if len(stream.PositionChecks) == 0 || len(stream.ReadChecks) == 0 || len(stream.Scenarios) == 0 {
		t.Error("change-stream: want position checks, read checks, and scenarios")
	}
	timing := fileNamed(t, files, TimingFile)
	if len(timing.TimingChecks) == 0 || len(timing.QueryChecks) != 0 {
		t.Errorf("release-timing: %d timing checks and %d query checks", len(timing.TimingChecks), len(timing.QueryChecks))
	}
	published := fileNamed(t, files, "published-as-of")
	contrasts := 0
	for _, c := range published.QueryChecks {
		if c.ContrastAvailableAsOf.Present {
			contrasts++
		}
	}
	if contrasts == 0 {
		t.Error("published-as-of: no check has contrast_available_as_of")
	}
	staged := 0
	for _, s := range fileNamed(t, files, "request-errors").Scenarios {
		if s.Stages != nil {
			staged++
			if !slices.Equal(*s.Stages, []int{1}) {
				t.Errorf("request-errors: scenario %q has stages %v, want [1]", s.Name, *s.Stages)
			}
		}
	}
	if staged != 1 {
		t.Errorf("request-errors: %d scenarios have stages, want 1", staged)
	}
}

// TestParseFileIsStrict checks that malformed files are refused before
// anything runs.
func TestParseFileIsStrict(t *testing.T) {
	scenario := func(steps string) string {
		return `{"scenarios": [{"name": "s", "reason": "r", "steps": [` + steps + `]}]}`
	}
	meta := `{"request": {"path": "/v1/meta"}, "expect": {"status": 200}}`
	tests := []struct {
		name, text, want string
	}{
		{"an unknown member", `{"contract_version": "0.3.0", "checkz": []}`, `unknown field "checkz"`},
		{"an unknown member of a query check", `{"checks": [{"name": "q", "query": {}, "expected": [], "clocks": "x"}]}`, `unknown field "clocks"`},
		{"a query check without expected", `{"checks": [{"name": "q", "query": {}}]}`, "needs name, query, and expected"},
		{"a query parameter that is not a string", `{"checks": [{"name": "q", "query": {"page_size": 10}, "expected": []}]}`, "cannot unmarshal number"},
		{"a contrast without published_as_of", `{"checks": [{"name": "q", "query": {"available_as_of": "2026-09-04T00:00:00Z"}, "expected": [], "contrast_available_as_of": []}]}`,
			"contrast_available_as_of needs a query with published_as_of"},
		{"pages without query checks", `{"pages_with_page_size_10": [{"first": "a", "last": "b", "count": 1}]}`, "needs query checks"},
		{"a read check without expected", `{"read_checks": [{"name": "r", "after_position": 0, "expected_next_position": 0, "expected_head_position": 0}]}`, "needs name, after_position, expected"},
		{"a position check without at", `{"position_checks": [{"name": "p", "expected_position": 0}]}`, "needs name, at, and expected_position"},
		{"trailing data", `{} {}`, "data after the JSON value"},
		{"a step with two actions", scenario(`{"set_clock": "2026-10-01T00:00:00Z", "reset": {}}`), "has 2 actions"},
		{"a step with no action", scenario(`{"expect": {"status": 200}}`), "has 0 actions"},
		{"an unknown step member", scenario(`{"set_clock": "2026-10-01T00:00:00Z", "reason": "r"}`), `unknown field "reason"`},
		{"an unknown request member", scenario(`{"request": {"path": "/v1/meta", "headers": {}}, "expect": {"status": 200}}`), `unknown field "headers"`},
		{"an unknown expect member", scenario(`{"request": {"path": "/v1/meta"}, "expect": {"status": 200, "json": {}}}`), `unknown field "json"`},
		{"a request without expect", scenario(`{"request": {"path": "/v1/meta"}}`), "a request step needs an expect"},
		{"a request without a path", scenario(`{"request": {}, "expect": {"status": 200}}`), "needs a path"},
		{"an expect without a status", scenario(`{"request": {"path": "/v1/meta"}, "expect": {}}`), "needs a status"},
		{"a test action with an id", scenario(`{"id": "a", "reset": {}}`), "only a request step may have an id or an expect"},
		{"set_credential without credential_id", scenario(`{"set_credential": {"active": false}}`), "needs a credential_id string"},
		{"a reused id", scenario(`{"id": "a", ` + meta[1:] + `, {"id": "a", ` + meta[1:]), `id "a" is empty or already used`},
		{"a query parameter that is a number", scenario(`{"request": {"path": "/v1/meta", "query": {"after": 1}}, "expect": {"status": 200}}`), "must be a string, null, or an array of strings"},
		{"an array parameter holding null", scenario(`{"request": {"path": "/v1/meta", "query": {"after": ["1", null]}}, "expect": {"status": 200}}`), "an array must hold only strings"},
		{"a credential that is a number", scenario(`{"request": {"path": "/v1/meta", "credential": 1}, "expect": {"status": 200}}`), "credential must be a string or null"},
		{"body_lines that is not an array", scenario(`{"request": {"path": "/v1/meta"}, "expect": {"status": 200, "body_lines": {}}}`),
			"body_lines must be an array or a string that is exactly one reference"},
		{"body_lines that is a longer string", scenario(`{"id": "a", "request": {"path": "/v1/meta"}, "expect": {"status": 200, "body_lines": "[${a.lines}]"}}`),
			"body_lines must be an array or a string that is exactly one reference"},
		{"a status that is a string", scenario(`{"request": {"path": "/v1/meta"}, "expect": {"status": "200"}}`),
			"status must be an integer or a string that is exactly one reference"},
		{"a status that is not an integer", scenario(`{"request": {"path": "/v1/meta"}, "expect": {"status": 200.5}}`),
			"status must be an integer or a string that is exactly one reference"},
		{"a status that is null", scenario(`{"request": {"path": "/v1/meta"}, "expect": {"status": null}}`),
			"status must be an integer or a string that is exactly one reference"},
		{"a status referring to a later step", scenario(`{"request": {"path": "/v1/meta"}, "expect": {"status": "${b.status}"}}, {"id": "b", ` + meta[1:]),
			"${b.status} names no earlier request step"},
		{"a malformed reference in status", scenario(`{"request": {"path": "/v1/meta"}, "expect": {"status": "${b}"}}`), "malformed reference ${b}"},
		{"a reference to a later step", scenario(`{"request": {"path": "/v1/exports/${b.export_id}"}, "expect": {"status": 200}}, {"id": "b", ` + meta[1:]),
			"${b.export_id} names no earlier request step"},
		{"a method referring to a later step", scenario(`{"request": {"method": "${b.method}", "path": "/v1/meta"}, "expect": {"status": 200}}, {"id": "b", ` + meta[1:]),
			"${b.method} names no earlier request step"},
		{"a request referring to its own step", scenario(`{"id": "a", "request": {"path": "/v1/exports/${a.export_id}"}, "expect": {"status": 200}}`),
			"${a.export_id} names no earlier request step"},
		{"a malformed reference", scenario(`{"request": {"path": "/v1/meta"}, "expect": {"status": 200, "body": {"x": "${a}"}}}`), "malformed reference ${a}"},
		{"stage 0", `{"scenarios": [{"name": "s", "stages": [0], "steps": [` + meta + `]}]}`, "stages lists 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseFile("test", []byte(tt.text))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestParseFileTimingChecks(t *testing.T) {
	if _, err := ParseFile(TimingFile, []byte(`{"checks": [{"name": "t", "period_start": "2026-08-01", "expected": {"delay": 0}}]}`)); err == nil ||
		!strings.Contains(err.Error(), `expected names "delay", which the check does not compute`) {
		t.Fatalf("got %v, want an error about the member delay", err)
	}
	// Another file's checks are query checks, which have no period_start.
	if _, err := ParseFile("other", []byte(`{"checks": [{"name": "t", "period_start": "2026-08-01", "expected": {}}]}`)); err == nil ||
		!strings.Contains(err.Error(), `unknown field "period_start"`) {
		t.Fatalf("got %v, want period_start refused in a query check", err)
	}
}

// TestExpectMayReferToItsOwnStep checks the reference rule of the
// conformance format: references in expect may name the step itself.
func TestExpectMayReferToItsOwnStep(t *testing.T) {
	_, err := ParseFile("test", []byte(`{"scenarios": [{"name": "s", "steps": [
		{"id": "c", "request": {"method": "POST", "path": "/v1/datasets/core-indicators/exports"},
		 "expect": {"status": 201, "headers": {"Location": "/v1/exports/${c.export_id}"}, "body": {"export_id": "${c.export_id}"}}}
	]}]}`))
	if err != nil {
		t.Fatal(err)
	}
}

// TestExpectMayBeWholeReferences checks that status and body_lines, which
// are not strings, may be strings that are exactly one reference.
func TestExpectMayBeWholeReferences(t *testing.T) {
	_, err := ParseFile("test", []byte(`{"scenarios": [{"name": "s", "steps": [
		{"id": "a", "request": {"path": "/v1/datasets/core-indicators/changes", "query": {"after": "0"}}, "expect": {"status": 200}},
		{"id": "b", "request": {"path": "/v1/exports/exp_1/files/revisions.jsonl"}, "expect": {"status": "${a.status}", "body_lines": "${a.data}"}},
		{"request": {"path": "/v1/meta"}, "expect": {"status": 2.0e2}}
	]}]}`))
	if err != nil {
		t.Fatal(err)
	}
}
