package conformance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// requestLines returns each request as "METHOD /path?query".
func requestLines(rs []recorded) []string {
	lines := make([]string, len(rs))
	for i, r := range rs {
		lines[i] = r.String()
	}
	return lines
}

func checkRequests(t *testing.T, f *fake, want ...string) []recorded {
	t.Helper()
	got := f.requests()
	if lines := requestLines(got); !slices.Equal(lines, want) {
		t.Fatalf("requests:\n  %s\nwant:\n  %s", strings.Join(lines, "\n  "), strings.Join(want, "\n  "))
	}
	return got
}

// observationIDs returns the expected list of a query check: each record's
// observation_id.
func observationIDs(records []map[string]any) string {
	var parts []string
	for _, r := range records {
		parts = append(parts, fmt.Sprintf(`{"observation_id": %q}`, r["observation_id"]))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func TestQueryCheckConcatenatesPages(t *testing.T) {
	f := newFake(t)
	recs := revisionRecords(t)
	f.handle("GET /v1/observations", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_token") == "" {
			writeJSON(w, http.StatusOK, observationPage(recs[0:2], "t1"))
			return
		}
		writeJSON(w, http.StatusOK, observationPage(recs[2:3], nil))
	})
	file := parse(t, `{"checks": [{"name": "q", "reason": "r",
		"query": {"series_id": "activity-index", "period_start": "2024-01-01", "available_as_of": null},
		"expected": [{"revision_id": "rev_jan24_1"}, {"revision_id": "rev_feb24_1"}, {"revision_id": "rev_mar24_1", "missing_reason": null}]}]}`)
	mustPass(t, runOne(t, newRunner(t, f, 1), file))
	got := checkRequests(t, f,
		"POST /test/reset",
		"GET /v1/observations?period_start=2024-01-01&series_id=activity-index",
		"GET /v1/observations?page_token=t1&period_start=2024-01-01&series_id=activity-index",
	)
	if got[0].body != "{}" || got[0].authorization != testControlAuth || got[0].contentType != "application/json" {
		t.Errorf("reset: body %q, Authorization %q, Content-Type %q", got[0].body, got[0].authorization, got[0].contentType)
	}
	if got[1].authorization != researchAuth || got[1].body != "" {
		t.Errorf("query: Authorization %q, body %q", got[1].authorization, got[1].body)
	}
}

func TestQueryCheckReportsTheFirstDifference(t *testing.T) {
	f := newFake(t)
	recs := revisionRecords(t)
	f.reply("GET /v1/observations", http.StatusOK, observationPage(recs[0:2], nil))
	file := parse(t, `{"checks": [{"name": "q", "query": {"series_id": "activity-index"},
		"expected": [{"revision_id": "rev_jan24_1"}, {"revision_id": "rev_feb24_2"}]}]}`)
	fail := mustFail(t, runOne(t, newRunner(t, f, 1), file), 0, "data[1].revision_id")
	if fail.Expected != `"rev_feb24_2"` || fail.Actual != `"rev_feb24_1"` {
		t.Errorf("expected %s, actual %s", fail.Expected, fail.Actual)
	}
	if fail.Request != "GET /v1/observations?series_id=activity-index, every page" {
		t.Errorf("request %q", fail.Request)
	}
}

func TestQueryCheckRequires200(t *testing.T) {
	f := newFake(t)
	f.handle("GET /v1/observations", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusBadRequest, "invalid_parameter", "Invalid parameter", "series_id")
	})
	file := parse(t, `{"checks": [{"name": "q", "query": {"series_id": "activity-index"}, "expected": []}]}`)
	fail := mustFail(t, runOne(t, newRunner(t, f, 1), file), 0, "status")
	if fail.Expected != "200" || !strings.HasPrefix(fail.Actual, `400 {"code":"invalid_parameter"`) {
		t.Errorf("expected %s, actual %s", fail.Expected, fail.Actual)
	}
}

func TestQueryCheckContrast(t *testing.T) {
	recs := revisionRecords(t)
	// rev_feb26_1, sequence 29, as served before the provider's correction,
	// and rev_feb26_2, sequence 30, the correction.
	served, corrected := recs[28], recs[29]
	file := parse(t, `{"checks": [{"name": "q",
		"query": {"series_id": "activity-index", "published_as_of": "2026-03-04T00:00:00Z"},
		"expected": [{"revision_id": "rev_feb26_2"}],
		"contrast_available_as_of": [{"revision_id": "rev_feb26_1"}]}]}`)

	for _, tt := range []struct {
		name     string
		contrast map[string]any
		at       string
	}{
		{"passes", served, ""},
		{"fails on the contrast", corrected, "data[0].revision_id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t)
			f.handle("GET /v1/observations", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has("published_as_of") {
					writeJSON(w, http.StatusOK, observationPage([]map[string]any{corrected}, nil))
					return
				}
				writeJSON(w, http.StatusOK, observationPage([]map[string]any{tt.contrast}, nil))
			})
			res := runOne(t, newRunner(t, f, 2), file)
			checkRequests(t, f,
				"POST /test/reset",
				"GET /v1/observations?published_as_of=2026-03-04T00%3A00%3A00Z&series_id=activity-index",
				"GET /v1/observations?available_as_of=2026-03-04T00%3A00%3A00Z&series_id=activity-index",
			)
			if tt.at == "" {
				mustPass(t, res)
				return
			}
			fail := mustFail(t, res, 0, tt.at)
			if !strings.Contains(fail.Request, "available_as_of=") {
				t.Errorf("request %q, want the contrast query", fail.Request)
			}
		})
	}
}

