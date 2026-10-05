package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestAuthentication(t *testing.T) {
	s := newServer(t, true)
	refused := []struct {
		name, target string
		r            req
	}{
		{"no header", "/v1/series", req{}},
		{"an unknown key", "/v1/series", req{auth: "Bearer not-a-demo-key"}},
		{"another scheme", "/v1/series", req{auth: "Basic ZGVtbzpkZW1v"}},
		{"a scheme without a key", "/v1/series", req{auth: "Bearer"}},
		{"a scheme and a space", "/v1/series", req{auth: "Bearer "}},
		{"a key without a scheme", "/v1/series", req{auth: researchKey}},
		{"an extra space", "/v1/series", req{auth: "Bearer  " + researchKey}},
		{"a key in another case", "/v1/series", req{auth: "Bearer " + strings.ToUpper(researchKey)}},
		{"the test-control key under /v1", "/v1/series", req{key: testControlKey}},
		{"a customer key under /test", "/test/clock", req{key: researchKey}},
		{"the unentitled key under /test", "/test/clock", req{key: unentitledKey}},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			r := s.do("GET", tt.target, tt.r)
			wantProblem(t, r, 401, "unauthenticated", nil)
			if strings.Contains(string(r.raw), researchKey) || strings.Contains(string(r.raw), testControlKey) {
				t.Errorf("the problem repeats a key: %s", r.raw)
			}
		})
	}
	for _, auth := range []string{"Bearer " + researchKey, "bearer " + researchKey, "BEARER " + researchKey} {
		wantOK(t, s.do("GET", "/v1/series", req{auth: auth}))
	}

	// Two Authorization headers are malformed.
	hr, _ := http.NewRequest("GET", s.url+"/v1/series", nil)
	hr.Header.Add("Authorization", "Bearer "+researchKey)
	hr.Header.Add("Authorization", "Bearer "+researchKey)
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("two Authorization headers: %d", resp.StatusCode)
	}
}

// The invariants allow a credential_id that is the empty string. An unknown
// key must still be refused, rather than taken for that credential.
func TestUnknownKeyWithAnEmptyCredentialID(t *testing.T) {
	for _, tt := range []struct {
		credentialID, key, target string
	}{
		{"cred_research", researchKey, "/v1/series"},
		{"cred_test_control", testControlKey, "/test/clock"},
	} {
		t.Run(tt.credentialID, func(t *testing.T) {
			f := loadFixtures(t)
			for i := range f.Credentials {
				if f.Credentials[i].CredentialID == tt.credentialID {
					f.Credentials[i].CredentialID = ""
				}
			}
			s := newServerFor(t, f, true)
			wantProblem(t, s.do("GET", tt.target, req{auth: "Bearer not-a-demo-key"}), 401, "unauthenticated", nil)
			wantOK(t, s.do("GET", tt.target, req{key: tt.key}))
		})
	}
}

func TestCatalog(t *testing.T) {
	s := newServer(t, true)
	fx := loadFixtures(t)
	series := fx.Series[0]

	want := compact(t, fmt.Sprintf(`{"data": [{"dataset_id": "core-indicators", "name": %q, "description": %q, "entitled": true, "head_position": 37}]}`,
		fx.Datasets[0].Name, fx.Datasets[0].Description))
	r := s.get("/v1/datasets")
	wantOK(t, r)
	if got := strings.TrimSpace(string(r.raw)); got != want {
		t.Errorf("datasets: got %s, want %s", got, want)
	}
	if got := wantOK(t, s.get("/v1/datasets/core-indicators")); got["head_position"] != float64(37) || got["entitled"] != true {
		t.Errorf("dataset: %v", got)
	}
	wantSeries := compact(t, fmt.Sprintf(`{"series_id": "activity-index", "dataset_id": "core-indicators", "name": %q, "description": %q,
		"frequency": "monthly", "unit": "index_points", "base_period": "2025 = 100", "seasonal_adjustment": "seasonally_adjusted",
		"source": %q, "release_schedule": %q, "entitled": true}`, series.Name, series.Description, series.Source, series.ReleaseSchedule))
	if got := strings.TrimSpace(string(s.get("/v1/series/activity-index").raw)); got != wantSeries {
		t.Errorf("series: got %s, want %s", got, wantSeries)
	}
	if got := strings.TrimSpace(string(s.get("/v1/series").raw)); got != `{"data":[`+wantSeries+`]}` {
		t.Errorf("series list: got %s", got)
	}

	// Without entitlement, the catalog is visible and the head position is
	// null.
	unentitled := req{key: unentitledKey}
	if got := wantOK(t, s.do("GET", "/v1/datasets/core-indicators", unentitled)); got["entitled"] != false || got["head_position"] != nil {
		t.Errorf("unentitled dataset: %v", got)
	}
	if got := wantOK(t, s.do("GET", "/v1/series/activity-index", unentitled)); got["entitled"] != false {
		t.Errorf("unentitled series: %v", got)
	}

	wantProblem(t, s.get("/v1/datasets/no-such-dataset"), 404, "not_found", nil)
	wantProblem(t, s.get("/v1/series/no-such-series"), 404, "not_found", nil)
	wantProblem(t, s.get("/v1/series?dataset_id=core-indicators"), 400, "unknown_parameter", "dataset_id")
	wantProblem(t, s.get("/v1/datasets?x="), 400, "unknown_parameter", "x")

	// The head position follows the clock.
	s.test("POST", "/test/reset", `{"clock": "2025-06-20T16:01:09Z"}`)
	if got := wantOK(t, s.get("/v1/datasets/core-indicators"))["head_position"]; got != float64(17) {
		t.Errorf("head position at 2025-06-20T16:01:09Z: %v", got)
	}
	s.test("POST", "/test/reset", `{"clock": "2024-01-01T00:00:00Z"}`)
	if got := wantOK(t, s.get("/v1/datasets/core-indicators"))["head_position"]; got != float64(0) {
		t.Errorf("head position before the first revision: %v", got)
	}
}

