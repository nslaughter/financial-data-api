// Package conformance is the API runner of spec/conformance.md. It runs the
// checks of expected/ over HTTP against a server started with
// TEST_CONTROL=enabled and the default CLOCK_START, compares results by the
// matching rule, and validates every response against spec/openapi.yaml,
// which catches what the matching rule does not compare, such as a field
// that is omitted instead of null.
package conformance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

const (
	// defaultCredential is the credential a request uses unless it names
	// another.
	defaultCredential = "cred_research"
	// testControlKind is the kind of the credential that test actions use.
	testControlKind = "test_control"
	// streamDataset is the dataset whose head position and change stream
	// the checks of change-stream.json read.
	streamDataset = "core-indicators"
	// timingSeries is the series whose releases release-timing.json checks.
	timingSeries = "activity-index"
	// pageSize is the page size of pages_with_page_size_10.
	pageSize = "10"
	// maxPages is the most pages the runner follows for one query, so a
	// server whose tokens never end cannot hold it forever.
	maxPages = 1000
	// timestampLayout is the timestamp format of spec/api.md.
	timestampLayout = "2006-01-02T15:04:05Z"
	// defaultTimeout is how long a request may take when Config sets no
	// timeout.
	defaultTimeout = 30 * time.Second
)

// Config is what a Runner needs.
type Config struct {
	// BaseURL is the server's URL, such as http://localhost:8080.
	BaseURL string
	// Stage is the stage the server serves, which selects the scenarios
	// that run.
	Stage int
	// Credentials are the fixture credentials, whose keys requests send.
	Credentials []fixtures.Credential
	// OpenAPI is the document every response is validated against.
	OpenAPI *Document
	// Timeout limits each request; zero means 30 seconds.
	Timeout time.Duration
}

// Runner runs checks against one server, one at a time.
type Runner struct {
	baseURL string
	stage   int
	client  *http.Client
	doc     *Document
	// keys holds each credential's api_key by credential_id, and testKey
	// the test-control credential's.
	keys    map[string]string
	testKey string
	// Progress, if set, receives each result as soon as its check ends.
	Progress func(Result)
}

// New returns a runner for the server that cfg describes.
func New(cfg Config) (*Runner, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("base URL %q: want http:// or https://, a host, and no query", cfg.BaseURL)
	}
	if cfg.Stage < 1 || cfg.Stage > LastStage {
		return nil, fmt.Errorf("stage %d: the API's stages are 1 to %d", cfg.Stage, LastStage)
	}
	if cfg.OpenAPI == nil {
		return nil, errors.New("no OpenAPI document")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	r := &Runner{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		stage:   cfg.Stage,
		client:  newClient(timeout),
		doc:     cfg.OpenAPI,
		keys:    map[string]string{},
	}
	for _, c := range cfg.Credentials {
		r.keys[c.CredentialID] = c.APIKey
		if c.Kind == testControlKind {
			if r.testKey != "" {
				return nil, errors.New("more than one test-control credential")
			}
			r.testKey = c.APIKey
		}
	}
	if r.testKey == "" {
		return nil, errors.New("no test-control credential")
	}
	if _, ok := r.keys[defaultCredential]; !ok {
		return nil, fmt.Errorf("no credential %s, which requests use by default", defaultCredential)
	}
	return r, nil
}

