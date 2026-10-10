package api

import (
	"errors"
	"testing"
	"time"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// The state returns errors, which the handlers map to problems. These tests
// check each error directly; the handlers' tests check the problems.

// newTestState returns the state and the catalog of a server at the default
// clock start.
func newTestState(t *testing.T) (*state, *catalog) {
	t.Helper()
	f := loadFixtures(t)
	srv, err := New(Config{Fixtures: f, ContractVersion: f.ContractVersion, ClockStart: mustTime(t, startClock), TestControl: true})
	if err != nil {
		t.Fatal(err)
	}
	return srv.state, srv.catalog
}

func TestStateSetClock(t *testing.T) {
	st, _ := newTestState(t)
	start := mustTime(t, startClock)

	_, err := st.setClock(start.Add(-time.Second))
	var backwards *clockBackwardsError
	if !errors.As(err, &backwards) || !backwards.clock.Equal(start) {
		t.Fatalf("a time before the clock: got %v, want a *clockBackwardsError at %s", err, startClock)
	}
	if got := st.now(); !got.Equal(start) {
		t.Errorf("a refused time moved the clock to %s", formatTimestamp(got))
	}

	// The current time changes nothing, and a later one moves the clock.
	for _, tm := range []time.Time{start, start.Add(time.Hour)} {
		if got, err := st.setClock(tm); err != nil || !got.Equal(tm) {
			t.Errorf("%s: got %s, %v", formatTimestamp(tm), formatTimestamp(got), err)
		}
	}
}

func TestStateChangeCredential(t *testing.T) {
	st, cat := newTestState(t)
	inactive := false

	// The credential is looked up first, then its kind is checked, and then
	// the datasets.
	_, err := st.changeCredential("no-such-credential", credentialChange{datasets: []string{"no-such-dataset"}}, cat)
	if !errors.Is(err, errNoCredential) {
		t.Errorf("an unknown credential: got %v, want errNoCredential", err)
	}
	_, err = st.changeCredential("cred_test_control", credentialChange{datasets: []string{"no-such-dataset"}}, cat)
	var kind *credentialKindError
	if !errors.As(err, &kind) || kind.kind != fixtures.TestControlKind {
		t.Errorf("the test-control credential: got %v, want a *credentialKindError of kind %s", err, fixtures.TestControlKind)
	}
	_, err = st.changeCredential("cred_research", credentialChange{active: &inactive, datasets: []string{"core-indicators", "no-such-dataset"}}, cat)
	var unknown *unknownDatasetError
	if !errors.As(err, &unknown) || unknown.datasetID != "no-such-dataset" {
		t.Errorf("an unknown dataset: got %v, want an *unknownDatasetError for no-such-dataset", err)
	}

	// A refused change changes nothing.
	if m := st.view(researchKey); m.cred == nil || !m.cred.active {
		t.Errorf("a refused change deactivated the credential: %+v", m.cred)
	}

	cred, err := st.changeCredential("cred_research", credentialChange{active: &inactive}, cat)
	if err != nil || cred.active {
		t.Errorf("a valid change: got %+v, %v", cred, err)
	}
}
