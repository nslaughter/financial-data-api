package api

import (
	"strings"
	"testing"
)

// changes sends GET /v1/datasets/core-indicators/changes with the query and
// the research key.
func (s *testServer) changes(query string) *response {
	s.t.Helper()
	return s.get("/v1/datasets/core-indicators/changes?" + query)
}

// sequences returns the sequence of each event of a read.
func sequences(t *testing.T, body map[string]any) []float64 {
	t.Helper()
	data, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("data is %T", body["data"])
	}
	out := make([]float64, len(data))
	for i, d := range data {
		out[i] = d.(map[string]any)["sequence"].(float64)
	}
	return out
}

// wantRead checks that a read returns the events first to last, in order,
// or none when last is 0, with its positions.
func wantRead(t *testing.T, r *response, first, last, next, head float64) {
	t.Helper()
	body := wantOK(t, r)
	got := sequences(t, body)
	var want []float64
	for seq := first; last > 0 && seq <= last; seq++ {
		want = append(want, seq)
	}
	if len(got) != len(want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events %v, want %v", got, want)
		}
	}
	if body["next_position"] != next || body["head_position"] != head {
		t.Errorf("next_position %v and head_position %v, want %v and %v", body["next_position"], body["head_position"], next, head)
	}
}

func TestChanges(t *testing.T) {
	s := newServer(t, true)

	// The example of spec/api.md, byte for byte.
	want := compact(t, `{
		"data": [{
			"sequence": 37, "series_id": "activity-index", "observation_id": "obs_aug26", "revision_id": "rev_aug26_2",
			"revision_number": 2, "change_type": "source_revision", "period_start": "2026-08-01", "period_end": "2026-09-01",
			"value": "102.1", "missing_reason": null, "unit": "index_points", "published_at": "2026-09-10T12:30:00Z",
			"received_at": "2026-09-10T12:30:04Z", "available_at": "2026-09-10T12:30:40Z"
		}],
		"next_position": 37, "head_position": 37}`)
	r := s.changes("after=36")
	wantOK(t, r)
	if got := strings.TrimSpace(string(r.raw)); got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}

	// The default limit is 100, which holds every event; a limit returns the
	// first events, and next_position is the last returned.
	wantRead(t, s.changes("after=0"), 1, 37, 37, 37)
	wantRead(t, s.changes("after=0&limit=1"), 1, 1, 1, 37)
	wantRead(t, s.changes("limit=5&after=30"), 31, 35, 35, 37)
	wantRead(t, s.changes("after=0&limit=1000"), 1, 37, 37, 37)
	// At the head, nothing is returned and the position is kept.
	wantRead(t, s.changes("after=37"), 0, 0, 37, 37)

	for _, tt := range []struct {
		query     string
		code      string
		parameter any
	}{
		{"", "missing_parameter", "after"},
		{"limit=10", "missing_parameter", "after"},
		{"after=0&page_size=10", "unknown_parameter", "page_size"},
		{"after=0&series_id=activity-index", "unknown_parameter", "series_id"},
		{"after=0&after=1", "invalid_parameter", "after"},
		{"after=", "invalid_parameter", "after"},
		{"after=-1", "invalid_parameter", "after"},
		{"after=%2B1", "invalid_parameter", "after"},
		{"after=01", "invalid_parameter", "after"},
		{"after=1.5", "invalid_parameter", "after"},
		{"after=one", "invalid_parameter", "after"},
		// Positions are integers below 2^53, like every integer the API
		// serves.
		{"after=9007199254740992", "invalid_parameter", "after"},
		{"after=0&limit=0", "invalid_parameter", "limit"},
		{"after=0&limit=1001", "invalid_parameter", "limit"},
		{"after=0&limit=01", "invalid_parameter", "limit"},
		{"after=0&limit=", "invalid_parameter", "limit"},
	} {
		wantProblem(t, s.changes(tt.query), 400, tt.code, tt.parameter)
	}
	wantProblem(t, s.changes("after=9007199254740991"), 400, "position_ahead", "after")
}

