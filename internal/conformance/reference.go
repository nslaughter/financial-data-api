package conformance

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// referencePattern matches a reference, ${<id>.<path>}.
var referencePattern = regexp.MustCompile(`\$\{([^{}]*)\}`)

// indexPattern matches an array index in a reference's path.
var indexPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// reference is a reference in a string: s[start:end] is ${<id>.<path>}.
type reference struct {
	start, end int
	id         string
	path       []string
}

func (r reference) String() string {
	return "${" + r.id + "." + strings.Join(r.path, ".") + "}"
}

// findReferences returns the references in s, in order.
func findReferences(s string) ([]reference, error) {
	var refs []reference
	for _, m := range referencePattern.FindAllStringSubmatchIndex(s, -1) {
		text := s[m[0]:m[1]]
		id, path, ok := strings.Cut(s[m[2]:m[3]], ".")
		if !ok || id == "" {
			return nil, fmt.Errorf("malformed reference %s: want ${<id>.<path>}", text)
		}
		r := reference{start: m[0], end: m[1], id: id, path: strings.Split(path, ".")}
		for _, elem := range r.path {
			if elem == "" {
				return nil, fmt.Errorf("malformed reference %s: its path has an empty member name", text)
			}
		}
		refs = append(refs, r)
	}
	return refs, nil
}

// whole reports whether refs, the references in s, are one reference that is
// all of s. Only such a string resolves to a value that may not be a string.
func whole(s string, refs []reference) bool {
	return len(refs) == 1 && refs[0].start == 0 && refs[0].end == len(s)
}

// wholeReference reports whether v is a string that is exactly one
// well-formed reference.
func wholeReference(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	refs, err := findReferences(s)
	return err == nil && whole(s, refs)
}

// bodies holds the parsed JSON body of each request step with an id that has
// received its response, by id.
type bodies map[string]parsedBody

// parsedBody is a response body parsed as JSON, or why it could not be.
type parsedBody struct {
	value any
	err   error
}

// lookup returns the value r names.
func (b bodies) lookup(r reference) (any, error) {
	body, ok := b[r.id]
	if !ok {
		return nil, fmt.Errorf("%s names no step that has run", r)
	}
	if body.err != nil {
		return nil, fmt.Errorf("%s: the body of step %s is not JSON: %v", r, r.id, body.err)
	}
	v := body.value
	for i, elem := range r.path {
		where := r.id + "." + strings.Join(r.path[:i], ".")
		if i == 0 {
			where = "the body of step " + r.id
		}
		switch c := v.(type) {
		case map[string]any:
			member, ok := c[elem]
			if !ok {
				return nil, fmt.Errorf("%s: %s has no member %q", r, where, elem)
			}
			v = member
		case []any:
			n, err := strconv.Atoi(elem)
			if !indexPattern.MatchString(elem) || err != nil || n >= len(c) {
				return nil, fmt.Errorf("%s: %s, an array of %d elements, has no element %s", r, where, len(c), elem)
			}
			v = c[n]
		default:
			return nil, fmt.Errorf("%s: %s is %s, which has no member %q", r, where, jsonType(v), elem)
		}
	}
	return v, nil
}

// resolveString replaces the references in s. A string that is exactly one
// reference becomes the referenced value, of any JSON type. A reference that
// is part of a longer string must name a string or a number, which is
// inserted as text.
func (b bodies) resolveString(s string) (any, error) {
	refs, err := findReferences(s)
	if err != nil || len(refs) == 0 {
		return s, err
	}
	if whole(s, refs) {
		return b.lookup(refs[0])
	}
	var sb strings.Builder
	last := 0
	for _, r := range refs {
		sb.WriteString(s[last:r.start])
		v, err := b.lookup(r)
		if err != nil {
			return nil, err
		}
		switch v := v.(type) {
		case string:
			sb.WriteString(v)
		case json.Number:
			sb.WriteString(v.String())
		default:
			return nil, fmt.Errorf("%s is part of a longer string, so it must name a string or a number, not %s", r, jsonType(v))
		}
		last = r.end
	}
	sb.WriteString(s[last:])
	return sb.String(), nil
}

// resolve returns v, a JSON value, with the references in its strings
// replaced. Object member names are not resolved. v is not changed.
func (b bodies) resolve(v any) (any, error) {
	switch v := v.(type) {
	case string:
		return b.resolveString(v)
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			r, err := b.resolve(e)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			r, err := b.resolve(e)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

// resolveText resolves the references in s, which must still be a string.
func (b bodies) resolveText(what, s string) (string, error) {
	v, err := b.resolveString(s)
	if err != nil {
		return "", err
	}
	text, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s %q is %s after resolving references; it must be a string", what, s, jsonType(v))
	}
	return text, nil
}

// jsonType names the JSON type of v.
func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case json.Number:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	default:
		return fmt.Sprintf("a %T", v)
	}
}
