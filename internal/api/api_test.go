package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	financialdataapi "github.com/nslaughter/financial-data-api"
	"github.com/nslaughter/financial-data-api/internal/conformance"
	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// These tests send requests to a server built with net/http/httptest, and
// validate every response against spec/openapi.yaml with the conformance
// runner's validator, so a response that omits a field or breaks a schema
// fails any test that receives it.

const (
	researchKey    = "demo-research-key"
	unentitledKey  = "demo-unentitled-key"
	testControlKey = "demo-test-control-key"
	startClock     = "2026-10-01T00:00:00Z"
)

var (
	docOnce sync.Once
	doc     *conformance.Document
	docErr  error
)

func openAPI(t *testing.T) *conformance.Document {
	t.Helper()
	docOnce.Do(func() { doc, docErr = conformance.ParseOpenAPI(financialdataapi.OpenAPI()) })
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

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, ok := fixtures.ParseTimestamp(s)
	if !ok {
		t.Fatalf("%q is not a timestamp", s)
	}
	return tm
}

// testServer is a server under test.
type testServer struct {
	t   *testing.T
	url string
}

// newServer starts a server at the default clock start, with test control
// enabled or not.
func newServer(t *testing.T, testControl bool) *testServer {
	t.Helper()
	return newServerFor(t, loadFixtures(t), testControl)
}

// newServerFor starts a server for the fixtures f at the default clock
// start, with test control enabled or not.
func newServerFor(t *testing.T, f *fixtures.Fixtures, testControl bool) *testServer {
	t.Helper()
	s, err := New(Config{Fixtures: f, ContractVersion: f.ContractVersion, ClockStart: mustTime(t, startClock), TestControl: testControl})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return &testServer{t: t, url: ts.URL}
}

// response is a response, with its body parsed as JSON when it is JSON.
type response struct {
	status int
	header http.Header
	raw    []byte
	body   map[string]any
}

// req describes a request: the key to send as a bearer token, or a raw
// Authorization header, and a body.
type req struct {
	key  string
	auth string
	body string
}

// do sends a request and validates the response against the OpenAPI
// document.
func (s *testServer) do(method, target string, r req) *response {
	s.t.Helper()
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	hr, err := http.NewRequest(method, s.url+target, body)
	if err != nil {
		s.t.Fatal(err)
	}
	switch {
	case r.auth != "":
		hr.Header.Set("Authorization", r.auth)
	case r.key != "":
		hr.Header.Set("Authorization", "Bearer "+r.key)
	}
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	out := &response{status: resp.StatusCode, header: resp.Header, raw: raw}
	// A response to HEAD has no body to validate or parse.
	if method == http.MethodHead {
		return out
	}
	if err := openAPI(s.t).Validate(method, hr.URL.EscapedPath(), resp.StatusCode, resp.Header, raw); err != nil {
		s.t.Errorf("%s %s: %v", method, target, err)
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "json") {
		if err := json.Unmarshal(raw, &out.body); err != nil {
			s.t.Fatalf("%s %s: body %q: %v", method, target, raw, err)
		}
	}
	return out
}

// get sends a GET with the research key.
func (s *testServer) get(target string) *response {
	s.t.Helper()
	return s.do(http.MethodGet, target, req{key: researchKey})
}

// test sends a request to a /test endpoint with the test-control key.
func (s *testServer) test(method, target, body string) *response {
	s.t.Helper()
	return s.do(method, target, req{key: testControlKey, body: body})
}

// wantProblem checks that a response is the problem with status and code,
// naming parameter, or no parameter when parameter is nil.
func wantProblem(t *testing.T, r *response, status int, code string, parameter any) {
	t.Helper()
	if r.status != status || r.body["code"] != code {
		t.Fatalf("got %d %s, want %d %s", r.status, r.raw, status, code)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type %q", ct)
	}
	if r.body["parameter"] != parameter {
		t.Errorf("parameter %v, want %v", r.body["parameter"], parameter)
	}
	if r.body["title"] != codes[code].title || r.body["status"] != float64(status) {
		t.Errorf("title %v and status %v do not match the code", r.body["title"], r.body["status"])
	}
	if status == http.StatusUnauthorized && r.header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate %q", r.header.Get("WWW-Authenticate"))
	}
}

// wantOK checks that a response is 200 with JSON, and returns its body.
func wantOK(t *testing.T, r *response) map[string]any {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("got %d %s, want 200", r.status, r.raw)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}
	return r.body
}

// compact returns JSON text without insignificant whitespace.
func compact(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
