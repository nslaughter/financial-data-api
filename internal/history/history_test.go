package history

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/nslaughter/financial-data-api/internal/expected"
	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

func TestPositionBeforeAnyRevision(t *testing.T) {
	f, h := load(t)
	first := timestamp(t, f.Revisions[0].AvailableAt)
	if got := h.Position(expected.StreamDataset, first.Add(-time.Second)); got != 0 {
		t.Errorf("position %d before the first revision, want 0", got)
	}
	if got := h.Position(expected.StreamDataset, first); got != f.Revisions[0].Sequence {
		t.Errorf("position %d at the first revision, want %d", got, f.Revisions[0].Sequence)
	}
	if got := h.Position("no-such-dataset", first); got != 0 {
		t.Errorf("position %d of an unknown dataset, want 0", got)
	}
}

// A snapshot at the position for T contains every revision available at T,
// so a query without a cutoff at that position selects what an
// available_as_of query for T selects at a later position.
func TestCutoffMatchesPositionForItsTime(t *testing.T) {
	f, h := load(t)
	head := h.Position(expected.StreamDataset, timestamp(t, defaultClock))
	for _, r := range f.Revisions {
		at := timestamp(t, r.AvailableAt)
		for _, cutoff := range []time.Time{at.Add(-time.Second), at} {
			atCutoff := h.Observations(Query{
				SeriesID: r.SeriesID,
				Position: head,
				Cutoff:   &Cutoff{Kind: AvailableAsOf, At: cutoff},
			})
			atPosition := h.Observations(Query{
				SeriesID: r.SeriesID,
				Position: h.Position(expected.StreamDataset, cutoff),
			})
			if !reflect.DeepEqual(atCutoff, atPosition) {
				t.Errorf("at %s: available_as_of selects %v, the position selects %v",
					cutoff.Format(fixtures.TimestampLayout), ids(atCutoff), ids(atPosition))
			}
		}
	}
}

func TestPeriodRangeMatchesWholePeriods(t *testing.T) {
	_, h := load(t)
	head := h.Position(expected.StreamDataset, timestamp(t, defaultClock))
	tests := []struct {
		start, end string
		want       []string // period_start of each result
	}{
		{"2026-07-15", "2026-09-01", []string{"2026-08-01"}},
		{"2026-07-01", "2026-08-15", []string{"2026-07-01"}},
		{"2026-07-15", "2026-08-15", []string{}},
		{"2026-07-01", "2026-09-01", []string{"2026-07-01", "2026-08-01"}},
	}
	for _, tt := range tests {
		start, end := date(t, tt.start), date(t, tt.end)
		q := Query{SeriesID: "activity-index", PeriodStart: &start, PeriodEnd: &end, Position: head}
		for name, got := range map[string][]string{
			"Observations": periods(h.Observations(q)),
			"Revisions":    unique(periods(h.Revisions(q))),
		} {
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s from %s to %s: periods %v, want %v", name, tt.start, tt.end, got, tt.want)
			}
		}
	}
}

func TestRevisionsAreOrderedByPeriodThenNumber(t *testing.T) {
	f, h := load(t)
	head := h.Position(expected.StreamDataset, timestamp(t, defaultClock))
	got := h.Revisions(Query{SeriesID: "activity-index", Position: head})
	if len(got) != len(f.Revisions) {
		t.Fatalf("%d revisions, want every one of the %d", len(got), len(f.Revisions))
	}
	for i := 1; i < len(got); i++ {
		a, b := got[i-1], got[i]
		if a.PeriodStart > b.PeriodStart ||
			a.PeriodStart == b.PeriodStart && a.RevisionNumber >= b.RevisionNumber {
			t.Errorf("%s (%s, %d) comes before %s (%s, %d)",
				a.RevisionID, a.PeriodStart, a.RevisionNumber, b.RevisionID, b.PeriodStart, b.RevisionNumber)
		}
	}
}