func TestQueryCheckResetsToItsClock(t *testing.T) {
	f := newFake(t)
	f.reply("GET /v1/observations", http.StatusOK, observationPage(nil, nil))
	file := parse(t, `{"checks": [{"name": "q", "clock": "2026-09-10T12:30:20Z", "query": {"series_id": "activity-index"}, "expected": []}]}`)
	mustPass(t, runOne(t, newRunner(t, f, 1), file))
	if body := f.requests()[0].body; body != `{"clock":"2026-09-10T12:30:20Z"}` {
		t.Errorf("reset body %s", body)
	}
}

func TestPageCheck(t *testing.T) {
	// The first 22 revisions by sequence. May 2025 has three revisions among
	// them, the last at sequence 20, so the second page of ten ends with it.
	recs := revisionRecords(t)[:22]
	pagesOf := func(sizes ...int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if q.Get("page_size") != "10" {
				writeJSON(w, http.StatusOK, observationPage(recs, nil))
				return
			}
			page, _ := strconv.Atoi(q.Get("page_token"))
			start := 0
			for _, s := range sizes[:page] {
				start += s
			}
			var token any
			if page+1 < len(sizes) {
				token = strconv.Itoa(page + 1)
			}
			writeJSON(w, http.StatusOK, observationPage(recs[start:start+sizes[page]], token))
		}
	}
	file := parse(t, `{
		"checks": [{"name": "q", "query": {"series_id": "activity-index"}, "expected": `+observationIDs(recs)+`}],
		"pages_with_page_size_10": [
			{"first": "obs_jan24", "last": "obs_oct24", "count": 10},
			{"first": "obs_nov24", "last": "obs_may25", "count": 10},
			{"first": "obs_jul25", "last": "obs_aug25", "count": 2}
		]}`)
	for _, tt := range []struct {
		name  string
		sizes []int
		at    string
		exp   string
		act   string
	}{
		{name: "passes", sizes: []int{10, 10, 2}},
		{name: "a page with too few records", sizes: []int{9, 10, 3}, at: "page 1", exp: "10 records", act: "9 records"},
		{name: "a page with too many records", sizes: []int{10, 12}, at: "page 2", exp: "10 records", act: "12 records"},
		{name: "an extra page", sizes: []int{10, 10, 2, 0}, at: "pages", exp: "3 pages", act: "4 pages"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t)
			f.handle("GET /v1/observations", pagesOf(tt.sizes...))
			results := newRunner(t, f, 1).Run(context.Background(), []*File{file}, "")
			if len(results) != 2 || results[0].Kind != "query check" || results[1].Kind != "page check" {
				t.Fatalf("results %v", results)
			}
			mustPass(t, results[0])
			requests := f.requests()
			for _, r := range requests[len(requests)-len(tt.sizes):] {
				if !strings.Contains(r.query, "page_size=10") {
					t.Errorf("%s: want page_size=10", r)
				}
			}
			if tt.at == "" {
				mustPass(t, results[1])
				return
			}
			fail := mustFail(t, results[1], 0, tt.at)
			if fail.Expected != tt.exp || fail.Actual != tt.act {
				t.Errorf("expected %s, actual %s", fail.Expected, fail.Actual)
			}
		})
	}
}

func TestPageCheckComparesFirstAndLastRecords(t *testing.T) {
	recs := revisionRecords(t)[:3]
	f := newFake(t)
	f.handle("GET /v1/observations", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_token") == "" {
			writeJSON(w, http.StatusOK, observationPage(recs[:2], "next"))
			return
		}
		writeJSON(w, http.StatusOK, observationPage(recs[2:], nil))
	})
	file := parse(t, `{
		"checks": [{"name": "q", "query": {"series_id": "activity-index"}, "expected": `+observationIDs(recs)+`}],
		"pages_with_page_size_10": [{"first": "obs_jan24", "last": "obs_mar24", "count": 2}, {"first": "obs_mar24", "last": "obs_mar24", "count": 1}]}`)
	results := newRunner(t, f, 1).Run(context.Background(), []*File{file}, "")
	fail := mustFail(t, results[1], 0, "page 1, last record.observation_id")
	if fail.Expected != `"obs_mar24"` || fail.Actual != `"obs_feb24"` {
		t.Errorf("expected %s, actual %s", fail.Expected, fail.Actual)
	}
}

func TestPositionCheck(t *testing.T) {
	file := parse(t, `{"position_checks": [{"name": "p", "at": "2025-07-03T12:31:10Z", "expected_position": 20}]}`)
	for _, tt := range []struct {
		served int
		at     string
	}{{20, ""}, {18, "body.head_position"}} {
		f := newFake(t)
		f.reply("GET /v1/datasets/core-indicators", http.StatusOK, dataset(tt.served))
		res := runOne(t, newRunner(t, f, 1), file)
		got := checkRequests(t, f, "POST /test/reset", "GET /v1/datasets/core-indicators")
		if got[0].body != `{"clock":"2025-07-03T12:31:10Z"}` {
			t.Errorf("reset body %s", got[0].body)
		}
		if tt.at == "" {
			mustPass(t, res)
			continue
		}
		fail := mustFail(t, res, 0, tt.at)
		if fail.Expected != "20" || fail.Actual != "18" {
			t.Errorf("expected %s, actual %s", fail.Expected, fail.Actual)
		}
	}
}

