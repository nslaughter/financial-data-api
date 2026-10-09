// Package expected owns the format of expected/, which spec/conformance.md
// defines: the file types, loading the files built into the module, their
// well-formedness checks, the matching rule, and the values the
// specification gives for running the checks. It has no HTTP, so the
// conformance runner and the tests of internal/history and internal/exports
// read the files the same way.
package expected

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	financialdataapi "github.com/nslaughter/financial-data-api"
)

// The values spec/conformance.md gives for running the checks.
const (
	// DefaultCredential is the credential a request uses unless its check
	// names another.
	DefaultCredential = "cred_research"
	// StreamDataset is the dataset whose head position and change stream
	// the checks of change-stream.json read.
	StreamDataset = "core-indicators"
	// TimingSeries is the series whose releases release-timing.json checks.
	TimingSeries = "activity-index"
	// PageSize is the page size of pages_with_page_size_10.
	PageSize = 10
)

// TimingFile is the file whose checks are release-timing checks. Every other
// file's checks are query checks.
const TimingFile = "release-timing"

// timingMembers are the values a release-timing check computes, which its
// expected object may name.
var timingMembers = map[string]bool{
	"scheduled_at":                true,
	"first_published_at":          true,
	"first_available_at":          true,
	"first_available_revision_id": true,
	"source_delay_seconds":        true,
	"availability_delay_seconds":  true,
}

// File is a file of expected/. Files are decoded strictly: a member that
// spec/conformance.md does not define is an error, so a runner never skips
// a check it does not understand.
type File struct {
	// Name is the file name without .json, such as "full-history".
	Name string
	// ContractVersion is the file's contract_version.
	ContractVersion string
	QueryChecks     []QueryCheck
	Pages           []ExpectedPage
	PositionChecks  []PositionCheck
	ReadChecks      []ReadCheck
	TimingChecks    []TimingCheck
	Scenarios       []Scenario
}

// rawFile is a file's members as they are written.
type rawFile struct {
	ContractVersion string          `json:"contract_version"`
	Case            string          `json:"case"`
	Description     string          `json:"description"`
	Checks          json.RawMessage `json:"checks"`
	Pages           []ExpectedPage  `json:"pages_with_page_size_10"`
	PositionChecks  []PositionCheck `json:"position_checks"`
	ReadChecks      []ReadCheck     `json:"read_checks"`
	// ApplyChecks are for SDK runners; the API runner does not run them.
	ApplyChecks json.RawMessage `json:"apply_checks"`
	Scenarios   []Scenario      `json:"scenarios"`
}

// QueryCheck is an entry of a file's checks: a query of GET /v1/observations
// and the records it must return.
type QueryCheck struct {
	Name     string             `json:"name"`
	Reason   string             `json:"reason"`
	Clock    *string            `json:"clock"`
	Query    map[string]*string `json:"query"`
	Expected Member             `json:"expected"`
	// ContrastAvailableAsOf, when present, is what the same query returns
	// with available_as_of in place of published_as_of.
	ContrastAvailableAsOf Member `json:"contrast_available_as_of"`
}

// ExpectedPage is an entry of pages_with_page_size_10: one page of the
// file's query checks run with page_size=10.
type ExpectedPage struct {
	First string `json:"first"`
	Last  string `json:"last"`
	Count int    `json:"count"`
}

// PositionCheck is an entry of position_checks.
type PositionCheck struct {
	Name             string `json:"name"`
	Reason           string `json:"reason"`
	At               string `json:"at"`
	ExpectedPosition Member `json:"expected_position"`
}

// ReadCheck is an entry of read_checks.
type ReadCheck struct {
	Name                 string       `json:"name"`
	Reason               string       `json:"reason"`
	Clock                *string      `json:"clock"`
	AfterPosition        json.Number  `json:"after_position"`
	Limit                *json.Number `json:"limit"`
	Expected             Member       `json:"expected"`
	ExpectedNextPosition Member       `json:"expected_next_position"`
	ExpectedHeadPosition Member       `json:"expected_head_position"`
}

