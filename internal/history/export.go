package history

import (
	"bytes"
	"encoding/json"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// ExportFile returns the canonical bytes of an export file holding revisions,
// in the order given. Each revision is one line: a JSON object with the 14
// fields in the order of the data contract's revision table, which is the
// field order of fixtures.Revision, and no whitespace outside strings. Strings
// are escaped as Go 1.22 and later encoding/json escapes them with HTML
// escaping off. Every line, including the last, ends with "\n", so no
// revisions make an empty file.
func ExportFile(revisions []fixtures.Revision) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, r := range revisions {
		if err := enc.Encode(r); err != nil {
			// A revision holds only strings, string pointers, and integers,
			// which always encode.
			panic(err)
		}
	}
	return buf.Bytes()
}
