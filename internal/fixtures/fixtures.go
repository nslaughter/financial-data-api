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
// in the data contract, each once, and only a nullable member may be null. If
// the fixtures decode but break invariants, the error is Violations.
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
	version, err := ReadContractVersion(fsys)
	if err != nil {
		return nil, err
	}
	f := Fixtures{ContractVersion: version}
	if f.Datasets, err = readFile[Dataset](fsys, DatasetsFile, "datasets"); err != nil {
		return nil, err
	}
	if f.Series, err = readFile[Series](fsys, SeriesFile, "series"); err != nil {
		return nil, err
	}
	if f.Revisions, err = readFile[Revision](fsys, RevisionsFile, "revisions"); err != nil {
		return nil, err
	}
	if f.Releases, err = readFile[Release](fsys, CalendarFile, "releases"); err != nil {
		return nil, err
	}
	if f.Credentials, err = readFile[Credential](fsys, CredentialsFile, "credentials", "description"); err != nil {
		return nil, err
	}
	return &f, nil
}

// ReadContractVersion returns the contract_version that every fixture file
// in fsys records. It reads no other member, so a caller can recognize
// fixtures of another contract version before decoding their records by
// this version's rules. A file that is missing or is not a JSON object, a
// contract_version that is missing, repeated, or not a string, and a
// contract_version that differs from the first file's are errors naming the
// file.
func ReadContractVersion(fsys fs.FS) (string, error) {
	files := [...]string{DatasetsFile, SeriesFile, RevisionsFile, CalendarFile, CredentialsFile}
	var first string
	for i, file := range files {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return "", err
		}
		version, err := contractVersion(data)
		if err != nil {
			return "", fmt.Errorf("%s: %w", file, err)
		}
		if i == 0 {
			first = version
		} else if version != first {
			return "", fmt.Errorf("%s: contract_version %q differs from %q in %s", file, version, first, files[0])
		}
	}
	return first, nil
}

// contractVersion returns the contract_version member of the JSON object
// data, ignoring its other members.
func contractVersion(data []byte) (string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return "", err
	}
	if top == nil {
		return "", fmt.Errorf("null, not an object")
	}
	if err := uniqueMembers(data); err != nil {
		return "", err
	}
	raw, ok := top["contract_version"]
	if !ok {
		return "", fmt.Errorf("missing member %q", "contract_version")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", fmt.Errorf("member %q is null", "contract_version")
	}
	var version string
	if err := json.Unmarshal(raw, &version); err != nil {
		return "", fmt.Errorf("contract_version: %w", err)
	}
	return version, nil
}

// readFile reads a fixture file: a JSON object whose members are
// contract_version, which ReadContractVersion reads, the array of records
// named by member, and the string members named by extra. It decodes each
// record into a T.
func readFile[T any](fsys fs.FS, file, member string, extra ...string) ([]T, error) {
	data, err := fs.ReadFile(fsys, file)
	if err != nil {
		return nil, err
	}
	nullable := map[string]bool{"contract_version": false, member: false}
	for _, name := range extra {
		nullable[name] = false
	}
	top, err := members(data, nullable)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	for _, name := range extra {
		var s string
		if err := json.Unmarshal(top[name], &s); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", file, name, err)
		}
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(top[member], &raws); err != nil {
		return nil, fmt.Errorf("%s: %s: %w", file, member, err)
	}
	records := make([]T, len(raws))
	for i, raw := range raws {
		if err := decodeObject(raw, &records[i]); err != nil {
			return nil, fmt.Errorf("%s: %s[%d]: %w", file, member, i, err)
		}
	}
	return records, nil
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
// exactly the members named in nullable, each once and matched exactly, and a
// member may be null only if nullable marks it true.
func members(data []byte, nullable map[string]bool) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("null, not an object")
	}
	if err := uniqueMembers(data); err != nil {
		return nil, err
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

// uniqueMembers reports a member name that occurs more than once in the JSON
// object data. json.Unmarshal keeps only the last occurrence, so the
// repetition would otherwise pass unnoticed.
func uniqueMembers(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		name, _ := tok.(string)
		if seen[name] {
			return fmt.Errorf("repeated member %q", name)
		}
		seen[name] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return err
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
