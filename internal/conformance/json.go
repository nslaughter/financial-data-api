package conformance

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/nslaughter/financial-data-api/internal/expected"
)

// decimal writes a JSON number in decimal: without an exponent, and without
// a fractional part when its value is an integer. 36, 36.0, and 3.6e1 are
// all "36", and 1.50 and 15e-1 are both "1.5".
func decimal(n json.Number) (string, error) {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return "", fmt.Errorf("the number %s cannot be written in decimal", expected.Shorten(string(n)))
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

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
