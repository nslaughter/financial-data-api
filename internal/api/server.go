// Package api serves the API of spec/api.md over HTTP: routing, errors,
// authentication and entitlements, the simulated clock, test control, and
// the endpoints. The data rules come from internal/history.
//
// A request is answered in the error order of spec/api.md: routing,
// authentication, request validation, lookup, entitlement, then state. The
// server routes requests itself rather than with net/http's ServeMux, which
// writes its own plain-text errors and redirects some paths, so every error
// is application/problem+json.
package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
	"github.com/nslaughter/financial-data-api/internal/history"
)

// apiVersion is the only API version the server serves.
const apiVersion = "v1"

// versionPattern matches a first path segment that names an API version.
var versionPattern = regexp.MustCompile(`^v[0-9]+$`)

// allowOrder is the order in which the Allow header lists methods.
var allowOrder = []string{http.MethodGet, http.MethodPut, http.MethodPost}

// Config is what a Server needs.
type Config struct {
	// Fixtures are the fixtures, which must pass the invariants, as
	// fixtures.Load returns them.
	Fixtures *fixtures.Fixtures
	// ClockStart is the clock at startup and after a reset without a
	// clock. It is no later than MaxClock.
	ClockStart time.Time
	// TestControl serves the /test endpoints.
	TestControl bool
}

// Server is the API. It is safe for concurrent use.
type Server struct {
	contractVersion string
	history         *history.History
	datasets        []fixtures.Dataset // by dataset_id
	series          []fixtures.Series  // by series_id
	state           *state
	routes          []route
}

// New returns a server for cfg.
func New(cfg Config) (*Server, error) {
	if cfg.Fixtures == nil {
		return nil, errors.New("api: no fixtures")
	}
	if cfg.ClockStart.After(MaxClock) {
		return nil, fmt.Errorf("api: the clock start %s is later than %s", formatTimestamp(cfg.ClockStart), formatTimestamp(MaxClock))
	}
	h, err := history.New(cfg.Fixtures)
	if err != nil {
		return nil, err
	}
	s := &Server{
		contractVersion: cfg.Fixtures.ContractVersion,
		history:         h,
		datasets:        slices.Clone(cfg.Fixtures.Datasets),
		series:          slices.Clone(cfg.Fixtures.Series),
		state:           newState(cfg.ClockStart, cfg.Fixtures.Credentials),
	}
	sort.Slice(s.datasets, func(i, j int) bool { return s.datasets[i].DatasetID < s.datasets[j].DatasetID })
	sort.Slice(s.series, func(i, j int) bool { return s.series[i].SeriesID < s.series[j].SeriesID })
	s.routes = s.routeTable(cfg.TestControl)
	return s, nil
}

// route is a path and the endpoint for each method it supports.
type route struct {
	// template is the path, with a segment in braces for each path
	// parameter, which matches any nonempty segment.
	template  string
	segments  []string
	endpoints map[string]endpoint
}

// endpoint is one method of a route.
type endpoint struct {
	// auth is the kind of credential the endpoint requires, or "" for none.
	auth   string
	handle func(w http.ResponseWriter, c *call) *problem
}

// call is one request, as an endpoint sees it.
type call struct {
	r *http.Request
	// endpoint names the endpoint, such as "GET /v1/series/{series_id}".
	endpoint string
	// path holds the path parameters by name.
	path map[string]string
	// now is the clock, read once for the request.
	now time.Time
	// cred is the authenticated credential, or nil for an endpoint that
	// requires none.
	cred *credential
}

func (s *Server) routeTable(testControl bool) []route {
	customer := func(h func(http.ResponseWriter, *call) *problem) endpoint {
		return endpoint{auth: customerKind, handle: h}
	}
	test := func(h func(http.ResponseWriter, *call) *problem) endpoint {
		return endpoint{auth: testControlKind, handle: h}
	}
	routes := []route{
		{template: "/v1/meta", endpoints: map[string]endpoint{http.MethodGet: {handle: s.meta}}},
		{template: "/v1/datasets", endpoints: map[string]endpoint{http.MethodGet: customer(s.listDatasets)}},
		{template: "/v1/datasets/{dataset_id}", endpoints: map[string]endpoint{http.MethodGet: customer(s.getDataset)}},
		{template: "/v1/series", endpoints: map[string]endpoint{http.MethodGet: customer(s.listSeries)}},
		{template: "/v1/series/{series_id}", endpoints: map[string]endpoint{http.MethodGet: customer(s.getSeries)}},
	}
	if testControl {
		routes = append(routes,
			route{template: "/test/clock", endpoints: map[string]endpoint{
				http.MethodGet: test(s.getClock),
				http.MethodPut: test(s.setClock),
			}},
			route{template: "/test/reset", endpoints: map[string]endpoint{http.MethodPost: test(s.reset)}},
			route{template: "/test/credentials/{credential_id}", endpoints: map[string]endpoint{http.MethodPut: test(s.changeCredential)}},
		)
	}
	for i := range routes {
		routes[i].segments = strings.Split(routes[i].template, "/")
	}
	return routes
}

