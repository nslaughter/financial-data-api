package conformance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// documentURL identifies the OpenAPI document to the JSON Schema compiler.
const documentURL = "urn:financial-data-api:openapi"

// maxErrorLines is the most lines of a schema validation error that a
// report shows.
const maxErrorLines = 8

// httpMethods are the members of an OpenAPI path item that are operations.
var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// integerPattern matches a header value that is an integer.
var integerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// Document is an OpenAPI 3.1 document, prepared for validating responses.
// Its schemas are JSON Schema 2020-12, and format is asserted, so a date
// must be a valid calendar date.
type Document struct {
	paths []pathItem
	// problem is the response that a request matching no operation must
	// return, and methodNotAllowed the one it must return with status 405:
	// the document's rules for every path.
	problem          *response
	methodNotAllowed *response
}

// pathItem is a path of the document and its operations by method, in upper
// case.
type pathItem struct {
	segments   []string
	operations map[string]*operation
}

type operation struct {
	// name is the method and path template, such as "GET /v1/meta".
	name string
	// responses are by status code, range such as "4XX", or "default".
	responses map[string]*response
}

type response struct {
	// location is where the response is defined, such as
	// "#/components/responses/BadRequest".
	location string
	headers  []header
	// content holds each media type's schema, by media type in lower case;
	// the schema is nil when the media type has none.
	content map[string]*jsonschema.Schema
}

type header struct {
	name     string
	required bool
	schema   *jsonschema.Schema
	// typ is the schema's type, by which the header's text is converted to
	// a JSON value before validation.
	typ string
}

// ParseOpenAPI parses an OpenAPI 3.1 document in YAML and compiles the
// schemas of every response it defines.
func ParseOpenAPI(data []byte) (*Document, error) {
	var parsed any
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	root, err := toJSON(parsed)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	if err := c.AddResource(documentURL, instance); err != nil {
		return nil, err
	}
	l := &loader{root: root, compiler: c}

	d := &Document{}
	top, _ := root.(map[string]any)
	paths, ok := top["paths"].(map[string]any)
	if !ok {
		return nil, errors.New("the document has no paths")
	}
	for _, template := range sortedKeys(paths) {
		item, ok := paths[template].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path %s is not an object", template)
		}
		p := pathItem{segments: strings.Split(template, "/"), operations: map[string]*operation{}}
		for _, method := range httpMethods {
			op, ok := item[method].(map[string]any)
			if !ok {
				continue
			}
			name := strings.ToUpper(method) + " " + template
			responses, ok := op["responses"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s has no responses", name)
			}
			o := &operation{name: name, responses: map[string]*response{}}
			for _, status := range sortedKeys(responses) {
				r, err := l.response(pointer("paths", template, method, "responses", status))
				if err != nil {
					return nil, fmt.Errorf("%s, response %s: %w", name, status, err)
				}
				o.responses[status] = r
			}
			p.operations[strings.ToUpper(method)] = o
		}
		d.paths = append(d.paths, p)
	}
	problem, err := l.compile(pointer("components", "schemas", "Problem"))
	if err != nil {
		return nil, fmt.Errorf("the Problem schema: %w", err)
	}
	d.problem = &response{
		location: "#" + pointer("components", "schemas", "Problem"),
		content:  map[string]*jsonschema.Schema{"application/problem+json": problem},
	}
	if d.methodNotAllowed, err = l.response(pointer("components", "responses", "MethodNotAllowed")); err != nil {
		return nil, fmt.Errorf("the MethodNotAllowed response: %w", err)
	}
	return d, nil
}

// Validate checks a response against the document. path is the request's
// path without its query. The response must be one the request's operation
// defines for its status, with every required header, each header and the
// body valid against their schemas, and a media type the response defines.
// A request that matches no operation, by its path or its method, follows
// the document's rules for every path: its body must be a Problem, and a 405
// must also be the MethodNotAllowed response, with its Allow header.
func (d *Document) Validate(method, path string, status int, h http.Header, body []byte) error {
	op := d.operation(method, path)
	if op == nil {
		r := d.problem
		if status == http.StatusMethodNotAllowed {
			r = d.methodNotAllowed
		}
		if err := r.validate(h, body); err != nil {
			return fmt.Errorf("%s %s matches no operation, so its response must match %s: %w", method, path, r.location, err)
		}
		return nil
	}
	r := op.response(status)
	if r == nil {
		return fmt.Errorf("%s does not define a %d response", op.name, status)
	}
	if err := r.validate(h, body); err != nil {
		return fmt.Errorf("%s, response %d (%s): %w", op.name, status, r.location, err)
	}
	return nil
}