// Run runs the checks of files whose names contain filter, in the order of
// files and of each file's members, and returns their results. Each check
// starts with a reset, so the checks are independent.
func (r *Runner) Run(ctx context.Context, files []*File, filter string) []Result {
	var results []Result
	report := func(res Result) {
		results = append(results, res)
		if r.Progress != nil {
			r.Progress(res)
		}
	}
	run := func(f *File, kind, name string, check func(*checkRun) *Failure) {
		if strings.Contains(name, filter) && ctx.Err() == nil {
			report(Result{File: f.Name, Kind: kind, Name: name, Failure: check(&checkRun{Runner: r, ctx: ctx})})
		}
	}
	for _, f := range files {
		for i := range f.QueryChecks {
			c := &f.QueryChecks[i]
			run(f, "query check", c.Name, func(x *checkRun) *Failure { return x.queryCheck(c) })
		}
		if len(f.Pages) > 0 {
			for i := range f.QueryChecks {
				c := &f.QueryChecks[i]
				run(f, "page check", c.Name, func(x *checkRun) *Failure { return x.pageCheck(c, f.Pages) })
			}
		}
		for i := range f.PositionChecks {
			c := &f.PositionChecks[i]
			run(f, "position check", c.Name, func(x *checkRun) *Failure { return x.positionCheck(c) })
		}
		for i := range f.ReadChecks {
			c := &f.ReadChecks[i]
			run(f, "read check", c.Name, func(x *checkRun) *Failure { return x.readCheck(c) })
		}
		for i := range f.TimingChecks {
			c := &f.TimingChecks[i]
			run(f, "release-timing check", c.Name, func(x *checkRun) *Failure { return x.timingCheck(c) })
		}
		for i := range f.Scenarios {
			s := &f.Scenarios[i]
			if !s.runsAt(r.stage) {
				if strings.Contains(s.Name, filter) && ctx.Err() == nil {
					report(Result{File: f.Name, Kind: "scenario", Name: s.Name, Skipped: true})
				}
				continue
			}
			run(f, "scenario", s.Name, func(x *checkRun) *Failure { return x.scenario(s) })
		}
	}
	return results
}

// checkRun is one check as it runs.
type checkRun struct {
	*Runner
	ctx context.Context
	// step is the scenario step running, from 1, or 0 before the steps.
	step int
}

func (x *checkRun) fail(c call, at, expected, actual string) *Failure {
	return &Failure{Step: x.step, Request: c.String(), At: at, Expected: expected, Actual: actual}
}

func (x *checkRun) differs(c call, d *difference) *Failure {
	return x.fail(c, d.at, d.expected, d.actual)
}

// customer returns a request with the default credential.
func (x *checkRun) customer(path string, query url.Values) call {
	return call{method: http.MethodGet, path: path, query: query, authorization: bearer(x.keys[defaultCredential])}
}

func bearer(key string) *string {
	header := "Bearer " + key
	return &header
}

// send sends c.
func (x *checkRun) send(c call) (*exchange, *Failure) {
	e, err := do(x.ctx, x.client, x.baseURL, c)
	if err != nil {
		return nil, x.fail(c, "response", "a response", err.Error())
	}
	return e, nil
}

// validate checks e's response against the OpenAPI document.
func (x *checkRun) validate(e *exchange) *Failure {
	if err := x.doc.Validate(e.method, e.path, e.status, e.header, e.body); err != nil {
		return x.fail(e.call, "OpenAPI", "a response that spec/openapi.yaml defines", err.Error())
	}
	return nil
}

// get sends c, which must succeed with 200 and a response the OpenAPI
// document defines, and returns its parsed body.
func (x *checkRun) get(c call) (map[string]any, *Failure) {
	e, f := x.send(c)
	if f != nil {
		return nil, f
	}
	if e.status != http.StatusOK {
		return nil, x.fail(c, "status", "200", e.statusLine())
	}
	if f := x.validate(e); f != nil {
		return nil, f
	}
	var body any
	if err := decodeJSON(e.body, &body); err != nil {
		return nil, x.fail(c, "body", "JSON", err.Error())
	}
	obj, ok := body.(map[string]any)
	if !ok {
		return nil, x.fail(c, "body", "an object", render(body))
	}
	return obj, nil
}

// testAction sends a test-control request, which must succeed with 200.
func (x *checkRun) testAction(method, path string, body map[string]any) *Failure {
	data, err := json.Marshal(body)
	c := call{method: method, path: path, authorization: bearer(x.testKey), body: data}
	if err != nil {
		return x.fail(c, "body", "JSON", err.Error())
	}
	e, f := x.send(c)
	if f != nil {
		return f
	}
	if e.status != http.StatusOK {
		return x.fail(c, "status", "200", e.statusLine())
	}
	return x.validate(e)
}

// reset resets the server, with the clock at clock when it is not nil.
func (x *checkRun) reset(clock *string) *Failure {
	body := map[string]any{}
	if clock != nil {
		body["clock"] = *clock
	}
	return x.testAction(http.MethodPost, "/test/reset", body)
}

