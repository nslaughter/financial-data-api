package api

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// observations sends GET /v1/observations with the query and the research
// key.
func (s *testServer) observations(query string) *response {
	s.t.Helper()
	return s.get("/v1/observations?" + query)
}

// ids returns the observation_id of each record of a page.
func ids(t *testing.T, body map[string]any) []string {
	t.Helper()
	data, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("data is %T", body["data"])
	}
	out := make([]string, len(data))
	for i, d := range data {
		out[i] = d.(map[string]any)["observation_id"].(string)
	}
	return out
}

// withToken returns query with page_token set to token.
func withToken(query string, token any) string {
	return query + "&page_token=" + url.QueryEscape(token.(string))
}

func TestObservations(t *testing.T) {
	s := newServer(t, true)

	// The example of spec/api.md, byte for byte.
	want := compact(t, `{
		"data": [{
			"sequence": 36, "series_id": "activity-index", "observation_id": "obs_aug26", "revision_id": "rev_aug26_1",
			"revision_number": 1, "change_type": "initial_release", "period_start": "2026-08-01", "period_end": "2026-09-01",
			"value": "102.4", "missing_reason": null, "unit": "index_points", "published_at": "2026-09-03T12:30:00Z",
			"received_at": "2026-09-03T12:30:04Z", "available_at": "2026-09-03T12:31:10Z"
		}],
		"position": 37, "snapshot_expires_at": "2026-10-01T01:00:00Z", "next_page_token": null}`)
	r := s.observations("series_id=activity-index&period_start=2026-08-01&period_end=2026-09-01&available_as_of=2026-09-04T00:00:00Z")
	wantOK(t, r)
	if got := strings.TrimSpace(string(r.raw)); got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}

	// The default page size is 100, which holds every observation.
	body := wantOK(t, s.observations("series_id=activity-index"))
	if got := ids(t, body); len(got) != 32 || got[0] != "obs_jan24" || got[31] != "obs_aug26" || body["next_page_token"] != nil {
		t.Errorf("default page: %v, next_page_token %v", got, body["next_page_token"])
	}

	// A cutoff may equal the clock, but not pass it.
	wantOK(t, s.observations("series_id=activity-index&available_as_of=2026-10-01T00:00:00Z"))
	wantProblem(t, s.observations("series_id=activity-index&available_as_of=2026-10-01T00:00:01Z"), 400, "cutoff_in_future", "available_as_of")

	for _, tt := range []struct {
		query     string
		code      string
		parameter any
	}{
		{"", "missing_parameter", "series_id"},
		{"series_id=activity-index&published_as_of=2026-09-04T00:00:00Z", "unknown_parameter", "published_as_of"},
		{"series_id=activity-index&period_start=2026-09-01&period_end=2026-09-01", "invalid_parameter", "period_end"},
		{"series_id=activity-index&period_start=2026-09-01&period_end=2026-08-01", "invalid_parameter", "period_end"},
		{"series_id=activity-index&period_start=2026-02-30", "invalid_parameter", "period_start"},
		{"series_id=activity-index&page_size=0", "invalid_parameter", "page_size"},
		{"series_id=activity-index&page_size=1001", "invalid_parameter", "page_size"},
		{"series_id=activity-index&page_token=", "invalid_parameter", "page_token"},
	} {
		wantProblem(t, s.observations(tt.query), 400, tt.code, tt.parameter)
	}
	wantOK(t, s.observations("series_id=activity-index&page_size=1000"))

	// Lookup comes before entitlement.
	unentitled := req{key: unentitledKey}
	wantProblem(t, s.do("GET", "/v1/observations?series_id=no-such-series", unentitled), 404, "not_found", nil)
	wantProblem(t, s.do("GET", "/v1/observations?series_id=activity-index", unentitled), 403, "not_entitled", nil)

	// Before the first revision, the result is empty at position 0.
	s.test("POST", "/test/reset", `{"clock": "2024-01-01T00:00:00Z"}`)
	if got := strings.TrimSpace(string(s.observations("series_id=activity-index").raw)); got != `{"data":[],"position":0,"snapshot_expires_at":"2024-01-01T01:00:00Z","next_page_token":null}` {
		t.Errorf("before the first revision: %s", got)
	}
}

