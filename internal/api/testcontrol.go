package api

import (
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
	if _, p := parseQuery(c.r.URL.RawQuery, c.endpoint); p != nil {
		return p
	}
	writeJSON(w, http.StatusOK, clockResponse{Now: formatTimestamp(c.now)})
	return nil
}

// setClock moves the clock forward. The new time is compared with the clock
// as it is when the change is made.
func (s *Server) setClock(w http.ResponseWriter, c *call) *problem {
	if _, p := parseQuery(c.r.URL.RawQuery, c.endpoint); p != nil {
		return p
	}
	body, p := parseBody(c.r, c.endpoint, "now")
	if p != nil {
		return p
	}
	now, present, p := body.timestamp("now")
	switch {
	case p != nil:
		return p
	case !present:
		return missingParameter("now", c.endpoint)
	}
	if p := beforeMaxClock("now", now); p != nil {
		return p
	}
	clock, p := s.state.setClock(now)
	if p != nil {
		return p
	}
	writeJSON(w, http.StatusOK, clockResponse{Now: formatTimestamp(clock)})
	return nil
}

// reset returns the server to its startup state, with an optional clock,
// which may be earlier than the current one.
func (s *Server) reset(w http.ResponseWriter, c *call) *problem {
	if _, p := parseQuery(c.r.URL.RawQuery, c.endpoint); p != nil {
		return p
	}
	body, p := parseBody(c.r, c.endpoint, "clock")
	if p != nil {
		return p
	}
	t, present, p := body.timestamp("clock")
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

// changeCredential changes a customer credential until the next reset. It
// checks the body's form, then that the credential exists, then its kind
// and the datasets it names.
func (s *Server) changeCredential(w http.ResponseWriter, c *call) *problem {
	if _, p := parseQuery(c.r.URL.RawQuery, c.endpoint); p != nil {
		return p
	}
	body, p := parseBody(c.r, c.endpoint, "active", "datasets")
	if p != nil {
		return p
	}
	var change credentialChange
	if change.active, p = body.boolean("active"); p != nil {
		return p
	}
	datasets, present, p := body.stringArray("datasets")
	if p != nil {
		return p
	}
	if present {
		change.datasets = datasets
	}
	cred, p := s.state.changeCredential(c.path["credential_id"], change, s.datasetExists)
	if p != nil {
		return p
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