func TestClock(t *testing.T) {
	s := newServer(t, true)
	if got := wantOK(t, s.test("GET", "/test/clock", ""))["now"]; got != startClock {
		t.Errorf("now %v", got)
	}
	steps := []struct {
		body      string
		status    int
		code      string
		parameter any
		now       string
	}{
		{body: `{"now": "2026-10-01T00:00:00Z"}`, status: 200, now: startClock},
		{body: `{"now": "2026-10-01T00:00:01Z"}`, status: 200, now: "2026-10-01T00:00:01Z"},
		{body: `{"now": "2026-10-01T00:00:00Z"}`, status: 409, code: "clock_backwards", parameter: "now"},
		{body: ``, status: 400, code: "missing_parameter", parameter: "now"},
		{body: `{}`, status: 400, code: "missing_parameter", parameter: "now"},
		{body: `["2026-10-02T00:00:00Z"]`, status: 400, code: "invalid_parameter", parameter: nil},
		{body: `{"now": "2026-10-02T00:00:00Z", "clock": "x"}`, status: 400, code: "unknown_parameter", parameter: "clock"},
		{body: `{"now": "2026-10-02"}`, status: 400, code: "invalid_parameter", parameter: "now"},
		{body: `{"now": null}`, status: 400, code: "invalid_parameter", parameter: "now"},
		{body: `{"now": "9999-12-31T00:00:00Z"}`, status: 400, code: "invalid_parameter", parameter: "now"},
		{body: `{"now": "9999-12-30T23:59:59Z"}`, status: 200, now: "9999-12-30T23:59:59Z"},
	}
	for i, st := range steps {
		r := s.test("PUT", "/test/clock", st.body)
		if st.status != 200 {
			wantProblem(t, r, st.status, st.code, st.parameter)
			continue
		}
		if got := wantOK(t, r)["now"]; got != st.now {
			t.Errorf("step %d: now %v, want %s", i+1, got, st.now)
		}
	}
	wantProblem(t, s.test("GET", "/test/clock?now=x", ""), 400, "unknown_parameter", "now")
}

func TestReset(t *testing.T) {
	s := newServer(t, true)
	s.test("PUT", "/test/clock", `{"now": "2027-01-01T00:00:00Z"}`)
	s.test("PUT", "/test/credentials/cred_research", `{"active": false}`)
	wantProblem(t, s.get("/v1/series"), 401, "unauthenticated", nil)

	for _, tt := range []struct{ body, now string }{
		{``, startClock},
		{`{}`, startClock},
		{`{"clock": "2025-06-20T16:01:09Z"}`, "2025-06-20T16:01:09Z"},
		{`{"clock": "9999-12-30T23:59:59Z"}`, "9999-12-30T23:59:59Z"},
	} {
		if got := wantOK(t, s.test("POST", "/test/reset", tt.body))["now"]; got != tt.now {
			t.Errorf("%q: now %v, want %s", tt.body, got, tt.now)
		}
		if got := wantOK(t, s.test("GET", "/test/clock", ""))["now"]; got != tt.now {
			t.Errorf("%q: GET /test/clock %v", tt.body, got)
		}
	}
	// A reset restores every credential.
	wantOK(t, s.get("/v1/series"))

	wantProblem(t, s.test("POST", "/test/reset", `{"clock": "9999-12-31T00:00:00Z"}`), 400, "invalid_parameter", "clock")
	wantProblem(t, s.test("POST", "/test/reset", `{"clock": "2026-10-01"}`), 400, "invalid_parameter", "clock")
	wantProblem(t, s.test("POST", "/test/reset", `{"now": "2026-10-01T00:00:00Z"}`), 400, "unknown_parameter", "now")
	wantProblem(t, s.test("POST", "/test/reset", `"2026-10-01T00:00:00Z"`), 400, "invalid_parameter", nil)
}

