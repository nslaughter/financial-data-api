package conformance

import (
	"io/fs"
	"slices"
	"strings"
	"testing"

	financialdataapi "github.com/nslaughter/financial-data-api"
	"github.com/nslaughter/financial-data-api/internal/expected"
)

func loadExpected(t *testing.T) []*expected.File {
	t.Helper()
	files, err := expected.Load()
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func fileNamed(t *testing.T, files []*expected.File, name string) *expected.File {
	t.Helper()
	for _, f := range files {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no file %s", name)
	return nil
}

// TestStageTableCoversEveryFile checks that every file of expected/ is in
// the stage table exactly once, so no file is left out of a run.
func TestStageTableCoversEveryFile(t *testing.T) {
	names, err := fs.Glob(financialdataapi.Expected(), "*.json")
	if err != nil {
		t.Fatal(err)
	}
	var staged []string
	for _, files := range stageFiles {
		staged = append(staged, files...)
	}
	for _, name := range names {
		name = strings.TrimSuffix(name, ".json")
		if n := countOf(staged, name); n != 1 {
			t.Errorf("%s is in the stage table %d times", name, n)
		}
	}
	if len(staged) != len(names) {
		t.Errorf("the stage table lists %d files; expected/ has %d", len(staged), len(names))
	}
}

func countOf(list []string, s string) int {
	n := 0
	for _, e := range list {
		if e == s {
			n++
		}
	}
	return n
}

func TestSelect(t *testing.T) {
	files := loadExpected(t)
	names := func(fs []*expected.File) []string {
		var out []string
		for _, f := range fs {
			out = append(out, f.Name)
		}
		return out
	}

	stage1, err := Select(files, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(stage1); !slices.Equal(got, stageFiles[1]) {
		t.Errorf("stage 1: got %v, want %v", got, stageFiles[1])
	}
	stage2, err := Select(files, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(stage2), append(slices.Clone(stageFiles[1]), stageFiles[2]...); !slices.Equal(got, want) {
		t.Errorf("stage 2: got %v, want %v", got, want)
	}
	// Named files run in the order of the stage table.
	some, err := Select(files, 2, []string{"exports.json", "pagination"})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(some); !slices.Equal(got, []string{"pagination", "exports"}) {
		t.Errorf("named files: got %v", got)
	}

	errorCases := []struct {
		stage int
		names []string
		want  string
	}{
		{0, nil, "stages are 1 to 2"},
		{3, nil, "stages are 1 to 2"},
		{1, []string{"exports"}, "exports is required from stage 2, so it does not run at stage 1"},
		{1, []string{"no-such-file"}, "no-such-file is not a file of the stage table"},
	}
	for _, tt := range errorCases {
		if _, err := Select(files, tt.stage, tt.names); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("stage %d, %v: got %v, want an error containing %q", tt.stage, tt.names, err, tt.want)
		}
	}
	if _, err := Select(files[1:], 1, nil); err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Errorf("without %s: got %v, want an error about a missing file", files[0].Name, err)
	}
}
