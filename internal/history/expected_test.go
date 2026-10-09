package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	financialdataapi "github.com/nslaughter/financial-data-api"
	"github.com/nslaughter/financial-data-api/internal/expected"
	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// These tests run the checks in expected/ that need no HTTP, reading the
// files with internal/expected and comparing by its matching rule, as the
// conformance runner does. The constants below are the API's defaults,
// which the files rely on without stating.
const (
	// defaultClock is the default CLOCK_START of spec/api.md. A check without
	// a clock member runs after a reset to it.
	defaultClock = "2026-10-01T00:00:00Z"
	// defaultLimit is the default limit of GET
	// /v1/datasets/{dataset_id}/changes in spec/api.md.
	defaultLimit = 100
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

// scenarioFiles are the files whose scenarios include reads of the revision
// history or a change stream, which these tests answer without HTTP: see
// asRead.
var scenarioFiles = []string{
	"change-stream",
	"revision-history",
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

// expectedFile returns expected/<name>.json as internal/expected loads it.
// The file's contract version must be the fixtures'.
func expectedFile(t *testing.T, f *fixtures.Fixtures, name string) *expected.File {
	t.Helper()
	files, err := expected.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Name != name {
			continue
		}
		if file.ContractVersion != f.ContractVersion {
			t.Fatalf("%s: contract_version %q, but the fixtures are %q", name, file.ContractVersion, f.ContractVersion)
		}
		return file
	}
	t.Fatalf("no file expected/%s.json", name)
	return nil
}

func timestamp(t *testing.T, s string) time.Time {
	t.Helper()
	tm, ok := fixtures.ParseTimestamp(s)
	if !ok {
		t.Fatalf("%q is not a timestamp", s)
	}
	return tm
}

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, ok := fixtures.ParseDate(s)
	if !ok {
		t.Fatalf("%q is not a date", s)
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
				t.Fatalf("%s %s is after the clock %s, which the API refuses", name, *value, clock.Format(fixtures.TimestampLayout))
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
			file := expectedFile(t, f, name)
			if len(file.QueryChecks) == 0 {
				t.Fatal("no query checks")
			}
			for _, c := range file.QueryChecks {
				t.Run(c.Name, func(t *testing.T) {
					clock := clockOf(t, c.Clock)
					got := h.Observations(queryOf(t, f, h, c.Query, clock))
					if d := expected.Match("data", c.Expected.Value, asJSON(t, got)); d != nil {
						t.Error(d)
					}
					if !c.ContrastAvailableAsOf.Present {
						return
					}
					// The same query with published_as_of replaced by
					// available_as_of at the same instant. internal/expected
					// refuses a contrast without published_as_of.
					contrast := make(map[string]*string, len(c.Query))
					for k, v := range c.Query {
						contrast[k] = v
					}
					contrast["available_as_of"] = contrast["published_as_of"]
					delete(contrast, "published_as_of")
					got = h.Observations(queryOf(t, f, h, contrast, clock))
					if d := expected.Match("contrast data", c.ContrastAvailableAsOf.Value, asJSON(t, got)); d != nil {
						t.Error(d)
					}
				})
			}
		})
	}
}

func TestPositionChecks(t *testing.T) {
	f, h := load(t)
	file := expectedFile(t, f, "change-stream")
	if len(file.PositionChecks) == 0 {
		t.Fatal("no position checks")
	}
	for _, c := range file.PositionChecks {
		t.Run(c.Name, func(t *testing.T) {
			// The head position with the clock at c.At.
			got := h.Position(expected.StreamDataset, timestamp(t, c.At))
			if d := expected.Match("head_position", c.ExpectedPosition.Value, asJSON(t, got)); d != nil {
				t.Error(d)
			}
		})
	}
}

func TestReadChecks(t *testing.T) {
	f, h := load(t)
	file := expectedFile(t, f, "change-stream")
	if len(file.ReadChecks) == 0 {
		t.Fatal("no read checks")
	}
	for _, c := range file.ReadChecks {
		t.Run(c.Name, func(t *testing.T) {
			// The API reads after and limit as exact integers.
			after, err := c.AfterPosition.Int64()
			if err != nil {
				t.Fatalf("after_position: %v", err)
			}
			limit := defaultLimit
			if c.Limit != nil {
				n, err := c.Limit.Int64()
				if err != nil {
					t.Fatalf("limit: %v", err)
				}
				limit = int(n)
			}
			got, err := h.ReadChanges(expected.StreamDataset, clockOf(t, c.Clock), after, limit)
			if err != nil {
				t.Fatalf("ReadChanges: %v", err)
			}
			if d := expected.Match("data", c.Expected.Value, asJSON(t, got.Events)); d != nil {
				t.Error(d)
			}
			if d := expected.Match("next_position", c.ExpectedNextPosition.Value, asJSON(t, got.NextPosition)); d != nil {
				t.Error(d)
			}
			if d := expected.Match("head_position", c.ExpectedHeadPosition.Value, asJSON(t, got.HeadPosition)); d != nil {
				t.Error(d)
			}
		})
	}
}