// pages follows a paged query from its first page until next_page_token is
// null, and returns each page's data.
func (x *checkRun) pages(path string, query url.Values) ([][]any, *Failure) {
	var pages [][]any
	q := query
	for {
		c := x.customer(path, q)
		if len(pages) == maxPages {
			return nil, x.fail(c, "body.next_page_token", fmt.Sprintf("null within %d pages", maxPages), "a token on every page")
		}
		body, f := x.get(c)
		if f != nil {
			return nil, f
		}
		data, ok := body["data"].([]any)
		if !ok {
			return nil, x.fail(c, "body.data", "an array", render(body["data"]))
		}
		pages = append(pages, data)
		switch token := body["next_page_token"].(type) {
		case nil:
			return pages, nil
		case string:
			q = clone(query)
			q.Set("page_token", token)
		default:
			return nil, x.fail(c, "body.next_page_token", "a string or null", render(token))
		}
	}
}

// compareAll queries GET /v1/observations, concatenates every page's data,
// and matches it against expected.
func (x *checkRun) compareAll(query url.Values, expected any) *Failure {
	const path = "/v1/observations"
	pages, f := x.pages(path, query)
	if f != nil {
		return f
	}
	data := []any{}
	for _, p := range pages {
		data = append(data, p...)
	}
	if d := match("data", expected, data); d != nil {
		f := x.differs(x.customer(path, query), d)
		f.Request += ", every page"
		return f
	}
	return nil
}

func (x *checkRun) queryCheck(c *QueryCheck) *Failure {
	if f := x.reset(c.Clock); f != nil {
		return f
	}
	query := c.query()
	if f := x.compareAll(query, c.Expected.v); f != nil {
		return f
	}
	if !c.ContrastAvailableAsOf.set {
		return nil
	}
	contrast := clone(query)
	contrast.Del("published_as_of")
	contrast.Set("available_as_of", *c.Query["published_as_of"])
	return x.compareAll(contrast, c.ContrastAvailableAsOf.v)
}

// query returns the check's query parameters, omitting null members.
func (c *QueryCheck) query() url.Values {
	q := url.Values{}
	for k, v := range c.Query {
		if v != nil {
			q.Set(k, *v)
		}
	}
	return q
}

// pageCheck runs a query check with page_size=10 and compares its pages with
// pages_with_page_size_10.
func (x *checkRun) pageCheck(c *QueryCheck, want []ExpectedPage) *Failure {
	if f := x.reset(c.Clock); f != nil {
		return f
	}
	const path = "/v1/observations"
	query := c.query()
	query.Set("page_size", pageSize)
	pages, f := x.pages(path, query)
	if f != nil {
		return f
	}
	first := x.customer(path, query)
	for i := range min(len(pages), len(want)) {
		page, w := pages[i], want[i]
		at := fmt.Sprintf("page %d", i+1)
		if len(page) != w.Count {
			return x.fail(first, at, fmt.Sprintf("%d records", w.Count), fmt.Sprintf("%d records", len(page)))
		}
		if d := match(at+", first record", map[string]any{"observation_id": w.First}, page[0]); d != nil {
			return x.differs(first, d)
		}
		if d := match(at+", last record", map[string]any{"observation_id": w.Last}, page[len(page)-1]); d != nil {
			return x.differs(first, d)
		}
	}
	if len(pages) != len(want) {
		return x.fail(first, "pages", fmt.Sprintf("%d pages", len(want)), fmt.Sprintf("%d pages", len(pages)))
	}
	return nil
}

func (x *checkRun) positionCheck(c *PositionCheck) *Failure {
	if f := x.reset(&c.At); f != nil {
		return f
	}
	req := x.customer("/v1/datasets/"+streamDataset, nil)
	body, f := x.get(req)
	if f != nil {
		return f
	}
	if d := match("body", map[string]any{"head_position": c.ExpectedPosition.v}, body); d != nil {
		return x.differs(req, d)
	}
	return nil
}

func (x *checkRun) readCheck(c *ReadCheck) *Failure {
	if f := x.reset(c.Clock); f != nil {
		return f
	}
	query := url.Values{"after": {c.AfterPosition.String()}}
	if c.Limit != nil {
		query.Set("limit", c.Limit.String())
	}
	req := x.customer("/v1/datasets/"+streamDataset+"/changes", query)
	body, f := x.get(req)
	if f != nil {
		return f
	}
	expected := map[string]any{
		"data":          c.Expected.v,
		"next_position": c.ExpectedNextPosition.v,
		"head_position": c.ExpectedHeadPosition.v,
	}
	if d := match("body", expected, body); d != nil {
		return x.differs(req, d)
	}
	return nil
}

