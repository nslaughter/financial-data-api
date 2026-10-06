package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/nslaughter/financial-data-api/internal/history"
)

const createExportPath = "/v1/datasets/core-indicators/exports"

// exportIDPattern is the form of the export identifiers the server issues.
var exportIDPattern = regexp.MustCompile(`^exp_[0-9a-f]{16}$`)

// createExport creates an export of the fixture dataset with the research
// key, checks the 201 and its Location header, and returns the export's
// identifier and the response.
func (s *testServer) createExport() (string, *response) {
	s.t.Helper()
	r := s.do(http.MethodPost, createExportPath, req{key: researchKey})
	if r.status != http.StatusCreated {
		s.t.Fatalf("got %d %s, want 201", r.status, r.raw)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/json" {
		s.t.Errorf("Content-Type %q", ct)
	}
	id, _ := r.body["export_id"].(string)
	if !exportIDPattern.MatchString(id) {
		s.t.Errorf("export_id %q", id)
	}
	if loc := r.header.Get("Location"); loc != "/v1/exports/"+id {
		s.t.Errorf("Location %q", loc)
	}
	return id, r
}

// download fetches an export's file with the research key, checks the 200
// and its headers, and returns the file.
func (s *testServer) download(id string) []byte {
	s.t.Helper()
	r := s.get("/v1/exports/" + id + "/files/revisions.jsonl")
	if r.status != http.StatusOK {
		s.t.Fatalf("got %d %s, want 200", r.status, r.raw)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/x-ndjson" {
		s.t.Errorf("Content-Type %q", ct)
	}
	if cl := r.header.Get("Content-Length"); cl != strconv.Itoa(len(r.raw)) {
		s.t.Errorf("Content-Length %q for %d bytes", cl, len(r.raw))
	}
	return r.raw
}

// TestExports creates an export before the revision of September 10
// arrives, and checks its manifest against the example of spec/api.md, and
// its file against the canonical file of the snapshot, after the revision
// arrives.
func TestExports(t *testing.T) {
	s := newServer(t, true)
	s.test("POST", "/test/reset", `{"clock": "2026-09-10T12:30:20Z"}`)

	// The manifest of spec/api.md, byte for byte, with this export's
	// identifier.
	id, r := s.createExport()
	want := compact(t, strings.ReplaceAll(`{
	  "export_id": "exp_5f0c9a7e2b14d6c3",
	  "dataset_id": "core-indicators",
	  "position": 36,
	  "created_at": "2026-09-10T12:30:20Z",
	  "expires_at": "2026-09-11T12:30:20Z",
	  "api_version": "v1",
	  "contract_version": "0.3.0",
	  "coverage": {
	    "series_ids": ["activity-index"],
	    "observation_count": 32,
	    "revision_count": 36,
	    "period_start": "2024-01-01",
	    "period_end": "2026-09-01"
	  },
	  "files": [
	    {
	      "name": "revisions.jsonl",
	      "url": "/v1/exports/exp_5f0c9a7e2b14d6c3/files/revisions.jsonl",
	      "media_type": "application/x-ndjson",
	      "record_count": 36,
	      "size_bytes": 13695,
	      "sha256": "e75e2d61e8ded1e54e210e7d854c394c9e880dcacafd2c6423ddfe5b81652469"
	    }
	  ]
	}`, "exp_5f0c9a7e2b14d6c3", id))
	if got := strings.TrimSpace(string(r.raw)); got != want {
		t.Errorf("created: got %s\nwant %s", got, want)
	}

	// The revision of September 10 arrives. The manifest is unchanged.
	s.test("PUT", "/test/clock", `{"now": "2026-09-10T12:30:50Z"}`)
	m := s.get("/v1/exports/" + id)
	wantOK(t, m)
	if got := strings.TrimSpace(string(m.raw)); got != want {
		t.Errorf("read: got %s\nwant %s", got, want)
	}

	// The file is the canonical file of the snapshot at 36, whose length
	// and digest the manifest reports, however often it is downloaded.
	h, err := history.New(loadFixtures(t))
	if err != nil {
		t.Fatal(err)
	}
	file := history.ExportFile(h.Snapshot("core-indicators", 36))
	for range 2 {
		got := s.download(id)
		if !bytes.Equal(got, file) {
			t.Errorf("the file is not the canonical file of the snapshot at 36")
		}
		sum := sha256.Sum256(got)
		if digest := hex.EncodeToString(sum[:]); len(got) != 13695 || digest != "e75e2d61e8ded1e54e210e7d854c394c9e880dcacafd2c6423ddfe5b81652469" {
			t.Errorf("%d bytes with SHA-256 %s, not those of the manifest", len(got), digest)
		}
	}
}

