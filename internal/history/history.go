// Package history implements the data contract's rules over the fixture
// revisions: positions in a dataset's change stream, selection at the
// available_as_of and published_as_of cutoffs, the revision history,
// change-stream reads with retention, the canonical bytes of an export file,
// and the entries of the release calendar in a period range. It knows nothing
// of HTTP: the API parses requests, reads the clock, and passes the clock or
// a snapshot position in.
//
// A revision is visible when its available_at is at or before the clock.
// Because available_at never decreases as sequence increases (invariant 7),
// a dataset's visible revisions are exactly those with sequence at or below
// its head position, Position(datasetID, clock). Every rule here takes the
// clock or a position at or below the head, and considers only the revisions
// at or below that position.
package history

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// Retention is how long the change stream keeps an event, measured from its
// available_at. The event expires at that instant.
const Retention = 1095 * 24 * time.Hour

// The errors ReadChanges returns.
var (
	// ErrPositionAhead reports a position after the dataset's head position.
	ErrPositionAhead = errors.New("position is ahead of the stream")
	// ErrPositionExpired reports a position whose next event is past
	// retention.
	ErrPositionExpired = errors.New("position has expired")
)

// History holds every dataset's revisions and every series' release
// calendar. It does not change after New, so it is safe for concurrent use.
type History struct {
	datasets map[string][]revision // each dataset's revisions, in sequence order
	series   map[string]string     // the dataset of each series
	releases map[string][]release  // each series' calendar, by period_start
}

// revision is a fixture revision with its dates and timestamps parsed.
type revision struct {
	fixtures.Revision
	periodStart, periodEnd   time.Time
	publishedAt, availableAt time.Time
}

// release is an entry of the release calendar with its period parsed.
type release struct {
	fixtures.Release
	periodStart, periodEnd time.Time
}

// New builds the history of fixtures that pass the invariants, as
// fixtures.Load returns them.
func New(f *fixtures.Fixtures) (*History, error) {
	h := &History{
		datasets: make(map[string][]revision),
		series:   make(map[string]string, len(f.Series)),
		releases: make(map[string][]release),
	}
	for _, s := range f.Series {
		h.series[s.SeriesID] = s.DatasetID
	}
	for _, r := range f.Revisions {
		dataset, ok := h.series[r.SeriesID]
		if !ok {
			return nil, fmt.Errorf("revision %s: unknown series %q", r.RevisionID, r.SeriesID)
		}
		rev := revision{Revision: r}
		times := []struct {
			dst         *time.Time
			parse       func(string) (time.Time, bool)
			name, value string
		}{
			{&rev.periodStart, fixtures.ParseDate, "period_start", r.PeriodStart},
			{&rev.periodEnd, fixtures.ParseDate, "period_end", r.PeriodEnd},
			{&rev.publishedAt, fixtures.ParseTimestamp, "published_at", r.PublishedAt},
			{&rev.availableAt, fixtures.ParseTimestamp, "available_at", r.AvailableAt},
		}
		for _, tm := range times {
			t, ok := tm.parse(tm.value)
			if !ok {
				return nil, fmt.Errorf("revision %s: %s %q is not valid", r.RevisionID, tm.name, tm.value)
			}
			*tm.dst = t
		}
		h.datasets[dataset] = append(h.datasets[dataset], rev)
	}
	for _, e := range f.Releases {
		rel := release{Release: e}
		var ok bool
		if rel.periodStart, ok = fixtures.ParseDate(e.PeriodStart); !ok {
			return nil, fmt.Errorf("release of %s: period_start %q is not valid", e.SeriesID, e.PeriodStart)
		}
		if rel.periodEnd, ok = fixtures.ParseDate(e.PeriodEnd); !ok {
			return nil, fmt.Errorf("release of %s for %s: period_end %q is not valid", e.SeriesID, e.PeriodStart, e.PeriodEnd)
		}
		h.releases[e.SeriesID] = append(h.releases[e.SeriesID], rel)
	}
	for _, rels := range h.releases {
		sort.SliceStable(rels, func(i, j int) bool { return rels[i].periodStart.Before(rels[j].periodStart) })
	}
	return h, nil
}