// ServeHTTP answers a request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			if v == http.ErrAbortHandler {
				panic(v)
			}
			log.Printf("api: panic serving %s %s: %v\n%s", r.Method, r.URL.Path, v, debug.Stack())
			writeProblem(w, newProblem("internal", nil, "An unexpected error occurred."))
		}
	}()
	if p := s.serve(w, r); p != nil {
		writeProblem(w, p)
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) *problem {
	// Routing.
	segments, ok := splitPath(r.URL.EscapedPath())
	if !ok {
		return notFound("There is no resource at %s.", r.URL.EscapedPath())
	}
	if len(segments) > 1 && versionPattern.MatchString(segments[1]) && segments[1] != apiVersion {
		return newProblem("unsupported_api_version", nil, "API version %s is not supported. This server supports %s.", segments[1], apiVersion)
	}
	rt, params := s.match(segments)
	if rt == nil {
		return notFound("There is no resource at %s.", r.URL.EscapedPath())
	}
	ep, ok := rt.endpoints[r.Method]
	if !ok {
		var allow []string
		for _, m := range allowOrder {
			if _, ok := rt.endpoints[m]; ok {
				allow = append(allow, m)
			}
		}
		p := newProblem("method_not_allowed", nil, "%s does not support %s; it supports %s.", rt.template, r.Method, strings.Join(allow, ", "))
		p.allow = strings.Join(allow, ", ")
		return p
	}

	// Authentication. The clock is read once, with the credential.
	c := &call{r: r, endpoint: r.Method + " " + rt.template, path: params}
	if ep.auth == "" {
		c.now = s.state.now()
	} else {
		var p *problem
		if c.now, c.cred, p = s.authenticate(r, ep.auth); p != nil {
			return p
		}
	}
	return ep.handle(w, c)
}

// splitPath splits an escaped path into its segments, decoding each. The
// first segment is the empty one before the leading slash.
func splitPath(escaped string) ([]string, bool) {
	if !strings.HasPrefix(escaped, "/") {
		return nil, false
	}
	segments := strings.Split(escaped, "/")
	for i, seg := range segments {
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			return nil, false
		}
		segments[i] = decoded
	}
	return segments, true
}

// match returns the route whose template matches the path's segments
// exactly, and its path parameters, or nil.
func (s *Server) match(segments []string) (*route, map[string]string) {
	for i := range s.routes {
		rt := &s.routes[i]
		if len(rt.segments) != len(segments) {
			continue
		}
		params := map[string]string{}
		matched := true
		for j, seg := range rt.segments {
			if name, ok := strings.CutPrefix(seg, "{"); ok {
				if segments[j] == "" {
					matched = false
					break
				}
				params[strings.TrimSuffix(name, "}")] = segments[j]
				continue
			}
			if seg != segments[j] {
				matched = false
				break
			}
		}
		if matched {
			return rt, params
		}
	}
	return nil, nil
}

// authenticate returns the clock and the active credential of kind that the
// request's Authorization header names, read together. The scheme name is
// case-insensitive, and the key is compared exactly.
func (s *Server) authenticate(r *http.Request, kind string) (time.Time, *credential, *problem) {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return time.Time{}, nil, unauthenticated("The request has no Authorization header; send Authorization: Bearer <api_key>.")
	}
	if len(values) > 1 {
		return time.Time{}, nil, unauthenticated("The request has more than one Authorization header.")
	}
	scheme, key, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || key == "" {
		return time.Time{}, nil, unauthenticated("The Authorization header is malformed; send Authorization: Bearer <api_key>.")
	}
	now, cred := s.state.view(key)
	switch {
	case cred == nil:
		return time.Time{}, nil, unauthenticated("The key is not known.")
	case !cred.active:
		return time.Time{}, nil, unauthenticated("The key is inactive.")
	case cred.kind != kind && kind == customerKind:
		return time.Time{}, nil, unauthenticated("A test-control key is not accepted under /v1; send a customer key.")
	case cred.kind != kind:
		return time.Time{}, nil, unauthenticated("A customer key is not accepted under /test; send the test-control key.")
	}
	return now, cred, nil
}
