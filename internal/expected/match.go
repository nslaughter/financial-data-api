package expected

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"unicode/utf8"
)

// maxRendered is the most bytes of a value that a report shows.
const maxRendered = 400

// Difference is where an actual value first differs from an expected one,
// and the two values there as a report shows them.
type Difference struct {
	At               string
	Expected, Actual string
}

// String describes the difference on one line.
func (d *Difference) String() string {
	return fmt.Sprintf("%s: expected %s, actual %s", d.At, d.Expected, d.Actual)
}

// Match compares an expected value with an actual JSON value by the matching
// rule of spec/conformance.md, and returns the first difference, or nil when
// they match. at names where the values are, such as "body". Both values are
// decoded as Decode decodes them, with json.Number for numbers.
//
// An expected object matches an object with every member it names, each
// matching; members it does not name are not compared. An expected array
// matches an array of the same length whose elements match in order. Any
// other expected value matches an equal value of the same JSON type, so
// "102.0" matches neither "102" nor 102, and null matches only a member that
// is present and null.
func Match(at string, expected, actual any) *Difference {
	switch e := expected.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return &Difference{at, Render(expected), Render(actual)}
		}
		for _, k := range sortedKeys(e) {
			v, ok := a[k]
			if !ok {
				return &Difference{memberAt(at, k), Render(e[k]), "no member"}
			}
			if d := Match(memberAt(at, k), e[k], v); d != nil {
				return d
			}
		}
		return nil
	case []any:
		a, ok := actual.([]any)
		if !ok {
			return &Difference{at, Render(expected), Render(actual)}
		}
		for i := range min(len(e), len(a)) {
			if d := Match(fmt.Sprintf("%s[%d]", at, i), e[i], a[i]); d != nil {
				return d
			}
		}
		if len(e) != len(a) {
			return &Difference{
				At:       at,
				Expected: fmt.Sprintf("%d elements", len(e)),
				Actual:   fmt.Sprintf("%d elements: %s", len(a), Render(actual)),
			}
		}
		return nil
	case json.Number:
		if a, ok := actual.(json.Number); !ok || !equalNumbers(e, a) {
			return &Difference{at, Render(expected), Render(actual)}
		}
		return nil
	default:
		// A string, a boolean, or nil. Interfaces are equal only when both
		// the type and the value are.
		if expected != actual {
			return &Difference{at, Render(expected), Render(actual)}
		}
		return nil
	}
}

// memberAt names member k of the value at at.
func memberAt(at, k string) string {
	if at == "" {
		return k
	}
	return at + "." + k
}

// equalNumbers reports whether two JSON numbers have the same value, however
// they are written.
func equalNumbers(a, b json.Number) bool {
	if a == b {
		return true
	}
	x, ok := new(big.Rat).SetString(string(a))
	if !ok {
		return false
	}
	y, ok := new(big.Rat).SetString(string(b))
	return ok && x.Cmp(y) == 0
}

// Integer returns the value of a JSON number whose value is an integer,
// however it is written: 36, 36.0, and 3.6e1 are all 36.
func Integer(n json.Number) (*big.Int, bool) {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok || !r.IsInt() {
		return nil, false
	}
	return r.Num(), true
}

// Render returns v as JSON as a Difference shows it, shortened if it is
// long, so that a report shows every value the same way.
func Render(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return Shorten(string(data))
}

// Shorten returns s, or its start if it is too long for a report.
func Shorten(s string) string {
	if len(s) <= maxRendered {
		return s
	}
	end := maxRendered
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