func TestReadCheck(t *testing.T) {
	recs := revisionRecords(t)
	f := newFake(t)
	f.handle("GET /v1/datasets/core-indicators/changes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") == "37" {
			writeJSON(w, http.StatusOK, map[string]any{"data": []any{}, "next_position": 37, "head_position": 37})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": recs[16:18], "next_position": 18, "head_position": 37})
	})
	file := parse(t, `{"read_checks": [
		{"name": "a", "after_position": 16, "limit": 2, "expected": [{"sequence": 17}, {"sequence": 18, "revision_id": "rev_may25_2"}],
		 "expected_next_position": 18, "expected_head_position": 37},
		{"name": "b", "after_position": 37, "limit": null, "expected": [], "expected_next_position": 37, "expected_head_position": 37},
		{"name": "c", "after_position": 37, "limit": null, "expected": [], "expected_next_position": 37, "expected_head_position": 36}
	]}`)
	results := newRunner(t, f, 1).Run(context.Background(), []*File{file}, "")
	mustPass(t, results[0])
	mustPass(t, results[1])
	fail := mustFail(t, results[2], 0, "body.head_position")
	if fail.Expected != "36" || fail.Actual != "37" {
		t.Errorf("expected %s, actual %s", fail.Expected, fail.Actual)
	}
	checkRequests(t, f,
		"POST /test/reset", "GET /v1/datasets/core-indicators/changes?after=16&limit=2",
		"POST /test/reset", "GET /v1/datasets/core-indicators/changes?after=37",
		"POST /test/reset", "GET /v1/datasets/core-indicators/changes?after=37",
	)
}

// TestTimingChecks runs release-timing.json against a fake that serves the
// release calendar and the revision history from the fixtures.
func TestTimingChecks(t *testing.T) {
	fx := loadFixtures(t)
	recs := revisionRecords(t)
	f := newFake(t)
	f.handle("GET /v1/release-calendar", func(w http.ResponseWriter, r *http.Request) {
		data := []any{}
		for _, rel := range fx.Releases {
			if rel.SeriesID == r.URL.Query().Get("series_id") && rel.PeriodStart >= r.URL.Query().Get("period_start") {
				data = append(data, rel)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data})
	})
	f.handle("GET /v1/revisions", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var data []map[string]any
		for _, rec := range recs {
			if rec["period_start"].(string) >= q.Get("period_start") && rec["period_end"].(string) <= q.Get("period_end") {
				data = append(data, rec)
			}
		}
		slices.SortStableFunc(data, func(a, b map[string]any) int {
			return strings.Compare(string(a["revision_number"].(json.Number)), string(b["revision_number"].(json.Number)))
		})
		// One revision per page, to check that every page is read.
		page, _ := strconv.Atoi(q.Get("page_token"))
		var token any
		if page+1 < len(data) {
			token = strconv.Itoa(page + 1)
		}
		writeJSON(w, http.StatusOK, observationPage(data[page:page+1], token))
	})
	file := fileNamed(t, loadExpected(t), timingFile)
	results := newRunner(t, f, 2).Run(context.Background(), []*File{file}, "")
	if len(results) != len(file.TimingChecks) {
		t.Fatalf("%d results for %d checks", len(results), len(file.TimingChecks))
	}
	for _, res := range results {
		mustPass(t, res)
	}
	if !slices.Contains(requestLines(f.requests()), "GET /v1/revisions?period_end=2025-12-01&period_start=2025-11-01&series_id=activity-index") {
		t.Error("the revision history was not read with the calendar's period_end")
	}
}

