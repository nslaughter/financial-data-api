package conformance

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/nslaughter/financial-data-api/internal/expected"
)

// indexPattern matches an array index in a reference's path.
var indexPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// bodies holds the parsed JSON body of each request step with an id that has
// received its response, by id.
type bodies map[string]parsedBody

// parsedBody is a response body parsed as JSON, or why it could not be.
type parsedBody struct {
	value any
	err   error
}

// lookup returns the value r names.
func (b bodies) lookup(r expected.Reference) (any, error) {
	body, ok := b[r.ID]
	if !ok {
		return nil, fmt.Errorf("%s names no step that has run", r)
	}
	if body.err != nil {
		return nil, fmt.Errorf("%s: the body of step %s is not JSON: %v", r, r.ID, body.err)
	}
	v := body.value
	for i, elem := range r.Path {
		where := r.ID + "." + strings.Join(r.Path[:i], ".")
		if i == 0 {
			where = "the body of step " + r.ID
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
	refs, err := expected.FindReferences(s)
	if err != nil || len(refs) == 0 {
		return s, err
	}
	if expected.Whole(s, refs) {
		return b.lookup(refs[0])
	}
	var sb strings.Builder
	last := 0
	for _, r := range refs {
		sb.WriteString(s[last:r.Start])
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
		last = r.End
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
