package exports

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sync"
	"testing"
	"time"

	financialdataapi "github.com/nslaughter/financial-data-api"
	"github.com/nslaughter/financial-data-api/internal/expected"
	"github.com/nslaughter/financial-data-api/internal/fixtures"
	"github.com/nslaughter/financial-data-api/internal/history"
)

// dataset is the fixtures' one dataset.
const dataset = "core-indicators"

// idPattern is the form of the identifiers the store issues.
var idPattern = regexp.MustCompile(`^exp_[0-9a-f]{16}$`)

var createdAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func loadFixtures(t *testing.T) *fixtures.Fixtures {
	t.Helper()
	f, err := fixtures.Load(financialdataapi.Fixtures())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func newHistory(t *testing.T, f *fixtures.Fixtures) *history.History {
	t.Helper()
	h, err := history.New(f)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func create(t *testing.T, s *Store, position int64) Export {
	t.Helper()
	e, err := s.Create(dataset, position, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestCreate creates an export at every position of the fixture dataset, and
// checks that its file is the canonical file of the snapshot at that
// position, whose length and digest the export reports.
func TestCreate(t *testing.T) {
	f := loadFixtures(t)
	h := newHistory(t, f)
	s := NewStore(h)
	head := f.Revisions[len(f.Revisions)-1].Sequence
	for position := int64(0); position <= head; position++ {
		e := create(t, s, position)
		if !idPattern.MatchString(e.ID) {
			t.Errorf("position %d: identifier %q", position, e.ID)
		}
		if e.DatasetID != dataset || e.Position != position || !e.CreatedAt.Equal(createdAt) {
			t.Errorf("position %d: %s at %d, created %s", position, e.DatasetID, e.Position, e.CreatedAt)
		}
		revisions := h.Snapshot(dataset, position)
		want := history.ExportFile(revisions)
		sum := sha256.Sum256(want)
		if e.File != (File{RecordCount: len(revisions), SizeBytes: len(want), SHA256: hex.EncodeToString(sum[:])}) {
			t.Errorf("position %d: file %+v, want %d records, %d bytes", position, e.File, len(revisions), len(want))
		}
		if got := s.FileBytes(e); !bytes.Equal(got, want) {
			t.Errorf("position %d: FileBytes differs from the canonical file", position)
		}
		if got, ok := s.Get(e.ID); !ok || !reflect.DeepEqual(got, e) {
			t.Errorf("position %d: Get returned %+v, %v", position, got, ok)
		}
	}
}

// TestExpectedManifests checks the coverage and file of every expected
// export manifest in expected/ that states them, against an export created
// at the manifest's position. It reads the files with internal/expected and
// compares with its matching rule, as the runner does.
func TestExpectedManifests(t *testing.T) {
	s := NewStore(newHistory(t, loadFixtures(t)))
	files, err := expected.Load()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range []string{"export-handoff", "exports"} {
		for _, sc := range expectedFile(t, files, name).Scenarios {
			for i, step := range sc.Steps {
				if step.Expect == nil {
					continue
				}
				body, _ := step.Expect.Body.Value.(map[string]any)
				position, ok := integerOf(body["position"])
				if !ok || (body["coverage"] == nil && body["files"] == nil) {
					continue
				}
				e := create(t, s, position)
				source := fmt.Sprintf("%s: %s: step %d", name, sc.Name, i+1)
				if want, ok := body["coverage"].(map[string]any); ok {
					if d := expected.Match("coverage", want, asJSON(t, coverageFields(e.Coverage))); d != nil {
						t.Errorf("%s: %s", source, d)
					}
					checked++
				}
				if files, ok := body["files"].([]any); ok {
					if len(files) != 1 {
						t.Fatalf("%s: %d files", source, len(files))
					}
					entry, ok := files[0].(map[string]any)
					if !ok {
						t.Fatalf("%s: file %s is not an object", source, expected.Render(files[0]))
					}
					got := asJSON(t, map[string]any{
						"record_count": e.File.RecordCount,
						"size_bytes":   e.File.SizeBytes,
						"sha256":       e.File.SHA256,
					})
					if d := expected.Match("file", storeMembers(entry), got); d != nil {
						t.Errorf("%s: %s", source, d)
					}
					checked++
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no expected manifest states a coverage or a file")
	}
}

// expectedFile returns the file of files named name.
func expectedFile(t *testing.T, files []*expected.File, name string) *expected.File {
	t.Helper()
	for _, f := range files {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no file expected/%s.json", name)
	return nil
}

// integerOf returns v, a JSON value, when it is an integer that fits an
// int64.
func integerOf(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, ok := expected.Integer(n)
	if !ok || !i.IsInt64() {
		return 0, false
	}
	return i.Int64(), true
}

// asJSON returns v as the runner decodes a response, with json.Number for
// numbers, so that expected.Match compares it as the runner does.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := expected.Decode(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// storeMembers returns a manifest's file without the members the API fixes
// and the store does not know: name, url, and media_type. Every other member
// stays, so one the test cannot check fails the match instead of being
// passed over.
func storeMembers(file map[string]any) map[string]any {
	out := make(map[string]any, len(file))
	for k, v := range file {
		switch k {
		case "name", "url", "media_type":
		default:
			out[k] = v
		}
	}
	return out
}

// coverageFields returns a coverage as its manifest's JSON members.
func coverageFields(c Coverage) map[string]any {
	return map[string]any{
		"series_ids":        c.SeriesIDs,
		"observation_count": c.ObservationCount,
		"revision_count":    c.RevisionCount,
		"period_start":      c.PeriodStart,
		"period_end":        c.PeriodEnd,
	}
}

// TestCoverageOfSeveralSeries adds a series to the fixture dataset whose one
// revision arrives last but has the earliest period_start and the latest
// period_end, and checks that the coverage lists the series ascending, not in
// the order their revisions arrived, and spans every period.
func TestCoverageOfSeveralSeries(t *testing.T) {
	f := loadFixtures(t)
	last := f.Revisions[len(f.Revisions)-1]
	base := create(t, NewStore(newHistory(t, f)), last.Sequence)

	extra := last
	extra.Sequence = last.Sequence + 1
	extra.SeriesID = "a-index"
	extra.ObservationID = "obs_a_index"
	extra.RevisionID = "rev_a_index_1"
	extra.RevisionNumber = 1
	extra.ChangeType = "initial_release"
	extra.PeriodStart, extra.PeriodEnd = "2023-12-01", "2027-01-01"
	f.Series = append(f.Series, fixtures.Series{SeriesID: extra.SeriesID, DatasetID: dataset})
	f.Revisions = append(f.Revisions, extra)

	e := create(t, NewStore(newHistory(t, f)), extra.Sequence)
	want := Coverage{
		SeriesIDs:        []string{"a-index", "activity-index"},
		ObservationCount: base.Coverage.ObservationCount + 1,
		RevisionCount:    base.Coverage.RevisionCount + 1,
		PeriodStart:      &extra.PeriodStart,
		PeriodEnd:        &extra.PeriodEnd,
	}
	if !reflect.DeepEqual(e.Coverage, want) {
		t.Errorf("coverage %s, want %s", show(e.Coverage), show(want))
	}
}

func show(c Coverage) string {
	data, _ := json.Marshal(coverageFields(c))
	return string(data)
}

// TestEmptySnapshot checks an export at position 0: no series, zero counts,
// no periods, and an empty file.
func TestEmptySnapshot(t *testing.T) {
	s := NewStore(newHistory(t, loadFixtures(t)))
	e := create(t, s, 0)
	sum := sha256.Sum256(nil)
	if !reflect.DeepEqual(e.Coverage, Coverage{SeriesIDs: []string{}}) {
		t.Errorf("coverage %+v", e.Coverage)
	}
	if e.File != (File{SHA256: hex.EncodeToString(sum[:])}) {
		t.Errorf("file %+v", e.File)
	}
	if got := s.FileBytes(e); len(got) != 0 {
		t.Errorf("file %q", got)
	}
}

// TestExpiry checks that an export is available until Lifetime after its
// creation, and expired from that instant on.
func TestExpiry(t *testing.T) {
	e := Export{CreatedAt: createdAt}
	if got, want := e.ExpiresAt(), createdAt.Add(86400*time.Second); !got.Equal(want) {
		t.Errorf("expires at %s, want %s", got, want)
	}
	for _, tt := range []struct {
		now     time.Time
		expired bool
	}{
		{createdAt, false},
		{e.ExpiresAt().Add(-time.Second), false},
		{e.ExpiresAt(), true},
		{e.ExpiresAt().Add(time.Second), true},
	} {
		if got := e.Expired(tt.now); got != tt.expired {
			t.Errorf("at %s: expired %v, want %v", tt.now, got, tt.expired)
		}
	}
}

// chunks is a random source that returns its chunks in turn, one per read.
type chunks [][]byte

func (c *chunks) Read(p []byte) (int, error) {
	if len(*c) == 0 {
		return 0, io.EOF
	}
	n := copy(p, (*c)[0])
	*c = (*c)[1:]
	return n, nil
}

// TestIdentifiersAreNeverReused gives the store a random source that repeats
// itself, and checks that no identifier is issued twice, by the store or by
// the stores that replace it when it is cleared.
func TestIdentifiersAreNeverReused(t *testing.T) {
	a, b, c := bytes.Repeat([]byte{0xa1}, idBytes), bytes.Repeat([]byte{0xb2}, idBytes), bytes.Repeat([]byte{0xc3}, idBytes)
	random := chunks{a, a, b, a, b, c}
	s := newStore(newHistory(t, loadFixtures(t)), &issuer{random: &random, issued: map[string]bool{}})

	first := create(t, s, 1)
	second := create(t, s, 1)
	if first.ID != "exp_a1a1a1a1a1a1a1a1" || second.ID != "exp_b2b2b2b2b2b2b2b2" {
		t.Errorf("identifiers %s and %s", first.ID, second.ID)
	}

	cleared := s.Cleared()
	if _, ok := cleared.Get(first.ID); ok {
		t.Error("a cleared store holds an export")
	}
	if _, ok := s.Get(first.ID); !ok {
		t.Error("clearing changed the store it was called on")
	}
	third := create(t, cleared, 1)
	if third.ID != "exp_c3c3c3c3c3c3c3c3" {
		t.Errorf("after clearing, identifier %s", third.ID)
	}
}

// TestRandomSourceFailure checks that an identifier the random source cannot
// supply fails the export, which the store then does not hold.
func TestRandomSourceFailure(t *testing.T) {
	random := chunks{}
	s := newStore(newHistory(t, loadFixtures(t)), &issuer{random: &random, issued: map[string]bool{}})
	if _, err := s.Create(dataset, 1, createdAt); !errors.Is(err, io.EOF) {
		t.Errorf("err %v, want io.EOF", err)
	}
	if len(s.exports) != 0 {
		t.Errorf("the store holds %d exports", len(s.exports))
	}
}

// TestConcurrentCreate creates exports in parallel, and in parallel with
// clearing, and checks that every identifier is distinct.
func TestConcurrentCreate(t *testing.T) {
	s := NewStore(newHistory(t, loadFixtures(t)))
	const n = 32
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store := s
			if i%2 == 1 {
				store = s.Cleared()
			}
			e, err := store.Create(dataset, 37, createdAt)
			ids[i], errs[i] = e.ID, err
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, id := range ids {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if seen[id] {
			t.Errorf("identifier %s issued twice", id)
		}
		seen[id] = true
	}
}
