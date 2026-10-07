package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	financialdataapi "github.com/nslaughter/financial-data-api"
	"github.com/nslaughter/financial-data-api/internal/expected"
	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// The runner tests run against fake servers built with net/http/httptest.
// The fakes answer with records from the fixtures, so their responses are
// valid against spec/openapi.yaml, as a correct server's are; every
// response the runner receives is validated against it.

const (
	testControlAuth = "Bearer demo-test-control-key"
	researchAuth    = "Bearer demo-research-key"
	unentitledAuth  = "Bearer demo-unentitled-key"
	startClock      = "2026-10-01T00:00:00Z"
)

var (
	docOnce sync.Once
	doc     *Document
	docErr  error
)

// openAPI returns spec/openapi.yaml, parsed once.
func openAPI(t *testing.T) *Document {
	t.Helper()
	docOnce.Do(func() { doc, docErr = ParseOpenAPI(financialdataapi.OpenAPI()) })
	if docErr != nil {
		t.Fatal(docErr)
	}
	return doc
}

func loadFixtures(t *testing.T) *fixtures.Fixtures {
	t.Helper()
	f, err := fixtures.Load(financialdataapi.Fixtures())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// revisionRecords returns the fixture revisions as the API serves them, by
// sequence: revisionRecords(t)[0] has sequence 1.
func revisionRecords(t *testing.T) []map[string]any {
	t.Helper()
	data, err := json.Marshal(loadFixtures(t).Revisions)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	if err := expected.Decode(data, &records); err != nil {
		t.Fatal(err)
	}
	return records
}

// recorded is a request a fake received.
type recorded struct {
	method, path, query, authorization, contentType, body string
}

func (r recorded) String() string {
	s := r.method + " " + r.path
	if r.query != "" {
		s += "?" + r.query
	}
	return s
}

// fake is a server whose routes are set by each test. It answers
// POST /test/reset and PUT /test/clock with the clock they set, refuses a
// /test request without the test-control key, and answers an unknown route
// with 404 not_found. It records every request.
type fake struct {
	*httptest.Server
	mu       sync.Mutex
	routes   map[string]http.HandlerFunc
	received []recorded
}

func newFake(t *testing.T) *fake {
	f := &fake{routes: map[string]http.HandlerFunc{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	clock := func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		now := startClock
		for _, k := range []string{"clock", "now"} {
			if s, ok := body[k].(string); ok {
				now = s
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"now": now})
	}
	f.handle("POST /test/reset", clock)
	f.handle("PUT /test/clock", clock)
	return f
}

// handle sets the handler for a route, such as "GET /v1/meta".
func (f *fake) handle(route string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[route] = h
}

// reply sets a route to answer every request with status and the JSON body.
func (f *fake) reply(route string, status int, body any) {
	f.handle(route, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, status, body) })
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.received = append(f.received, recorded{
		method:        r.Method,
		path:          r.URL.Path,
		query:         r.URL.RawQuery,
		authorization: r.Header.Get("Authorization"),
		contentType:   r.Header.Get("Content-Type"),
		body:          string(body),
	})
	h := f.routes[r.Method+" "+r.URL.Path]
	f.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/test/") && r.Header.Get("Authorization") != testControlAuth {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "Unauthenticated", nil)
		return
	}
	if h == nil {
		writeProblem(w, http.StatusNotFound, "not_found", "Not found", nil)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	h(w, r)
}

// requests returns the requests the fake received.
func (f *fake) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.received...)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeProblem(w http.ResponseWriter, status int, code, title string, parameter any) {
	w.Header().Set("Content-Type", "application/problem+json")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": status, "code": code, "title": title, "detail": "A fake problem.", "parameter": parameter,
	})
}

// observationPage returns a page of GET /v1/observations or
// GET /v1/revisions.
func observationPage(data []map[string]any, token any) map[string]any {
	if data == nil {
		data = []map[string]any{}
	}
	return map[string]any{
		"data":                data,
		"position":            37,
		"snapshot_expires_at": "2026-10-01T01:00:00Z",
		"next_page_token":     token,
	}
}

// dataset returns the dataset object of GET /v1/datasets/core-indicators.
func dataset(headPosition any) map[string]any {
	return map[string]any{
		"dataset_id":    "core-indicators",
		"name":          "Core indicators (synthetic)",
		"description":   "Synthetic economic indicators.",
		"entitled":      headPosition != nil,
		"head_position": headPosition,
	}
}

func meta() map[string]any {
	return map[string]any{
		"api_version":            "v1",
		"supported_api_versions": []string{"v1"},
		"contract_version":       "0.3.0",
		"server_time":            startClock,
	}
}

// newRunner returns a runner for f at stage.
func newRunner(t *testing.T, f *fake, stage int) *Runner {
	t.Helper()
	r, err := New(Config{BaseURL: f.URL, Stage: stage, Credentials: loadFixtures(t).Credentials, OpenAPI: openAPI(t)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// parse parses text as a file of expected/ named "test".
func parse(t *testing.T, text string) *expected.File {
	t.Helper()
	f, err := expected.ParseFile("test", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// runOne runs a file that holds one check and returns its result.
func runOne(t *testing.T, r *Runner, f *expected.File) Result {
	t.Helper()
	results := r.Run(context.Background(), []*expected.File{f}, "")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %v", len(results), results)
	}
	return results[0]
}

// mustPass fails the test if res failed.
func mustPass(t *testing.T, res Result) {
	t.Helper()
	if res.Failure != nil {
		t.Fatalf("%s failed:\n%s", res, res.Failure)
	}
}

// mustFail fails the test unless res failed at the step, at the place at.
func mustFail(t *testing.T, res Result, step int, at string) *Failure {
	t.Helper()
	f := res.Failure
	if f == nil {
		t.Fatalf("%s passed; want a failure at step %d, %s", res, step, at)
	}
	if f.Step != step || f.At != at {
		t.Fatalf("%s failed at step %d, %s; want step %d, %s:\n%s", res, f.Step, f.At, step, at, f)
	}
	return f
}