func TestTiming(t *testing.T) {
	rec := func(seq int, id, published, available string) any {
		return map[string]any{"sequence": json.Number(strconv.Itoa(seq)), "revision_id": id, "published_at": published, "available_at": available}
	}
	got, err := timing("2025-12-03T12:30:00Z", []any{
		rec(26, "late", "2025-12-03T12:30:00Z", "2025-12-04T12:31:10Z"),
		rec(25, "tie", "2025-12-03T12:35:00Z", "2025-12-04T12:31:10Z"),
		rec(27, "later", "2025-12-05T12:30:00Z", "2025-12-06T12:31:10Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"scheduled_at":                "2025-12-03T12:30:00Z",
		"first_published_at":          "2025-12-03T12:30:00Z",
		"first_available_at":          "2025-12-04T12:31:10Z",
		"first_available_revision_id": "tie",
		"source_delay_seconds":        json.Number("0"),
		"availability_delay_seconds":  json.Number("86470"),
	}
	if d := match("", want, got); d != nil {
		t.Fatalf("%s: expected %s, got %s", d.at, d.expected, d.actual)
	}
	if _, err := timing("2025-12-03T12:30:00Z", nil); err == nil {
		t.Error("timing of no revisions: want an error")
	}
	if _, err := timing("2025-12-03T12:30:00Z", []any{map[string]any{"sequence": json.Number("1")}}); err == nil {
		t.Error("timing of a revision without times: want an error")
	}
}

func TestScenarioActions(t *testing.T) {
	f := newFake(t)
	f.reply("PUT /test/credentials/cred_research", http.StatusOK, map[string]any{
		"credential_id": "cred_research", "kind": "customer", "active": false, "datasets": []string{},
	})
	f.reply("GET /v1/meta", http.StatusOK, meta())
	file := parse(t, `{"scenarios": [{"name": "s", "reason": "r", "clock": "2026-09-10T12:30:20Z", "steps": [
		{"set_clock": "2026-09-10T12:30:40Z"},
		{"set_credential": {"credential_id": "cred_research", "active": false, "datasets": []}},
		{"reset": {"clock": "2025-06-20T16:01:09Z"}},
		{"reset": {}},
		{"request": {"path": "/v1/meta", "credential": null}, "expect": {"status": 200, "body": {"api_version": "v1"}}}
	]}]}`)
	mustPass(t, runOne(t, newRunner(t, f, 1), file))
	got := checkRequests(t, f,
		"POST /test/reset",
		"PUT /test/clock",
		"PUT /test/credentials/cred_research",
		"POST /test/reset",
		"POST /test/reset",
		"GET /v1/meta",
	)
	bodies := []string{
		`{"clock":"2026-09-10T12:30:20Z"}`,
		`{"now":"2026-09-10T12:30:40Z"}`,
		`{"active":false,"datasets":[]}`,
		`{"clock":"2025-06-20T16:01:09Z"}`,
		`{}`,
		``,
	}
	for i, r := range got {
		if r.body != bodies[i] {
			t.Errorf("%s: body %q, want %q", r, r.body, bodies[i])
		}
		if i < 5 && (r.authorization != testControlAuth || r.contentType != "application/json") {
			t.Errorf("%s: Authorization %q, Content-Type %q", r, r.authorization, r.contentType)
		}
	}
	if got[5].authorization != "" {
		t.Errorf("credential null sent Authorization %q", got[5].authorization)
	}
}

func TestAFailedActionEndsTheScenario(t *testing.T) {
	f := newFake(t)
	f.handle("PUT /test/clock", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusConflict, "clock_backwards", "Clock cannot move backwards", "now")
	})
	file := parse(t, `{"scenarios": [{"name": "s", "steps": [
		{"set_clock": "2026-09-10T12:30:40Z"},
		{"request": {"path": "/v1/meta"}, "expect": {"status": 200}}
	]}]}`)
	fail := mustFail(t, runOne(t, newRunner(t, f, 1), file), 1, "status")
	if fail.Request != "PUT /test/clock" || fail.Expected != "200" || !strings.HasPrefix(fail.Actual, "409 ") {
		t.Errorf("request %s, expected %s, actual %s", fail.Request, fail.Expected, fail.Actual)
	}
	checkRequests(t, f, "POST /test/reset", "PUT /test/clock")
}

func TestRequestSteps(t *testing.T) {
	f := newFake(t)
	f.handle("GET /v1/datasets/core-indicators", func(w http.ResponseWriter, r *http.Request) {
		switch strings.ToLower(r.Header.Get("Authorization")) {
		case strings.ToLower(researchAuth):
			writeJSON(w, http.StatusOK, dataset(37))
		case strings.ToLower(unentitledAuth):
			writeJSON(w, http.StatusOK, dataset(nil))
		default:
			writeProblem(w, http.StatusUnauthorized, "unauthenticated", "Unauthenticated", nil)
		}
	})
	f.handle("PUT /test/clock", func(w http.ResponseWriter, r *http.Request) {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeProblem(w, http.StatusBadRequest, "missing_parameter", "Missing parameter", "now")
			return
		}
		obj, ok := body.(map[string]any)
		if !ok {
			writeProblem(w, http.StatusBadRequest, "invalid_parameter", "Invalid parameter", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"now": obj["now"]})
	})
	file := parse(t, `{"scenarios": [{"name": "s", "steps": [
		{"request": {"path": "/v1/datasets/core-indicators"}, "expect": {"status": 200, "body": {"entitled": true, "head_position": 37}}},
		{"request": {"path": "/v1/datasets/core-indicators", "credential": "cred_unentitled"}, "expect": {"status": 200, "body": {"head_position": null}}},
		{"request": {"path": "/v1/datasets/core-indicators", "credential": null}, "expect": {"status": 401, "code": "unauthenticated"}},
		{"request": {"path": "/v1/datasets/core-indicators", "authorization": "bearer demo-research-key"}, "expect": {"status": 200}},
		{"request": {"method": "PUT", "path": "/test/clock", "credential": "cred_test_control", "body": {"now": "2026-10-02T00:00:00Z"}},
		 "expect": {"status": 200, "body": {"now": "2026-10-02T00:00:00Z"}}},
		{"request": {"method": "PUT", "path": "/test/clock", "credential": "cred_test_control", "body": ["2026-10-01T00:00:00Z"]},
		 "expect": {"status": 400, "code": "invalid_parameter", "body": {"parameter": null}}},
		{"request": {"method": "PUT", "path": "/test/clock", "credential": "cred_test_control"},
		 "expect": {"status": 400, "code": "missing_parameter", "body": {"parameter": "now"}}}
	]}]}`)
	mustPass(t, runOne(t, newRunner(t, f, 1), file))
	got := f.requests()[1:]
	want := []struct{ line, auth, contentType, body string }{
		{"GET /v1/datasets/core-indicators", researchAuth, "", ""},
		{"GET /v1/datasets/core-indicators", unentitledAuth, "", ""},
		{"GET /v1/datasets/core-indicators", "", "", ""},
		{"GET /v1/datasets/core-indicators", "bearer demo-research-key", "", ""},
		{"PUT /test/clock", testControlAuth, "application/json", `{"now":"2026-10-02T00:00:00Z"}`},
		{"PUT /test/clock", testControlAuth, "application/json", `["2026-10-01T00:00:00Z"]`},
		{"PUT /test/clock", testControlAuth, "", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d requests: %v", len(got), requestLines(got))
	}
	for i, w := range want {
		g := got[i]
		if g.String() != w.line || g.authorization != w.auth || g.contentType != w.contentType || g.body != w.body {
			t.Errorf("step %d: got %s, Authorization %q, Content-Type %q, body %q; want %s, %q, %q, %q",
				i+1, g, g.authorization, g.contentType, g.body, w.line, w.auth, w.contentType, w.body)
		}
	}
}

