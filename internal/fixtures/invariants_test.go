package fixtures

import (
	"strings"
	"testing"

	financialdataapi "github.com/nslaughter/financial-data-api"
)

// decodeEmbedded returns a fresh copy of the embedded fixtures, decoded
// without checking the invariants, for a test to alter.
func decodeEmbedded(t *testing.T) *Fixtures {
	t.Helper()
	f, err := decode(financialdataapi.Fixtures())
	if err != nil {
		t.Fatalf("decode the embedded fixtures: %v", err)
	}
	return f
}

// revision returns the fixture revision with the given revision_id.
func revision(t *testing.T, f *Fixtures, id string) *Revision {
	t.Helper()
	for i := range f.Revisions {
		if f.Revisions[i].RevisionID == id {
			return &f.Revisions[i]
		}
	}
	t.Fatalf("no revision %q in the fixtures", id)
	return nil
}

func ptr(s string) *string { return &s }

func TestFixturesPassEveryInvariant(t *testing.T) {
	f := decodeEmbedded(t)
	if vs := Check(f); len(vs) > 0 {
		t.Fatalf("the fixtures break invariants:\n%v", vs)
	}
}

// Each case alters the fixtures so that exactly one invariant fails, and
// checks that only that rule is reported and that it names the altered
// record. Indexes refer to the records in file order: revisions[34] is
// rev_jul26_1, which has a single revision, and revisions[36] is rev_aug26_2,
// the second revision of August 2026.
func TestEachInvariantFailsAlone(t *testing.T) {
	tests := []struct {
		name   string
		rule   int
		file   string
		record string // the start of the reported record
		alter  func(t *testing.T, f *Fixtures)
	}{
		// Invariant 1.
		{
			name: "repeated dataset_id", rule: 1, file: DatasetsFile, record: "datasets[1] ",
			alter: func(t *testing.T, f *Fixtures) { f.Datasets = append(f.Datasets, f.Datasets[0]) },
		},
		{
			name: "repeated series_id", rule: 1, file: SeriesFile, record: "series[1] ",
			alter: func(t *testing.T, f *Fixtures) { f.Series = append(f.Series, f.Series[0]) },
		},
		{
			name: "repeated revision_id", rule: 1, file: RevisionsFile, record: "revisions[36] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_aug26_2").RevisionID = "rev_aug26_1" },
		},
		{
			name: "repeated credential_id", rule: 1, file: CredentialsFile, record: "credentials[1] ",
			alter: func(t *testing.T, f *Fixtures) { f.Credentials[1].CredentialID = f.Credentials[0].CredentialID },
		},
		{
			name: "repeated api_key", rule: 1, file: CredentialsFile, record: "credentials[1] ",
			alter: func(t *testing.T, f *Fixtures) { f.Credentials[1].APIKey = f.Credentials[0].APIKey },
		},

		// Invariant 2.
		{
			name: "series names no dataset", rule: 2, file: SeriesFile, record: "series[0] ",
			alter: func(t *testing.T, f *Fixtures) { f.Series[0].DatasetID = "no-such-dataset" },
		},
		{
			name: "revision names no series", rule: 2, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").SeriesID = "no-such-series" },
		},
		{
			name: "calendar entry names no series", rule: 2, file: CalendarFile, record: "releases[0] ",
			alter: func(t *testing.T, f *Fixtures) { f.Releases[0].SeriesID = "no-such-series" },
		},
		{
			name: "credential names no dataset", rule: 2, file: CredentialsFile, record: "credentials[0] ",
			alter: func(t *testing.T, f *Fixtures) { f.Credentials[0].Datasets = []string{"no-such-dataset"} },
		},

		// Invariant 3.
		{
			name: "two observations for one period", rule: 3, file: RevisionsFile, record: "revisions[36] ",
			alter: func(t *testing.T, f *Fixtures) {
				r := revision(t, f, "rev_aug26_2")
				r.ObservationID = "obs_aug26_second"
				r.RevisionNumber = 1
				r.ChangeType = InitialRelease
			},
		},

		// Invariant 4.
		{
			name: "revisions of an observation differ in unit", rule: 4, file: RevisionsFile, record: "revisions[36] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_aug26_2").Unit = "percent" },
		},
		{
			name: "unit differs from the series", rule: 4, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").Unit = "percent" },
		},

		// Invariant 5.
		{
			name: "period_start not the first of a month", rule: 5, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").PeriodStart = "2026-07-02" },
		},
		{
			name: "period_end not the first of the next month", rule: 5, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").PeriodEnd = "2026-07-31" },
		},
		{
			name: "calendar period_end not the first of the next month", rule: 5, file: CalendarFile, record: "releases[0] ",
			alter: func(t *testing.T, f *Fixtures) { f.Releases[0].PeriodEnd = "2024-03-01" },
		},

		// Invariant 6.
		{
			name: "sequence below 1", rule: 6, file: RevisionsFile, record: "revisions[0] ",
			alter: func(t *testing.T, f *Fixtures) { f.Revisions[0].Sequence = 0 },
		},
		{
			name: "repeated sequence", rule: 6, file: RevisionsFile, record: "revisions[1] ",
			alter: func(t *testing.T, f *Fixtures) { f.Revisions[1].Sequence = f.Revisions[0].Sequence },
		},

		// Invariant 7.
		{
			name: "available_at decreases", rule: 7, file: RevisionsFile, record: "revisions[19] ",
			alter: func(t *testing.T, f *Fixtures) {
				// rev_may25_3 shares its available_at with rev_jun25_1, which
				// precedes it; make it a little earlier.
				revision(t, f, "rev_may25_3").AvailableAt = "2025-07-03T12:31:00Z"
			},
		},

		// Invariant 8.
		{
			name: "published after received", rule: 8, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").ReceivedAt = "2026-08-03T14:04:00Z" },
		},
		{
			name: "received after available", rule: 8, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").AvailableAt = "2026-08-03T14:05:02Z" },
		},

		// Invariant 9.
		{
			name: "gap in revision numbers", rule: 9, file: RevisionsFile, record: "revisions[36] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_aug26_2").RevisionNumber = 3 },
		},
		{
			name: "repeated revision number", rule: 9, file: RevisionsFile, record: "revisions[36] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_aug26_2").RevisionNumber = 1 },
		},
		{
			name: "second initial_release", rule: 9, file: RevisionsFile, record: "revisions[36] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_aug26_2").ChangeType = InitialRelease },
		},
		{
			name: "revision 1 not an initial_release", rule: 9, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").ChangeType = SourceRevision },
		},

		// Invariant 10.
		{
			name: "null value without missing_reason", rule: 10, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").Value = nil },
		},
		{
			name: "value with missing_reason", rule: 10, file: RevisionsFile, record: "revisions[9] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_oct24_1").Value = ptr("98.9") },
		},
		{
			name: "provider_correction without a value", rule: 10, file: RevisionsFile, record: "revisions[29] ",
			alter: func(t *testing.T, f *Fixtures) {
				r := revision(t, f, "rev_feb26_2")
				r.Value = nil
				r.MissingReason = ptr("not_collected")
			},
		},
		{
			name: "withdrawal with a value", rule: 10, file: RevisionsFile, record: "revisions[17] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_may25_2").Value = ptr("100.6") },
		},
		{
			name: "withdrawal with missing_reason", rule: 10, file: RevisionsFile, record: "revisions[17] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_may25_2").MissingReason = ptr("not_collected") },
		},
		{
			name: "unknown change_type", rule: 10, file: RevisionsFile, record: "revisions[36] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_aug26_2").ChangeType = "restatement" },
		},
		{
			name: "exponent in value", rule: 10, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").Value = ptr("1.022e2") },
		},
		{
			name: "trailing point in value", rule: 10, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").Value = ptr("102.") },
		},
		{
			name: "leading point in value", rule: 10, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").Value = ptr(".5") },
		},
		{
			name: "plus sign in value", rule: 10, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").Value = ptr("+102.2") },
		},
		{
			name: "space in value", rule: 10, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").Value = ptr(" 102.2") },
		},
		{
			name: "empty value", rule: 10, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").Value = ptr("") },
		},
		{
			name: "non-ASCII digits in value", rule: 10, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").Value = ptr("١٠٢") },
		},

		// Invariant 11.
		{
			name: "withdrawal after withdrawal", rule: 11, file: RevisionsFile, record: "revisions[19] ",
			alter: func(t *testing.T, f *Fixtures) {
				r := revision(t, f, "rev_may25_3")
				r.ChangeType = Withdrawal
				r.Value = nil
			},
		},

		// Invariant 12.
		{
			name: "correction changes published_at", rule: 12, file: RevisionsFile, record: "revisions[29] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_feb26_2").PublishedAt = "2026-03-03T12:30:01Z" },
		},
		{
			name: "correction changes received_at", rule: 12, file: RevisionsFile, record: "revisions[29] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_feb26_2").ReceivedAt = "2026-03-05T15:19:00Z" },
		},
		{
			name: "correction of a withdrawal", rule: 12, file: RevisionsFile, record: "revisions[19] ",
			alter: func(t *testing.T, f *Fixtures) {
				r := revision(t, f, "rev_may25_3")
				r.ChangeType = ProviderCorrection
				r.PublishedAt = "2025-06-20T16:00:00Z"
				r.ReceivedAt = "2025-06-20T16:00:04Z"
			},
		},

		// Invariant 13.
		{
			name: "received_at does not increase", rule: 13, file: RevisionsFile, record: "revisions[36] ",
			alter: func(t *testing.T, f *Fixtures) {
				r := revision(t, f, "rev_aug26_2")
				r.PublishedAt = "2026-09-03T12:30:00Z"
				r.ReceivedAt = "2026-09-03T12:30:04Z"
			},
		},

		// Invariant 14.
		{
			name: "impossible date", rule: 14, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").PeriodEnd = "2026-08-32" },
		},
		{
			name: "date without leading zeros", rule: 14, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").PeriodStart = "2026-7-01" },
		},
		{
			name: "fractional seconds", rule: 14, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").PublishedAt = "2026-08-03T14:05:00.5Z" },
		},
		{
			name: "offset instead of Z", rule: 14, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) {
				revision(t, f, "rev_jul26_1").ReceivedAt = "2026-08-03T14:05:04+00:00"
			},
		},
		{
			name: "lowercase t and z", rule: 14, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").PublishedAt = "2026-08-03t14:05:00z" },
		},
		{
			name: "hour 24", rule: 14, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").AvailableAt = "2026-08-03T24:06:10Z" },
		},
		{
			name: "second 60", rule: 14, file: RevisionsFile, record: "revisions[34] ",
			alter: func(t *testing.T, f *Fixtures) { revision(t, f, "rev_jul26_1").AvailableAt = "2026-08-03T14:06:60Z" },
		},
		{
			name: "calendar timestamp with a space", rule: 14, file: CalendarFile, record: "releases[0] ",
			alter: func(t *testing.T, f *Fixtures) { f.Releases[0].ScheduledAt = "2024-02-03 12:30:00Z" },
		},
		{
			name: "calendar impossible date", rule: 14, file: CalendarFile, record: "releases[1] ",
			alter: func(t *testing.T, f *Fixtures) { f.Releases[1].PeriodStart = "2024-02-30" },
		},

		// Invariant 15.
		{
			name: "two calendar entries for one period", rule: 15, file: CalendarFile, record: "releases[1] ",
			alter: func(t *testing.T, f *Fixtures) {
				f.Releases[1].PeriodStart = f.Releases[0].PeriodStart
				f.Releases[1].PeriodEnd = f.Releases[0].PeriodEnd
			},
		},
	}

	covered := make(map[int]bool)
	for _, tt := range tests {
		covered[tt.rule] = true
		t.Run(tt.name, func(t *testing.T) {
			f := decodeEmbedded(t)
			tt.alter(t, f)
			vs := Check(f)
			if len(vs) == 0 {
				t.Fatalf("no invariant failed; want invariant %d", tt.rule)
			}
			named := false
			for _, v := range vs {
				if v.Rule != tt.rule {
					t.Errorf("invariant %d also failed: %v", v.Rule, v)
				}
				if v.File == tt.file && strings.HasPrefix(v.Record, tt.record) {
					named = true
				}
			}
			if !named {
				t.Errorf("no violation names %s %s...; got:\n%v", tt.file, tt.record, vs)
			}
		})
	}
	for rule := 1; rule <= 15; rule++ {
		if !covered[rule] {
			t.Errorf("no case covers invariant %d", rule)
		}
	}
}

