package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
	"github.com/nslaughter/financial-data-api/internal/history"
)

const (
	defaultLimit = 100
	maxLimit     = 1000
)

// changesResponse is a read of a dataset's change stream.
type changesResponse struct {
	Data         []fixtures.Revision `json:"data"`
	NextPosition int64               `json:"next_position"`
	HeadPosition int64               `json:"head_position"`
}

// readChanges reads a dataset's change stream: up to limit visible revisions
// with sequence greater than after, in sequence order. after is a position
// below 2^53, like every integer the API serves. A position ahead of the
// head, or one whose next event is past retention, is a state error, which
// comes after lookup and entitlement.
func (s *Server) readChanges(w http.ResponseWriter, c *call) *problem {
	params, p := parseQuery(c.r.URL.RawQuery, c.endpoint, "after", "limit")
	if p != nil {
		return p
	}
	v, p := params.required("after", c.endpoint)
	if p != nil {
		return p
	}
	after, p := parseInteger("after", v, 0, fixtures.MaxInteger)
	if p != nil {
		return p
	}
	limit := int64(defaultLimit)
	if v, ok := params["limit"]; ok {
		if limit, p = parseInteger("limit", v, 1, maxLimit); p != nil {
			return p
		}
	}
	id := c.path["dataset_id"]
	if !s.datasetExists(id) {
		return notFound("There is no dataset %q.", id)
	}
	if !c.cred.entitled(id) {
		return notEntitled(id)
	}
	changes, err := s.history.ReadChanges(id, c.now, after, int(limit))
	switch {
	case errors.Is(err, history.ErrPositionAhead):
		return newProblem("position_ahead", named("after"),
			"after is %d, but the head position of %s is %d; a position past the head comes from a later clock, before a reset.",
			after, id, s.history.Position(id, c.now))
	case errors.Is(err, history.ErrPositionExpired):
		return newProblem("position_expired", named("after"),
			"The event after position %d is past retention, 1,095 days from its available_at. Load a fresh snapshot, then continue from its position.",
			after)
	case err != nil:
		log.Printf("api: reading the change stream of %s: %v", id, err)
		return newProblem("internal", nil, "The change stream could not be read.")
	}
	writeJSON(w, http.StatusOK, changesResponse{
		Data:         changes.Events,
		NextPosition: changes.NextPosition,
		HeadPosition: changes.HeadPosition,
	})
	return nil
}