// TimingCheck is an entry of the checks of release-timing.json.
type TimingCheck struct {
	Name        string         `json:"name"`
	Reason      string         `json:"reason"`
	PeriodStart string         `json:"period_start"`
	Expected    map[string]any `json:"expected"`
}

// Scenario is a sequence of requests and test actions, run in order.
type Scenario struct {
	Name   string  `json:"name"`
	Reason string  `json:"reason"`
	Clock  *string `json:"clock"`
	// Stages, when present, lists the only stages at which the scenario
	// runs.
	Stages *[]int `json:"stages"`
	Steps  []Step `json:"steps"`
	// ExpectedLocalCopy is for SDK runners; the API runner ignores it.
	ExpectedLocalCopy json.RawMessage `json:"expected_local_copy"`
}

// Step is a scenario step. It has exactly one action: SetClock,
// SetCredential, Reset, or Request.
type Step struct {
	ID            *string        `json:"id"`
	SetClock      *string        `json:"set_clock"`
	SetCredential map[string]any `json:"set_credential"`
	Reset         map[string]any `json:"reset"`
	Request       *Request       `json:"request"`
	Expect        *Expect        `json:"expect"`
}

// Request is a request step's request. Its strings may hold references,
// which are resolved before it is sent.
type Request struct {
	Method        *string        `json:"method"`
	Path          *string        `json:"path"`
	Query         map[string]any `json:"query"`
	Credential    Member         `json:"credential"`
	Authorization *string        `json:"authorization"`
	Body          Member         `json:"body"`
}

// Expect is what a request step's response must meet. Its strings may hold
// references, which are resolved after the response arrives. A string that
// is exactly one reference may stand for a value of any type, so Status and
// BodyLines may be such strings as well as an integer and an array.
type Expect struct {
	Status     Member            `json:"status"`
	Code       *string           `json:"code"`
	Body       Member            `json:"body"`
	Headers    map[string]string `json:"headers"`
	BodySHA256 *string           `json:"body_sha256"`
	BodyLines  Member            `json:"body_lines"`
}

// Member is an object member that may be absent: whether it was present, so
// that an absent member differs from null, and its JSON value as Decode
// decodes it.
type Member struct {
	Present bool
	Value   any
}

func (m *Member) UnmarshalJSON(data []byte) error {
	m.Present = true
	return Decode(data, &m.Value)
}

// Decode decodes the JSON value data into v as Match compares JSON values,
// with json.Number for numbers, refusing object members that v does not
// define and data after the value.
func Decode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("data after the JSON value")
	}
	return nil
}

// Load reads every file of expected/ from financialdataapi.Expected(), in
// name order, and checks that each is a well-formed conformance file.
func Load() ([]*File, error) {
	fsys := financialdataapi.Expected()
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errors.New("no expected files")
	}
	files := make([]*File, 0, len(names))
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		f, err := ParseFile(strings.TrimSuffix(name, ".json"), data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		files = append(files, f)
	}
	return files, nil
}

// ParseFile decodes the file of expected/ named name, without .json, and
// checks that it is well formed.
func ParseFile(name string, data []byte) (*File, error) {
	var raw rawFile
	if err := Decode(data, &raw); err != nil {
		return nil, err
	}
	f := &File{
		Name:            name,
		ContractVersion: raw.ContractVersion,
		Pages:           raw.Pages,
		PositionChecks:  raw.PositionChecks,
		ReadChecks:      raw.ReadChecks,
		Scenarios:       raw.Scenarios,
	}
	if raw.Checks != nil {
		checks := any(&f.QueryChecks)
		if name == TimingFile {
			checks = &f.TimingChecks
		}
		if err := Decode(raw.Checks, checks); err != nil {
			return nil, fmt.Errorf("checks: %w", err)
		}
	}
	if err := f.check(); err != nil {
		return nil, err
	}
	return f, nil
}

