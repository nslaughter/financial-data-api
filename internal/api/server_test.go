package api

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRouting(t *testing.T) {
	s := newServer(t, true)
	tests := []struct {
		name, method, target string
		r                    req
		status               int
		code                 string
		allow                string
	}{
		{name: "another version", method: "GET", target: "/v2/series", r: req{key: researchKey}, status: 404, code: "unsupported_api_version"},
		{name: "version zero without a key", method: "GET", target: "/v0/meta", status: 404, code: "unsupported_api_version"},
		{name: "a version with leading zeros", method: "GET", target: "/v01/meta", status: 404, code: "unsupported_api_version"},
		{name: "a version before a test path", method: "POST", target: "/v3/test/reset", status: 404, code: "unsupported_api_version"},
		{name: "an unknown path", method: "GET", target: "/v1/nothing", r: req{key: researchKey}, status: 404, code: "not_found"},
		{name: "a trailing slash", method: "GET", target: "/v1/series/", r: req{key: researchKey}, status: 404, code: "not_found"},
		{name: "an empty segment", method: "GET", target: "/v1//series", r: req{key: researchKey}, status: 404, code: "not_found"},
		{name: "paths are case-sensitive", method: "GET", target: "/V1/meta", status: 404, code: "not_found"},
		{name: "the version alone", method: "GET", target: "/v1", status: 404, code: "not_found"},
		{name: "the root", method: "GET", target: "/", status: 404, code: "not_found"},
		{name: "an unknown test path", method: "GET", target: "/test/nothing", r: req{key: testControlKey}, status: 404, code: "not_found"},
		{name: "an export without its identifier", method: "GET", target: "/v1/exports/", r: req{key: researchKey}, status: 404, code: "not_found"},
		{name: "export creation", method: "GET", target: "/v1/datasets/core-indicators/exports", r: req{key: researchKey}, status: 405, code: "method_not_allowed", allow: "POST"},
		{name: "an export", method: "POST", target: "/v1/exports/exp_0", r: req{key: researchKey}, status: 405, code: "method_not_allowed", allow: "GET"},
		{name: "an export file", method: "PUT", target: "/v1/exports/exp_0/files/revisions.jsonl", r: req{key: researchKey}, status: 405, code: "method_not_allowed", allow: "GET"},
		{name: "an export's files", method: "GET", target: "/v1/exports/exp_0/files", r: req{key: researchKey}, status: 404, code: "not_found"},
		{name: "the revision history", method: "POST", target: "/v1/revisions?series_id=activity-index", r: req{key: researchKey}, status: 405, code: "method_not_allowed", allow: "GET"},
		{name: "the release calendar", method: "PUT", target: "/v1/release-calendar?series_id=activity-index", r: req{key: researchKey}, status: 405, code: "method_not_allowed", allow: "GET"},
		{name: "an unsupported method", method: "POST", target: "/v1/series", r: req{key: researchKey}, status: 405, code: "method_not_allowed", allow: "GET"},
		{name: "DELETE", method: "DELETE", target: "/v1/datasets/core-indicators", r: req{key: researchKey}, status: 405, code: "method_not_allowed", allow: "GET"},
		{name: "the change stream", method: "PUT", target: "/v1/datasets/core-indicators/changes?after=0", r: req{key: researchKey}, status: 405, code: "method_not_allowed", allow: "GET"},
		{name: "the change stream with an empty dataset", method: "GET", target: "/v1/datasets//changes?after=0", r: req{key: researchKey}, status: 404, code: "not_found"},
		{name: "HEAD", method: "HEAD", target: "/v1/meta", status: 405, allow: "GET"},
		{name: "a test path with two methods", method: "POST", target: "/test/clock", r: req{key: testControlKey}, status: 405, code: "method_not_allowed", allow: "GET, PUT"},
		{name: "a test path with a parameter", method: "GET", target: "/test/credentials/cred_research", r: req{key: testControlKey}, status: 405, code: "method_not_allowed", allow: "PUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := s.do(tt.method, tt.target, tt.r)
			if tt.method == "HEAD" {
				if r.status != tt.status || r.header.Get("Allow") != tt.allow {
					t.Fatalf("got %d, Allow %q", r.status, r.header.Get("Allow"))
				}
				return
			}
			wantProblem(t, r, tt.status, tt.code, nil)
			if got := r.header.Get("Allow"); got != tt.allow {
				t.Errorf("Allow %q, want %q", got, tt.allow)
			}
		})
	}
}

// TestTestPathsWithoutTestControl checks that every /test path is unknown
// when test control is disabled, even with the test-control key.
func TestTestPathsWithoutTestControl(t *testing.T) {
	s := newServer(t, false)
	for _, rq := range []struct{ method, target string }{
		{"GET", "/test/clock"},
		{"PUT", "/test/clock"},
		{"POST", "/test/reset"},
		{"PUT", "/test/credentials/cred_research"},
	} {
		wantProblem(t, s.test(rq.method, rq.target, `{}`), 404, "not_found", nil)
	}
}

