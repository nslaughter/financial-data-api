// Package financialdataapi carries the fixtures of the contract version this
// module implements, so a binary built from it serves the fixtures it was
// built with.
package financialdataapi

import (
	"embed"
	"io/fs"
)

//go:embed fixtures/*.json
var embedded embed.FS

// Fixtures returns the fixture files at the root of a file system:
// datasets.json, series.json, revisions.json, release-calendar.json, and
// credentials.json.
func Fixtures() fs.FS {
	fsys, err := fs.Sub(embedded, "fixtures")
	if err != nil {
		// fs.Sub fails only for an invalid directory name.
		panic(err)
	}
	return fsys
}