// check reports the first member that is missing or malformed.
func (f *File) check() error {
	for _, c := range f.QueryChecks {
		if err := c.check(); err != nil {
			return fmt.Errorf("check %q: %w", c.Name, err)
		}
	}
	if len(f.Pages) > 0 && len(f.QueryChecks) == 0 {
		return errors.New("pages_with_page_size_10 needs query checks to page")
	}
	for i, p := range f.Pages {
		if p.First == "" || p.Last == "" || p.Count < 1 {
			return fmt.Errorf("pages_with_page_size_10[%d]: needs first, last, and a count of at least 1", i)
		}
	}
	for _, c := range f.PositionChecks {
		if c.Name == "" || c.At == "" || !c.ExpectedPosition.Present {
			return fmt.Errorf("position check %q: needs name, at, and expected_position", c.Name)
		}
	}
	for _, c := range f.ReadChecks {
		if c.Name == "" || c.AfterPosition == "" || !c.Expected.Present || !c.ExpectedNextPosition.Present || !c.ExpectedHeadPosition.Present {
			return fmt.Errorf("read check %q: needs name, after_position, expected, expected_next_position, and expected_head_position", c.Name)
		}
	}
	for _, c := range f.TimingChecks {
		if c.Name == "" || c.PeriodStart == "" || len(c.Expected) == 0 {
			return fmt.Errorf("release-timing check %q: needs name, period_start, and expected", c.Name)
		}
		for _, k := range sortedKeys(c.Expected) {
			if !timingMembers[k] {
				return fmt.Errorf("release-timing check %q: expected names %q, which the check does not compute", c.Name, k)
			}
		}
	}
	for _, s := range f.Scenarios {
		if err := s.check(); err != nil {
			return fmt.Errorf("scenario %q: %w", s.Name, err)
		}
	}
	return nil
}

func (c *QueryCheck) check() error {
	if c.Name == "" || c.Query == nil || !c.Expected.Present {
		return errors.New("needs name, query, and expected")
	}
	if c.ContrastAvailableAsOf.Present && c.Query["published_as_of"] == nil {
		return errors.New("contrast_available_as_of needs a query with published_as_of")
	}
	return nil
}