// Position returns the dataset's position for time t: the highest sequence
// among its revisions with available_at at or before t, or 0 if there is
// none. At the clock, it is the dataset's head position.
func (h *History) Position(datasetID string, t time.Time) int64 {
	revs := h.datasets[datasetID]
	n := sort.Search(len(revs), func(i int) bool { return revs[i].availableAt.After(t) })
	if n == 0 {
		return 0
	}
	return revs[n-1].Sequence
}

// upTo returns the dataset's revisions with sequence at or below position,
// in sequence order.
func (h *History) upTo(datasetID string, position int64) []revision {
	revs := h.datasets[datasetID]
	n := sort.Search(len(revs), func(i int) bool { return revs[i].Sequence > position })
	return revs[:n]
}

// CutoffKind names the time field a cutoff compares.
type CutoffKind int

const (
	// AvailableAsOf compares available_at: the result is what an entitled
	// customer could retrieve at the cutoff.
	AvailableAsOf CutoffKind = iota + 1
	// PublishedAsOf compares published_at: the result is what the source had
	// published by the cutoff, with the provider's corrections applied.
	PublishedAsOf
)

// Cutoff limits a query to the revisions whose time field, named by Kind, is
// at or before At. The boundary is inclusive.
type Cutoff struct {
	Kind CutoffKind
	At   time.Time
}

// admits reports whether r passes the cutoff. A nil cutoff admits every
// revision.
func (c *Cutoff) admits(r *revision) bool {
	if c == nil {
		return true
	}
	switch c.Kind {
	case AvailableAsOf:
		return !r.availableAt.After(c.At)
	case PublishedAsOf:
		return !r.publishedAt.After(c.At)
	}
	panic(fmt.Sprintf("history: unknown cutoff kind %d", c.Kind))
}

// Query names a series' observations in a period range, evaluated at a
// snapshot position.
type Query struct {
	SeriesID string
	// PeriodStart and PeriodEnd bound the range when they are not nil. An
	// observation is in the range when its period_start is at or after
	// PeriodStart and its period_end is at or before PeriodEnd, so that its
	// whole period lies within the range.
	PeriodStart, PeriodEnd *time.Time
	// Position is the snapshot position, at or below the head position of
	// the series' dataset. Only revisions with sequence at or below it are
	// considered, whatever arrives later.
	Position int64
	// Cutoff, when not nil, further limits the revisions considered.
	Cutoff *Cutoff
}

// inRange reports whether r's period lies within the query's range.
func (q *Query) inRange(r *revision) bool {
	return withinRange(r.periodStart, r.periodEnd, q.PeriodStart, q.PeriodEnd)
}

// withinRange reports whether the period from periodStart up to periodEnd
// lies entirely within the range from start up to end, either of which may
// be nil for no bound: periodStart is at or after start, and periodEnd is at
// or before end.
func withinRange(periodStart, periodEnd time.Time, start, end *time.Time) bool {
	return (start == nil || !periodStart.Before(*start)) &&
		(end == nil || !periodEnd.After(*end))
}

// considered returns the revisions the query considers: those of its series
// and range, at or below its position, that its cutoff admits.
func (h *History) considered(q Query) []revision {
	var out []revision
	revs := h.upTo(h.series[q.SeriesID], q.Position)
	for i := range revs {
		r := &revs[i]
		if r.SeriesID == q.SeriesID && q.inRange(r) && q.Cutoff.admits(r) {
			out = append(out, *r)
		}
	}
	return out
}

