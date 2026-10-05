package fixtures

import (
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	financialdataapi "github.com/nslaughter/financial-data-api"
)

func TestLoadEmbeddedFixtures(t *testing.T) {
	f, err := Load(financialdataapi.Fixtures())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.ContractVersion == "" {
		t.Error("ContractVersion is empty")
	}
	// The data contract's fixture timeline.
	observations := make(map[string]bool)
	for _, r := range f.Revisions {
		observations[r.ObservationID] = true
	}
	counts := []struct {
		name      string
		got, want int
	}{
		{"datasets", len(f.Datasets), 1},
		{"series", len(f.Series), 1},
		{"revisions", len(f.Revisions), 37},
		{"observations", len(observations), 32},
		{"calendar entries", len(f.Releases), 32},
		{"credentials", len(f.Credentials), 3},
	}
	for _, c := range counts {
		if c.got != c.want {
			t.Errorf("%d %s, want %d", c.got, c.name, c.want)
		}
	}
}

func TestNullableFieldsArePointers(t *testing.T) {
	f := decodeEmbedded(t)
	tests := []struct {
		id            string
		value, reason *string
	}{
		{"rev_jan24_1", ptr("97.1"), nil},
		{"rev_jun24_1", ptr("98.0"), nil}, // the fixture's text, not a float
		{"rev_oct24_1", nil, ptr("not_collected")},
		{"rev_may25_2", nil, nil},
	}
	str := func(p *string) string {
		if p == nil {
			return "null"
		}
		return `"` + *p + `"`
	}
	for _, tt := range tests {
		r := revision(t, f, tt.id)
		if str(r.Value) != str(tt.value) {
			t.Errorf("%s: value %s, want %s", tt.id, str(r.Value), str(tt.value))
		}
		if str(r.MissingReason) != str(tt.reason) {
			t.Errorf("%s: missing_reason %s, want %s", tt.id, str(r.MissingReason), str(tt.reason))
		}
	}
}

// embeddedFS returns a writable copy of the embedded fixture files.
func embeddedFS(t *testing.T) fstest.MapFS {
	t.Helper()
	src := financialdataapi.Fixtures()
	fsys := fstest.MapFS{}
	for _, name := range []string{DatasetsFile, SeriesFile, RevisionsFile, CalendarFile, CredentialsFile} {
		data, err := fs.ReadFile(src, name)
		if err != nil {
			t.Fatal(err)
		}
		fsys[name] = &fstest.MapFile{Data: data}
	}
	return fsys
}

// replace replaces old, which must occur in the file, with new.
func replace(t *testing.T, fsys fstest.MapFS, file, old, new string) {
	t.Helper()
	data := string(fsys[file].Data)
	if !strings.Contains(data, old) {
		t.Fatalf("%s does not contain %q", file, old)
	}
	fsys[file].Data = []byte(strings.Replace(data, old, new, 1))
}

