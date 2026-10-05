package api

import (
	"slices"
	"sync"
	"time"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// MaxClock is the latest the simulated clock may be: the latest instant
// whose derived timestamps, such as an export's expires_at a day later,
// still fit the timestamp format.
var MaxClock = time.Date(9999, 12, 30, 23, 59, 59, 0, time.UTC)

// The kinds of credential in fixtures/credentials.json.
const (
	customerKind    = "customer"
	testControlKind = "test_control"
)

// credential is a credential as the server holds it. Its key and kind never
// change; test control changes whether it is active and its datasets.
type credential struct {
	id, key, kind string
	active        bool
	datasets      []string
}

// entitled reports whether the credential may read the dataset's data.
func (c *credential) entitled(datasetID string) bool {
	return slices.Contains(c.datasets, datasetID)
}

// state is everything requests can change: the clock and the credentials.
// Every read and change holds mu, so a request sees the clock and its
// credential as they were at one moment.
type state struct {
	mu          sync.Mutex
	clock       time.Time
	credentials map[string]*credential // by credential_id
	ids         map[string]string      // credential_id by api_key

	// start and initial are what a reset restores: CLOCK_START and the
	// fixture credentials.
	start   time.Time
	initial []fixtures.Credential
}

func newState(start time.Time, initial []fixtures.Credential) *state {
	s := &state{start: start, initial: initial, ids: make(map[string]string, len(initial))}
	for _, c := range initial {
		s.ids[c.APIKey] = c.CredentialID
	}
	s.restore(start)
	return s
}

// restore sets the clock and restores every credential from the fixtures.
// The caller holds mu, or has the only reference to s.
func (s *state) restore(clock time.Time) {
	s.clock = clock
	s.credentials = make(map[string]*credential, len(s.initial))
	for _, c := range s.initial {
		s.credentials[c.CredentialID] = &credential{
			id:       c.CredentialID,
			key:      c.APIKey,
			kind:     c.Kind,
			active:   c.Active,
			datasets: slices.Clone(c.Datasets),
		}
	}
}

// now reads the clock.
func (s *state) now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

// view reads the clock and a copy of the credential whose api_key is key,
// together, or nil if no credential has that key.
func (s *state) view(key string) (time.Time, *credential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.credentials[s.ids[key]]
	if !ok {
		return s.clock, nil
	}
	copied := *c
	copied.datasets = slices.Clone(c.datasets)
	return s.clock, &copied
}

// setClock moves the clock forward to t. A time before the clock is
// clock_backwards; the current time changes nothing.
func (s *state) setClock(t time.Time) (time.Time, *problem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.Before(s.clock) {
		return s.clock, newProblem("clock_backwards", named("now"), "now is before the clock, %s; the clock moves only forward.", formatTimestamp(s.clock))
	}
	s.clock = t
	return s.clock, nil
}

// reset returns the state to its startup state, with the clock at clock, or
// at CLOCK_START if clock is nil.
func (s *state) reset(clock *time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.start
	if clock != nil {
		t = *clock
	}
	s.restore(t)
	return s.clock
}

// credentialChange is a change PUT /test/credentials/{credential_id} makes:
// a nil field is unchanged.
type credentialChange struct {
	active   *bool
	datasets []string
}

// changeCredential applies change to the credential id, after checking, in
// the order of spec/api.md, that it exists, that it is a customer
// credential, and that every dataset it names exists. It returns a copy of
// the credential as changed.
func (s *state) changeCredential(id string, change credentialChange, datasetExists func(string) bool) (*credential, *problem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.credentials[id]
	if !ok {
		return nil, notFound("There is no credential %q.", id)
	}
	if c.kind != customerKind {
		return nil, invalidParameter("credential_id", "%s is a %s credential; only a customer credential can be changed.", id, c.kind)
	}
	var datasets []string
	if change.datasets != nil {
		datasets = make([]string, 0, len(change.datasets))
		for _, d := range change.datasets {
			if !datasetExists(d) {
				return nil, invalidParameter("datasets", "There is no dataset %q.", d)
			}
			if !slices.Contains(datasets, d) {
				datasets = append(datasets, d)
			}
		}
	}
	if change.active != nil {
		c.active = *change.active
	}
	if datasets != nil {
		c.datasets = datasets
	}
	copied := *c
	copied.datasets = slices.Clone(c.datasets)
	return &copied, nil
}

// formatTimestamp formats a time in the timestamp format of spec/api.md.
func formatTimestamp(t time.Time) string {
	return t.UTC().Format(fixtures.TimestampLayout)
}