func TestUnknownCredentialFailsTheStep(t *testing.T) {
	f := newFake(t)
	file := parse(t, `{"scenarios": [{"name": "s", "steps": [
		{"request": {"path": "/v1/meta", "credential": "cred_nobody"}, "expect": {"status": 200}}
	]}]}`)
	fail := mustFail(t, runOne(t, newRunner(t, f, 1), file), 1, "references")
	if !strings.Contains(fail.Actual, "credential cred_nobody is not in fixtures/credentials.json") {
		t.Errorf("actual %s", fail.Actual)
	}
}

// TestExpect checks each member of expect against fixed responses.
func TestExpect(t *testing.T) {
	recs := revisionRecords(t)
	var file strings.Builder
	for _, r := range recs[:2] {
		data, _ := json.Marshal(r)
		file.Write(data)
		file.WriteString("\n")
	}
	f := newFake(t)
	f.handle("GET /v1/meta", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(meta())
	})
	f.handle("GET /v1/series", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "Unauthenticated", nil)
	})
	f.handle("GET /v1/observations", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusBadRequest, "unknown_parameter", "Unknown parameter", "available_asof")
	})
	f.handle("GET /v1/series/odd", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(problemJSON(400, "not_found", "Not found", nil)))
	})
	serveFile := func(text string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Header().Set("Content-Length", strconv.Itoa(len(text)))
			_, _ = w.Write([]byte(text))
		}
	}
	f.handle("GET /v1/exports/exp_1/files/revisions.jsonl", serveFile(file.String()))
	f.handle("GET /v1/exports/exp_2/files/revisions.jsonl", serveFile(strings.TrimSuffix(file.String(), "\n")))
	f.handle("GET /v1/exports/exp_3/files/revisions.jsonl", serveFile(file.String()+"\n"))
	sum := "1b1f2b8f8a73b4db0d5b6a6a0c4c3bd7c4e4f1e5f1e7d6f5c0b3a2e4f6a7b8c9"

	tests := []struct {
		name, path, expect string
		// at is where the step fails, or "" if it passes.
		at, exp, act string
	}{
		{name: "status", path: "/v1/meta", expect: `{"status": 201}`, at: "status", exp: "201"},
		{name: "status before body", path: "/v1/meta", expect: `{"status": 404, "body": {"api_version": "v2"}}`, at: "status", exp: "404"},
		{name: "code", path: "/v1/observations", expect: `{"status": 400, "code": "unknown_parameter"}`},
		{name: "another code", path: "/v1/observations", expect: `{"status": 400, "code": "invalid_parameter"}`, at: "body.code", exp: `"invalid_parameter"`, act: `"unknown_parameter"`},
		{name: "code needs a problem", path: "/v1/meta", expect: `{"status": 200, "code": "unknown_parameter"}`, at: "header Content-Type", exp: "application/problem+json", act: `"application/json; charset=utf-8"`},
		{name: "code needs the status in the body", path: "/v1/series/odd", expect: `{"status": 404, "code": "not_found"}`, at: "body.status", exp: "404", act: "400"},
		{name: "header names are case-insensitive", path: "/v1/series", expect: `{"status": 401, "headers": {"www-authenticate": "Bearer"}}`},
		{name: "header values are exact", path: "/v1/series", expect: `{"status": 401, "headers": {"WWW-Authenticate": "bearer"}}`, at: "header WWW-Authenticate", exp: `"bearer"`, act: `"Bearer"`},
		{name: "a missing header", path: "/v1/series", expect: `{"status": 401, "headers": {"Allow": "GET"}}`, at: "header Allow", exp: `"GET"`, act: "no header"},
		{name: "Content-Type ignores parameters", path: "/v1/meta", expect: `{"status": 200, "headers": {"Content-Type": "application/json"}}`},
		{name: "Content-Type compares the media type", path: "/v1/meta", expect: `{"status": 200, "headers": {"Content-Type": "application/problem+json"}}`,
			at: "header Content-Type", exp: `"application/problem+json"`, act: `"application/json; charset=utf-8"`},
		{name: "body", path: "/v1/meta", expect: `{"status": 200, "body": {"api_version": "v1", "supported_api_versions": ["v1"]}}`},
		{name: "a body that differs", path: "/v1/meta", expect: `{"status": 200, "body": {"supported_api_versions": ["v1", "v2"]}}`,
			at: "body.supported_api_versions", exp: "2 elements", act: `1 elements: ["v1"]`},
		{name: "a body that is not JSON", path: "/v1/exports/exp_1/files/revisions.jsonl", expect: `{"status": 200, "body": {}}`, at: "body", exp: "{}"},
		{name: "body_sha256", path: "/v1/exports/exp_1/files/revisions.jsonl", expect: fmt.Sprintf(`{"status": 200, "body_sha256": %q}`, sha256Hex(file.String()))},
		{name: "another body_sha256", path: "/v1/exports/exp_1/files/revisions.jsonl", expect: fmt.Sprintf(`{"status": 200, "body_sha256": %q}`, sum),
			at: "body_sha256", exp: sum, act: sha256Hex(file.String())},
		{name: "body_lines", path: "/v1/exports/exp_1/files/revisions.jsonl", expect: `{"status": 200, "body_lines": [{"sequence": 1}, {"sequence": 2, "value": "97.4"}]}`},
		{name: "body_lines that differ", path: "/v1/exports/exp_1/files/revisions.jsonl", expect: `{"status": 200, "body_lines": [{"sequence": 1}, {"sequence": 3}]}`,
			at: "body_lines[1].sequence", exp: "3", act: "2"},
		{name: "too few body_lines", path: "/v1/exports/exp_1/files/revisions.jsonl", expect: `{"status": 200, "body_lines": [{"sequence": 1}]}`,
			at: "body_lines", exp: "1 elements"},
		{name: "body_lines without a final newline", path: "/v1/exports/exp_2/files/revisions.jsonl", expect: `{"status": 200, "body_lines": [{}, {}]}`,
			at: "body_lines", exp: `lines of JSON, each ending with \n`},
		{name: "body_lines with an empty line", path: "/v1/exports/exp_3/files/revisions.jsonl", expect: `{"status": 200, "body_lines": [{}, {}]}`,
			at: "body_lines", exp: `lines of JSON, each ending with \n`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := fmt.Sprintf(`{"scenarios": [{"name": "s", "steps": [{"request": {"path": %q}, "expect": %s}]}]}`, tt.path, tt.expect)
			res := runOne(t, newRunner(t, f, 2), parse(t, text))
			if tt.at == "" {
				mustPass(t, res)
				return
			}
			fail := mustFail(t, res, 1, tt.at)
			if fail.Expected != tt.exp {
				t.Errorf("expected %s, want %s", fail.Expected, tt.exp)
			}
			if tt.act != "" && fail.Actual != tt.act {
				t.Errorf("actual %s, want %s", fail.Actual, tt.act)
			}
		})
	}
}