func TestDecimalValuesPass(t *testing.T) {
	for _, value := range []string{"0", "102", "102.2", "-0.5", "-12", "0.000", "1234567890.0987654321"} {
		t.Run(value, func(t *testing.T) {
			f := decodeEmbedded(t)
			revision(t, f, "rev_jul26_1").Value = ptr(value)
			if vs := Check(f); len(vs) > 0 {
				t.Errorf("value %q failed:\n%v", value, vs)
			}
		})
	}
}

func TestEveryViolationIsReported(t *testing.T) {
	f := decodeEmbedded(t)
	revision(t, f, "rev_jul26_1").Unit = "percent"
	revision(t, f, "rev_aug26_2").RevisionNumber = 3
	f.Releases[0].ScheduledAt = "2024-02-03"
	vs := Check(f)
	var rules []int
	for _, v := range vs {
		rules = append(rules, v.Rule)
	}
	if len(rules) != 3 || rules[0] != 4 || rules[1] != 9 || rules[2] != 14 {
		t.Fatalf("got rules %v, want [4 9 14] in rule order:\n%v", rules, vs)
	}
}

// TestEveryRepeatedSequenceIsReported checks that invariant 6 names a
// repeated sequence even when a lower sequence separates it from its first
// use. The change also breaks invariant 7, which this test ignores.
func TestEveryRepeatedSequenceIsReported(t *testing.T) {
	f := decodeEmbedded(t)
	// Sequences 35, 36, 37 become 37, 36, 37.
	f.Revisions[34].Sequence = f.Revisions[36].Sequence
	var got []string
	for _, v := range Check(f) {
		if v.Rule == 6 {
			got = append(got, v.Error())
		}
	}
	want := []string{
		`revisions.json: revisions[35] (revision_id "rev_aug26_1"): invariant 6: ` +
			`sequence 36 is less than sequence 37 of revisions[34], which precedes it`,
		`revisions.json: revisions[36] (revision_id "rev_aug26_2"): invariant 6: ` +
			`sequence 37 repeats revisions[34]`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("invariant 6 reported:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestOutOfOrderComparesWithEveryLowerKey(t *testing.T) {
	// Keys 1, 1, 2: the second item with key 1 is earlier than the first,
	// which is allowed, but the item with key 2 is earlier than the first.
	at := func(s string) timed {
		ts, ok := parseTimestamp(s)
		if !ok {
			t.Fatalf("bad timestamp %q", s)
		}
		return timed{at: ts}
	}
	items := []timed{at("2026-01-01T00:00:05Z"), at("2026-01-01T00:00:01Z"), at("2026-01-01T00:00:03Z")}
	for i, key := range []int64{1, 1, 2} {
		items[i].i, items[i].key = i, key
	}
	var got [][2]int
	outOfOrder(items, false, func(i, j int) { got = append(got, [2]int{i, j}) })
	if len(got) != 1 || got[0] != [2]int{2, 0} {
		t.Fatalf("got %v, want [[2 0]]", got)
	}
}
