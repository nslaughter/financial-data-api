package api

import (
	"context"
	"strings"
	"testing"

	financialdataapi "github.com/nslaughter/financial-data-api"
	"github.com/nslaughter/financial-data-api/internal/conformance"
)

// laterEndpoint names the endpoint that a check reads and a later step of
// the implementation plan serves, or returns "" if the server already
// serves every endpoint the check reads. As the later steps land, they
// remove their endpoints from it, until every stage 1 check runs.
func laterEndpoint(files []*conformance.File, res conformance.Result) string {
	switch res.Kind {
	case "read check":
		return "GET /v1/datasets/{dataset_id}/changes (step 6)"
	case "scenario":
		for _, f := range files {
			if f.Name != res.File {
				continue
			}
			for _, sc := range f.Scenarios {
				if sc.Name != res.Name {
					continue
				}
				for _, st := range sc.Steps {
					if st.Request == nil {
						continue
					}
					if strings.HasSuffix(*st.Request.Path, "/changes") {
						return "GET /v1/datasets/{dataset_id}/changes (step 6)"
					}
				}
			}
		}
	}
	return ""
}

// TestConformance runs the stage 1 files of expected/ against the server in
// process, with the conformance runner, which also validates every response
// against spec/openapi.yaml. Every check that reads only endpoints the
// server serves must pass.
func TestConformance(t *testing.T) {
	all, err := conformance.Load(financialdataapi.Expected())
	if err != nil {
		t.Fatal(err)
	}
	files, err := conformance.Select(all, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := newServer(t, true)
	runner, err := conformance.New(conformance.Config{BaseURL: s.url, Stage: 1, Credentials: loadFixtures(t).Credentials, OpenAPI: openAPI(t)})
	if err != nil {
		t.Fatal(err)
	}
	passed, later := 0, 0
	for _, res := range runner.Run(context.Background(), files, "") {
		switch {
		case res.Skipped:
		case laterEndpoint(files, res) != "":
			later++
		case res.Failure != nil:
			t.Errorf("%s failed:\n%s", res, res.Failure)
		default:
			passed++
		}
	}
	t.Logf("%d checks passed; %d read endpoints that later steps serve", passed, later)
	if passed == 0 {
		t.Error("no check ran against the server")
	}
}
