package conformance

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxRendered is the most bytes of a value that a report shows.
const maxRendered = 400

// difference is where an actual value first differs from an expected one.
type difference struct {
	at               string
	expected, actual string
}

// match compares an expected value with an actual JSON value by the matching
// rule of spec/conformance.md, and returns the first difference, or nil when
// they match. at names where the values are, such as "body". Both values are
// decoded with json.Number for numbers.
//
// An expected object matches an object with every member it names, each
// matching; members it does not name are not compared. An expected array
// matches an array of the same length whose elements match in order. Any
// other expected value matches an equal value of the same JSON type, so
// "102.0" matches neither "102" nor 102, and null matches only a member that
// is present and null.
func match(at string, expected, actual any) *difference {
	switch e := expected.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return &difference{at, render(expected), render(actual)}
		}
		for _, k := range sortedKeys(e) {
			v, ok := a[k]
			if !ok {
				return &difference{member(at, k), render(e[k]), "no member"}
			}
			if d := match(member(at, k), e[k], v); d != nil {
				return d
			}
		}
		return nil
	case []any:
		a, ok := actual.([]any)
		if !ok {
			return &difference{at, render(expected), render(actual)}
		}
		for i := range min(len(e), len(a)) {
			if d := match(fmt.Sprintf("%s[%d]", at, i), e[i], a[i]); d != nil {
				return d
			}
		}
		if len(e) != len(a) {
			return &difference{
				at:       at,
				expected: fmt.Sprintf("%d elements", len(e)),
				actual:   fmt.Sprintf("%d elements: %s", len(a), render(actual)),
			}
		}
		return nil
	case json.Number:
		if a, ok := actual.(json.Number); !ok || !equalNumbers(e, a) {
			return &difference{at, render(expected), render(actual)}
		}
		return nil
	default:
		// A string, a boolean, or nil. Interfaces are equal only when both
		// the type and the value are.
		if expected != actual {
			return &difference{at, render(expected), render(actual)}
		}
		return nil
	}
}

// member names member k of the value at at.
func member(at, k string) string {
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

// integer returns the value of a JSON number whose value is an integer,
// however it is written: 36, 36.0, and 3.6e1 are all 36.
func integer(n json.Number) (*big.Int, bool) {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok || !r.IsInt() {
		return nil, false
	}
	return r.Num(), true
}

// decimal writes a JSON number in decimal: without an exponent, and without
// a fractional part when its value is an integer. 36, 36.0, and 3.6e1 are
// all "36", and 1.50 and 15e-1 are both "1.5".
func decimal(n json.Number) (string, error) {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return "", fmt.Errorf("the number %s cannot be written in decimal", shorten(string(n)))
	}
	if r.IsInt() {
		return r.Num().String(), nil
	}
	// n is an integer times 10^-k, where k is the number of digits after
	// its point less its exponent, so k digits after the point write it
	// exactly. k is at least 1, since n is not an integer.
	mantissa, exponent, _ := strings.Cut(strings.ToLower(string(n)), "e")
	_, fraction, _ := strings.Cut(mantissa, ".")
	exp := 0
	if exponent != "" {
		// SetString accepted the exponent, so it is small enough.
		exp, _ = strconv.Atoi(exponent)
	}
	return strings.TrimRight(r.FloatString(len(fraction)-exp), "0"), nil
}

// render returns v as JSON for a report, shortened if it is long.
func render(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return shorten(string(data))
}

// shorten returns s, or its start if it is long.
func shorten(s string) string {
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