// operation returns the operation that a request matches, or nil. Of the
// paths that match, the one with the most literal segments is used.
//
// This path matching is deliberately separate from the server's routing in
// internal/api. It reads the paths from spec/openapi.yaml, not from the
// server's route table, so that it checks the server's routing independently
// instead of sharing its mistakes.
func (d *Document) operation(method, path string) *operation {
	segments := strings.Split(path, "/")
	var best *pathItem
	bestLiterals := -1
	for i := range d.paths {
		if n, ok := d.paths[i].match(segments); ok && n > bestLiterals {
			best, bestLiterals = &d.paths[i], n
		}
	}
	if best == nil {
		return nil
	}
	return best.operations[method]
}

// match reports whether the path's segments match the template's, and how
// many of the template's segments are literal. A template segment in braces
// matches any nonempty segment.
func (p *pathItem) match(segments []string) (literals int, ok bool) {
	if len(segments) != len(p.segments) {
		return 0, false
	}
	for i, s := range p.segments {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			if segments[i] == "" {
				return 0, false
			}
			continue
		}
		if s != segments[i] {
			return 0, false
		}
		literals++
	}
	return literals, true
}

// response returns the response the operation defines for status, by its
// code, then its range, then the default.
func (o *operation) response(status int) *response {
	for _, key := range []string{strconv.Itoa(status), fmt.Sprintf("%dXX", status/100), "default"} {
		if r, ok := o.responses[key]; ok {
			return r
		}
	}
	return nil
}

func (r *response) validate(h http.Header, body []byte) error {
	for _, hd := range r.headers {
		values := h.Values(hd.name)
		if len(values) == 0 {
			if hd.required {
				return fmt.Errorf("the %s header is required", hd.name)
			}
			continue
		}
		v, err := hd.value(strings.Join(values, ", "))
		if err != nil {
			return err
		}
		if err := hd.schema.Validate(v); err != nil {
			return fmt.Errorf("the %s header:\n%s", hd.name, describe(err))
		}
	}
	if len(r.content) == 0 {
		if len(body) > 0 {
			return errors.New("the response has a body, but the document defines none")
		}
		return nil
	}
	contentType := h.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("Content-Type %q is not a media type; the document defines %s", contentType, strings.Join(sortedKeys(r.content), ", "))
	}
	schema, ok := r.content[mediaType]
	if !ok {
		return fmt.Errorf("media type %s is not defined; the document defines %s", mediaType, strings.Join(sortedKeys(r.content), ", "))
	}
	if schema == nil {
		return nil
	}
	var instance any = string(body)
	if isJSON(mediaType) {
		if instance, err = jsonschema.UnmarshalJSON(bytes.NewReader(body)); err != nil {
			return fmt.Errorf("the body is not JSON: %v", err)
		}
	}
	if err := schema.Validate(instance); err != nil {
		return fmt.Errorf("the body:\n%s", describe(err))
	}
	return nil
}

// value converts a header's text to the JSON value its schema validates.
func (hd *header) value(text string) (any, error) {
	switch hd.typ {
	case "integer":
		if !integerPattern.MatchString(text) {
			return nil, fmt.Errorf("the %s header %q is not an integer", hd.name, text)
		}
		return json.Number(text), nil
	case "number":
		if _, err := strconv.ParseFloat(text, 64); err != nil {
			return nil, fmt.Errorf("the %s header %q is not a number", hd.name, text)
		}
		return json.Number(text), nil
	case "boolean":
		b, err := strconv.ParseBool(text)
		if err != nil || (text != "true" && text != "false") {
			return nil, fmt.Errorf("the %s header %q is not a boolean", hd.name, text)
		}
		return b, nil
	default:
		return text, nil
	}
}

// isJSON reports whether a media type is JSON.
func isJSON(mediaType string) bool {
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// describe returns a schema validation error without the line naming the
// schema, which the caller names, and shortened if it is long.
func describe(err error) string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return err.Error()
	}
	lines := strings.Split(strings.TrimSpace(ve.Error()), "\n")
	if len(lines) > 1 {
		lines = lines[1:]
	}
	if len(lines) > maxErrorLines {
		lines = append(lines[:maxErrorLines], "  …")
	}
	return strings.Join(lines, "\n")
}