// TestEmptyExport checks an export taken before any revision is available:
// an empty file, with Content-Length 0.
func TestEmptyExport(t *testing.T) {
	s := newServer(t, true)
	s.test("POST", "/test/reset", `{"clock": "2024-01-01T00:00:00Z"}`)
	id, r := s.createExport()
	const coverage = `"coverage":{"series_ids":[],"observation_count":0,"revision_count":0,"period_start":null,"period_end":null}`
	if !strings.Contains(string(r.raw), coverage) {
		t.Errorf("got %s, want %s", r.raw, coverage)
	}
	if got := s.download(id); len(got) != 0 {
		t.Errorf("file %q", got)
	}
}

// TestExportErrors checks the errors of the export endpoints and their order:
// authentication, request validation, lookup, entitlement, then expiry.
func TestExportErrors(t *testing.T) {
	s := newServer(t, true)
	id, _ := s.createExport()
	manifest, file := "/v1/exports/"+id, "/v1/exports/"+id+"/files/revisions.jsonl"
	research, unentitled := req{key: researchKey}, req{key: unentitledKey}
	withBody := func(r req, body string) req { r.body = body; return r }

	tests := []struct {
		name, method, target string
		r                    req
		status               int
		code                 string
		parameter            any
	}{
		{"a query parameter of creation", "POST", createExportPath + "?position=10", research, 400, "unknown_parameter", "position"},
		{"a body field", "POST", createExportPath, withBody(research, `{"position": 10}`), 400, "unknown_parameter", "position"},
		{"a body that is not an object", "POST", createExportPath, withBody(research, `[]`), 400, "invalid_parameter", nil},
		{"a body with more after it", "POST", createExportPath, withBody(research, `{} {}`), 400, "invalid_parameter", nil},
		{"a query parameter of the manifest", "GET", manifest + "?verbose=true", research, 400, "unknown_parameter", "verbose"},
		{"a query parameter of the file", "GET", file + "?verbose=true", research, 400, "unknown_parameter", "verbose"},

		{"authentication before validation", "POST", "/v1/datasets/no-such-dataset/exports", withBody(req{}, `{"position": 10}`), 401, "unauthenticated", nil},
		{"the test-control key", "GET", file, req{key: testControlKey}, 401, "unauthenticated", nil},
		{"validation before lookup of a dataset", "POST", "/v1/datasets/no-such-dataset/exports", withBody(research, `{"position": 10}`), 400, "unknown_parameter", "position"},
		{"validation before lookup of an export", "GET", "/v1/exports/exp_none?verbose=true", research, 400, "unknown_parameter", "verbose"},

		{"an unknown dataset, without entitlement", "POST", "/v1/datasets/no-such-dataset/exports", unentitled, 404, "not_found", nil},
		{"an unknown export, without entitlement", "GET", "/v1/exports/exp_none", unentitled, 404, "not_found", nil},
		{"an unknown file, without entitlement", "GET", "/v1/exports/" + id + "/files/other.jsonl", unentitled, 404, "not_found", nil},
		{"an unknown export's file", "GET", "/v1/exports/exp_none/files/revisions.jsonl", research, 404, "not_found", nil},
		{"a file name in another case", "GET", "/v1/exports/" + id + "/files/REVISIONS.jsonl", research, 404, "not_found", nil},

		{"creation without entitlement", "POST", createExportPath, unentitled, 403, "not_entitled", nil},
		{"the manifest without entitlement", "GET", manifest, unentitled, 403, "not_entitled", nil},
		{"the file without entitlement", "GET", file, unentitled, 403, "not_entitled", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantProblem(t, s.do(tt.method, tt.target, tt.r), tt.status, tt.code, tt.parameter)
		})
	}

	// An empty body and {} create an export.
	for _, body := range []string{"", "{}"} {
		if r := s.do("POST", createExportPath, withBody(research, body)); r.status != http.StatusCreated {
			t.Errorf("body %q: got %d %s", body, r.status, r.raw)
		}
	}

	// At expiry, lookup and entitlement still come first.
	s.test("PUT", "/test/clock", `{"now": "2026-10-01T23:59:59Z"}`)
	wantOK(t, s.get(manifest))
	s.download(id)
	s.test("PUT", "/test/clock", `{"now": "2026-10-02T00:00:00Z"}`)
	wantProblem(t, s.get("/v1/exports/"+id+"/files/other.jsonl"), 404, "not_found", nil)
	wantProblem(t, s.do("GET", manifest, unentitled), 403, "not_entitled", nil)
	wantProblem(t, s.get(manifest), 410, "export_expired", nil)
	wantProblem(t, s.get(file), 410, "export_expired", nil)
}