// Observations returns the selected revision of each observation in the
// query's range: among the revisions the query considers, the one with the
// highest revision_number, whatever order they arrived in. An observation
// without a considered revision is absent. A withdrawal, or a released
// observation without a value, is returned like any other selected revision.
// Without a cutoff, the result is the latest revision of each observation at
// the position. Results are ordered by period_start, ascending, and the slice
// is never nil.
func (h *History) Observations(q Query) []fixtures.Revision {
	selected := make(map[string]revision)
	for _, r := range h.considered(q) {
		if s, ok := selected[r.ObservationID]; !ok || r.RevisionNumber > s.RevisionNumber {
			selected[r.ObservationID] = r
		}
	}
	revs := make([]revision, 0, len(selected))
	for _, r := range selected {
		revs = append(revs, r)
	}
	// A series has one observation per period (invariant 3).
	sort.Slice(revs, func(i, j int) bool { return revs[i].periodStart.Before(revs[j].periodStart) })
	return records(revs)
}

// Revisions returns every revision the query considers, including
// superseded, erroneous, and withdrawn ones: the revision history. Results
// are ordered by period_start, then revision_number, both ascending, and the
// slice is never nil. The API offers the revision history only the
// AvailableAsOf cutoff.
func (h *History) Revisions(q Query) []fixtures.Revision {
	revs := h.considered(q)
	sort.Slice(revs, func(i, j int) bool {
		a, b := revs[i], revs[j]
		if !a.periodStart.Equal(b.periodStart) {
			return a.periodStart.Before(b.periodStart)
		}
		return a.RevisionNumber < b.RevisionNumber
	})
	return records(revs)
}

// records returns the fixture records of revs, in the same order and never
// nil. It is the one place revisions become fixture records.
func records(revs []revision) []fixtures.Revision {
	out := make([]fixtures.Revision, len(revs))
	for i := range revs {
		out[i] = revs[i].Revision
	}
	return out
}

// Changes is one read of a dataset's change stream.
type Changes struct {
	// Events are the revisions read, in sequence order. Never nil.
	Events []fixtures.Revision
	// NextPosition is the sequence of the last event read, or the position
	// read after when no event was read.
	NextPosition int64
	// HeadPosition is the dataset's head position.
	HeadPosition int64
}

// ReadChanges reads the dataset's change stream at the clock now: up to limit
// visible revisions with sequence greater than after, in sequence order.
// after is 0 or more, and limit is 1 or more.
//
// It returns ErrPositionAhead if after is greater than the head position, and
// ErrPositionExpired if the first visible revision after it is past retention
// at now. Events expire in sequence order, so when the first is retained, the
// rest are too, and a position equal to the head never expires.
func (h *History) ReadChanges(datasetID string, now time.Time, after int64, limit int) (Changes, error) {
	head := h.Position(datasetID, now)
	if after > head {
		return Changes{}, ErrPositionAhead
	}
	visible := h.upTo(datasetID, head)
	first := sort.Search(len(visible), func(i int) bool { return visible[i].Sequence > after })
	pending := visible[first:]
	if len(pending) > 0 && !now.Before(pending[0].availableAt.Add(Retention)) {
		return Changes{}, ErrPositionExpired
	}
	if len(pending) > limit {
		pending = pending[:limit]
	}
	c := Changes{Events: records(pending), NextPosition: after, HeadPosition: head}
	if len(pending) > 0 {
		c.NextPosition = pending[len(pending)-1].Sequence
	}
	return c, nil
}

// Snapshot returns the dataset's revisions with sequence at or below
// position, in sequence order: the content of an export at that position,
// which is at or below the head position. The slice is never nil.
func (h *History) Snapshot(datasetID string, position int64) []fixtures.Revision {
	return records(h.upTo(datasetID, position))
}

// Releases returns the release calendar's entries for the series whose
// periods lie entirely within the range from start up to end, either of which
// may be nil for no bound, as for a query. The calendar is a published plan,
// so it does not depend on the clock. Results are ordered by period_start,
// ascending, and the slice is never nil.
func (h *History) Releases(seriesID string, start, end *time.Time) []fixtures.Release {
	out := []fixtures.Release{}
	for _, r := range h.releases[seriesID] {
		if withinRange(r.periodStart, r.periodEnd, start, end) {
			out = append(out, r.Release)
		}
	}
	return out
}
