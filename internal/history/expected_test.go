package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	financialdataapi "github.com/nslaughter/financial-data-api"
	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// These tests run the checks in expected/ that need no HTTP, reading the
// files as spec/conformance.md describes. The constants below are the
// specifications' defaults, which the files rely on without stating.
const (
	// defaultClock is the default CLOCK_START of spec/api.md. A check without
	// a clock member runs after a reset to it.
	defaultClock = "2026-10-01T00:00:00Z"
	// defaultLimit is the default limit of GET
	// /v1/datasets/{dataset_id}/changes in spec/api.md.
	defaultLimit = 100
	// streamDataset is the dataset whose change stream spec/conformance.md
	// reads for the checks of change-stream.json.
	streamDataset = "core-indicators"
)

// queryFiles are the files whose query checks spec/conformance.md lists.
var queryFiles = []string{
	"august-2026-at-cutoffs",
	"full-history",
	"missing-value",
	"withdrawal-and-rerelease",
	"out-of-order-arrival",
	"provider-correction",
	"late-source-release",
	"published-as-of",
}

// expectedFile is a file in expected/. Members these tests do not run, which
// need HTTP, a runner's paging, or an SDK, are kept raw.
type expectedFile struct {
	ContractVersion     string          `json:"contract_version"`
	Case                string          `json:"case"`
	Description         string          `json:"description"`
	Checks              []queryCheck    `json:"checks"`
	PositionChecks      []positionCheck `json:"position_checks"`
	ReadChecks          []readCheck     `json:"read_checks"`
	PagesWithPageSize10 json.RawMessage `json:"pages_with_page_size_10"`
	ApplyChecks         json.RawMessage `json:"apply_checks"`
	Scenarios           json.RawMessage `json:"scenarios"`
}

type queryCheck struct {
	Name                  string             `json:"name"`
	Clock                 *string            `json:"clock"`
	Query                 map[string]*string `json:"query"`
	Expected              any                `json:"expected"`
	ContrastAvailableAsOf any                `json:"contrast_available_as_of"`
	Reason                string             `json:"reason"`
}

type positionCheck struct {
	Name             string `json:"name"`
	At               string `json:"at"`
	ExpectedPosition int64  `json:"expected_position"`
	Reason           string `json:"reason"`
}

type readCheck struct {
	Name                 string  `json:"name"`
	Clock                *string `json:"clock"`
	AfterPosition        int64   `json:"after_position"`
	Limit                *int    `json:"limit"`
	Expected             any     `json:"expected"`
	ExpectedNextPosition int64   `json:"expected_next_position"`
	ExpectedHeadPosition int64   `json:"expected_head_position"`
	Reason               string  `json:"reason"`
}

// load returns the embedded fixtures and their history.
func load(t *testing.T) (*fixtures.Fixtures, *History) {
	t.Helper()
	f, err := fixtures.Load(financialdataapi.Fixtures())
	if err != nil {
		t.Fatalf("load the fixtures: %v", err)
	}
	h, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return f, h
}

// readExpected decodes expected/<name>.json into v. A member v does not
// define fails the test, so that a check these tests do not understand is
// never passed unread. The file's contract version must be the fixtures'.
func readExpected(t *testing.T, f *fixtures.Fixtures, name string, v *expectedFile) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "expected", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if v.ContractVersion != f.ContractVersion {
		t.Fatalf("%s: contract_version %q, but the fixtures are %q", name, v.ContractVersion, f.ContractVersion)
	}
}