func TestReferences(t *testing.T) {
	recs := revisionRecords(t)
	var file strings.Builder
	for _, r := range recs[:2] {
		data, _ := json.Marshal(r)
		file.Write(data)
		file.WriteString("\n")
	}
	f := newFake(t)
	f.handle("POST /v1/datasets/core-indicators/exports", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/v1/exports/exp_5f0c")
		writeJSON(w, http.StatusCreated, jsonValue(t, `{"export_id": "exp_5f0c", "dataset_id": "core-indicators", "position": 36,
			"created_at": "2026-09-10T12:30:20Z", "expires_at": "2026-09-11T12:30:20Z", "api_version": "v1", "contract_version": "0.3.0",
			"coverage": {"series_ids": ["activity-index"], "observation_count": 32, "revision_count": 36, "period_start": "2024-01-01", "period_end": "2026-09-01"},
			"files": [{"name": "revisions.jsonl", "url": "/v1/exports/exp_5f0c/files/revisions.jsonl", "media_type": "application/x-ndjson",
				"record_count": 2, "size_bytes": `+strconv.Itoa(file.Len())+`, "sha256": "`+sha256Hex(file.String())+`"}]}`))
	})
	f.handle("GET /v1/exports/exp_5f0c/files/revisions.jsonl", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Length", strconv.Itoa(file.Len()))
		_, _ = w.Write([]byte(file.String()))
	})
	f.reply("GET /v1/datasets/core-indicators/changes", http.StatusOK, map[string]any{"data": recs[36:], "next_position": 37, "head_position": 37})
	f.reply("GET /v1/datasets/core-indicators", http.StatusOK, dataset(nil))

	scenario := parse(t, `{"scenarios": [{"name": "s", "steps": [
		{"id": "c", "request": {"method": "POST", "path": "/v1/datasets/core-indicators/exports"},
		 "expect": {"status": 201, "headers": {"Location": "/v1/exports/${c.export_id}"},
		            "body": {"export_id": "${c.export_id}", "position": 36, "files": [{"url": "/v1/exports/${c.export_id}/files/revisions.jsonl", "size_bytes": "${c.files.0.size_bytes}"}]}}},
		{"request": {"path": "${c.files.0.url}"}, "expect": {"status": 200, "body_sha256": "${c.files.0.sha256}", "body_lines": [{"sequence": 1}, {"sequence": 2}]}},
		{"request": {"path": "/v1/datasets/core-indicators/changes", "query": {"after": "${c.position}"}}, "expect": {"status": 200, "body": {"head_position": 37}}},
		{"id": "d", "request": {"path": "/v1/datasets/core-indicators", "credential": "cred_unentitled"}, "expect": {"status": 200, "body": {"head_position": null}}},
		{"request": {"path": "/v1/datasets/core-indicators/changes", "query": {"after": "${d.head_position}", "limit": "1${c.position}"}},
		 "expect": {"status": 200, "body": {"data": [{"sequence": 37}]}}}
	]}]}`)
	mustPass(t, runOne(t, newRunner(t, f, 2), scenario))
	checkRequests(t, f,
		"POST /test/reset",
		"POST /v1/datasets/core-indicators/exports",
		"GET /v1/exports/exp_5f0c/files/revisions.jsonl",
		// A number is sent in decimal.
		"GET /v1/datasets/core-indicators/changes?after=36",
		"GET /v1/datasets/core-indicators",
		// null omits the parameter, and a reference in a longer string is
		// inserted as text.
		"GET /v1/datasets/core-indicators/changes?limit=136",
	)

	for _, tt := range []struct {
		name, steps, request, want string
		step                       int
	}{
		{
			name:    "a missing member in a request",
			steps:   `{"request": {"path": "/v1/exports/${c.nothing}"}, "expect": {"status": 200}}`,
			request: "GET /v1/exports/${c.nothing}", want: `the body of step c has no member "nothing"`, step: 2,
		},
		{
			name:    "a missing member in an expect",
			steps:   `{"request": {"path": "/v1/datasets/core-indicators"}, "expect": {"status": 200, "body": {"x": "${c.files.3.url}"}}}`,
			request: "GET /v1/datasets/core-indicators", want: "an array of 1 elements, has no element 3", step: 2,
		},
		{
			name: "a body that is not JSON",
			steps: `{"id": "file", "request": {"path": "${c.files.0.url}"}, "expect": {"status": 200}},
				{"request": {"path": "/v1/exports/${file.export_id}"}, "expect": {"status": 200}}`,
			request: "GET /v1/exports/${file.export_id}", want: "the body of step file is not JSON", step: 3,
		},
		{
			name:    "an object in a query parameter",
			steps:   `{"request": {"path": "/v1/datasets/core-indicators/changes", "query": {"after": "${c.coverage}"}}, "expect": {"status": 200}}`,
			request: "GET /v1/datasets/core-indicators/changes", want: "query parameter after is an object after resolving references", step: 2,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text := `{"scenarios": [{"name": "s", "steps": [
				{"id": "c", "request": {"method": "POST", "path": "/v1/datasets/core-indicators/exports"}, "expect": {"status": 201}},
				` + tt.steps + `]}]}`
			fail := mustFail(t, runOne(t, newRunner(t, f, 2), parse(t, text)), tt.step, "references")
			if fail.Request != tt.request || !strings.Contains(fail.Actual, tt.want) {
				t.Errorf("request %q, actual %q; want %q and %q", fail.Request, fail.Actual, tt.request, tt.want)
			}
		})
	}
}

