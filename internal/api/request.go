package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// maxBodyBytes is the most bytes of a request body the server reads.
const maxBodyBytes = 1 << 20

// integerPattern matches an integer parameter: ASCII digits, without a sign
// or leading zeros.
var integerPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// queryParams holds a request's query parameters, each named once, with a
// nonempty value.
type queryParams map[string]string

// parseQuery parses the raw query of a request to endpoint, which defines
// the parameters in allowed. A parameter the endpoint does not define is
// unknown_parameter. One that is repeated, has an empty value, or is not
// valid percent-encoding is invalid_parameter.
func parseQuery(raw, endpoint string, allowed ...string) (queryParams, *problem) {
	params := queryParams{}
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		rawName, rawValue, _ := strings.Cut(pair, "=")
		name, err := url.QueryUnescape(rawName)
		if err != nil {
			return nil, invalidParameter(rawName, "The parameter name %q is not valid percent-encoding.", rawName)
		}
		if !slices.Contains(allowed, name) {
			return nil, unknownParameter(name, endpoint)
		}
		value, err := url.QueryUnescape(rawValue)
		if err != nil {
			return nil, invalidParameter(name, "The value of %s is not valid percent-encoding.", name)
		}
		if _, ok := params[name]; ok {
			return nil, invalidParameter(name, "%s is given more than once.", name)
		}
		if value == "" {
			return nil, invalidParameter(name, "%s is empty.", name)
		}
		params[name] = value
	}
	return params, nil
}

// required returns a parameter that endpoint requires.
func (q queryParams) required(name, endpoint string) (string, *problem) {
	v, ok := q[name]
	if !ok {
		return "", missingParameter(name, endpoint)
	}
	return v, nil
}

// parseTimestamp parses the timestamp value of a parameter or field: exactly
// YYYY-MM-DDTHH:MM:SSZ, a valid instant.
func parseTimestamp(name, value string) (time.Time, *problem) {
	t, ok := fixtures.ParseTimestamp(value)
	if !ok {
		return time.Time{}, invalidParameter(name, "%s must be a timestamp in the form YYYY-MM-DDTHH:MM:SSZ, a valid instant.", name)
	}
	return t, nil
}

// parseDate parses the date value of a parameter: YYYY-MM-DD, a valid
// calendar date.
func parseDate(name, value string) (time.Time, *problem) {
	t, ok := fixtures.ParseDate(value)
	if !ok {
		return time.Time{}, invalidParameter(name, "%s must be a date in the form YYYY-MM-DD, a valid calendar date.", name)
	}
	return t, nil
}

// parseInteger parses the value of an integer parameter, from lo to hi.
func parseInteger(name, value string, lo, hi int64) (int64, *problem) {
	n, err := strconv.ParseInt(value, 10, 64)
	if !integerPattern.MatchString(value) || err != nil || n < lo || n > hi {
		return 0, invalidParameter(name, "%s must be an integer from %d to %d, without a sign or leading zeros.", name, lo, hi)
	}
	return n, nil
}

// bodyFields holds the members of a request body, each a JSON value.
type bodyFields map[string]json.RawMessage

// parseBody reads the body of a request to endpoint, which defines the
// fields in allowed. An absent or empty body is {}. A body that is not a
// JSON object is invalid_parameter, naming no parameter; a field the
// endpoint does not define is unknown_parameter.
func parseBody(r *http.Request, endpoint string, allowed ...string) (bodyFields, *problem) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return nil, malformedBody("The body could not be read.")
	}
	if len(data) > maxBodyBytes {
		return nil, malformedBody("The body is longer than %d bytes.", maxBodyBytes)
	}
	fields := bodyFields{}
	if len(data) == 0 {
		return fields, nil
	}
	if trimmed := bytes.TrimLeft(data, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, malformedBody("The body must be a JSON object.")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&fields); err != nil {
		return nil, malformedBody("The body must be a JSON object.")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, malformedBody("The body must be one JSON object, with nothing after it.")
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !slices.Contains(allowed, name) {
			return nil, unknownParameter(name, endpoint)
		}
	}
	return fields, nil
}

// timestamp returns a timestamp field, and whether it is present.
func (b bodyFields) timestamp(name string) (time.Time, bool, *problem) {
	raw, ok := b[name]
	if !ok {
		return time.Time{}, false, nil
	}
	var s *string
	if err := json.Unmarshal(raw, &s); err != nil || s == nil {
		return time.Time{}, true, invalidParameter(name, "%s must be a timestamp string.", name)
	}
	t, p := parseTimestamp(name, *s)
	return t, true, p
}

// boolean returns a boolean field, or nil if it is absent.
func (b bodyFields) boolean(name string) (*bool, *problem) {
	raw, ok := b[name]
	if !ok {
		return nil, nil
	}
	var v *bool
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		return nil, invalidParameter(name, "%s must be true or false.", name)
	}
	return v, nil
}

// stringArray returns a field that is an array of strings, and whether it is
// present.
func (b bodyFields) stringArray(name string) ([]string, bool, *problem) {
	raw, ok := b[name]
	if !ok {
		return nil, false, nil
	}
	var elems *[]*string
	if err := json.Unmarshal(raw, &elems); err != nil || elems == nil {
		return nil, true, invalidParameter(name, "%s must be an array of strings.", name)
	}
	values := make([]string, 0, len(*elems))
	for _, e := range *elems {
		if e == nil {
			return nil, true, invalidParameter(name, "%s must be an array of strings.", name)
		}
		values = append(values, *e)
	}
	return values, true, nil
}