// timingCheck computes a release's timing from the release calendar and the
// revision history, at the default clock.
func (x *checkRun) timingCheck(c *TimingCheck) *Failure {
	if f := x.reset(nil); f != nil {
		return f
	}
	calendar := x.customer("/v1/release-calendar", url.Values{"series_id": {timingSeries}, "period_start": {c.PeriodStart}})
	body, f := x.get(calendar)
	if f != nil {
		return f
	}
	data, _ := body["data"].([]any)
	var entries []map[string]any
	for _, e := range data {
		if entry, ok := e.(map[string]any); ok && entry["period_start"] == c.PeriodStart {
			entries = append(entries, entry)
		}
	}
	if len(entries) != 1 {
		return x.fail(calendar, "body.data", "one entry whose period_start is "+c.PeriodStart, render(data))
	}
	scheduledAt, ok1 := entries[0]["scheduled_at"].(string)
	periodEnd, ok2 := entries[0]["period_end"].(string)
	if !ok1 || !ok2 {
		return x.fail(calendar, "body.data", "an entry with scheduled_at and period_end", render(entries[0]))
	}
	query := url.Values{"series_id": {timingSeries}, "period_start": {c.PeriodStart}, "period_end": {periodEnd}}
	pages, f := x.pages("/v1/revisions", query)
	if f != nil {
		return f
	}
	revisions := x.customer("/v1/revisions", query)
	var records []any
	for _, p := range pages {
		records = append(records, p...)
	}
	computed, err := timing(scheduledAt, records)
	if err != nil {
		return x.fail(revisions, "body.data", "revisions to time the release by", err.Error())
	}
	if d := match("", c.Expected, computed); d != nil {
		f := x.differs(revisions, d)
		f.Request += ", every page"
		return f
	}
	return nil
}

// timing computes the values a release-timing check compares, from the
// release's scheduled_at and its revisions.
func timing(scheduledAt string, revisions []any) (map[string]any, error) {
	scheduled, err := time.Parse(timestampLayout, scheduledAt)
	if err != nil {
		return nil, fmt.Errorf("scheduled_at: %w", err)
	}
	if len(revisions) == 0 {
		return nil, errors.New("the release has no revisions")
	}
	type instant struct {
		text string
		t    time.Time
	}
	var published, available instant
	var revisionID string
	var sequence *big.Int
	for i, v := range revisions {
		rec, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("revision %d is not an object", i)
		}
		pubText, _ := rec["published_at"].(string)
		availText, _ := rec["available_at"].(string)
		id, _ := rec["revision_id"].(string)
		seqText, _ := rec["sequence"].(json.Number)
		pub, err1 := time.Parse(timestampLayout, pubText)
		avail, err2 := time.Parse(timestampLayout, availText)
		// The sequence is compared by value, however it is written.
		seq, isInt := integer(seqText)
		if err := errors.Join(err1, err2); err != nil || !isInt || id == "" {
			return nil, fmt.Errorf("revision %d needs an integer sequence, revision_id, published_at, and available_at: %s", i, render(rec))
		}
		if i == 0 || pub.Before(published.t) {
			published = instant{pubText, pub}
		}
		if i == 0 || avail.Before(available.t) || (avail.Equal(available.t) && seq.Cmp(sequence) < 0) {
			available = instant{availText, avail}
			revisionID, sequence = id, seq
		}
	}
	seconds := func(d time.Duration) json.Number {
		return json.Number(strconv.FormatInt(int64(d/time.Second), 10))
	}
	return map[string]any{
		"scheduled_at":                scheduledAt,
		"first_published_at":          published.text,
		"first_available_at":          available.text,
		"first_available_revision_id": revisionID,
		"source_delay_seconds":        seconds(published.t.Sub(scheduled)),
		"availability_delay_seconds":  seconds(available.t.Sub(published.t)),
	}, nil
}

func (x *checkRun) scenario(s *Scenario) *Failure {
	if f := x.reset(s.Clock); f != nil {
		return f
	}
	b := bodies{}
	for i := range s.Steps {
		x.step = i + 1
		if f := x.runStep(&s.Steps[i], b); f != nil {
			return f
		}
	}
	return nil
}

