package history

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/nslaughter/financial-data-api/internal/expected"
	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// exportDigest is the expected file of an export at a position. Size and
// records are -1 when the source does not state them.
type exportDigest struct {
	source   string
	position int64
	size     int
	records  int
	sha256   string
}

// specDigest matches a sentence of spec/api.md giving an export's size and
// digest, such as "An export at position 36 is 13,695 bytes with SHA-256
// `...`", across line breaks.
var specDigest = regexp.MustCompile(
	"position\\s+([0-9]+)\\s+(?:it\\s+)?is\\s+([0-9,]+)\\s+bytes\\s+with\\s+SHA-256\\s+`([0-9a-f]{64})`")

// specDigests returns the export digests that spec/api.md states.
func specDigests(t *testing.T) []exportDigest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "spec", "api.md"))
	if err != nil {
		t.Fatal(err)
	}
	var out []exportDigest
	for _, m := range specDigest.FindAllStringSubmatch(string(data), -1) {
		position, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		size, err := strconv.Atoi(strings.ReplaceAll(m[2], ",", ""))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, exportDigest{"spec/api.md", position, size, -1, m[3]})
	}
	return out
}

// manifestDigests returns the export digests stated by every expected export
// manifest in the named file: each response body with a position and a file
// with a sha256, and the file's size_bytes and record_count where given.
func manifestDigests(t *testing.T, f *fixtures.Fixtures, name string) []exportDigest {
	t.Helper()
	var out []exportDigest
	for _, s := range expectedFile(t, f, name).Scenarios {
		for i, step := range s.Steps {
			if step.Expect == nil {
				continue
			}
			body, _ := step.Expect.Body.Value.(map[string]any)
			position, ok := integerOf(body["position"])
			files, _ := body["files"].([]any)
			if !ok || len(files) != 1 {
				continue
			}
			entry, _ := files[0].(map[string]any)
			sum, ok := entry["sha256"].(string)
			if !ok {
				continue
			}
			d := exportDigest{
				source:   fmt.Sprintf("%s: %s: step %d", name, s.Name, i+1),
				position: position,
				size:     -1,
				records:  -1,
				sha256:   sum,
			}
			if size, ok := integerOf(entry["size_bytes"]); ok {
				d.size = int(size)
			}
			if records, ok := integerOf(entry["record_count"]); ok {
				d.records = int(records)
			}
			out = append(out, d)
		}
	}
	return out
}

// integerOf returns v, a JSON value, when it is an integer that fits an
// int64.
func integerOf(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, ok := expected.Integer(n)
	if !ok || !i.IsInt64() {
		return 0, false
	}
	return i.Int64(), true
}

func TestExportDigests(t *testing.T) {
	f, h := load(t)
	spec := specDigests(t)
	if len(spec) != 2 {
		t.Fatalf("spec/api.md states %d export digests, want the 2 at positions 36 and 37", len(spec))
	}
	digests := spec
	for _, name := range []string{"export-handoff", "exports"} {
		found := manifestDigests(t, f, name)
		if len(found) == 0 {
			t.Fatalf("%s: no export manifest states a digest", name)
		}
		digests = append(digests, found...)
	}
	for _, d := range digests {
		t.Run(fmt.Sprintf("%s: position %d", d.source, d.position), func(t *testing.T) {
			file := ExportFile(h.Snapshot(expected.StreamDataset, d.position))
			sum := sha256.Sum256(file)
			if got := hex.EncodeToString(sum[:]); got != d.sha256 {
				t.Errorf("sha256 %s, want %s", got, d.sha256)
			}
			if d.size >= 0 && len(file) != d.size {
				t.Errorf("%d bytes, want %d", len(file), d.size)
			}
			if got := bytes.Count(file, []byte("\n")); d.records >= 0 && got != d.records {
				t.Errorf("%d records, want %d", got, d.records)
			}
		})
	}
}

// canonicalString writes s as a JSON string by the escaping rules of
// spec/api.md, independently of encoding/json.
func canonicalString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20, r == 0x2028, r == 0x2029:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestExportEscapesStrings(t *testing.T) {
	// Every character spec/api.md names, each with the escape it requires.
	tests := []struct {
		in, want string
	}{
		{`"`, `\"`},
		{`\`, `\\`},
		{"\b", `\b`},
		{"\f", `\f`},
		{"\n", `\n`},
		{"\r", `\r`},
		{"\t", `\t`},
		{string(rune(0x2028)), "\\u2028"},
		{string(rune(0x2029)), "\\u2029"},
		{"\u007f", "\u007f"},
		{"<", "<"},
		{">", ">"},
		{"&", "&"},
	}
	for r := range rune(0x20) {
		switch r {
		case '\b', '\f', '\n', '\r', '\t':
		default:
			tests = append(tests, struct{ in, want string }{string(r), fmt.Sprintf(`\u%04x`, r)})
		}
	}
	for r := rune(0x80); r <= 0x9f; r++ {
		tests = append(tests, struct{ in, want string }{string(r), string(r)})
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("U+%04X", []rune(tt.in)[0]), func(t *testing.T) {
			// The oracle's own escaping must agree with the table.
			if got := canonicalString(tt.in); got != `"`+tt.want+`"` {
				t.Fatalf("canonicalString(%q) = %s, want %q", tt.in, got, `"`+tt.want+`"`)
			}
			s := "a" + tt.in + "z"
			r := fixtures.Revision{
				Sequence:       1,
				SeriesID:       s,
				ObservationID:  "obs",
				RevisionID:     "rev",
				RevisionNumber: 1,
				ChangeType:     fixtures.InitialRelease,
				PeriodStart:    "2024-01-01",
				PeriodEnd:      "2024-02-01",
				Value:          nil,
				MissingReason:  &s,
				Unit:           "index_points",
				PublishedAt:    "2024-02-03T12:30:00Z",
				ReceivedAt:     "2024-02-03T12:30:04Z",
				AvailableAt:    "2024-02-03T12:31:10Z",
			}
			want := `{"sequence":1,"series_id":` + canonicalString(s) +
				`,"observation_id":"obs","revision_id":"rev","revision_number":1` +
				`,"change_type":"initial_release","period_start":"2024-01-01","period_end":"2024-02-01"` +
				`,"value":null,"missing_reason":` + canonicalString(s) +
				`,"unit":"index_points","published_at":"2024-02-03T12:30:00Z"` +
				`,"received_at":"2024-02-03T12:30:04Z","available_at":"2024-02-03T12:31:10Z"}` + "\n"
			if got := string(ExportFile([]fixtures.Revision{r})); got != want {
				t.Errorf("ExportFile:\n got %q\nwant %q", got, want)
			}
		})
	}
}