func TestRepeatedQueryParameters(t *testing.T) {
	f := newFake(t)
	f.handle("GET /v1/observations", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusBadRequest, "invalid_parameter", "Invalid parameter", "series_id")
	})
	file := parse(t, `{"scenarios": [{"name": "s", "steps": [
		{"request": {"path": "/v1/observations",
		             "query": {"series_id": ["b-series", "a-series", "b-series"], "available_as_of": "2026-09-04 00:00:00Z", "page_size": "", "period_end": null}},
		 "expect": {"status": 400, "code": "invalid_parameter", "body": {"parameter": "series_id"}}}
	]}]}`)
	mustPass(t, runOne(t, newRunner(t, f, 1), file))
	got := f.requests()[1]
	// Each element is sent once, in order; a null parameter is omitted; an
	// empty string is sent empty; values are percent-encoded.
	if want := "available_as_of=2026-09-04+00%3A00%3A00Z&page_size=&series_id=b-series&series_id=a-series&series_id=b-series"; got.query != want {
		t.Errorf("query %s, want %s", got.query, want)
	}
}

func TestStages(t *testing.T) {
	file := parse(t, `{"scenarios": [
		{"name": "stage 1 only", "stages": [1], "steps": [{"request": {"path": "/v1/meta"}, "expect": {"status": 200}}]},
		{"name": "stage 2 only", "stages": [2], "steps": [{"request": {"path": "/v1/meta"}, "expect": {"status": 200}}]},
		{"name": "every stage", "steps": [{"request": {"path": "/v1/meta"}, "expect": {"status": 200}}]}
	]}`)
	for _, stage := range []int{1, 2} {
		f := newFake(t)
		f.reply("GET /v1/meta", http.StatusOK, meta())
		results := newRunner(t, f, stage).Run(context.Background(), []*File{file}, "")
		var skipped []string
		for _, res := range results {
			mustPass(t, res)
			if res.Skipped {
				skipped = append(skipped, res.Name)
			}
		}
		want := []string{"stage 2 only"}
		if stage == 2 {
			want = []string{"stage 1 only"}
		}
		if len(results) != 3 || !slices.Equal(skipped, want) {
			t.Errorf("stage %d: %d results, skipped %v; want 3, %v", stage, len(results), skipped, want)
		}
		// A skipped scenario sends nothing, not even its reset.
		if n := len(f.requests()); n != 4 {
			t.Errorf("stage %d: %d requests, want 4", stage, n)
		}
	}
}

func TestEveryResponseIsValidated(t *testing.T) {
	f := newFake(t)
	incomplete := meta()
	delete(incomplete, "server_time")
	f.reply("GET /v1/meta", http.StatusOK, incomplete)
	record := revisionRecords(t)[0]
	delete(record, "missing_reason")
	f.reply("GET /v1/observations", http.StatusOK, observationPage([]map[string]any{record}, nil))
	file := parse(t, `{
		"checks": [{"name": "query", "query": {"series_id": "activity-index"}, "expected": [{"revision_id": "rev_jan24_1"}]}],
		"scenarios": [{"name": "scenario", "steps": [{"request": {"path": "/v1/meta"}, "expect": {"status": 200, "body": {"api_version": "v1"}}}]}]
	}`)
	results := newRunner(t, f, 1).Run(context.Background(), []*File{file}, "")
	fail := mustFail(t, results[0], 0, "OpenAPI")
	if !strings.Contains(fail.Actual, "missing property 'missing_reason'") {
		t.Errorf("query check: actual %s", fail.Actual)
	}
	fail = mustFail(t, results[1], 1, "OpenAPI")
	if !strings.Contains(fail.Actual, "missing property 'server_time'") {
		t.Errorf("scenario: actual %s", fail.Actual)
	}
}