func TestPagination(t *testing.T) {
	s := newServer(t, true)

	// Pages of 10 hold every observation once, at one position, with one
	// expiry, and the last holds the rest.
	const query = "series_id=activity-index&page_size=10"
	all := ids(t, wantOK(t, s.observations("series_id=activity-index&page_size=1000")))
	var got []string
	body := wantOK(t, s.observations(query))
	for page := 1; ; page++ {
		n := len(ids(t, body))
		if want := min(10, 32-len(got)); n != want {
			t.Errorf("page %d has %d records, want %d", page, n, want)
		}
		if body["position"] != float64(37) || body["snapshot_expires_at"] != "2026-10-01T01:00:00Z" {
			t.Errorf("page %d: position %v, snapshot_expires_at %v", page, body["position"], body["snapshot_expires_at"])
		}
		got = append(got, ids(t, body)...)
		if body["next_page_token"] == nil {
			break
		}
		body = wantOK(t, s.observations(withToken(query, body["next_page_token"])))
	}
	if fmt.Sprint(got) != fmt.Sprint(all) {
		t.Errorf("pages hold %v, want %v", got, all)
	}

	// When the results divide evenly, the last full page has no token.
	p1 := wantOK(t, s.observations("series_id=activity-index&page_size=16"))
	p2 := wantOK(t, s.observations(withToken("series_id=activity-index&page_size=16", p1["next_page_token"])))
	if len(ids(t, p2)) != 16 || p2["next_page_token"] != nil {
		t.Errorf("second of two full pages: %v, next_page_token %v", ids(t, p2), p2["next_page_token"])
	}

	// A query keeps its snapshot, and its cutoff, while the clock moves.
	s.test("POST", "/test/reset", `{"clock": "2026-09-10T12:30:20Z"}`)
	const cutoff = "series_id=activity-index&period_start=2026-07-01&page_size=1&available_as_of=2026-09-10T12:30:20Z"
	p1 = wantOK(t, s.observations(cutoff))
	s.test("PUT", "/test/clock", `{"now": "2026-09-10T12:30:40Z"}`)
	p2 = wantOK(t, s.observations(withToken(cutoff, p1["next_page_token"])))
	if rec := p2["data"].([]any)[0].(map[string]any); rec["revision_id"] != "rev_aug26_1" || p2["position"] != float64(36) || p2["snapshot_expires_at"] != "2026-09-10T13:30:20Z" {
		t.Errorf("resumed page: %v", p2)
	}
	if got := wantOK(t, s.observations("series_id=activity-index&period_start=2026-08-01"))["position"]; got != float64(37) {
		t.Errorf("a new query's position: %v", got)
	}
}