// TestScenarios runs the scenarios of scenarioFiles whose every step is a
// read that asRead accepts; the API runner runs the rest. Each runs at its
// clock, and its first failing step ends it, as spec/conformance.md
// describes.
func TestScenarios(t *testing.T) {
	f, h := load(t)
	for _, name := range scenarioFiles {
		t.Run(name, func(t *testing.T) {
			file := expectedFile(t, f, name)
			ran := 0
			for _, s := range file.Scenarios {
				reads, ok := readsOf(s)
				if !ok {
					continue
				}
				ran++
				t.Run(s.Name, func(t *testing.T) {
					clock := clockOf(t, s.Clock)
					for i, r := range reads {
						if d := runRead(t, f, h, clock, r); d != "" {
							t.Fatalf("step %d: %s", i+1, d)
						}
					}
				})
			}
			if ran == 0 {
				t.Fatal("no scenario only reads the revision history or a change stream")
			}
		})
	}
}

// read is a request step these tests answer without HTTP: a GET of the
// revision history or of a dataset's change stream, with the default
// credential and only the parameters that select what is read.
type read struct {
	dataset string             // the change stream's dataset, or "" for the revision history
	params  map[string]*string // a null parameter is not sent
	expect  *expected.Expect
}

// readErrors are the errors ReadChanges returns, by their problem codes in
// spec/api.md.
var readErrors = map[string]error{
	"position_ahead":   ErrPositionAhead,
	"position_expired": ErrPositionExpired,
}

// readsOf returns the steps of a scenario as reads, or false when a step is
// not one asRead accepts.
func readsOf(s expected.Scenario) ([]read, bool) {
	reads := make([]read, len(s.Steps))
	for i, step := range s.Steps {
		r, ok := asRead(step)
		if !ok {
			return nil, false
		}
		reads[i] = r
	}
	return reads, true
}

// asRead returns a step as a read, or false when the API runner must run it:
// another action or endpoint; a method, a credential, or a body; a parameter
// that pages, or that the endpoint refuses; or an expectation of headers, of
// raw bytes, or of an error other than those ReadChanges returns.
func asRead(s expected.Step) (read, bool) {
	if s.SetClock != nil || s.SetCredential != nil || s.Reset != nil || s.Request == nil || s.Expect == nil {
		return read{}, false
	}
	req, exp := s.Request, s.Expect
	if req.Method != nil && *req.Method != "GET" ||
		req.Credential.Present || req.Authorization != nil || req.Body.Present ||
		exp.Headers != nil || exp.BodySHA256 != nil || exp.BodyLines.Present {
		return read{}, false
	}
	r := read{params: make(map[string]*string, len(req.Query)), expect: exp}
	var selecting []string
	if *req.Path == "/v1/revisions" {
		selecting = []string{"series_id", "period_start", "period_end", "available_as_of"}
	} else if dataset, ok := changesDataset(*req.Path); ok {
		r.dataset = dataset
		selecting = []string{"after", "limit"}
	} else {
		return read{}, false
	}
	for name, value := range req.Query {
		if !slices.Contains(selecting, name) {
			return read{}, false
		}
		switch v := value.(type) {
		case nil:
			r.params[name] = nil
		case string:
			r.params[name] = &v
		default:
			return read{}, false
		}
	}
	if exp.Code == nil {
		// The status must be 200, HTTP's OK.
		return r, expected.Match("status", exp.Status.Value, json.Number("200")) == nil
	}
	_, ok := readErrors[*exp.Code]
	return r, ok && r.dataset != ""
}

// changesDataset returns the dataset of a /v1/datasets/{dataset_id}/changes
// path, or false when path is not one.
func changesDataset(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/v1/datasets/")
	if !ok {
		return "", false
	}
	dataset, ok := strings.CutSuffix(rest, "/changes")
	return dataset, ok && dataset != "" && !strings.Contains(dataset, "/")
}

// runRead answers a read at the clock as the API does, and describes the
// first difference from the step's expectation, or returns "" when the
// answer matches. An expected error is compared by its problem code alone:
// its status and its problem's members are the API's.
func runRead(t *testing.T, f *fixtures.Fixtures, h *History, clock time.Time, r read) string {
	t.Helper()
	var body any
	if r.dataset == "" {
		// Revisions returns the whole history, so the response holds it in
		// one page, without a next page.
		q := queryOf(t, f, h, r.params, clock)
		body = map[string]any{
			"data":            h.Revisions(q),
			"position":        q.Position,
			"next_page_token": nil,
		}
	} else {
		v := r.params["after"]
		if v == nil {
			t.Fatal("no after, which the API requires")
		}
		after, err := strconv.ParseInt(*v, 10, 64)
		if err != nil {
			t.Fatalf("after: %v", err)
		}
		limit := defaultLimit
		if v := r.params["limit"]; v != nil {
			if limit, err = strconv.Atoi(*v); err != nil {
				t.Fatalf("limit: %v", err)
			}
		}
		c, err := h.ReadChanges(r.dataset, clock, after, limit)
		if r.expect.Code != nil {
			if want := readErrors[*r.expect.Code]; !errors.Is(err, want) {
				return fmt.Sprintf("error %v, want %v", err, want)
			}
			return ""
		}
		if err != nil {
			return fmt.Sprintf("ReadChanges: %v", err)
		}
		body = map[string]any{
			"data":          c.Events,
			"next_position": c.NextPosition,
			"head_position": c.HeadPosition,
		}
	}
	if !r.expect.Body.Present {
		return ""
	}
	if d := expected.Match("body", r.expect.Body.Value, asJSON(t, body)); d != nil {
		return d.String()
	}
	return ""
}

// asJSON returns v as the runner decodes a response, with json.Number for
// numbers, so that expected.Match compares it as the runner does.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := expected.Decode(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
