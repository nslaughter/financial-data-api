// Package fixtures loads the data contract's fixture files into typed records
// and checks the contract's invariants.
package fixtures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"sort"
	"strings"
)

// The fixture files, as named in the fixtures directory.
const (
	DatasetsFile    = "datasets.json"
	SeriesFile      = "series.json"
	RevisionsFile   = "revisions.json"
	CalendarFile    = "release-calendar.json"
	CredentialsFile = "credentials.json"
)

// The change types of the data contract.
const (
	InitialRelease     = "initial_release"
	SourceRevision     = "source_revision"
	ProviderCorrection = "provider_correction"
	Withdrawal         = "withdrawal"
)

// Fixtures holds every fixture record in file order.
type Fixtures struct {
	// ContractVersion is the contract_version that every fixture file records.
	ContractVersion string
	Datasets        []Dataset
	Series          []Series
	Revisions       []Revision
	Releases        []Release
	Credentials     []Credential
}

// Dataset is a record of datasets.json.
type Dataset struct {
	DatasetID   string `json:"dataset_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Series is a record of series.json.
type Series struct {
	SeriesID           string `json:"series_id"`
	DatasetID          string `json:"dataset_id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	Frequency          string `json:"frequency"`
	Unit               string `json:"unit"`
	BasePeriod         string `json:"base_period"`
	SeasonalAdjustment string `json:"seasonal_adjustment"`
	Source             string `json:"source"`
	ReleaseSchedule    string `json:"release_schedule"`
}

// Revision is a record of revisions.json. Value is the fixture's decimal
// string, never parsed into a number. Dates and timestamps keep the fixture's
// text; invariant 14 checks their format.
type Revision struct {
	Sequence       int64   `json:"sequence"`
	SeriesID       string  `json:"series_id"`
	ObservationID  string  `json:"observation_id"`
	RevisionID     string  `json:"revision_id"`
	RevisionNumber int64   `json:"revision_number"`
	ChangeType     string  `json:"change_type"`
	PeriodStart    string  `json:"period_start"`
	PeriodEnd      string  `json:"period_end"`
	Value          *string `json:"value"`
	MissingReason  *string `json:"missing_reason"`
	Unit           string  `json:"unit"`
	PublishedAt    string  `json:"published_at"`
	ReceivedAt     string  `json:"received_at"`
	AvailableAt    string  `json:"available_at"`
}

// Release is an entry of release-calendar.json.
type Release struct {
	SeriesID    string `json:"series_id"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	ScheduledAt string `json:"scheduled_at"`
}

// Credential is a record of credentials.json.
type Credential struct {
	CredentialID string   `json:"credential_id"`
	APIKey       string   `json:"api_key"`
	Kind         string   `json:"kind"`
	Active       bool     `json:"active"`
	Datasets     []string `json:"datasets"`
	Description  string   `json:"description"`
}

// Load reads the fixture files from fsys and checks the invariants. A file
// that is missing or cannot be decoded is an error naming the file and, for a
// record, the record. Every record must have exactly the members of its table
// in the data contract, and only a nullable member may be null. If the
// fixtures decode but break invariants, the error is Violations.
func Load(fsys fs.FS) (*Fixtures, error) {
	f, err := decode(fsys)
	if err != nil {
		return nil, err
	}
	if vs := Check(f); len(vs) > 0 {
		return nil, vs
	}
	return f, nil
}

// decode reads the fixture files without checking the invariants.
func decode(fsys fs.FS) (*Fixtures, error) {
	var (
		f        Fixtures
		versions [5]string
		err      error
	)
	if versions[0], f.Datasets, err = readFile[Dataset](fsys, DatasetsFile, "datasets"); err != nil {
		return nil, err
	}
	if versions[1], f.Series, err = readFile[Series](fsys, SeriesFile, "series"); err != nil {
		return nil, err
	}
	if versions[2], f.Revisions, err = readFile[Revision](fsys, RevisionsFile, "revisions"); err != nil {
		return nil, err
	}
	if versions[3], f.Releases, err = readFile[Release](fsys, CalendarFile, "releases"); err != nil {
		return nil, err
	}
	if versions[4], f.Credentials, err = readFile[Credential](fsys, CredentialsFile, "credentials", "description"); err != nil {
		return nil, err
	}
	files := [5]string{DatasetsFile, SeriesFile, RevisionsFile, CalendarFile, CredentialsFile}
	for i := 1; i < len(files); i++ {
		if versions[i] != versions[0] {
			return nil, fmt.Errorf("%s: contract_version %q differs from %q in %s",
				files[i], versions[i], versions[0], files[0])
		}
	}
	f.ContractVersion = versions[0]
	return &f, nil
}

// readFile reads a fixture file: a JSON object whose members are
// contract_version, the array of records named by member, and the string
// members named by extra. It decodes each record into a T.
func readFile[T any](fsys fs.FS, file, member string, extra ...string) (string, []T, error) {
	data, err := fs.ReadFile(fsys, file)
	if err != nil {
		return "", nil, err
	}
	nullable := map[string]bool{"contract_version": false, member: false}
	for _, name := range extra {
		nullable[name] = false
	}
	top, err := members(data, nullable)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", file, err)
	}
	var version string
	if err := json.Unmarshal(top["contract_version"], &version); err != nil {
		return "", nil, fmt.Errorf("%s: contract_version: %w", file, err)
	}
	for _, name := range extra {
		var s string
		if err := json.Unmarshal(top[name], &s); err != nil {
			return "", nil, fmt.Errorf("%s: %s: %w", file, name, err)
		}
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(top[member], &raws); err != nil {
		return "", nil, fmt.Errorf("%s: %s: %w", file, member, err)
	}
	records := make([]T, len(raws))
	for i, raw := range raws {
		if err := decodeObject(raw, &records[i]); err != nil {
			return "", nil, fmt.Errorf("%s: %s[%d]: %w", file, member, i, err)
		}
	}
	return version, records, nil
}

// decodeObject decodes the JSON object data into v, a pointer to a struct.
// The object must have exactly the struct's members, by their JSON names, and
// only a pointer field accepts null.
func decodeObject(data []byte, v any) error {
	t := reflect.TypeOf(v).Elem()
	nullable := make(map[string]bool, t.NumField())
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		nullable[name] = field.Type.Kind() == reflect.Pointer
	}
	if _, err := members(data, nullable); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// members decodes a JSON object into its members. The object must have
// exactly the members named in nullable, matched exactly, and a member may be
// null only if nullable marks it true.
func members(data []byte, nullable map[string]bool) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("null, not an object")
	}
	for _, name := range sortedKeys(m) {
		if _, ok := nullable[name]; !ok {
			return nil, fmt.Errorf("unknown member %q", name)
		}
	}
	for _, name := range sortedKeys(nullable) {
		raw, ok := m[name]
		if !ok {
			return nil, fmt.Errorf("missing member %q", name)
		}
		if !nullable[name] && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("member %q is null", name)
		}
	}
	return m, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
