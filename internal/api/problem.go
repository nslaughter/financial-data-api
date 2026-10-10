package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
)

// codes holds the status and fixed title of each error code in the Errors
// table of spec/api.md.
var codes = map[string]struct {
	status int
	title  string
}{
	"unknown_parameter":       {http.StatusBadRequest, "Unknown parameter"},
	"missing_parameter":       {http.StatusBadRequest, "Missing parameter"},
	"invalid_parameter":       {http.StatusBadRequest, "Invalid parameter"},
	"conflicting_cutoffs":     {http.StatusBadRequest, "Conflicting cutoffs"},
	"cutoff_in_future":        {http.StatusBadRequest, "Cutoff in the future"},
	"invalid_page_token":      {http.StatusBadRequest, "Invalid page token"},
	"page_token_mismatch":     {http.StatusBadRequest, "Page token mismatch"},
	"position_ahead":          {http.StatusBadRequest, "Position ahead of the stream"},
	"unauthenticated":         {http.StatusUnauthorized, "Unauthenticated"},
	"not_entitled":            {http.StatusForbidden, "Not entitled"},
	"not_found":               {http.StatusNotFound, "Not found"},
	"unsupported_api_version": {http.StatusNotFound, "Unsupported API version"},
	"method_not_allowed":      {http.StatusMethodNotAllowed, "Method not allowed"},
	"clock_backwards":         {http.StatusConflict, "Clock cannot move backwards"},
	"page_token_expired":      {http.StatusGone, "Page token expired"},
	"position_expired":        {http.StatusGone, "Position expired"},
	"export_expired":          {http.StatusGone, "Export expired"},
	"internal":                {http.StatusInternalServerError, "Internal error"},
}

// problem is an error response, written as application/problem+json with
// its members in the order of spec/api.md.
type problem struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// Parameter names the query parameter, body field, or path parameter at
	// fault, or is nil.
	Parameter *string `json:"parameter"`
	// allow is the Allow header of a 405.
	allow string
}

func (p *problem) Error() string { return p.Code + ": " + p.Detail }

// newProblem returns a problem with code's status and title. parameter is
// nil for a problem that names no parameter.
func newProblem(code string, parameter *string, format string, args ...any) *problem {
	c, ok := codes[code]
	if !ok {
		panic("api: unknown error code " + code)
	}
	return &problem{Status: c.status, Code: code, Title: c.title, Detail: fmt.Sprintf(format, args...), Parameter: parameter}
}

// named returns a pointer to a parameter's name, for a problem.
func named(parameter string) *string { return &parameter }

func unknownParameter(name, endpoint string) *problem {
	return newProblem("unknown_parameter", named(name), "%s is not a parameter of %s.", name, endpoint)
}

func missingParameter(name, endpoint string) *problem {
	return newProblem("missing_parameter", named(name), "%s requires %s.", endpoint, name)
}

func invalidParameter(name, format string, args ...any) *problem {
	return newProblem("invalid_parameter", named(name), format, args...)
}

// malformedBody reports a body that is not a JSON object, which names no
// parameter.
func malformedBody(format string, args ...any) *problem {
	return newProblem("invalid_parameter", nil, format, args...)
}

func notFound(format string, args ...any) *problem {
	return newProblem("not_found", nil, format, args...)
}

func unauthenticated(format string, args ...any) *problem {
	return newProblem("unauthenticated", nil, format, args...)
}

// writeProblem writes p, with WWW-Authenticate on a 401 and Allow on a 405.
func writeProblem(w http.ResponseWriter, p *problem) {
	h := w.Header()
	if p.Status == http.StatusUnauthorized {
		h.Set("WWW-Authenticate", "Bearer")
	}
	if p.allow != "" {
		h.Set("Allow", p.allow)
	}
	write(w, "application/problem+json", p.Status, p)
}

// writeJSON writes a successful response.
func writeJSON(w http.ResponseWriter, status int, body any) {
	write(w, "application/json", status, body)
}

// write writes body encoded as JSON and followed by a newline.
func write(w http.ResponseWriter, contentType string, status int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		// The response types always encode, so this is a defect.
		log.Printf("api: encoding a %d response: %v", status, err)
		contentType, status = "application/problem+json", http.StatusInternalServerError
		data, _ = json.Marshal(newProblem("internal", nil, "The response could not be encoded."))
	}
	writeBytes(w, contentType, status, append(data, '\n'))
}

// writeBytes writes a response whose body is data, with its Content-Type and
// Content-Length. Every response goes through it: JSON bodies through write,
// and export files directly.
func writeBytes(w http.ResponseWriter, contentType string, status int, data []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