func TestATokenThatNeverEnds(t *testing.T) {
	f := newFake(t)
	f.reply("GET /v1/observations", http.StatusOK, observationPage(revisionRecords(t)[:1], "again"))
	file := parse(t, `{"checks": [{"name": "q", "query": {"series_id": "activity-index"}, "expected": []}]}`)
	fail := mustFail(t, runOne(t, newRunner(t, f, 1), file), 0, "body.next_page_token")
	if fail.Expected != "null within 1000 pages" {
		t.Errorf("expected %s", fail.Expected)
	}
}

func TestRunFilterAndProgress(t *testing.T) {
	f := newFake(t)
	f.reply("GET /v1/datasets/core-indicators", http.StatusOK, dataset(37))
	file := parse(t, `{
		"position_checks": [
			{"name": "alpha", "at": "2026-10-01T00:00:00Z", "expected_position": 37},
			{"name": "beta", "at": "2026-10-01T00:00:00Z", "expected_position": 37},
			{"name": "alphabet", "at": "2026-10-01T00:00:00Z", "expected_position": 37}
		],
		"scenarios": [{"name": "alpha at stage 2", "stages": [2], "steps": [{"request": {"path": "/v1/meta"}, "expect": {"status": 200}}]}]
	}`)
	r := newRunner(t, f, 1)
	var progress []string
	r.Progress = func(res Result) { progress = append(progress, res.String()) }
	results := r.Run(context.Background(), []*File{file}, "alpha")
	want := []string{
		`test: position check "alpha"`,
		`test: position check "alphabet"`,
		`test: scenario "alpha at stage 2"`,
	}
	if !slices.Equal(progress, want) {
		t.Errorf("progress %v, want %v", progress, want)
	}
	if len(results) != 3 || !results[2].Skipped {
		t.Errorf("results %v", results)
	}
}

func TestNew(t *testing.T) {
	creds := loadFixtures(t).Credentials
	for _, tt := range []struct {
		cfg  Config
		want string
	}{
		{Config{BaseURL: "localhost:8080", Stage: 1, Credentials: creds, OpenAPI: openAPI(t)}, "want http:// or https://"},
		{Config{BaseURL: "http://localhost:8080?x=1", Stage: 1, Credentials: creds, OpenAPI: openAPI(t)}, "no query"},
		{Config{BaseURL: "http://localhost:8080", Stage: 3, Credentials: creds, OpenAPI: openAPI(t)}, "stages are 1 to 2"},
		{Config{BaseURL: "http://localhost:8080", Stage: 1, Credentials: creds}, "no OpenAPI document"},
		{Config{BaseURL: "http://localhost:8080", Stage: 1, Credentials: creds[:2], OpenAPI: openAPI(t)}, "no test-control credential"},
		{Config{BaseURL: "http://localhost:8080", Stage: 1, Credentials: creds[1:], OpenAPI: openAPI(t)}, "no credential cred_research"},
	} {
		if _, err := New(tt.cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%+v: got %v, want an error containing %q", tt.cfg.BaseURL, err, tt.want)
		}
	}
	// A trailing slash on the base URL is dropped.
	f := newFake(t)
	f.reply("GET /v1/meta", http.StatusOK, meta())
	r, err := New(Config{BaseURL: f.URL + "/", Stage: 1, Credentials: creds, OpenAPI: openAPI(t)})
	if err != nil {
		t.Fatal(err)
	}
	mustPass(t, runOne(t, r, parse(t, `{"scenarios": [{"name": "s", "steps": [{"request": {"path": "/v1/meta"}, "expect": {"status": 200}}]}]}`)))
}

func TestFailureString(t *testing.T) {
	f := &Failure{Step: 5, Request: "GET /v1/observations?page_token=t&series_id=activity-index", At: "status", Expected: "410", Actual: "200 {}"}
	want := "step 5: GET /v1/observations?page_token=t&series_id=activity-index\nat status\n  expected: 410\n  actual:   200 {}"
	if got := f.String(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	f = &Failure{Request: "GET /v1/meta", At: "OpenAPI", Expected: "a response", Actual: "line 1\nline 2"}
	want = "GET /v1/meta\nat OpenAPI\n  expected: a response\n  actual:   line 1\n            line 2"
	if got := f.String(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestEveryCheckOfTheSuiteRuns runs every file of expected/ at stage 2
// against a fake that answers each test action but serves nothing else, so
// every check runs and fails with a report instead of stopping the run.
func TestEveryCheckOfTheSuiteRuns(t *testing.T) {
	files, err := Select(loadExpected(t), 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := newFake(t)
	r := newRunner(t, f, 2)
	results := r.Run(context.Background(), files, "")
	want := 0
	for _, file := range files {
		want += len(file.QueryChecks) + len(file.PositionChecks) + len(file.ReadChecks) + len(file.TimingChecks) + len(file.Scenarios)
		if len(file.Pages) > 0 {
			want += len(file.QueryChecks)
		}
	}
	if len(results) != want {
		t.Fatalf("%d results, want %d", len(results), want)
	}
	for _, res := range results {
		if res.Skipped {
			if res.File != "request-errors" {
				t.Errorf("%s was skipped", res)
			}
			continue
		}
		if res.Failure == nil {
			// Steps that expect 404 not_found from the fake pass until one
			// does not; a scenario of only such steps passes.
			continue
		}
		if res.Failure.Request == "" || res.Failure.At == "" {
			t.Errorf("%s: incomplete failure %+v", res, res.Failure)
		}
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