func TestLoadRefusesMalformedFiles(t *testing.T) {
	tests := []struct {
		name  string
		alter func(t *testing.T, fsys fstest.MapFS)
		want  string
	}{
		{
			name:  "missing file",
			alter: func(t *testing.T, fsys fstest.MapFS) { delete(fsys, CalendarFile) },
			want:  "open release-calendar.json",
		},
		{
			name:  "not JSON",
			alter: func(t *testing.T, fsys fstest.MapFS) { fsys[SeriesFile].Data = []byte("{") },
			want:  "series.json: unexpected end of JSON input",
		},
		{
			name:  "not an object",
			alter: func(t *testing.T, fsys fstest.MapFS) { fsys[SeriesFile].Data = []byte("[]") },
			want:  "series.json: json: cannot unmarshal array",
		},
		{
			name: "unknown file member",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, DatasetsFile, `"contract_version"`, `"extra": 1, "contract_version"`)
			},
			want: `datasets.json: unknown member "extra"`,
		},
		{
			name: "missing records member",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, DatasetsFile, `"datasets"`, `"dataset"`)
			},
			want: `datasets.json: unknown member "dataset"`,
		},
		{
			name: "null records member",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				fsys[DatasetsFile].Data = []byte(`{"contract_version": "0.3.0", "datasets": null}`)
			},
			want: `datasets.json: member "datasets" is null`,
		},
		{
			name: "missing credentials description",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, CredentialsFile, `"description": "Demonstration credentials.`, `"note": "Demonstration credentials.`)
			},
			want: `credentials.json: unknown member "note"`,
		},
		{
			name: "unknown record member",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, RevisionsFile, `"revision_id": "rev_jan24_1",`, `"revision_id": "rev_jan24_1", "extra": 1,`)
			},
			want: `revisions.json: revisions[0]: unknown member "extra"`,
		},
		{
			name: "missing record member",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, RevisionsFile, `"revision_id": "rev_feb24_1",`, ``)
			},
			want: `revisions.json: revisions[1]: missing member "revision_id"`,
		},
		{
			name: "repeated file member",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, DatasetsFile, `"contract_version"`, `"contract_version": "0.3.0", "contract_version"`)
			},
			want: `datasets.json: repeated member "contract_version"`,
		},
		{
			name: "repeated record member",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, RevisionsFile, `"value": "97.1",`, `"value": "97.1", "value": "55.5",`)
			},
			want: `revisions.json: revisions[0]: repeated member "value"`,
		},
		{
			name: "member name in another case",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, RevisionsFile, `"value": "97.1"`, `"Value": "97.1"`)
			},
			want: `revisions.json: revisions[0]: unknown member "Value"`,
		},
		{
			name: "missing nullable member",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, RevisionsFile, `"value": "97.1",`, ``)
			},
			want: `revisions.json: revisions[0]: missing member "value"`,
		},
		{
			name: "null in a member that is not nullable",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, RevisionsFile, `"revision_id": "rev_jan24_1"`, `"revision_id": null`)
			},
			want: `revisions.json: revisions[0]: member "revision_id" is null`,
		},
		{
			name: "number instead of a decimal string",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, RevisionsFile, `"value": "97.1"`, `"value": 97.1`)
			},
			want: `revisions.json: revisions[0]: json: cannot unmarshal number into Go struct field Revision.value of type string`,
		},
		{
			name: "fractional sequence",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, RevisionsFile, `"sequence": 1,`, `"sequence": 1.0,`)
			},
			want: `revisions.json: revisions[0]: json: cannot unmarshal number 1.0 into Go struct field Revision.sequence of type int64`,
		},
		{
			name: "null credential datasets",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, CredentialsFile, `"datasets": ["core-indicators"]`, `"datasets": null`)
			},
			want: `credentials.json: credentials[0]: member "datasets" is null`,
		},
		{
			name: "contract versions disagree",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, SeriesFile, `"contract_version": "`, `"contract_version": "9.`)
			},
			want: `series.json: contract_version "9.`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := embeddedFS(t)
			tt.alter(t, fsys)
			_, err := Load(fsys)
			if err == nil {
				t.Fatalf("Load succeeded; want an error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load error:\n%v\nwant it to contain %q", err, tt.want)
			}
		})
	}
}

