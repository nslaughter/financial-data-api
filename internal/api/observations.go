package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
	"github.com/nslaughter/financial-data-api/internal/history"
)

// cutoffs are the cutoff parameters, in the order a conflict names them, and
// the time field each compares.
var cutoffs = []struct {
	name string
	kind history.CutoffKind
}{
	{"available_as_of", history.AvailableAsOf},
	{"published_as_of", history.PublishedAsOf},
}

// queryObservations returns the selected revision of each observation of a
// series, at the available_as_of or published_as_of cutoff or, without one,
// the clock, among the revisions at or below the query's snapshot position.
func (s *Server) queryObservations(w http.ResponseWriter, c *call) *problem {
	return s.pagedQuery(w, c, s.history.Observations)
}

// listRevisions returns the revision history of a series' observations:
// every revision at or below the query's snapshot position, including
// superseded, erroneous, and withdrawn ones, that the available_as_of cutoff
// admits. It does not define published_as_of, which is unknown_parameter.
func (s *Server) listRevisions(w http.ResponseWriter, c *call) *problem {
	return s.pagedQuery(w, c, s.history.Revisions)
}

// pagedQuery answers a paged query of a series' revisions, which takes the
// cutoff parameters its endpoint defines, at most one at a time, and whose
// result at its snapshot position comes from results. It requires
// entitlement to the series' dataset, on every page.
func (s *Server) pagedQuery(w http.ResponseWriter, c *call, results func(history.Query) []fixtures.Revision) *problem {
	q, p := parseSeriesQuery(c)
	if p != nil {
		return p
	}
	if q.Cutoff, p = parseCutoffs(c); p != nil {
		return p
	}
	pg, p := parsePaging(c)
	if p != nil {
		return p
	}
	series, ok := s.catalog.lookupSeries(q.SeriesID)
	if !ok {
		return notFound("There is no series %q.", q.SeriesID)
	}
	if !c.cred.entitled(series.DatasetID) {
		return notEntitled(series.DatasetID)
	}
	if p := pg.begin(c.now, func() int64 { return s.history.Position(series.DatasetID, c.now) }); p != nil {
		return p
	}
	q.Position = pg.position
	writePage(w, c, pg, results(q))
	return nil
}

// releaseCalendar returns the scheduled releases of a series whose periods
// lie within the range, whatever the clock shows. The calendar is metadata,
// like the catalog, so any customer key may read it without an entitlement.
func (s *Server) releaseCalendar(w http.ResponseWriter, c *call) *problem {
	q, p := parseSeriesQuery(c)
	if p != nil {
		return p
	}
	if _, ok := s.catalog.lookupSeries(q.SeriesID); !ok {
		return notFound("There is no series %q.", q.SeriesID)
	}
	writeJSON(w, http.StatusOK, list[fixtures.Release]{Data: s.history.Releases(q.SeriesID, q.PeriodStart, q.PeriodEnd)})
	return nil
}

// parseSeriesQuery reads series_id, which is required, and the optional
// period range, period_start to period_end. When both bounds are given,
// period_start must be before period_end; an empty or reversed range names
// period_end.
func parseSeriesQuery(c *call) (history.Query, *problem) {
	var q history.Query
	var p *problem
	if q.SeriesID, p = c.query.required("series_id", c.endpoint); p != nil {
		return q, p
	}
	for _, bound := range []struct {
		name string
		dst  **time.Time
	}{
		{"period_start", &q.PeriodStart},
		{"period_end", &q.PeriodEnd},
	} {
		if v, ok := c.query[bound.name]; ok {
			d, p := parseDate(bound.name, v)
			if p != nil {
				return q, p
			}
			*bound.dst = &d
		}
	}
	if q.PeriodStart != nil && q.PeriodEnd != nil && !q.PeriodStart.Before(*q.PeriodEnd) {
		return q, invalidParameter("period_end", "period_end must be after period_start.")
	}
	return q, nil
}

// parseCutoffs reads the query's cutoff, if it gives a cutoff parameter: a
// timestamp at or before the clock. A query takes at most one cutoff, so
// giving two is conflicting_cutoffs, which names no parameter. The query
// holds only the cutoff parameters its endpoint defines.
func parseCutoffs(c *call) (*history.Cutoff, *problem) {
	var given []string
	var kind history.CutoffKind
	for _, cutoff := range cutoffs {
		if _, ok := c.query[cutoff.name]; ok {
			given = append(given, cutoff.name)
			kind = cutoff.kind
		}
	}
	switch {
	case len(given) == 0:
		return nil, nil
	case len(given) > 1:
		return nil, newProblem("conflicting_cutoffs", nil, "%s cannot be combined; a query takes at most one cutoff.", strings.Join(given, " and "))
	}
	name := given[0]
	at, p := parseTimestamp(name, c.query[name])
	if p != nil {
		return nil, p
	}
	if at.After(c.now) {
		return nil, newProblem("cutoff_in_future", named(name), "%s is later than the clock, %s.", name, formatTimestamp(c.now))
	}
	return &history.Cutoff{Kind: kind, At: at}, nil
}

func notEntitled(datasetID string) *problem {
	return newProblem("not_entitled", nil, "The key is not entitled to the dataset %s.", datasetID)
}
