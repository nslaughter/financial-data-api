package api

import (
	"context"
	"slices"
	"testing"

	financialdataapi "github.com/nslaughter/financial-data-api"
	"github.com/nslaughter/financial-data-api/internal/conformance"
)

// stage2Files are the stage 2 files the server passes so far: those of step 8
// of the implementation plan. Exports arrive in step 9.
var stage2Files = []string{"published-as-of", "revision-history", "release-calendar", "release-timing"}

// TestConformance runs, at stage 2, every stage 1 file of expected/ and the
// stage 2 files in stage2Files against the server in process, with the
// conformance runner, which also validates every response against
// spec/openapi.yaml. Every check must pass, except a scenario whose stages
// member leaves out stage 2, which the runner skips.
func TestConformance(t *testing.T) {
	all, err := conformance.Load(financialdataapi.Expected())
	if err != nil {
		t.Fatal(err)
	}
	stage1, err := conformance.Select(all, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := slices.Clone(stage2Files)
	for _, f := range stage1 {
		names = append(names, f.Name)
	}
	files, err := conformance.Select(all, 2, names)
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
