package main

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	financialdataapi "github.com/nslaughter/financial-data-api"
)

// env returns a getenv that reads vars; a variable not in vars is unset.
func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestLoadConfigDefaults(t *testing.T) {
	// A variable set to an empty value takes its default, as an unset one
	// does.
	for _, vars := range []map[string]string{
		{},
		{"PORT": "", "CLOCK_START": "", "TEST_CONTROL": "", "FIXTURES_DIR": ""},
	} {
		cfg, err := loadConfig(env(vars))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.port != 8080 || cfg.clockStart.Format(time.RFC3339) != "2026-10-01T00:00:00Z" || cfg.testControl || cfg.fixturesDir != "" {
			t.Errorf("%v: got %+v", vars, cfg)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	cfg, err := loadConfig(env(map[string]string{
		"PORT":         "9090",
		"CLOCK_START":  "9999-12-30T23:59:59Z",
		"TEST_CONTROL": "enabled",
		"FIXTURES_DIR": "/srv/fixtures",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.port != 9090 || cfg.clockStart.Format(time.RFC3339) != "9999-12-30T23:59:59Z" || !cfg.testControl || cfg.fixturesDir != "/srv/fixtures" {
		t.Errorf("got %+v", cfg)
	}
	if cfg, err := loadConfig(env(map[string]string{"TEST_CONTROL": "disabled"})); err != nil || cfg.testControl {
		t.Errorf("TEST_CONTROL=disabled: %+v, %v", cfg, err)
	}

	for _, tt := range []struct {
		name, value, want string
	}{
		{"CLOCK_START", "9999-12-31T00:00:00Z", "later than 9999-12-30T23:59:59Z"},
		{"CLOCK_START", "2026-10-01", "not a timestamp"},
		{"CLOCK_START", "2026-10-01T00:00:00+00:00", "not a timestamp"},
		{"CLOCK_START", "2026-02-30T00:00:00Z", "not a timestamp"},
		{"TEST_CONTROL", "true", "neither enabled nor disabled"},
		{"TEST_CONTROL", "Enabled", "neither enabled nor disabled"},
		{"PORT", "0", "not a TCP port"},
		{"PORT", "65536", "not a TCP port"},
		{"PORT", "08080", "not a TCP port"},
		{"PORT", "-1", "not a TCP port"},
		{"PORT", "http", "not a TCP port"},
	} {
		if _, err := loadConfig(env(map[string]string{tt.name: tt.value})); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s=%s: got %v, want an error containing %q", tt.name, tt.value, err, tt.want)
		}
	}
}

func TestRunRefusesToStart(t *testing.T) {
	broken := copyFixtures(t)
	path := filepath.Join(broken, "credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"datasets": ["core-indicators"]`), []byte(`"datasets": ["no-such-dataset"]`), 1)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name string
		vars map[string]string
		want []string
	}{
		{"a late clock", map[string]string{"CLOCK_START": "9999-12-31T00:00:00Z"}, []string{"CLOCK_START", "later than 9999-12-30T23:59:59Z"}},
		{"an invalid variable", map[string]string{"TEST_CONTROL": "yes"}, []string{"TEST_CONTROL"}},
		{"fixtures that break an invariant", map[string]string{"FIXTURES_DIR": broken}, []string{"credentials.json", "cred_research", "invariant 2"}},
		{"a directory without fixtures", map[string]string{"FIXTURES_DIR": t.TempDir()}, []string{"datasets.json"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := run(context.Background(), env(tt.vars), &stderr); code == 0 {
				t.Fatal("exit status 0")
			}
			for _, want := range tt.want {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
				}
			}
		})
	}
}

// TestRunServes starts the server on a free port, with every other variable
// empty, and stops it.
func TestRunServes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int)
	go func() {
		done <- run(ctx, env(map[string]string{"PORT": strconv.Itoa(port), "CLOCK_START": "", "TEST_CONTROL": "", "FIXTURES_DIR": ""}), io.Discard)
	}()
	url := "http://127.0.0.1:" + strconv.Itoa(port)
	var body []byte
	for start := time.Now(); time.Since(start) < 5*time.Second; time.Sleep(20 * time.Millisecond) {
		resp, err := http.Get(url + "/v1/meta")
		if err != nil {
			continue
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		break
	}
	if !strings.Contains(string(body), `"server_time":"2026-10-01T00:00:00Z"`) {
		t.Errorf("GET /v1/meta: %s", body)
	}
	// Test control is disabled by default.
	resp, err := http.Get(url + "/test/clock")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /test/clock: %d", resp.StatusCode)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit status %d", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the server did not stop")
	}
}

// copyFixtures copies the built-in fixtures to a new directory.
func copyFixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fsys := financialdataapi.Fixtures()
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
