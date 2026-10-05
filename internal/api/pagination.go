package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// A paged query is evaluated at a snapshot position, the dataset's head
// position when its first page is served. Each page is computed from the
// whole result at that position, so pages never mix states. A page that is
// not the last carries a token, signed with a key the server generates at
// startup and at every reset, that binds the rest of the query to its
// endpoint, its credential, and every other parameter, and records the
// snapshot and the offset of the next result.

const (
	// snapshotLifetime is how long a query's snapshot lives, measured from
	// its first page. A token is accepted while the clock is before the
	// snapshot's time plus this.
	snapshotLifetime = 3600 * time.Second
	defaultPageSize  = 100
	maxPageSize      = 1000
	// tokenKeyBytes is the length of the key that signs page tokens.
	tokenKeyBytes = 32
)

// tokenEncoding encodes a token's parts. It is strict, so a token whose
// encoding was altered without changing the bytes it decodes to is still
// refused.
var tokenEncoding = base64.RawURLEncoding.Strict()

// newTokenKey returns a key for signing page tokens, from crypto/rand.
func newTokenKey() ([]byte, error) {
	key := make([]byte, tokenKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("api: generating a page-token key: %w", err)
	}
	return key, nil
}

// pageToken is what a page token carries.
type pageToken struct {
	// Endpoint, Credential, and Params bind the token to the query that
	// issued it: its endpoint, its credential_id, and every parameter except
	// page_token.
	Endpoint   string            `json:"endpoint"`
	Credential string            `json:"credential"`
	Params     map[string]string `json:"params"`
	// Position is the snapshot position, and Snapshot the clock when the
	// first page was served, in Unix seconds.
	Position int64 `json:"position"`
	Snapshot int64 `json:"snapshot"`
	// Offset is the index of the next page's first result.
	Offset int `json:"offset"`
}

// sign encodes t and signs it with key: the encoded JSON of t, a dot, and
// the encoded HMAC-SHA256 of that JSON.
func (t pageToken) sign(key []byte) string {
	payload, err := json.Marshal(t)
	if err != nil {
		// A token holds only strings and integers, which always encode.
		panic(err)
	}
	return tokenEncoding.EncodeToString(payload) + "." + tokenEncoding.EncodeToString(tokenMAC(key, payload))
}

func tokenMAC(key, payload []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	return m.Sum(nil)
}

// verifyToken returns the token that s encodes if key signed it. A token
// signed with an earlier key, from before the last reset or restart, fails
// like an altered one.
func verifyToken(key []byte, s string) (pageToken, bool) {
	encodedPayload, encodedMAC, ok := strings.Cut(s, ".")
	if !ok {
		return pageToken{}, false
	}
	payload, err := tokenEncoding.DecodeString(encodedPayload)
	if err != nil {
		return pageToken{}, false
	}
	mac, err := tokenEncoding.DecodeString(encodedMAC)
	if err != nil || !hmac.Equal(mac, tokenMAC(key, payload)) {
		return pageToken{}, false
	}
	var t pageToken
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil || t.Offset < 0 {
		return pageToken{}, false
	}
	return t, true
}

// paging is where a page of a query starts.
type paging struct {
	size int
	// params holds every parameter of the request except page_token, which
	// a token binds.
	params map[string]string
	// resumed is true when the request carries a page token. Its snapshot
	// then comes from the token; otherwise the first page fixes it.
	resumed  bool
	position int64
	snapshot time.Time
	offset   int
}

// parsePaging reads page_size and page_token, which every paged endpoint
// defines. A token must carry the server's current signature
// (invalid_page_token) and have been issued for this endpoint, this
// credential, and every other parameter as the request gives it, including
// page_size and including omitted parameters left omitted
// (page_token_mismatch).
func parsePaging(c *call, params queryParams) (*paging, *problem) {
	pg := &paging{size: defaultPageSize, params: maps.Clone(params)}
	delete(pg.params, "page_token")
	if v, ok := params["page_size"]; ok {
		n, p := parseInteger("page_size", v, 1, maxPageSize)
		if p != nil {
			return nil, p
		}
		pg.size = int(n)
	}
	v, ok := params["page_token"]
	if !ok {
		return pg, nil
	}
	t, ok := verifyToken(c.tokenKey, v)
	if !ok {
		return nil, newProblem("invalid_page_token", named("page_token"), "The page token is malformed, altered, or was issued before the server last reset or restarted.")
	}
	switch {
	case t.Endpoint != c.endpoint:
		return nil, pageTokenMismatch("The page token was issued for %s, not %s.", t.Endpoint, c.endpoint)
	case t.Credential != c.cred.id:
		return nil, pageTokenMismatch("The page token was issued to another credential.")
	case !maps.Equal(t.Params, pg.params):
		return nil, pageTokenMismatch("The parameters differ from those of the query's first page; send every parameter except page_token as the first request did.")
	}
	pg.resumed = true
	pg.position = t.Position
	pg.snapshot = time.Unix(t.Snapshot, 0).UTC()
	pg.offset = t.Offset
	return pg, nil
}

func pageTokenMismatch(format string, args ...any) *problem {
	return newProblem("page_token_mismatch", named("page_token"), format, args...)
}

// expiresAt is when the query's snapshot expires.
func (pg *paging) expiresAt() time.Time {
	return pg.snapshot.Add(snapshotLifetime)
}

// begin fixes the snapshot of a query's first page at the clock and the
// dataset's head position. A resumed page has a live snapshot already, or
// page_token_expired if its snapshot has expired at the clock.
func (pg *paging) begin(now time.Time, head func() int64) *problem {
	if pg.resumed {
		if !now.Before(pg.expiresAt()) {
			return newProblem("page_token_expired", named("page_token"), "The query's snapshot expired at %s; restart the query.", formatTimestamp(pg.expiresAt()))
		}
		return nil
	}
	pg.position, pg.snapshot = head(), now
	return nil
}

// pageResponse is a page of a paged endpoint.
type pageResponse struct {
	Data              []fixtures.Revision `json:"data"`
	Position          int64               `json:"position"`
	SnapshotExpiresAt string              `json:"snapshot_expires_at"`
	NextPageToken     *string             `json:"next_page_token"`
}

// writePage writes the page of results, the whole result at the snapshot
// position, that starts at the paging's offset. It carries a token when
// results remain after it, so a page is empty only when the whole result
// is. A token's offset is always within its result, since the result at a
// position never changes.
func writePage(w http.ResponseWriter, c *call, pg *paging, results []fixtures.Revision) {
	start := min(pg.offset, len(results))
	end := min(start+pg.size, len(results))
	resp := pageResponse{
		Data:              results[start:end],
		Position:          pg.position,
		SnapshotExpiresAt: formatTimestamp(pg.expiresAt()),
	}
	if end < len(results) {
		next := pageToken{
			Endpoint:   c.endpoint,
			Credential: c.cred.id,
			Params:     pg.params,
			Position:   pg.position,
			Snapshot:   pg.snapshot.Unix(),
			Offset:     end,
		}.sign(c.tokenKey)
		resp.NextPageToken = &next
	}
	writeJSON(w, http.StatusOK, resp)
}
