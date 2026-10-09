package expected

import (
	"fmt"
	"regexp"
	"strings"
)

// referencePattern matches a reference, ${<id>.<path>}.
var referencePattern = regexp.MustCompile(`\$\{([^{}]*)\}`)

// Reference is a reference in a string: s[Start:End] is ${<ID>.<Path>},
// where Path holds the path's member names and array indexes in order.
type Reference struct {
	Start, End int
	ID         string
	Path       []string
}

func (r Reference) String() string {
	return "${" + r.ID + "." + strings.Join(r.Path, ".") + "}"
}

// FindReferences returns the references in s, in order.
func FindReferences(s string) ([]Reference, error) {
	var refs []Reference
	for _, m := range referencePattern.FindAllStringSubmatchIndex(s, -1) {
		text := s[m[0]:m[1]]
		id, path, ok := strings.Cut(s[m[2]:m[3]], ".")
		if !ok || id == "" {
			return nil, fmt.Errorf("malformed reference %s: want ${<id>.<path>}", text)
		}
		r := Reference{Start: m[0], End: m[1], ID: id, Path: strings.Split(path, ".")}
		for _, elem := range r.Path {
			if elem == "" {
				return nil, fmt.Errorf("malformed reference %s: its path has an empty member name", text)
			}
		}
		refs = append(refs, r)
	}
	return refs, nil
}

// Whole reports whether refs, the references in s, are one reference that is
// all of s. Only such a string resolves to a value that may not be a string.
func Whole(s string, refs []Reference) bool {
	return len(refs) == 1 && refs[0].Start == 0 && refs[0].End == len(s)
}

// wholeReference reports whether v is a string that is exactly one
// well-formed reference.
func wholeReference(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	refs, err := FindReferences(s)
	return err == nil && Whole(s, refs)
}