// The data contract: "With the server's clock at 00:00 on March 4, 2026, this
// query returns February as 1.013, because the correction does not exist yet;
// with the clock after March 5 at 15:20, the same query returns 101.3."
func TestPublishedAsOfConsidersOnlyItsPosition(t *testing.T) {
	_, h := load(t)
	start, end := date(t, "2026-02-01"), date(t, "2026-03-01")
	cutoff := &Cutoff{Kind: PublishedAsOf, At: timestamp(t, "2026-03-04T00:00:00Z")}
	for clock, want := range map[string]string{
		"2026-03-04T00:00:00Z": "1.013",
		"2026-03-05T15:20:00Z": "101.3",
	} {
		got := h.Observations(Query{
			SeriesID:    "activity-index",
			PeriodStart: &start,
			PeriodEnd:   &end,
			Position:    h.Position(expected.StreamDataset, timestamp(t, clock)),
			Cutoff:      cutoff,
		})
		if len(got) != 1 || got[0].Value == nil || *got[0].Value != want {
			t.Errorf("clock %s: %v, want February as %s", clock, ids(got), want)
		}
	}
}

func TestReadChangesRetention(t *testing.T) {
	f, h := load(t)
	first, second := f.Revisions[0], f.Revisions[1]
	expiry := timestamp(t, first.AvailableAt).Add(Retention)
	if !timestamp(t, second.AvailableAt).Add(Retention).After(expiry) {
		t.Fatal("the test needs the second revision to expire after the first")
	}

	// One second before the first event expires, position 0 continues.
	got, err := h.ReadChanges(expected.StreamDataset, expiry.Add(-time.Second), 0, 1)
	if err != nil {
		t.Fatalf("before expiry: %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].Sequence != first.Sequence || got.NextPosition != first.Sequence {
		t.Errorf("before expiry: events %v, next position %d", ids(got.Events), got.NextPosition)
	}

	// At the instant it expires, position 0 needs it and has expired, but
	// the position after it still continues.
	if _, err := h.ReadChanges(expected.StreamDataset, expiry, 0, 1); !errors.Is(err, ErrPositionExpired) {
		t.Errorf("at expiry, after 0: error %v, want ErrPositionExpired", err)
	}
	got, err = h.ReadChanges(expected.StreamDataset, expiry, first.Sequence, 1)
	if err != nil {
		t.Fatalf("at expiry, after %d: %v", first.Sequence, err)
	}
	if len(got.Events) != 1 || got.Events[0].Sequence != second.Sequence {
		t.Errorf("at expiry, after %d: events %v", first.Sequence, ids(got.Events))
	}

	// Long after every event has expired, a position at the head continues
	// with nothing to read, and the position before it has expired.
	last, prev := f.Revisions[len(f.Revisions)-1], f.Revisions[len(f.Revisions)-2]
	late := timestamp(t, last.AvailableAt).Add(2 * Retention)
	got, err = h.ReadChanges(expected.StreamDataset, late, last.Sequence, defaultLimit)
	if err != nil {
		t.Fatalf("caught up: %v", err)
	}
	want := Changes{Events: []fixtures.Revision{}, NextPosition: last.Sequence, HeadPosition: last.Sequence}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("caught up: %+v, want %+v", got, want)
	}
	if _, err := h.ReadChanges(expected.StreamDataset, late, prev.Sequence, defaultLimit); !errors.Is(err, ErrPositionExpired) {
		t.Errorf("one behind the head: error %v, want ErrPositionExpired", err)
	}
}

func TestReadChangesAheadOfTheStream(t *testing.T) {
	f, h := load(t)
	at := timestamp(t, f.Revisions[0].AvailableAt)
	head := h.Position(expected.StreamDataset, at)
	if _, err := h.ReadChanges(expected.StreamDataset, at, head+1, defaultLimit); !errors.Is(err, ErrPositionAhead) {
		t.Errorf("after %d with head %d: error %v, want ErrPositionAhead", head+1, head, err)
	}
	got, err := h.ReadChanges(expected.StreamDataset, at, head, defaultLimit)
	if err != nil {
		t.Fatalf("after the head: %v", err)
	}
	if len(got.Events) != 0 || got.NextPosition != head || got.HeadPosition != head {
		t.Errorf("after the head: %+v", got)
	}
}