func timestamp(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(timestampLayout, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(dateLayout, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// clockOf returns a check's clock: its clock member, or the default.
func clockOf(t *testing.T, clock *string) time.Time {
	t.Helper()
	if clock == nil {
		return timestamp(t, defaultClock)
	}
	return timestamp(t, *clock)
}

// datasetOf returns the dataset of a fixture series.
func datasetOf(t *testing.T, f *fixtures.Fixtures, seriesID string) string {
	t.Helper()
	for _, s := range f.Series {
		if s.SeriesID == seriesID {
			return s.DatasetID
		}
	}
	t.Fatalf("no series %q in the fixtures", seriesID)
	return ""
}

// queryOf converts the parameters of a GET /v1/observations request into the
// Query the API evaluates for its first page at the clock. A null parameter
// is not sent.
func queryOf(t *testing.T, f *fixtures.Fixtures, h *History, params map[string]*string, clock time.Time) Query {
	t.Helper()
	var q Query
	for _, name := range sortedKeys(params) {
		value := params[name]
		if value == nil {
			continue
		}
		switch name {
		case "series_id":
			q.SeriesID = *value
		case "period_start":
			d := date(t, *value)
			q.PeriodStart = &d
		case "period_end":
			d := date(t, *value)
			q.PeriodEnd = &d
		case "available_as_of", "published_as_of":
			if q.Cutoff != nil {
				t.Fatal("the query has both cutoffs, which the API refuses")
			}
			kind := AvailableAsOf
			if name == "published_as_of" {
				kind = PublishedAsOf
			}
			at := timestamp(t, *value)
			if at.After(clock) {
				t.Fatalf("%s %s is after the clock %s, which the API refuses", name, *value, clock.Format(timestampLayout))
			}
			q.Cutoff = &Cutoff{Kind: kind, At: at}
		default:
			t.Fatalf("query parameter %q is not one these tests run", name)
		}
	}
	q.Position = h.Position(datasetOf(t, f, q.SeriesID), clock)
	return q
}

func TestQueryChecks(t *testing.T) {
	f, h := load(t)
	for _, name := range queryFiles {
		t.Run(name, func(t *testing.T) {
			var file expectedFile
			readExpected(t, f, name, &file)
			if len(file.Checks) == 0 {
				t.Fatal("no query checks")
			}
			for _, c := range file.Checks {
				t.Run(c.Name, func(t *testing.T) {
					clock := clockOf(t, c.Clock)
					got := h.Observations(queryOf(t, f, h, c.Query, clock))
					if d := match("data", c.Expected, asJSON(t, got)); d != "" {
						t.Error(d)
					}
					if c.ContrastAvailableAsOf == nil {
						return
					}
					// The same query with published_as_of replaced by
					// available_as_of at the same instant.
					contrast := make(map[string]*string, len(c.Query))
					for k, v := range c.Query {
						contrast[k] = v
					}
					if contrast["published_as_of"] == nil {
						t.Fatal("contrast_available_as_of without published_as_of")
					}
					contrast["available_as_of"] = contrast["published_as_of"]
					delete(contrast, "published_as_of")
					got = h.Observations(queryOf(t, f, h, contrast, clock))
					if d := match("contrast data", c.ContrastAvailableAsOf, asJSON(t, got)); d != "" {
						t.Error(d)
					}
				})
			}
		})
	}
}

func TestPositionChecks(t *testing.T) {
	f, h := load(t)
	var file expectedFile
	readExpected(t, f, "change-stream", &file)
	if len(file.PositionChecks) == 0 {
		t.Fatal("no position checks")
	}
	for _, c := range file.PositionChecks {
		t.Run(c.Name, func(t *testing.T) {
			// The head position with the clock at c.At.
			if got := h.Position(streamDataset, timestamp(t, c.At)); got != c.ExpectedPosition {
				t.Errorf("head_position %d, want %d", got, c.ExpectedPosition)
			}
		})
	}
}

func TestReadChecks(t *testing.T) {
	f, h := load(t)
	var file expectedFile
	readExpected(t, f, "change-stream", &file)
	if len(file.ReadChecks) == 0 {
		t.Fatal("no read checks")
	}
	for _, c := range file.ReadChecks {
		t.Run(c.Name, func(t *testing.T) {
			limit := defaultLimit
			if c.Limit != nil {
				limit = *c.Limit
			}
			got, err := h.ReadChanges(streamDataset, clockOf(t, c.Clock), c.AfterPosition, limit)
			if err != nil {
				t.Fatalf("ReadChanges: %v", err)
			}
			if d := match("data", c.Expected, asJSON(t, got.Events)); d != "" {
				t.Error(d)
			}
			if got.NextPosition != c.ExpectedNextPosition {
				t.Errorf("next_position %d, want %d", got.NextPosition, c.ExpectedNextPosition)
			}
			if got.HeadPosition != c.ExpectedHeadPosition {
				t.Errorf("head_position %d, want %d", got.HeadPosition, c.ExpectedHeadPosition)
			}
		})
	}
}

// asJSON returns v as a JSON value would decode: maps, slices, strings,
// float64 numbers, booleans, and nil.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// match compares an expected value with an actual JSON value by the matching
// rule of spec/conformance.md, and describes the first difference, or returns
// "" when they match. An expected object matches an object with every member
// it names, each matching; an expected array matches an array of the same
// length whose elements match in order; any other value matches an equal
// value of the same JSON type.
func match(path string, expected, actual any) string {
	switch e := expected.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: expected an object, got %s", path, show(actual))
		}
		for _, k := range sortedKeys(e) {
			v, ok := a[k]
			if !ok {
				return fmt.Sprintf("%s.%s: expected %s, got no member", path, k, show(e[k]))
			}
			if d := match(path+"."+k, e[k], v); d != "" {
				return d
			}
		}
		return ""
	case []any:
		a, ok := actual.([]any)
		if !ok {
			return fmt.Sprintf("%s: expected an array, got %s", path, show(actual))
		}
		for i := range min(len(e), len(a)) {
			if d := match(fmt.Sprintf("%s[%d]", path, i), e[i], a[i]); d != "" {
				return d
			}
		}
		if len(e) != len(a) {
			return fmt.Sprintf("%s: expected %d elements, got %d: %s", path, len(e), len(a), show(actual))
		}
		return ""
	default:
		// A string, float64, bool, or nil: the interfaces are equal only when
		// both the JSON type and the value are.
		if expected != actual {
			return fmt.Sprintf("%s: expected %s, got %s", path, show(expected), show(actual))
		}
		return ""
	}
}

func show(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