// TestExportAccess checks that the key and its entitlement are checked on
// every request for an export, using their state at that moment.
func TestExportAccess(t *testing.T) {
	s := newServer(t, true)
	id, _ := s.createExport()
	file := s.download(id)
	manifest := "/v1/exports/" + id

	s.test("PUT", "/test/credentials/cred_research", `{"active": false}`)
	wantProblem(t, s.get(manifest), 401, "unauthenticated", nil)
	wantProblem(t, s.get(manifest+"/files/revisions.jsonl"), 401, "unauthenticated", nil)

	s.test("PUT", "/test/credentials/cred_research", `{"active": true, "datasets": []}`)
	wantProblem(t, s.get(manifest), 403, "not_entitled", nil)
	wantProblem(t, s.get(manifest+"/files/revisions.jsonl"), 403, "not_entitled", nil)

	s.test("PUT", "/test/credentials/cred_research", `{"datasets": ["core-indicators"]}`)
	if got := s.download(id); !bytes.Equal(got, file) {
		t.Error("the file changed while access was removed")
	}

	// An entitlement given later reads an export another key created.
	s.test("PUT", "/test/credentials/cred_unentitled", `{"datasets": ["core-indicators"]}`)
	wantOK(t, s.do("GET", manifest, req{key: unentitledKey}))
}

// TestResetDeletesExports checks that a reset deletes every export, and that
// an export created afterwards, at the reset's clock, has a new identifier.
func TestResetDeletesExports(t *testing.T) {
	s := newServer(t, true)
	old, _ := s.createExport()
	s.test("POST", "/test/reset", `{"clock": "2026-09-10T12:30:20Z"}`)
	wantProblem(t, s.get("/v1/exports/"+old), 404, "not_found", nil)
	wantProblem(t, s.get("/v1/exports/"+old+"/files/revisions.jsonl"), 404, "not_found", nil)

	id, r := s.createExport()
	if id == old {
		t.Errorf("identifier %s reused after a reset", id)
	}
	if got := r.body["position"]; got != float64(36) {
		t.Errorf("position %v after the reset, want 36", got)
	}
	wantOK(t, s.get("/v1/exports/"+id))
}

// TestExportCreatedAcrossAReset checks that the handler puts an export
// created by a request that read the state before a reset into the store it
// read, which the reset replaced, so that no export from before a reset
// survives it.
func TestExportCreatedAcrossAReset(t *testing.T) {
	f := loadFixtures(t)
	srv, err := New(Config{Fixtures: f, ContractVersion: f.ContractVersion, ClockStart: mustTime(t, startClock), TestControl: true})
	if err != nil {
		t.Fatal(err)
	}
	// The request reads the state as the server's authentication does, the
	// state is reset, and then the handler runs.
	c := &call{
		r:        httptest.NewRequest(http.MethodPost, createExportPath, nil),
		endpoint: "POST /v1/datasets/{dataset_id}/exports",
		path:     map[string]string{"dataset_id": "core-indicators"},
		moment:   srv.state.view(researchKey),
	}
	if _, err := srv.state.reset(nil); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if p := srv.createExport(rec, c); p != nil || rec.Code != http.StatusCreated {
		t.Fatalf("got %d %s, problem %+v", rec.Code, rec.Body, p)
	}
	var m manifestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.exports.Get(m.ExportID); !ok {
		t.Error("the export is not in the store the request read")
	}
	if _, ok := srv.state.view(researchKey).exports.Get(m.ExportID); ok {
		t.Error("an export created before a reset survived it")
	}
}

// TestConcurrentExports creates and downloads exports in parallel, and checks
// that every export has its own identifier, that every download is the
// canonical file of the snapshot, with the length and digest its manifest
// reports, and that every export can be read back.
func TestConcurrentExports(t *testing.T) {
	s := newServer(t, true)
	h, err := history.New(loadFixtures(t))
	if err != nil {
		t.Fatal(err)
	}
	want := history.ExportFile(h.Snapshot("core-indicators", 37))
	const n = 16
	ids := make([]string, n)
	// Each export is created and downloaded by a parallel subtest, which has
	// its own T, so that a failed request can stop it.
	ok := t.Run("parallel", func(t *testing.T) {
		for i := range n {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()
				ps := &testServer{t: t, url: s.url}
				id, r := ps.createExport()
				ids[i] = id
				var m manifestResponse
				if err := json.Unmarshal(r.raw, &m); err != nil || len(m.Files) != 1 {
					t.Fatalf("manifest %s: %v", r.raw, err)
				}
				got := ps.download(id)
				if !bytes.Equal(got, want) {
					t.Errorf("the file of %s is not the canonical file of the snapshot at 37", id)
				}
				sum := sha256.Sum256(got)
				if digest := hex.EncodeToString(sum[:]); len(got) != m.Files[0].SizeBytes || digest != m.Files[0].SHA256 {
					t.Errorf("%d bytes with SHA-256 %s, not those of the manifest", len(got), digest)
				}
			})
		}
	})
	if !ok {
		return
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("identifier %s issued twice", id)
		}
		seen[id] = true
		wantOK(t, s.get("/v1/exports/"+id))
	}
}