// gapFactor multiplies every sequence of the fixtures in gapped.
const gapFactor = 10

// gapped returns the history of the embedded fixtures with every sequence
// multiplied by gapFactor. The fixtures number each dataset's revisions from
// 1 without gaps, so their sequences are also indexes; the contract allows
// gaps (invariant 6).
func gapped(t *testing.T) *History {
	t.Helper()
	f, _ := load(t)
	for i := range f.Revisions {
		f.Revisions[i].Sequence *= gapFactor
	}
	if vs := fixtures.Check(f); len(vs) > 0 {
		t.Fatalf("the gapped fixtures break invariants: %v", vs)
	}
	h, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

// renumber returns revs with every sequence multiplied by gapFactor.
func renumber(revs []fixtures.Revision) []fixtures.Revision {
	out := make([]fixtures.Revision, len(revs))
	for i, r := range revs {
		r.Sequence *= gapFactor
		out[i] = r
	}
	return out
}

// Positions are sequences, not indexes. With gaps between the sequences,
// every rule answers as it does without them, renumbered, and a position
// inside a gap answers as the sequence below it does.
func TestPositionsAreSequences(t *testing.T) {
	f, h := load(t)
	g := gapped(t)

	for _, r := range f.Revisions {
		at := timestamp(t, r.AvailableAt)
		for _, tm := range []time.Time{at.Add(-time.Second), at} {
			if got, want := g.Position(expected.StreamDataset, tm), gapFactor*h.Position(expected.StreamDataset, tm); got != want {
				t.Errorf("position %d at %s, want %d", got, tm.Format(fixtures.TimestampLayout), want)
			}
		}
	}

	now := timestamp(t, defaultClock)
	head := h.Position(expected.StreamDataset, now)
	for p := int64(0); p <= head; p++ {
		positions := []int64{gapFactor * p}
		if p < head {
			positions = append(positions, gapFactor*p+gapFactor/2)
		}
		for _, gp := range positions {
			if got, want := g.Snapshot(expected.StreamDataset, gp), renumber(h.Snapshot(expected.StreamDataset, p)); !reflect.DeepEqual(got, want) {
				t.Errorf("snapshot at %d: %v, want %v", gp, ids(got), ids(want))
			}
			for _, s := range f.Series {
				if s.DatasetID != expected.StreamDataset {
					continue
				}
				gq := Query{SeriesID: s.SeriesID, Position: gp}
				hq := Query{SeriesID: s.SeriesID, Position: p}
				if got, want := g.Observations(gq), renumber(h.Observations(hq)); !reflect.DeepEqual(got, want) {
					t.Errorf("%s observations at %d: %v, want %v", s.SeriesID, gp, ids(got), ids(want))
				}
				if got, want := g.Revisions(gq), renumber(h.Revisions(hq)); !reflect.DeepEqual(got, want) {
					t.Errorf("%s revisions at %d: %v, want %v", s.SeriesID, gp, ids(got), ids(want))
				}
			}
			got, err := g.ReadChanges(expected.StreamDataset, now, gp, 2)
			if err != nil {
				t.Fatalf("read after %d: %v", gp, err)
			}
			want, err := h.ReadChanges(expected.StreamDataset, now, p, 2)
			if err != nil {
				t.Fatalf("read after %d without gaps: %v", p, err)
			}
			want = Changes{
				Events:       renumber(want.Events),
				NextPosition: gapFactor * want.NextPosition,
				HeadPosition: gapFactor * want.HeadPosition,
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("read after %d: events %v, next %d, head %d; want events %v, next %d, head %d",
					gp, ids(got.Events), got.NextPosition, got.HeadPosition,
					ids(want.Events), want.NextPosition, want.HeadPosition)
			}
		}
	}
	if _, err := g.ReadChanges(expected.StreamDataset, now, gapFactor*head+1, defaultLimit); !errors.Is(err, ErrPositionAhead) {
		t.Errorf("after %d with head %d: error %v, want ErrPositionAhead", gapFactor*head+1, gapFactor*head, err)
	}
}

func TestEmptyResultsAreNotNil(t *testing.T) {
	_, h := load(t)
	q := Query{SeriesID: "activity-index", Position: 0}
	if got := h.Observations(q); got == nil {
		t.Error("Observations returned nil")
	}
	if got := h.Revisions(q); got == nil {
		t.Error("Revisions returned nil")
	}
	if got := h.Snapshot(expected.StreamDataset, 0); got == nil {
		t.Error("Snapshot returned nil")
	}
	if len(ExportFile(h.Snapshot(expected.StreamDataset, 0))) != 0 {
		t.Error("the export file of an empty snapshot is not empty")
	}
}

func ids(revs []fixtures.Revision) []string {
	out := make([]string, len(revs))
	for i, r := range revs {
		out[i] = r.RevisionID
	}
	return out
}

func periods(revs []fixtures.Revision) []string {
	out := make([]string, len(revs))
	for i, r := range revs {
		out[i] = r.PeriodStart
	}
	return out
}

// unique removes adjacent repeats from s.
func unique(s []string) []string {
	out := []string{}
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

func TestReleases(t *testing.T) {
	f, h := load(t)
	all := h.Releases("activity-index", nil, nil)
	if len(all) != len(f.Releases) {
		t.Fatalf("%d releases, want every one of the %d", len(all), len(f.Releases))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].PeriodStart >= all[i].PeriodStart {
			t.Errorf("%s comes before %s", all[i-1].PeriodStart, all[i].PeriodStart)
		}
	}
	tests := []struct {
		start, end string // "" for no bound
		want       []string
	}{
		{"2026-07-15", "2026-09-01", []string{"2026-08-01"}},
		{"2026-07-01", "2026-08-15", []string{"2026-07-01"}},
		{"2026-07-15", "2026-08-15", []string{}},
		{"2026-07-01", "2026-09-01", []string{"2026-07-01", "2026-08-01"}},
		{"2026-08-01", "", []string{"2026-08-01"}},
		{"", "2024-02-01", []string{"2024-01-01"}},
	}
	for _, tt := range tests {
		var start, end *time.Time
		if tt.start != "" {
			d := date(t, tt.start)
			start = &d
		}
		if tt.end != "" {
			d := date(t, tt.end)
			end = &d
		}
		var got []string
		for _, r := range h.Releases("activity-index", start, end) {
			got = append(got, r.PeriodStart)
		}
		if got == nil {
			got = []string{}
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("from %q to %q: periods %v, want %v", tt.start, tt.end, got, tt.want)
		}
	}
	if got := h.Releases("no-such-series", nil, nil); got == nil || len(got) != 0 {
		t.Errorf("an unknown series: %v, want an empty slice", got)
	}
}

// The fixture lists the calendar in period_start order, but the data
// contract does not require that order of the file, so Releases must not
// depend on it.
func TestReleasesAreOrderedWhateverTheFileOrder(t *testing.T) {
	f, _ := load(t)
	slices.Reverse(f.Releases)
	if vs := fixtures.Check(f); len(vs) > 0 {
		t.Fatalf("the reversed fixtures break invariants: %v", vs)
	}
	h, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := h.Releases("activity-index", nil, nil)
	if len(got) != len(f.Releases) {
		t.Fatalf("%d releases, want every one of the %d", len(got), len(f.Releases))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].PeriodStart >= got[i].PeriodStart {
			t.Errorf("%s comes before %s", got[i-1].PeriodStart, got[i].PeriodStart)
		}
	}
}
