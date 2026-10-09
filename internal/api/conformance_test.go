package api

import (
	"context"
	"testing"

	"github.com/nslaughter/financial-data-api/internal/conformance"
	"github.com/nslaughter/financial-data-api/internal/expected"
)

// TestConformance runs every file of expected/ at stage 2 against the server
// in process, with the conformance runner, which also validates every
// response against spec/openapi.yaml. Every check must pass, except a
// scenario whose stages member leaves out stage 2, which the runner skips.
func TestConformance(t *testing.T) {
	all, err := expected.Load()
	if err != nil {
		t.Fatal(err)
	}
	files, err := conformance.Select(all, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := newServer(t, true)
	runner, err := conformance.New(conformance.Config{BaseURL: s.url, Stage: 2, Credentials: loadFixtures(t).Credentials, OpenAPI: openAPI(t)})
	if err != nil {
		t.Fatal(err)
	}
	passed := 0
	for _, res := range runner.Run(context.Background(), files, "") {
		switch {
		case res.Skipped:
		case res.Failure != nil:
			t.Errorf("%s failed:\n%s", res, res.Failure)
		default:
			passed++
		}
	}
	t.Logf("%d checks passed", passed)
	if passed == 0 {
		t.Error("no check ran against the server")
	}
}