// TestLoadNamesFileRecordAndRule checks the error a server prints when it
// refuses to start.
func TestLoadNamesFileRecordAndRule(t *testing.T) {
	f := decodeEmbedded(t)
	revision(t, f, "rev_aug26_2").ReceivedAt = "2026-09-03T12:30:04Z"
	revision(t, f, "rev_aug26_2").PublishedAt = "2026-09-03T12:30:00Z"
	data, err := json.MarshalIndent(map[string]any{
		"contract_version": f.ContractVersion,
		"revisions":        f.Revisions,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fsys := embeddedFS(t)
	fsys[RevisionsFile].Data = data

	_, err = Load(fsys)
	var vs Violations
	if !errors.As(err, &vs) {
		t.Fatalf("Load error %v is not Violations", err)
	}
	want := `revisions.json: revisions[36] (revision_id "rev_aug26_2"): invariant 13: ` +
		`received_at 2026-09-03T12:30:04Z is not after received_at 2026-09-03T12:30:04Z ` +
		`of revisions[35], which has a lower revision_number (1)`
	if err.Error() != want {
		t.Fatalf("Load error:\n%v\nwant:\n%s", err, want)
	}
}

// TestReadContractVersion checks that the contract version is read without
// the records, so that fixtures of another version are recognized even when
// this version cannot decode them.
func TestReadContractVersion(t *testing.T) {
	if v, err := ReadContractVersion(financialdataapi.Fixtures()); err != nil || v != "0.3.0" {
		t.Fatalf("embedded fixtures: %q, %v", v, err)
	}

	// otherVersion returns the fixtures with contract version 0.4.0 in every
	// file and a member 0.3.0 does not have in a file and in a record.
	otherVersion := func(t *testing.T) fstest.MapFS {
		fsys := embeddedFS(t)
		for _, file := range []string{DatasetsFile, SeriesFile, RevisionsFile, CalendarFile, CredentialsFile} {
			replace(t, fsys, file, `"contract_version": "0.3.0"`, `"contract_version": "0.4.0"`)
		}
		replace(t, fsys, DatasetsFile, `"contract_version"`, `"new_member": 1, "contract_version"`)
		replace(t, fsys, RevisionsFile, `"revision_id": "rev_jan24_1",`, `"revision_id": "rev_jan24_1", "new_member": null,`)
		return fsys
	}
	fsys := otherVersion(t)
	if v, err := ReadContractVersion(fsys); err != nil || v != "0.4.0" {
		t.Errorf("fixtures of 0.4.0: %q, %v", v, err)
	}
	if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), `unknown member "new_member"`) {
		t.Errorf("Load of fixtures of 0.4.0: %v", err)
	}

	tests := []struct {
		name  string
		alter func(t *testing.T, fsys fstest.MapFS)
		want  string
	}{
		{
			name: "versions disagree in fixtures that do not decode",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, DatasetsFile, `"contract_version": "0.4.0"`, `"contract_version": "0.3.0"`)
			},
			want: `series.json: contract_version "0.4.0" differs from "0.3.0" in datasets.json`,
		},
		{
			name:  "missing file",
			alter: func(t *testing.T, fsys fstest.MapFS) { delete(fsys, CredentialsFile) },
			want:  "open credentials.json",
		},
		{
			name:  "null file",
			alter: func(t *testing.T, fsys fstest.MapFS) { fsys[SeriesFile].Data = []byte("null") },
			want:  "series.json: null, not an object",
		},
		{
			name: "missing contract_version",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, SeriesFile, `"contract_version"`, `"version"`)
			},
			want: `series.json: missing member "contract_version"`,
		},
		{
			name: "null contract_version",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, SeriesFile, `"contract_version": "0.4.0"`, `"contract_version": null`)
			},
			want: `series.json: member "contract_version" is null`,
		},
		{
			name: "contract_version not a string",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, SeriesFile, `"contract_version": "0.4.0"`, `"contract_version": 4`)
			},
			want: `series.json: contract_version: json: cannot unmarshal number`,
		},
		{
			name: "repeated contract_version",
			alter: func(t *testing.T, fsys fstest.MapFS) {
				replace(t, fsys, SeriesFile, `"contract_version"`, `"contract_version": "0.4.0", "contract_version"`)
			},
			want: `series.json: repeated member "contract_version"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := otherVersion(t)
			tt.alter(t, fsys)
			v, err := ReadContractVersion(fsys)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ReadContractVersion: %q, %v; want an error containing %q", v, err, tt.want)
			}
		})
	}
}