// TestChangesErrorOrder checks the order of spec/api.md on the change
// stream: request validation, lookup, entitlement, then state.
func TestChangesErrorOrder(t *testing.T) {
	s := newServer(t, true)
	unentitled := req{key: unentitledKey}
	tests := []struct {
		name, target string
		r            req
		status       int
		code         string
		parameter    any
	}{
		{"authentication before validation", "/v1/datasets/core-indicators/changes?after=-1", req{}, 401, "unauthenticated", nil},
		{"validation before lookup", "/v1/datasets/no-such-dataset/changes?after=-1", req{key: researchKey}, 400, "invalid_parameter", "after"},
		{"lookup", "/v1/datasets/no-such-dataset/changes?after=0", req{key: researchKey}, 404, "not_found", nil},
		{"lookup before entitlement", "/v1/datasets/no-such-dataset/changes?after=0", unentitled, 404, "not_found", nil},
		{"entitlement before state", "/v1/datasets/core-indicators/changes?after=38", unentitled, 403, "not_entitled", nil},
		{"state", "/v1/datasets/core-indicators/changes?after=38", req{key: researchKey}, 400, "position_ahead", "after"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantProblem(t, s.do("GET", tt.target, tt.r), tt.status, tt.code, tt.parameter)
		})
	}
}

// TestChangesOnTheClock checks that the stream holds only the revisions
// visible at the clock, that moving the clock reveals the rest, that a reset
// to an earlier clock leaves a later position ahead of the stream, and that
// retention is measured on the clock.
func TestChangesOnTheClock(t *testing.T) {
	s := newServer(t, true)

	// Before the first revision, the stream is empty and position 0 is the
	// head.
	s.test("POST", "/test/reset", `{"clock": "2024-01-01T00:00:00Z"}`)
	wantRead(t, s.changes("after=0"), 0, 0, 0, 0)
	wantProblem(t, s.changes("after=1"), 400, "position_ahead", "after")

	// At the export snapshot time the head is 36; moving the clock past
	// rev_aug26_2's available_at reveals it.
	s.test("POST", "/test/reset", `{"clock": "2026-09-10T12:30:20Z"}`)
	wantRead(t, s.changes("after=30"), 31, 36, 36, 36)
	wantProblem(t, s.changes("after=37"), 400, "position_ahead", "after")
	s.test("PUT", "/test/clock", `{"now": "2026-09-10T12:30:40Z"}`)
	wantRead(t, s.changes("after=36"), 37, 37, 37, 37)

	// A reset to an earlier clock does not invalidate a client's position,
	// but a position past the new head is ahead of the stream.
	s.test("POST", "/test/reset", `{"clock": "2026-09-10T12:30:20Z"}`)
	wantProblem(t, s.changes("after=37"), 400, "position_ahead", "after")

	// The first event expires 1,095 days after its available_at,
	// 2024-02-03T12:31:10Z. A position that needs it expires then; one that
	// does not continues.
	s.test("PUT", "/test/clock", `{"now": "2027-02-02T12:31:09Z"}`)
	wantRead(t, s.changes("after=0&limit=1"), 1, 1, 1, 37)
	s.test("PUT", "/test/clock", `{"now": "2027-02-02T12:31:10Z"}`)
	wantProblem(t, s.changes("after=0"), 410, "position_expired", "after")
	wantRead(t, s.changes("after=1&limit=1"), 2, 2, 2, 37)

	// Long after every event has expired, the head still does not.
	s.test("PUT", "/test/clock", `{"now": "9999-12-30T23:59:59Z"}`)
	wantRead(t, s.changes("after=37"), 0, 0, 37, 37)
	wantProblem(t, s.changes("after=36"), 410, "position_expired", "after")
	wantProblem(t, s.changes("after=38"), 400, "position_ahead", "after")
}

// TestChangesEntitlement checks that the change stream applies the
// credential's state at each request.
func TestChangesEntitlement(t *testing.T) {
	s := newServer(t, true)
	unentitled := req{key: unentitledKey}
	const target = "/v1/datasets/core-indicators/changes?after=36"
	wantProblem(t, s.do("GET", target, unentitled), 403, "not_entitled", nil)
	s.test("PUT", "/test/credentials/cred_unentitled", `{"datasets": ["core-indicators"]}`)
	wantRead(t, s.do("GET", target, unentitled), 37, 37, 37, 37)
	s.test("PUT", "/test/credentials/cred_unentitled", `{"active": false}`)
	wantProblem(t, s.do("GET", target, unentitled), 401, "unauthenticated", nil)
	s.test("POST", "/test/reset", ``)
	wantProblem(t, s.do("GET", target, unentitled), 403, "not_entitled", nil)
}
