package fixtures

import (
	"errors"
	"fmt"
	"strings"
)

// MaxInteger is the largest integer the API serves: spec/api.md keeps
// integers below 2^53, which every JSON client reads exactly.
const MaxInteger = 1<<53 - 1

// CheckIntegers returns an error naming the file, the record, and the rule
// for each sequence above MaxInteger, one a line, or nil if there is none.
// Invariant 6 bounds sequence only from below, so Load accepts such
// fixtures, but the API could not serve them. revision_number needs no
// check: invariant 9 keeps it no larger than the number of revisions.
func CheckIntegers(f *Fixtures) error {
	c := &checker{f: f}
	var lines []string
	for i, r := range f.Revisions {
		if r.Sequence > MaxInteger {
			lines = append(lines, fmt.Sprintf("%s: %s: spec/api.md keeps integers below 2^53: sequence %d is above %d",
				RevisionsFile, c.revision(i), r.Sequence, int64(MaxInteger)))
		}
	}
	if lines == nil {
		return nil
	}
	return errors.New(strings.Join(lines, "\n"))
}
