package history

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

func TestPositionBeforeAnyRevision(t *testing.T) {
	f, h := load(t)
	first := timestamp(t, f.Revisions[0].AvailableAt)
	if got := h.Position(streamDataset, first.Add(-time.Second)); got != 0 {
		t.Errorf("position %d before the first revision, want 0", got)
	}
	if got := h.Position(streamDataset, first); got != f.Revisions[0].Sequence {
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
	head := h.Position(streamDataset, timestamp(t, defaultClock))
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
				Position: h.Position(streamDataset, cutoff),
			})
			if !reflect.DeepEqual(atCutoff, atPosition) {
				t.Errorf("at %s: available_as_of selects %v, the position selects %v",
					cutoff.Format(timestampLayout), ids(atCutoff), ids(atPosition))
			}
		}
	}
}

func TestPeriodRangeMatchesWholePeriods(t *testing.T) {
	_, h := load(t)
	head := h.Position(streamDataset, timestamp(t, defaultClock))
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
	head := h.Position(streamDataset, timestamp(t, defaultClock))
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
			Position:    h.Position(streamDataset, timestamp(t, clock)),
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
	got, err := h.ReadChanges(streamDataset, expiry.Add(-time.Second), 0, 1)
	if err != nil {
		t.Fatalf("before expiry: %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].Sequence != first.Sequence || got.NextPosition != first.Sequence {
		t.Errorf("before expiry: events %v, next position %d", ids(got.Events), got.NextPosition)
	}

	// At the instant it expires, position 0 needs it and has expired, but
	// the position after it still continues.
	if _, err := h.ReadChanges(streamDataset, expiry, 0, 1); !errors.Is(err, ErrPositionExpired) {
		t.Errorf("at expiry, after 0: error %v, want ErrPositionExpired", err)
	}
	got, err = h.ReadChanges(streamDataset, expiry, first.Sequence, 1)
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
	got, err = h.ReadChanges(streamDataset, late, last.Sequence, defaultLimit)
	if err != nil {
		t.Fatalf("caught up: %v", err)
	}
	want := Changes{Events: []fixtures.Revision{}, NextPosition: last.Sequence, HeadPosition: last.Sequence}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("caught up: %+v, want %+v", got, want)
	}
	if _, err := h.ReadChanges(streamDataset, late, prev.Sequence, defaultLimit); !errors.Is(err, ErrPositionExpired) {
		t.Errorf("one behind the head: error %v, want ErrPositionExpired", err)
	}
}

func TestReadChangesAheadOfTheStream(t *testing.T) {
	f, h := load(t)
	at := timestamp(t, f.Revisions[0].AvailableAt)
	head := h.Position(streamDataset, at)
	if _, err := h.ReadChanges(streamDataset, at, head+1, defaultLimit); !errors.Is(err, ErrPositionAhead) {
		t.Errorf("after %d with head %d: error %v, want ErrPositionAhead", head+1, head, err)
	}
	got, err := h.ReadChanges(streamDataset, at, head, defaultLimit)
	if err != nil {
		t.Fatalf("after the head: %v", err)
	}
	if len(got.Events) != 0 || got.NextPosition != head || got.HeadPosition != head {
		t.Errorf("after the head: %+v", got)
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
	if got := h.Snapshot(streamDataset, 0); got == nil {
		t.Error("Snapshot returned nil")
	}
	if len(ExportFile(h.Snapshot(streamDataset, 0))) != 0 {
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
