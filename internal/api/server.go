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
	"strings"
	"time"

	"github.com/nslaughter/financial-data-api/internal/exports"
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
	// ContractVersion is the contract version the server implements, which
	// GET /v1/meta reports: that of the fixtures built into the binary.
	ContractVersion string
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
	catalog         *catalog
	state           *state
	routes          []route
}

// New returns a server for cfg.
func New(cfg Config) (*Server, error) {
	if cfg.Fixtures == nil {
		return nil, errors.New("api: no fixtures")
	}
	if cfg.ContractVersion == "" {
		return nil, errors.New("api: no contract version")
	}
	if cfg.ClockStart.After(MaxClock) {
		return nil, fmt.Errorf("api: the clock start %s is later than %s", formatTimestamp(cfg.ClockStart), formatTimestamp(MaxClock))
	}
	h, err := history.New(cfg.Fixtures)
	if err != nil {
		return nil, err
	}
	st, err := newState(cfg.ClockStart, cfg.Fixtures.Credentials, exports.NewStore(h))
	if err != nil {
		return nil, err
	}
	s := &Server{
		contractVersion: cfg.ContractVersion,
		history:         h,
		catalog:         newCatalog(cfg.Fixtures),
		state:           st,
	}
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

// endpoint is one method of a route, with its request contract.
type endpoint struct {
	// auth is the kind of credential the endpoint requires, or "" for none.
	auth string
	// query names the query parameters the endpoint defines.
	query []string
	// body is the body the endpoint takes, or nil if it takes none, so that
	// a body sent with it is ignored.
	body   *bodyContract
	handle func(w http.ResponseWriter, c *call) *problem
}

// bodyContract is the body an endpoint takes: a JSON object whose fields are
// among those it names. An endpoint whose body has no fields still declares
// one, so that any field is refused.
type bodyContract struct {
	fields []string
}

// call is one request, as an endpoint sees it.
type call struct {
	// endpoint names the endpoint, such as "GET /v1/series/{series_id}".
	endpoint string
	// path holds the path parameters by name.
	path map[string]string
	// query holds the query parameters, which the endpoint defines.
	query queryParams
	// body holds the fields of the body, which the endpoint defines, or is
	// nil for an endpoint that takes no body.
	body bodyFields
	// moment holds the clock, read once for the request, the page-token key,
	// the export store, and the authenticated credential, which is nil for
	// an endpoint that requires none.
	moment
}

func (s *Server) routeTable(testControl bool) []route {
	const (
		customer = fixtures.CustomerKind
		test     = fixtures.TestControlKind
	)
	routes := []route{
		{template: "/v1/meta", endpoints: map[string]endpoint{
			http.MethodGet: {handle: s.meta},
		}},
		{template: "/v1/datasets", endpoints: map[string]endpoint{
			http.MethodGet: {auth: customer, handle: s.listDatasets},
		}},
		{template: "/v1/datasets/{dataset_id}", endpoints: map[string]endpoint{
			http.MethodGet: {auth: customer, handle: s.getDataset},
		}},
		{template: "/v1/datasets/{dataset_id}/changes", endpoints: map[string]endpoint{
			http.MethodGet: {auth: customer, query: []string{"after", "limit"}, handle: s.readChanges},
		}},
		{template: "/v1/datasets/{dataset_id}/exports", endpoints: map[string]endpoint{
			http.MethodPost: {auth: customer, body: &bodyContract{}, handle: s.createExport},
		}},
		{template: "/v1/series", endpoints: map[string]endpoint{
			http.MethodGet: {auth: customer, handle: s.listSeries},
		}},
		{template: "/v1/series/{series_id}", endpoints: map[string]endpoint{
			http.MethodGet: {auth: customer, handle: s.getSeries},
		}},
		{template: "/v1/observations", endpoints: map[string]endpoint{
			http.MethodGet: {
				auth:   customer,
				query:  []string{"series_id", "period_start", "period_end", "available_as_of", "published_as_of", "page_size", "page_token"},
				handle: s.queryObservations,
			},
		}},
		{template: "/v1/revisions", endpoints: map[string]endpoint{
			http.MethodGet: {
				auth:   customer,
				query:  []string{"series_id", "period_start", "period_end", "available_as_of", "page_size", "page_token"},
				handle: s.listRevisions,
			},
		}},
		{template: "/v1/release-calendar", endpoints: map[string]endpoint{
			http.MethodGet: {auth: customer, query: []string{"series_id", "period_start", "period_end"}, handle: s.releaseCalendar},
		}},
		{template: "/v1/exports/{export_id}", endpoints: map[string]endpoint{
			http.MethodGet: {auth: customer, handle: s.getExport},
		}},
		{template: "/v1/exports/{export_id}/files/{file_name}", endpoints: map[string]endpoint{
			http.MethodGet: {auth: customer, handle: s.downloadExportFile},
		}},
	}
	if testControl {
		routes = append(routes,
			route{template: "/test/clock", endpoints: map[string]endpoint{
				http.MethodGet: {auth: test, handle: s.getClock},
				http.MethodPut: {auth: test, body: &bodyContract{fields: []string{"now"}}, handle: s.setClock},
			}},
			route{template: "/test/reset", endpoints: map[string]endpoint{
				http.MethodPost: {auth: test, body: &bodyContract{fields: []string{"clock"}}, handle: s.reset},
			}},
			route{template: "/test/credentials/{credential_id}", endpoints: map[string]endpoint{
				http.MethodPut: {auth: test, body: &bodyContract{fields: []string{"active", "datasets"}}, handle: s.changeCredential},
			}},
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
	c := &call{endpoint: r.Method + " " + rt.template, path: params}
	var p *problem
	if ep.auth == "" {
		c.now = s.state.now()
	} else if c.moment, p = s.authenticate(r, ep.auth); p != nil {
		return p
	}

	// Request validation: the names of the parameters and fields, repeated
	// and empty parameters, and the form of the body. The handler converts
	// the values.
	if c.query, p = parseQuery(r.URL.RawQuery, c.endpoint, ep.query...); p != nil {
		return p
	}
	if ep.body != nil {
		if c.body, p = parseBody(r, c.endpoint, ep.body.fields...); p != nil {
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

// authenticate returns the active credential of kind that the request's
// Authorization header names, read together with the clock and the
// page-token key. The scheme name is case-insensitive and followed by one or
// more spaces, as RFC 6750 allows, and the key is compared exactly.
func (s *Server) authenticate(r *http.Request, kind string) (moment, *problem) {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return moment{}, unauthenticated("The request has no Authorization header; send Authorization: Bearer <api_key>.")
	}
	if len(values) > 1 {
		return moment{}, unauthenticated("The request has more than one Authorization header.")
	}
	scheme, key, ok := strings.Cut(values[0], " ")
	key = strings.TrimLeft(key, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || key == "" {
		return moment{}, unauthenticated("The Authorization header is malformed; send Authorization: Bearer <api_key>.")
	}
	m := s.state.view(key)
	switch cred := m.cred; {
	case cred == nil:
		return moment{}, unauthenticated("The key is not known.")
	case !cred.active:
		return moment{}, unauthenticated("The key is inactive.")
	case cred.kind != kind && kind == fixtures.CustomerKind:
		return moment{}, unauthenticated("A test-control key is not accepted under /v1; send a customer key.")
	case cred.kind != kind:
		return moment{}, unauthenticated("A customer key is not accepted under /test; send the test-control key.")
	}
	return m, nil
}
