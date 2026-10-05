package api

import (
	"net/http"
	"time"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
	"github.com/nslaughter/financial-data-api/internal/history"
)

// queryObservations returns the selected revision of each observation of a
// series, at the available_as_of cutoff or, without one, the clock, among
// the revisions at or below the query's snapshot position. A stage 1 server
// does not define published_as_of, so it is unknown_parameter.
func (s *Server) queryObservations(w http.ResponseWriter, c *call) *problem {
	params, p := parseQuery(c.r.URL.RawQuery, c.endpoint,
		"series_id", "period_start", "period_end", "available_as_of", "page_size", "page_token")
	if p != nil {
		return p
	}
	q, p := parseSeriesQuery(c, params)
	if p != nil {
		return p
	}
	if v, ok := params["available_as_of"]; ok {
		at, p := parseCutoff(c, "available_as_of", v)
		if p != nil {
			return p
		}
		q.Cutoff = &history.Cutoff{Kind: history.AvailableAsOf, At: at}
	}
	pg, p := parsePaging(c, params)
	if p != nil {
		return p
	}
	series, ok := s.findSeries(q.SeriesID)
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
	writePage(w, c, pg, s.history.Observations(q))
	return nil
}

// parseSeriesQuery reads series_id, which is required, and the optional
// period range, period_start to period_end. When both bounds are given,
// period_start must be before period_end; an empty or reversed range names
// period_end.
func parseSeriesQuery(c *call, params queryParams) (history.Query, *problem) {
	var q history.Query
	var p *problem
	if q.SeriesID, p = params.required("series_id", c.endpoint); p != nil {
		return q, p
	}
	for _, bound := range []struct {
		name string
		dst  **time.Time
	}{
		{"period_start", &q.PeriodStart},
		{"period_end", &q.PeriodEnd},
	} {
		if v, ok := params[bound.name]; ok {
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

// parseCutoff reads a cutoff, a timestamp at or before the clock.
func parseCutoff(c *call, name, value string) (time.Time, *problem) {
	at, p := parseTimestamp(name, value)
	if p != nil {
		return at, p
	}
	if at.After(c.now) {
		return at, newProblem("cutoff_in_future", named(name), "%s is later than the clock, %s.", name, formatTimestamp(c.now))
	}
	return at, nil
}

// findSeries returns the series of the catalog with the given id.
func (s *Server) findSeries(id string) (fixtures.Series, bool) {
	for _, sr := range s.series {
		if sr.SeriesID == id {
			return sr, true
		}
	}
	return fixtures.Series{}, false
}

func notEntitled(datasetID string) *problem {
	return newProblem("not_entitled", nil, "The key is not entitled to the dataset %s.", datasetID)
}
