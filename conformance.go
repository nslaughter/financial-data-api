package financialdataapi

import (
	"embed"
	"io/fs"
)

//go:embed expected/*.json spec/openapi.yaml
var contract embed.FS

// Expected returns the files of expected/ at the root of a file system.
func Expected() fs.FS {
	fsys, err := fs.Sub(contract, "expected")
	if err != nil {
		// fs.Sub fails only for an invalid directory name.
		panic(err)
	}
	return fsys
}

// OpenAPI returns the contents of spec/openapi.yaml.
func OpenAPI() []byte {
	data, err := contract.ReadFile("spec/openapi.yaml")
	if err != nil {
		// The file is embedded, so reading it cannot fail.
		panic(err)
	}
	return data
}