// TestErrorOrder checks the order of spec/api.md: routing, authentication,
// request validation, lookup, then state.
func TestErrorOrder(t *testing.T) {
	s := newServer(t, true)
	tests := []struct {
		name, method, target string
		r                    req
		status               int
		code                 string
		parameter            any
	}{
		{"an unknown path before authentication", "GET", "/v1/nothing", req{}, 404, "not_found", nil},
		{"a trailing slash before authentication", "GET", "/v1/series/", req{}, 404, "not_found", nil},
		{"a version before authentication", "GET", "/v2/series", req{}, 404, "unsupported_api_version", nil},
		{"a method before authentication", "POST", "/v1/series", req{}, 405, "method_not_allowed", nil},
		{"authentication before validation", "GET", "/v1/series?dataset_id=x", req{}, 401, "unauthenticated", nil},
		{"validation before lookup", "GET", "/v1/series/no-such-series?verbose=true", req{key: researchKey}, 400, "unknown_parameter", "verbose"},
		{"lookup", "GET", "/v1/series/no-such-series", req{key: researchKey}, 404, "not_found", nil},
		{"a body's form before lookup", "PUT", "/test/credentials/no-such-credential", req{key: testControlKey, body: `[]`}, 400, "invalid_parameter", nil},
		{"lookup before the credential's kind", "PUT", "/test/credentials/no-such-credential", req{key: testControlKey, body: `{"active": false}`}, 404, "not_found", nil},
		{"validation before state", "PUT", "/test/clock", req{key: testControlKey, body: `{"now": "2026-09-01"}`}, 400, "invalid_parameter", "now"},
		{"state", "PUT", "/test/clock", req{key: testControlKey, body: `{"now": "2026-09-01T00:00:00Z"}`}, 409, "clock_backwards", "now"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantProblem(t, s.do(tt.method, tt.target, tt.r), tt.status, tt.code, tt.parameter)
		})
	}
}

func TestMeta(t *testing.T) {
	s := newServer(t, true)
	want := compact(t, `{"api_version": "v1", "supported_api_versions": ["v1"], "contract_version": "0.3.0", "server_time": "2026-10-01T00:00:00Z"}`)
	// GET /v1/meta requires no key and ignores the Authorization header.
	for _, rq := range []req{{}, {key: researchKey}, {key: "not-a-key"}, {key: testControlKey}, {auth: "Basic ZGVtbzpkZW1v"}} {
		r := s.do("GET", "/v1/meta", rq)
		wantOK(t, r)
		if got := strings.TrimSpace(string(r.raw)); got != want {
			t.Errorf("%+v: got %s, want %s", rq, got, want)
		}
	}
	wantProblem(t, s.do("GET", "/v1/meta?verbose=true", req{}), 400, "unknown_parameter", "verbose")
	s.test("PUT", "/test/clock", `{"now": "2026-10-02T03:04:05Z"}`)
	if got := wantOK(t, s.do("GET", "/v1/meta", req{}))["server_time"]; got != "2026-10-02T03:04:05Z" {
		t.Errorf("server_time %v after moving the clock", got)
	}
}

// TestMetaReportsTheConfiguredContractVersion checks that GET /v1/meta
// reports the contract version the server implements, not the one its
// fixtures record.
func TestMetaReportsTheConfiguredContractVersion(t *testing.T) {
	s, err := New(Config{Fixtures: loadFixtures(t), ContractVersion: "9.9.9", ClockStart: mustTime(t, startClock)})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/meta", nil))
	if !strings.Contains(rec.Body.String(), `"contract_version":"9.9.9"`) {
		t.Errorf("got %d %s", rec.Code, rec.Body)
	}
	if _, err := New(Config{Fixtures: loadFixtures(t), ClockStart: mustTime(t, startClock)}); err == nil {
		t.Error("New accepted a config without a contract version")
	}
}

func TestNewRefusesALateClock(t *testing.T) {
	f := loadFixtures(t)
	_, err := New(Config{Fixtures: f, ContractVersion: f.ContractVersion, ClockStart: MaxClock.Add(time.Second)})
	if err == nil {
		t.Fatal("New accepted a clock later than MaxClock")
	}
	if _, err := New(Config{Fixtures: f, ContractVersion: f.ContractVersion, ClockStart: MaxClock}); err != nil {
		t.Fatalf("New refused MaxClock: %v", err)
	}
}

// TestAGetIgnoresItsBody checks that a body sent with a GET, including one
// that is not a JSON object, is answered as if there were none.
func TestAGetIgnoresItsBody(t *testing.T) {
	s := newServer(t, true)
	for _, body := range []string{`not JSON`, `["2026-10-01T00:00:00Z"]`, `{"unknown": 1}`} {
		wantOK(t, s.do("GET", "/v1/series", req{key: researchKey, body: body}))
		wantOK(t, s.do("GET", "/v1/meta", req{body: body}))
		if got := wantOK(t, s.test("GET", "/test/clock", body))["now"]; got != startClock {
			t.Errorf("now %v", got)
		}
	}
}

// TestPanicsAreInternalErrors checks that a handler's panic is answered with
// a problem.
func TestPanicsAreInternalErrors(t *testing.T) {
	f := loadFixtures(t)
	s, err := New(Config{Fixtures: f, ContractVersion: f.ContractVersion, ClockStart: mustTime(t, startClock)})
	if err != nil {
		t.Fatal(err)
	}
	s.routes[0].endpoints[http.MethodGet] = endpoint{handle: func(http.ResponseWriter, *call) *problem { panic("boom") }}
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", s.routes[0].template, nil))
	if rec.Code != 500 || rec.Header().Get("Content-Type") != "application/problem+json" || !strings.Contains(rec.Body.String(), `"code":"internal"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}
