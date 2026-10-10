package api

import (
	"errors"
	"log"
	"net/http"
	"time"
)

// The /test endpoints, served only with TEST_CONTROL=enabled.

type clockResponse struct {
	Now string `json:"now"`
}

type credentialResponse struct {
	CredentialID string   `json:"credential_id"`
	Kind         string   `json:"kind"`
	Active       bool     `json:"active"`
	Datasets     []string `json:"datasets"`
}

func (s *Server) getClock(w http.ResponseWriter, c *call) *problem {
	writeJSON(w, http.StatusOK, clockResponse{Now: formatTimestamp(c.now)})
	return nil
}

// setClock moves the clock forward. The new time is compared with the clock
// as it is when the change is made.
func (s *Server) setClock(w http.ResponseWriter, c *call) *problem {
	now, present, p := c.body.timestamp("now")
	switch {
	case p != nil:
		return p
	case !present:
		return missingParameter("now", c.endpoint)
	}
	if p := beforeMaxClock("now", now); p != nil {
		return p
	}
	clock, err := s.state.setClock(now)
	var backwards *clockBackwardsError
	switch {
	case errors.As(err, &backwards):
		return newProblem("clock_backwards", named("now"), "now is before the clock, %s; the clock moves only forward.", formatTimestamp(backwards.clock))
	case err != nil:
		log.Printf("api: setting the clock: %v", err)
		return newProblem("internal", nil, "The clock could not be set.")
	}
	writeJSON(w, http.StatusOK, clockResponse{Now: formatTimestamp(clock)})
	return nil
}

// reset returns the server to its startup state, with an optional clock,
// which may be earlier than the current one.
func (s *Server) reset(w http.ResponseWriter, c *call) *problem {
	t, present, p := c.body.timestamp("clock")
	if p != nil {
		return p
	}
	var clock *time.Time
	if present {
		if p := beforeMaxClock("clock", t); p != nil {
			return p
		}
		clock = &t
	}
	now, err := s.state.reset(clock)
	if err != nil {
		log.Printf("api: resetting: %v", err)
		return newProblem("internal", nil, "The server could not reset.")
	}
	writeJSON(w, http.StatusOK, clockResponse{Now: formatTimestamp(now)})
	return nil
}

// changeCredential changes a customer credential until the next reset. The
// body's form is checked before the handler runs, then the values of its
// fields, then that the credential exists, and then its kind and the
// datasets it names.
func (s *Server) changeCredential(w http.ResponseWriter, c *call) *problem {
	var change credentialChange
	var p *problem
	if change.active, p = c.body.boolean("active"); p != nil {
		return p
	}
	datasets, present, p := c.body.stringArray("datasets")
	if p != nil {
		return p
	}
	if present {
		change.datasets = datasets
	}
	id := c.path["credential_id"]
	cred, err := s.state.changeCredential(id, change, s.catalog)
	var kind *credentialKindError
	var unknown *unknownDatasetError
	switch {
	case errors.Is(err, errNoCredential):
		return notFound("There is no credential %q.", id)
	case errors.As(err, &kind):
		return invalidParameter("credential_id", "%s is a %s credential; only a customer credential can be changed.", id, kind.kind)
	case errors.As(err, &unknown):
		return invalidParameter("datasets", "There is no dataset %q.", unknown.datasetID)
	case err != nil:
		log.Printf("api: changing the credential %s: %v", id, err)
		return newProblem("internal", nil, "The credential could not be changed.")
	}
	datasets = cred.datasets
	if datasets == nil {
		datasets = []string{}
	}
	writeJSON(w, http.StatusOK, credentialResponse{CredentialID: cred.id, Kind: cred.kind, Active: cred.active, Datasets: datasets})
	return nil
}

// beforeMaxClock refuses a clock later than MaxClock.
func beforeMaxClock(name string, t time.Time) *problem {
	if t.After(MaxClock) {
		return invalidParameter(name, "%s is later than %s, the latest the clock may be.", name, formatTimestamp(MaxClock))
	}
	return nil
}
