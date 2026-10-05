package conformance

import (
	"fmt"
	"strings"
)

// Result is the outcome of one check.
type Result struct {
	// File is the file's name without .json.
	File string
	// Kind is "query check", "page check" (a query check's pages with
	// page_size=10), "position check", "read check",
	// "release-timing check", or "scenario".
	Kind string
	Name string
	// Skipped is set for a scenario whose stages do not list the runner's
	// stage.
	Skipped bool
	// Failure is the first difference, or nil when the check passed or was
	// skipped.
	Failure *Failure
}

// String names the check, such as `full-history: query check "latest"`.
func (r Result) String() string {
	return fmt.Sprintf("%s: %s %q", r.File, r.Kind, r.Name)
}

// Failure is the first difference in a failing check: the request whose
// response differed, where, and the expected and actual values there.
type Failure struct {
	// Step is the scenario step that failed, from 1. It is 0 for the reset
	// that starts every check and for checks without steps.
	Step int
	// Request is the request's method and target, such as
	// "GET /v1/datasets/core-indicators".
	Request string
	// At is where the difference is, such as "status",
	// "body.data[0].value", "header Allow", or "OpenAPI".
	At       string
	Expected string
	Actual   string
}

// String describes the failure in lines, without indentation.
func (f *Failure) String() string {
	var sb strings.Builder
	if f.Step > 0 {
		fmt.Fprintf(&sb, "step %d: ", f.Step)
	}
	fmt.Fprintf(&sb, "%s\n", f.Request)
	fmt.Fprintf(&sb, "at %s\n", f.At)
	fmt.Fprintf(&sb, "  expected: %s\n", indent(f.Expected, "            "))
	fmt.Fprintf(&sb, "  actual:   %s", indent(f.Actual, "            "))
	return sb.String()
}

// indent puts prefix before every line of s but the first.
func indent(s, prefix string) string {
	return strings.ReplaceAll(s, "\n", "\n"+prefix)
}
