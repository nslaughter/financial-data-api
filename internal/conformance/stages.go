package conformance

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nslaughter/financial-data-api/internal/expected"
)

// LastStage is the API's last stage: stage 1 is the demo API, and stage 2
// the full API.
const LastStage = 2

// stageFiles lists, for each stage, the files that the stage table of
// spec/data-contract.md first requires of the API at that stage.
var stageFiles = [LastStage + 1][]string{
	1: {
		"august-2026-at-cutoffs",
		"full-history",
		"missing-value",
		"withdrawal-and-rerelease",
		"out-of-order-arrival",
		"provider-correction",
		"late-source-release",
		"change-stream",
		"simulated-clock",
		"pagination",
		"access-control",
		"request-errors",
	},
	2: {
		"published-as-of",
		"revision-history",
		"release-calendar",
		"export-handoff",
		"exports",
		expected.TimingFile,
	},
}

// Select returns the files the API must pass at stage: every file required
// at or before it, in the order of the stage table. When names is not empty,
// it returns only the files named, with or without .json, each of which
// must be required at stage.
func Select(files []*expected.File, stage int, names []string) ([]*expected.File, error) {
	if stage < 1 || stage > LastStage {
		return nil, fmt.Errorf("stage %d: the API's stages are 1 to %d", stage, LastStage)
	}
	var required []string
	for s := 1; s <= stage; s++ {
		required = append(required, stageFiles[s]...)
	}
	named := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSuffix(name, ".json")
		if !slices.Contains(required, name) {
			for s := stage + 1; s <= LastStage; s++ {
				if slices.Contains(stageFiles[s], name) {
					return nil, fmt.Errorf("%s is required from stage %d, so it does not run at stage %d", name, s, stage)
				}
			}
			return nil, fmt.Errorf("%s is not a file of the stage table", name)
		}
		named[name] = true
	}
	byName := map[string]*expected.File{}
	for _, f := range files {
		byName[f.Name] = f
	}
	var selected []*expected.File
	for _, name := range required {
		if len(named) > 0 && !named[name] {
			continue
		}
		f, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("expected/%s.json is missing", name)
		}
		selected = append(selected, f)
	}
	return selected, nil
}

// runsAt reports whether a scenario runs at stage: it does unless its stages
// member does not list stage.
func runsAt(s *expected.Scenario, stage int) bool {
	return s.Stages == nil || slices.Contains(*s.Stages, stage)
}
