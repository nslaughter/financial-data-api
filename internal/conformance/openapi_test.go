package conformance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// These tests validate responses against spec/openapi.yaml itself.

func problemJSON(status int, code, title string, parameter any) string {
	data, _ := json.Marshal(map[string]any{"status": status, "code": code, "title": title, "detail": "d", "parameter": parameter})
	return string(data)
}

func toJSONText(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestValidate(t *testing.T) {
	record := revisionRecords(t)[35]
	withRecord := func(change func(map[string]any)) string {
		r := map[string]any{}
		for k, v := range record {
			r[k] = v
		}
		change(r)
		return toJSONText(t, observationPage([]map[string]any{r}, nil))
	}
	manifest := `{"export_id": "exp_1", "dataset_id": "core-indicators", "position": 0,
		"created_at": "2024-01-01T00:00:00Z", "expires_at": "2024-01-02T00:00:00Z",
		"api_version": "v1", "contract_version": "0.3.0",
		"coverage": {"series_ids": [], "observation_count": 0, "revision_count": 0, "period_start": null, "period_end": null},
		"files": [{"name": "revisions.jsonl", "url": "/v1/exports/exp_1/files/revisions.jsonl", "media_type": "application/x-ndjson",
			"record_count": 0, "size_bytes": 0, "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}]}`
	jsonType := "application/json"
	problemType := "application/problem+json"

	tests := []struct {
		name         string
		method, path string
		status       int
		headers      map[string]string
		body         string
		// want is part of the error, or "" for a valid response.
		want string
	}{
		{name: "meta", method: "GET", path: "/v1/meta", status: 200, headers: map[string]string{"Content-Type": jsonType}, body: toJSONText(t, meta())},
		{name: "a media type parameter", method: "GET", path: "/v1/meta", status: 200, headers: map[string]string{"Content-Type": "application/json; charset=utf-8"}, body: toJSONText(t, meta())},
		{name: "an observation page", method: "GET", path: "/v1/observations", status: 200, headers: map[string]string{"Content-Type": jsonType}, body: withRecord(func(map[string]any) {})},
		{name: "a dataset", method: "GET", path: "/v1/datasets/core-indicators", status: 200, headers: map[string]string{"Content-Type": jsonType}, body: toJSONText(t, dataset(nil))},
		{name: "a change page", method: "GET", path: "/v1/datasets/core-indicators/changes", status: 200, headers: map[string]string{"Content-Type": jsonType},
			body: `{"data": [], "next_position": 37, "head_position": 37}`},
		{name: "a problem", method: "GET", path: "/v1/observations", status: 400, headers: map[string]string{"Content-Type": problemType},
			body: problemJSON(400, "unknown_parameter", "Unknown parameter", "available_asof")},
		{name: "a 401 with WWW-Authenticate", method: "GET", path: "/v1/series", status: 401, headers: map[string]string{"Content-Type": problemType, "WWW-Authenticate": "Bearer"},
			body: problemJSON(401, "unauthenticated", "Unauthenticated", nil)},
		{name: "a created export", method: "POST", path: "/v1/datasets/core-indicators/exports", status: 201,
			headers: map[string]string{"Content-Type": jsonType, "Location": "/v1/exports/exp_1"}, body: manifest},
		{name: "an export file", method: "GET", path: "/v1/exports/exp_1/files/revisions.jsonl", status: 200,
			headers: map[string]string{"Content-Type": "application/x-ndjson", "Content-Length": "3"}, body: "{}\n"},
		{name: "an unknown path", method: "GET", path: "/v1/nothing", status: 404, headers: map[string]string{"Content-Type": problemType},
			body: problemJSON(404, "not_found", "Not found", nil)},
		{name: "a trailing slash is an unknown path", method: "GET", path: "/v1/series/", status: 404, headers: map[string]string{"Content-Type": problemType},
			body: problemJSON(404, "not_found", "Not found", nil)},
		{name: "an unsupported method with Allow", method: "POST", path: "/test/clock", status: 405, headers: map[string]string{"Content-Type": problemType, "Allow": "GET, PUT"},
			body: problemJSON(405, "method_not_allowed", "Method not allowed", nil)},

		{name: "an omitted nullable field", method: "GET", path: "/v1/observations", status: 200, headers: map[string]string{"Content-Type": jsonType},
			body: withRecord(func(r map[string]any) { delete(r, "missing_reason") }), want: "missing property 'missing_reason'"},
		{name: "a value that is a number", method: "GET", path: "/v1/observations", status: 200, headers: map[string]string{"Content-Type": jsonType},
			body: withRecord(func(r map[string]any) { r["value"] = json.Number("102.4") }), want: "/data/0/value"},
		{name: "a withdrawal with a value", method: "GET", path: "/v1/observations", status: 200, headers: map[string]string{"Content-Type": jsonType},
			body: withRecord(func(r map[string]any) { r["change_type"] = "withdrawal" }), want: "want null"},
		{name: "an invalid calendar date", method: "GET", path: "/v1/observations", status: 200, headers: map[string]string{"Content-Type": jsonType},
			body: withRecord(func(r map[string]any) { r["period_start"] = "2026-02-30" }), want: "is not valid date"},
		{name: "a fractional timestamp", method: "GET", path: "/v1/observations", status: 200, headers: map[string]string{"Content-Type": jsonType},
			body: withRecord(func(r map[string]any) { r["available_at"] = "2026-09-03T12:31:10.5Z" }), want: "/data/0/available_at"},
		{name: "a problem with the wrong title", method: "GET", path: "/v1/observations", status: 400, headers: map[string]string{"Content-Type": problemType},
			body: problemJSON(400, "unknown_parameter", "Unknown", "x"), want: "'oneOf' failed"},
		{name: "a problem without parameter", method: "GET", path: "/v1/observations", status: 400, headers: map[string]string{"Content-Type": problemType},
			body: `{"status": 400, "code": "unknown_parameter", "title": "Unknown parameter", "detail": "d"}`, want: "missing property 'parameter'"},
		{name: "a 401 without WWW-Authenticate", method: "GET", path: "/v1/series", status: 401, headers: map[string]string{"Content-Type": problemType},
			body: problemJSON(401, "unauthenticated", "Unauthenticated", nil), want: "the WWW-Authenticate header is required"},
		{name: "a 401 with another scheme", method: "GET", path: "/v1/series", status: 401, headers: map[string]string{"Content-Type": problemType, "WWW-Authenticate": "Basic"},
			body: problemJSON(401, "unauthenticated", "Unauthenticated", nil), want: "the WWW-Authenticate header"},
		{name: "an undefined status", method: "GET", path: "/v1/meta", status: 401, headers: map[string]string{"Content-Type": problemType},
			body: problemJSON(401, "unauthenticated", "Unauthenticated", nil), want: "GET /v1/meta does not define a 401 response"},
		{name: "a 405 for an operation that exists", method: "GET", path: "/v1/meta", status: 405, headers: map[string]string{"Content-Type": problemType, "Allow": "GET"},
			body: problemJSON(405, "method_not_allowed", "Method not allowed", nil), want: "does not define a 405 response"},
		{name: "an undefined media type", method: "GET", path: "/v1/meta", status: 200, headers: map[string]string{"Content-Type": "text/plain"},
			body: toJSONText(t, meta()), want: "media type text/plain is not defined"},
		{name: "no Content-Type", method: "GET", path: "/v1/meta", status: 200, body: toJSONText(t, meta()), want: "is not a media type"},
		{name: "a body that is not JSON", method: "GET", path: "/v1/meta", status: 200, headers: map[string]string{"Content-Type": jsonType},
			body: `{"api_version": "v1"`, want: "the body is not JSON"},
		{name: "a created export without Location", method: "POST", path: "/v1/datasets/core-indicators/exports", status: 201,
			headers: map[string]string{"Content-Type": jsonType}, body: manifest, want: "the Location header is required"},
		{name: "an export file without Content-Length", method: "GET", path: "/v1/exports/exp_1/files/revisions.jsonl", status: 200,
			headers: map[string]string{"Content-Type": "application/x-ndjson"}, body: "{}\n", want: "the Content-Length header is required"},
		{name: "an export file whose Content-Length is not an integer", method: "GET", path: "/v1/exports/exp_1/files/revisions.jsonl", status: 200,
			headers: map[string]string{"Content-Type": "application/x-ndjson", "Content-Length": "3.0"}, body: "{}\n", want: "is not an integer"},
		{name: "an unknown path that is not a problem", method: "GET", path: "/v1/nothing", status: 404, headers: map[string]string{"Content-Type": jsonType},
			body: `{}`, want: "matches no operation"},
		{name: "a trailing slash answered as the series", method: "GET", path: "/v1/series/", status: 200, headers: map[string]string{"Content-Type": jsonType},
			body: `{"data": []}`, want: "matches no operation"},
		{name: "an unsupported method without Allow", method: "DELETE", path: "/v1/observations", status: 405, headers: map[string]string{"Content-Type": problemType},
			body: problemJSON(405, "method_not_allowed", "Method not allowed", nil), want: "the Allow header is required"},
		{name: "the change stream answered as a dataset", method: "GET", path: "/v1/datasets/core-indicators/changes", status: 200, headers: map[string]string{"Content-Type": jsonType},
			body: toJSONText(t, dataset(json.Number("37"))), want: "missing properties 'data', 'next_position'"},
	}
	d := openAPI(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tt.headers {
				h.Set(k, v)
			}
			err := d.Validate(tt.method, tt.path, tt.status, h, []byte(tt.body))
			if tt.want == "" {
				if err != nil {
					t.Fatalf("got %v, want no error", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestParseOpenAPIRefusesWhatJSONCannotHold(t *testing.T) {
	_, err := ParseOpenAPI([]byte("openapi: 3.1.0\npaths: {}\nx-when: !!timestamp 2026-10-01\n"))
	if err == nil || !strings.Contains(err.Error(), "JSON cannot represent") {
		t.Fatalf("got %v, want an error about a type JSON cannot represent", err)
	}
}

func TestParseOpenAPINeedsTheRulesForEveryPath(t *testing.T) {
	_, err := ParseOpenAPI([]byte("openapi: 3.1.0\npaths: {}\ncomponents:\n  schemas:\n    Problem: {type: object}\n"))
	if err == nil || !strings.Contains(err.Error(), "MethodNotAllowed") {
		t.Fatalf("got %v, want an error about the MethodNotAllowed response", err)
	}
}