// loader reads the document's responses and compiles their schemas.
type loader struct {
	root     any
	compiler *jsonschema.Compiler
}

// response reads the response object at ptr, following references.
func (l *loader) response(ptr string) (*response, error) {
	obj, loc, err := l.resolve(ptr)
	if err != nil {
		return nil, err
	}
	r := &response{location: "#" + loc, content: map[string]*jsonschema.Schema{}}
	if headers, ok := obj["headers"].(map[string]any); ok {
		for _, name := range sortedKeys(headers) {
			// OpenAPI ignores a Content-Type header definition.
			if strings.EqualFold(name, "Content-Type") {
				continue
			}
			hobj, hloc, err := l.resolve(loc + pointer("headers", name))
			if err != nil {
				return nil, err
			}
			required, _ := hobj["required"].(bool)
			schemaObj, schemaLoc, err := l.resolve(hloc + pointer("schema"))
			if err != nil {
				return nil, fmt.Errorf("header %s: %w", name, err)
			}
			schema, err := l.compile(schemaLoc)
			if err != nil {
				return nil, fmt.Errorf("header %s: %w", name, err)
			}
			typ, _ := schemaObj["type"].(string)
			r.headers = append(r.headers, header{name: name, required: required, schema: schema, typ: typ})
		}
	}
	if content, ok := obj["content"].(map[string]any); ok {
		for _, mediaType := range sortedKeys(content) {
			media, ok := content[mediaType].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("media type %s is not an object", mediaType)
			}
			var schema *jsonschema.Schema
			if _, ok := media["schema"]; ok {
				if schema, err = l.compile(loc + pointer("content", mediaType, "schema")); err != nil {
					return nil, fmt.Errorf("media type %s: %w", mediaType, err)
				}
			}
			r.content[strings.ToLower(mediaType)] = schema
		}
	}
	return r, nil
}

// resolve returns the object at ptr and its location, following $ref to
// another place in the document.
func (l *loader) resolve(ptr string) (map[string]any, string, error) {
	for range 10 {
		v, err := l.at(ptr)
		if err != nil {
			return nil, "", err
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("#%s is not an object", ptr)
		}
		ref, ok := obj["$ref"].(string)
		if !ok {
			return obj, ptr, nil
		}
		if !strings.HasPrefix(ref, "#") {
			return nil, "", fmt.Errorf("#%s refers outside the document, to %s", ptr, ref)
		}
		next, err := url.PathUnescape(ref[1:])
		if err != nil {
			return nil, "", fmt.Errorf("#%s: reference %s: %w", ptr, ref, err)
		}
		ptr = next
	}
	return nil, "", fmt.Errorf("#%s: too many references", ptr)
}

// at returns the value at a JSON pointer into the document.
func (l *loader) at(ptr string) (any, error) {
	if ptr == "" {
		return l.root, nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, fmt.Errorf("#%s is not a JSON pointer", ptr)
	}
	v := l.root
	for _, token := range strings.Split(ptr[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("#%s does not exist", ptr)
		}
		if v, ok = obj[token]; !ok {
			return nil, fmt.Errorf("#%s does not exist", ptr)
		}
	}
	return v, nil
}

// compile compiles the schema at a JSON pointer into the document.
func (l *loader) compile(ptr string) (*jsonschema.Schema, error) {
	return l.compiler.Compile(documentURL + "#" + (&url.URL{Fragment: ptr}).EscapedFragment())
}

// pointer returns the JSON pointer to a member path, escaping each name.
func pointer(names ...string) string {
	var sb strings.Builder
	for _, name := range names {
		sb.WriteString("/")
		sb.WriteString(strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1"))
	}
	return sb.String()
}

// toJSON returns a value decoded from YAML with only JSON's types, failing
// on any other, such as a time.
func toJSON(v any) (any, error) {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			c, err := toJSON(e)
			if err != nil {
				return nil, err
			}
			out[k] = c
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			c, err := toJSON(e)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case nil, bool, string, int, int64, uint64, float64:
		return v, nil
	default:
		return nil, fmt.Errorf("the document holds %v, a %T, which JSON cannot represent", v, v)
	}
}