func (s *Scenario) check() error {
	if s.Name == "" || len(s.Steps) == 0 {
		return errors.New("needs name and steps")
	}
	if s.Stages != nil {
		for _, stage := range *s.Stages {
			if stage < 1 {
				return fmt.Errorf("stages lists %d, which is not a stage", stage)
			}
		}
	}
	ids := map[string]bool{}
	for i := range s.Steps {
		if err := s.Steps[i].check(ids); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	return nil
}

// check reports a malformed step. ids holds the ids of the earlier request
// steps, which the step's references may name; check adds the step's own.
func (s *Step) check(ids map[string]bool) error {
	actions := 0
	for _, set := range []bool{s.SetClock != nil, s.SetCredential != nil, s.Reset != nil, s.Request != nil} {
		if set {
			actions++
		}
	}
	if actions != 1 {
		return fmt.Errorf("has %d actions; a step has exactly one of set_clock, set_credential, reset, and request", actions)
	}
	if s.Request == nil {
		if s.ID != nil || s.Expect != nil {
			return errors.New("only a request step may have an id or an expect")
		}
		if s.SetCredential != nil {
			if _, ok := s.SetCredential["credential_id"].(string); !ok {
				return errors.New("set_credential needs a credential_id string")
			}
		}
		return nil
	}
	if s.Expect == nil {
		return errors.New("a request step needs an expect")
	}
	if err := s.Request.check(); err != nil {
		return fmt.Errorf("request: %w", err)
	}
	if err := checkReferences(s.Request.strings(), ids); err != nil {
		return fmt.Errorf("request: %w", err)
	}
	if s.ID != nil {
		if *s.ID == "" || ids[*s.ID] {
			return fmt.Errorf("id %q is empty or already used", *s.ID)
		}
		ids[*s.ID] = true
	}
	// References in expect may also name the step itself. They are checked
	// first, so a malformed one in status or body_lines is reported as such.
	if err := checkReferences(s.Expect.strings(), ids); err != nil {
		return fmt.Errorf("expect: %w", err)
	}
	if err := s.Expect.check(); err != nil {
		return fmt.Errorf("expect: %w", err)
	}
	return nil
}

func (r *Request) check() error {
	if r.Path == nil {
		return errors.New("needs a path")
	}
	if r.Method != nil && *r.Method == "" {
		return errors.New("method is empty")
	}
	for _, k := range sortedKeys(r.Query) {
		switch v := r.Query[k].(type) {
		case string, nil:
		case []any:
			for _, e := range v {
				if _, ok := e.(string); !ok {
					return fmt.Errorf("query parameter %s: an array must hold only strings", k)
				}
			}
		default:
			return fmt.Errorf("query parameter %s: must be a string, null, or an array of strings", k)
		}
	}
	if r.Credential.Present {
		switch r.Credential.Value.(type) {
		case string, nil:
		default:
			return errors.New("credential must be a string or null")
		}
	}
	return nil
}

func (e *Expect) check() error {
	if !e.Status.Present {
		return errors.New("needs a status")
	}
	n, isNumber := e.Status.Value.(json.Number)
	if _, isInt := Integer(n); !(isNumber && isInt) && !wholeReference(e.Status.Value) {
		return errors.New("status must be an integer or a string that is exactly one reference")
	}
	if e.BodyLines.Present {
		if _, ok := e.BodyLines.Value.([]any); !ok && !wholeReference(e.BodyLines.Value) {
			return errors.New("body_lines must be an array or a string that is exactly one reference")
		}
	}
	return nil
}

// strings returns every string in the request that may hold a reference.
func (r *Request) strings() []string {
	ss := []string{*r.Path}
	if r.Method != nil {
		ss = append(ss, *r.Method)
	}
	for _, k := range sortedKeys(r.Query) {
		ss = appendStrings(ss, r.Query[k])
	}
	ss = appendStrings(ss, r.Credential.Value)
	if r.Authorization != nil {
		ss = append(ss, *r.Authorization)
	}
	return appendStrings(ss, r.Body.Value)
}

// strings returns every string in the expectation that may hold a reference.
func (e *Expect) strings() []string {
	ss := appendStrings(nil, e.Status.Value)
	if e.Code != nil {
		ss = append(ss, *e.Code)
	}
	ss = appendStrings(ss, e.Body.Value)
	for _, k := range sortedKeys(e.Headers) {
		ss = append(ss, e.Headers[k])
	}
	if e.BodySHA256 != nil {
		ss = append(ss, *e.BodySHA256)
	}
	return appendStrings(ss, e.BodyLines.Value)
}

// appendStrings appends the strings in v, a JSON value, to ss.
func appendStrings(ss []string, v any) []string {
	switch v := v.(type) {
	case string:
		ss = append(ss, v)
	case []any:
		for _, e := range v {
			ss = appendStrings(ss, e)
		}
	case map[string]any:
		for _, k := range sortedKeys(v) {
			ss = appendStrings(ss, v[k])
		}
	}
	return ss
}

// checkReferences reports a malformed reference in ss, or one that names a
// step not in ids.
func checkReferences(ss []string, ids map[string]bool) error {
	for _, s := range ss {
		refs, err := FindReferences(s)
		if err != nil {
			return err
		}
		for _, r := range refs {
			if !ids[r.ID] {
				return fmt.Errorf("%s names no earlier request step", r)
			}
		}
	}
	return nil
}