func TestChangeCredential(t *testing.T) {
	s := newServer(t, true)
	r := s.test("PUT", "/test/credentials/cred_research", `{"active": false}`)
	if got := strings.TrimSpace(string(r.raw)); got != `{"credential_id":"cred_research","kind":"customer","active":false,"datasets":["core-indicators"]}` {
		t.Errorf("revoked: %s", got)
	}
	wantProblem(t, s.get("/v1/series"), 401, "unauthenticated", nil)
	s.test("PUT", "/test/credentials/cred_research", `{"active": true, "datasets": []}`)
	if got := wantOK(t, s.get("/v1/datasets/core-indicators")); got["entitled"] != false || got["head_position"] != nil {
		t.Errorf("without its dataset: %v", got)
	}
	r = s.test("PUT", "/test/credentials/cred_unentitled", `{"datasets": ["core-indicators", "core-indicators"]}`)
	if got := wantOK(t, r)["datasets"]; fmt.Sprint(got) != "[core-indicators]" {
		t.Errorf("datasets %v", got)
	}
	if got := wantOK(t, s.do("GET", "/v1/series/activity-index", req{key: unentitledKey}))["entitled"]; got != true {
		t.Errorf("granted: entitled %v", got)
	}
	// An empty body changes nothing.
	r = s.test("PUT", "/test/credentials/cred_unentitled", ``)
	if got := strings.TrimSpace(string(r.raw)); got != `{"credential_id":"cred_unentitled","kind":"customer","active":true,"datasets":["core-indicators"]}` {
		t.Errorf("unchanged: %s", got)
	}

	for _, tt := range []struct {
		target, body string
		status       int
		code         string
		parameter    any
	}{
		{"/test/credentials/cred_test_control", `{"active": false}`, 400, "invalid_parameter", "credential_id"},
		{"/test/credentials/no-such-credential", `{"active": false}`, 404, "not_found", nil},
		{"/test/credentials/cred_research", `{"datasets": ["no-such-dataset"]}`, 400, "invalid_parameter", "datasets"},
		{"/test/credentials/cred_research", `{"datasets": "core-indicators"}`, 400, "invalid_parameter", "datasets"},
		{"/test/credentials/cred_research", `{"datasets": ["core-indicators", null]}`, 400, "invalid_parameter", "datasets"},
		{"/test/credentials/cred_research", `{"active": "false"}`, 400, "invalid_parameter", "active"},
		{"/test/credentials/cred_research", `{"key": "x"}`, 400, "unknown_parameter", "key"},
		{"/test/credentials/no-such-credential", `{"active": 0}`, 400, "invalid_parameter", "active"},
	} {
		wantProblem(t, s.test("PUT", tt.target, tt.body), tt.status, tt.code, tt.parameter)
	}
	// A refused change changes nothing.
	if got := wantOK(t, s.test("PUT", "/test/credentials/cred_research", `{}`)); got["active"] != true || fmt.Sprint(got["datasets"]) != "[]" {
		t.Errorf("after refused changes: %v", got)
	}
}

// TestConcurrentRequests changes the clock and credentials while other
// requests read them. Run with -race, it checks that state is guarded.
func TestConcurrentRequests(t *testing.T) {
	s := newServer(t, true)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			s.test("PUT", "/test/clock", fmt.Sprintf(`{"now": "2027-01-0%dT00:00:00Z"}`, i+1))
		}()
		go func() {
			defer wg.Done()
			s.test("PUT", "/test/credentials/cred_unentitled", `{"datasets": ["core-indicators"]}`)
		}()
		go func() {
			defer wg.Done()
			s.do("GET", "/v1/datasets", req{key: unentitledKey})
			s.do("GET", "/v1/meta", req{})
		}()
	}
	wg.Wait()
	if got := wantOK(t, s.test("GET", "/test/clock", ""))["now"]; !strings.HasPrefix(got.(string), "2027-01-0") {
		t.Errorf("now %v", got)
	}
}