func (x *checkRun) runStep(st *Step, b bodies) *Failure {
	switch {
	case st.SetClock != nil:
		return x.testAction(http.MethodPut, "/test/clock", map[string]any{"now": *st.SetClock})
	case st.SetCredential != nil:
		body := map[string]any{}
		for k, v := range st.SetCredential {
			if k != "credential_id" {
				body[k] = v
			}
		}
		id := st.SetCredential["credential_id"].(string)
		return x.testAction(http.MethodPut, "/test/credentials/"+url.PathEscape(id), body)
	case st.Reset != nil:
		return x.testAction(http.MethodPost, "/test/reset", st.Reset)
	default:
		return x.requestStep(st, b)
	}
}

// requestStep sends a request step's request and checks the response
// against its expect and the OpenAPI document.
func (x *checkRun) requestStep(st *Step, b bodies) *Failure {
	c, err := x.build(st.Request, b)
	if err != nil {
		return &Failure{Step: x.step, Request: c.method + " " + *st.Request.Path, At: "references", Expected: "every reference resolved", Actual: err.Error()}
	}
	e, f := x.send(c)
	if f != nil {
		return f
	}
	if st.ID != nil {
		var body any
		err := decodeJSON(e.body, &body)
		b[*st.ID] = parsedBody{value: body, err: err}
	}
	exp, err := resolveExpect(st.Expect, b)
	if err != nil {
		return x.fail(c, "references", "every reference resolved", err.Error())
	}
	if f := x.meets(e, exp); f != nil {
		return f
	}
	return x.validate(e)
}

// build resolves a request's references and returns the request to send.
func (x *checkRun) build(req *Request, b bodies) (call, error) {
	c := call{method: http.MethodGet, query: url.Values{}}
	if req.Method != nil {
		// A report of a reference that fails shows the method as written.
		c.method = *req.Method
		method, err := b.resolveText("method", *req.Method)
		if err != nil {
			return c, err
		}
		if method == "" {
			return c, fmt.Errorf("method %q is empty after resolving references", *req.Method)
		}
		c.method = method
	}
	path, err := b.resolveText("path", *req.Path)
	if err != nil {
		return c, err
	}
	if !strings.HasPrefix(path, "/") {
		return c, fmt.Errorf("path %q does not start with /", path)
	}
	c.path = path
	for _, k := range sortedKeys(req.Query) {
		var values []any
		switch v := req.Query[k].(type) {
		case string:
			values = []any{v}
		case []any:
			values = v
		}
		for _, v := range values {
			r, err := b.resolve(v)
			if err != nil {
				return c, err
			}
			switch r := r.(type) {
			case nil:
			case string:
				c.query.Add(k, r)
			case json.Number:
				// A number is sent in decimal, however the body wrote it.
				text, err := decimal(r)
				if err != nil {
					return c, fmt.Errorf("query parameter %s: %w", k, err)
				}
				c.query.Add(k, text)
			default:
				return c, fmt.Errorf("query parameter %s is %s after resolving references; it must be a string, a number, or null", k, jsonType(r))
			}
		}
	}
	switch {
	case req.Authorization != nil:
		auth, err := b.resolveText("authorization", *req.Authorization)
		if err != nil {
			return c, err
		}
		c.authorization = &auth
	case !req.Credential.set:
		c.authorization = bearer(x.keys[defaultCredential])
	case req.Credential.v == nil:
	default:
		id, err := b.resolveText("credential", req.Credential.v.(string))
		if err != nil {
			return c, err
		}
		key, ok := x.keys[id]
		if !ok {
			return c, fmt.Errorf("credential %s is not in fixtures/credentials.json", id)
		}
		c.authorization = bearer(key)
	}
	if req.Body.set {
		body, err := b.resolve(req.Body.v)
		if err != nil {
			return c, err
		}
		if c.body, err = json.Marshal(body); err != nil {
			return c, err
		}
	}
	return c, nil
}

// expectation is an Expect with its references resolved.
type expectation struct {
	status     int
	code       *string
	body       value
	headers    map[string]string
	bodySHA256 *string
	bodyLines  value
}