func TestPageTokens(t *testing.T) {
	f := loadFixtures(t)
	srv, err := New(Config{Fixtures: f, ContractVersion: f.ContractVersion, ClockStart: mustTime(t, startClock), TestControl: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	s := &testServer{t: t, url: ts.URL}

	const query = "series_id=activity-index&page_size=10"
	first := func() string {
		t.Helper()
		return wantOK(t, s.observations(query))["next_page_token"].(string)
	}
	token := first()
	wantOK(t, s.observations(withToken(query, token)))

	// A token is refused if any part of it is altered, including bits its
	// encoding leaves unused.
	payload, mac, _ := strings.Cut(token, ".")
	flip := func(s string, i int) string {
		b := []byte(s)
		b[i] = map[bool]byte{true: 'B', false: 'A'}[b[i] == 'A']
		return string(b)
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, mac[len(mac)-1])
	for name, altered := range map[string]string{
		"payload":         flip(payload, len(payload)/2) + "." + mac,
		"signature":       payload + "." + flip(mac, 0),
		"unused bits":     payload + "." + mac[:len(mac)-1] + string(alphabet[last|1]),
		"no signature":    payload,
		"empty signature": payload + ".",
		"padding":         token + "=",
		"another key":     pageToken{Endpoint: "GET /v1/observations", Credential: "cred_research", Params: map[string]string{"series_id": "activity-index", "page_size": "10"}, Position: 37, Snapshot: mustTime(t, startClock).Unix(), Offset: 10}.sign(make([]byte, tokenKeyBytes)),
		"not a token":     "not-a-page-token",
	} {
		if altered == token {
			t.Fatalf("%s: the token is unchanged", name)
		}
		wantProblem(t, s.observations(withToken(query, altered)), 400, "invalid_page_token", "page_token")
	}

	// A token is bound to its endpoint, its credential, and every other
	// parameter.
	srv.state.mu.Lock()
	key := srv.state.tokenKey
	srv.state.mu.Unlock()
	forged := pageToken{Endpoint: "GET /v1/revisions", Credential: "cred_research", Params: map[string]string{"series_id": "activity-index", "page_size": "10"}, Position: 37, Snapshot: mustTime(t, startClock).Unix(), Offset: 10}
	wantProblem(t, s.observations(withToken(query, forged.sign(key))), 400, "page_token_mismatch", "page_token")
	forged.Endpoint = "GET /v1/observations"
	wantOK(t, s.observations(withToken(query, forged.sign(key))))
	for _, q := range []string{
		"series_id=activity-index",
		"series_id=activity-index&page_size=20",
		"series_id=activity-index&page_size=10&period_start=2024-01-01",
		"series_id=activity-index&page_size=10&available_as_of=2026-10-01T00:00:00Z",
		"series_id=no-such-series&page_size=10",
	} {
		wantProblem(t, s.observations(withToken(q, token)), 400, "page_token_mismatch", "page_token")
	}
	// Parameters may come in another order.
	wantOK(t, s.observations("page_size=10&page_token="+url.QueryEscape(token)+"&series_id=activity-index"))
	s.test("PUT", "/test/credentials/cred_unentitled", `{"datasets": ["core-indicators"]}`)
	wantProblem(t, s.do("GET", "/v1/observations?"+withToken(query, token), req{key: unentitledKey}), 400, "page_token_mismatch", "page_token")

	// The snapshot lives for 3,600 seconds from the first page.
	s.test("PUT", "/test/clock", `{"now": "2026-10-01T00:59:59Z"}`)
	if got := wantOK(t, s.observations(withToken(query, token)))["snapshot_expires_at"]; got != "2026-10-01T01:00:00Z" {
		t.Errorf("snapshot_expires_at %v", got)
	}
	s.test("PUT", "/test/clock", `{"now": "2026-10-01T01:00:00Z"}`)
	wantProblem(t, s.observations(withToken(query, token)), 410, "page_token_expired", "page_token")
	// Validation comes before state, and entitlement before state.
	wantProblem(t, s.observations(withToken("series_id=activity-index", token)), 400, "page_token_mismatch", "page_token")
	s.test("PUT", "/test/credentials/cred_research", `{"datasets": []}`)
	wantProblem(t, s.observations(withToken(query, token)), 403, "not_entitled", nil)
	s.test("PUT", "/test/credentials/cred_research", `{"active": false}`)
	wantProblem(t, s.observations(withToken(query, token)), 401, "unauthenticated", nil)

	// A reset replaces the key, even when it keeps the clock, and so does a
	// restart.
	s.test("POST", "/test/reset", ``)
	token = first()
	s.test("POST", "/test/reset", ``)
	wantProblem(t, s.observations(withToken(query, token)), 400, "invalid_page_token", "page_token")
	// A token from this server is refused by another, as by this one after a
	// restart.
	restarted := newServer(t, true)
	wantProblem(t, restarted.observations(withToken(query, first())), 400, "invalid_page_token", "page_token")
}

// TestConcurrentPaging pages through queries while the clock moves and the
// server resets. Run with -race, it checks that the page-token key is
// guarded. A page either continues its query or is refused because a reset
// replaced the key or the clock passed the snapshot's expiry.
func TestConcurrentPaging(t *testing.T) {
	s := newServer(t, true)
	const query = "series_id=activity-index&page_size=5"
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.test("PUT", "/test/clock", fmt.Sprintf(`{"now": "2027-01-0%dT00:00:00Z"}`, i+1))
			s.test("POST", "/test/reset", ``)
		}()
		go func() {
			defer wg.Done()
			r := s.observations(query)
			for r.status == 200 && r.body["next_page_token"] != nil {
				r = s.observations(withToken(query, r.body["next_page_token"]))
			}
			switch code := r.body["code"]; {
			case r.status == 200:
			case r.status == 400 && code == "invalid_page_token":
			case r.status == 410 && code == "page_token_expired":
			default:
				t.Errorf("got %d %s", r.status, r.raw)
			}
		}()
	}
	wg.Wait()
}
