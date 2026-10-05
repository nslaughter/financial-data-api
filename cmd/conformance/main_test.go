package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// server returns a fake server that answers the resets that start every
// check and serves core-indicators with headPosition.
func server(t *testing.T, headPosition int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /test/reset":
			var body struct{ Clock string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{"now": body.Clock})
		case "GET /v1/datasets/core-indicators":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dataset_id": "core-indicators", "name": "n", "description": "d", "entitled": true, "head_position": headPosition,
			})
		default:
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 404, "code": "not_found", "title": "Not found", "detail": "d", "parameter": nil})
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func runCommand(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestPassingRun(t *testing.T) {
	url := server(t, 36)
	code, stdout, stderr := runCommand("--base-url", url, "--stage", "1", "--file", "change-stream", "--check", "export snapshot time")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	want := "ok    change-stream: position check \"export snapshot time\"\n\n1 passed, 0 failed, 0 skipped\n"
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestFailingRun(t *testing.T) {
	url := server(t, 37)
	code, stdout, _ := runCommand("--base-url", url, "--stage", "1", "--file", "change-stream.json", "--check", "export snapshot time")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, stdout)
	}
	for _, want := range []string{
		"FAIL  change-stream: position check \"export snapshot time\"\n",
		"      GET /v1/datasets/core-indicators\n",
		"      at body.head_position\n",
		"        expected: 36\n",
		"        actual:   37\n",
		"0 passed, 1 failed, 0 skipped\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

// TestSkippedScenario runs a scenario that does not run at stage 2, which
// sends no request, so no server is needed.
func TestSkippedScenario(t *testing.T) {
	code, stdout, stderr := runCommand("--base-url", "http://127.0.0.1:1", "--stage", "2", "--file", "request-errors", "--check", "a stage 1 server refuses")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "skip  request-errors: scenario \"a stage 1 server refuses stage 2 parameters and paths\": its stages do not list stage 2\n") ||
		!strings.Contains(stdout, "0 passed, 0 failed, 1 skipped\n") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{nil, "--stage is required"},
		{[]string{"--stage", "3"}, "stages are 1 to 2"},
		{[]string{"--stage", "1", "--file", "exports"}, "exports is required from stage 2"},
		{[]string{"--stage", "1", "--file", "nothing"}, "nothing is not a file of the stage table"},
		{[]string{"--stage", "1", "--base-url", "localhost:8080"}, "want http:// or https://"},
		{[]string{"--stage", "1", "extra"}, "unexpected arguments: extra"},
		{[]string{"--stage", "1", "--check", "no check has this name"}, `no check's name contains "no check has this name"`},
		{[]string{"--stages", "1"}, "flag provided but not defined: -stages"},
	} {
		code, stdout, stderr := runCommand(tt.args...)
		if code != 2 || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: exit %d, stderr %q, stdout %q; want 2 and %q", tt.args, code, stderr, stdout, tt.want)
		}
	}
}