func resolveExpect(e *Expect, b bodies) (*expectation, error) {
	exp := &expectation{status: *e.Status, headers: map[string]string{}}
	if e.Code != nil {
		code, err := b.resolveText("code", *e.Code)
		if err != nil {
			return nil, err
		}
		exp.code = &code
	}
	for _, name := range sortedKeys(e.Headers) {
		v, err := b.resolveText("header "+name, e.Headers[name])
		if err != nil {
			return nil, err
		}
		exp.headers[name] = v
	}
	if e.BodySHA256 != nil {
		sum, err := b.resolveText("body_sha256", *e.BodySHA256)
		if err != nil {
			return nil, err
		}
		exp.bodySHA256 = &sum
	}
	for _, p := range []struct{ from, to *value }{{&e.Body, &exp.body}, {&e.BodyLines, &exp.bodyLines}} {
		if !p.from.set {
			continue
		}
		v, err := b.resolve(p.from.v)
		if err != nil {
			return nil, err
		}
		*p.to = value{set: true, v: v}
	}
	return exp, nil
}

// meets checks a response against a request step's expectation, in the
// order of the expect table: status, code, body, headers, body_sha256, and
// body_lines.
func (x *checkRun) meets(e *exchange, exp *expectation) *Failure {
	if e.status != exp.status {
		return x.fail(e.call, "status", strconv.Itoa(exp.status), e.statusLine())
	}
	if exp.code != nil {
		if mt := mediaType(e.header.Get("Content-Type")); mt != "application/problem+json" {
			return x.fail(e.call, "header Content-Type", "application/problem+json", render(e.header.Get("Content-Type")))
		}
		var body any
		if err := decodeJSON(e.body, &body); err != nil {
			return x.fail(e.call, "body", "a problem", "not JSON: "+shorten(string(e.body)))
		}
		problem := map[string]any{"code": *exp.code, "status": json.Number(strconv.Itoa(e.status))}
		if d := match("body", problem, body); d != nil {
			return x.differs(e.call, d)
		}
	}
	if exp.body.set {
		var body any
		if err := decodeJSON(e.body, &body); err != nil {
			return x.fail(e.call, "body", render(exp.body.v), "not JSON: "+shorten(string(e.body)))
		}
		if d := match("body", exp.body.v, body); d != nil {
			return x.differs(e.call, d)
		}
	}
	for _, name := range sortedKeys(exp.headers) {
		want := exp.headers[name]
		values := e.header.Values(name)
		if len(values) == 0 {
			return x.fail(e.call, "header "+name, render(want), "no header")
		}
		got := strings.Join(values, ", ")
		if strings.EqualFold(name, "Content-Type") {
			if mediaType(got) != mediaType(want) {
				return x.fail(e.call, "header "+name, render(want), render(got))
			}
		} else if got != want {
			return x.fail(e.call, "header "+name, render(want), render(got))
		}
	}
	if exp.bodySHA256 != nil {
		sum := sha256.Sum256(e.body)
		if got := hex.EncodeToString(sum[:]); got != *exp.bodySHA256 {
			return x.fail(e.call, "body_sha256", *exp.bodySHA256, got)
		}
	}
	if exp.bodyLines.set {
		lines, err := ndjson(e.body)
		if err != nil {
			return x.fail(e.call, "body_lines", "lines of JSON, each ending with \\n", err.Error())
		}
		if d := match("body_lines", exp.bodyLines.v, lines); d != nil {
			return x.differs(e.call, d)
		}
	}
	return nil
}

// ndjson splits a body after each \n and parses every line as JSON. A body
// that does not end with \n is an error.
func ndjson(body []byte) ([]any, error) {
	text := string(body)
	if !strings.HasSuffix(text, "\n") {
		return nil, fmt.Errorf("the body does not end with \\n: %s", render(shorten(text)))
	}
	lines := []any{}
	for i, line := range strings.SplitAfter(strings.TrimSuffix(text, "\n"), "\n") {
		var v any
		if err := decodeJSON([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("line %d is not JSON: %v: %s", i+1, err, render(shorten(line)))
		}
		lines = append(lines, v)
	}
	return lines, nil
}

// mediaType returns a Content-Type's media type in lower case, without
// parameters, or the value as it is if it cannot be parsed.
func mediaType(contentType string) string {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return contentType
	}
	return mt
}

func clone(q url.Values) url.Values {
	c := make(url.Values, len(q))
	for k, v := range q {
		c[k] = append([]string(nil), v...)
	}
	return c
}
