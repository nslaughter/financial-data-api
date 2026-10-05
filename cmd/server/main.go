// Command server serves the financial data API of spec/api.md over plain
// HTTP. It reads these environment variables, treating one set to an empty
// value as unset:
//
//	PORT          the TCP port, 8080 by default
//	CLOCK_START   the simulated clock at startup and after a reset without a
//	              clock, 2026-10-01T00:00:00Z by default, and no later than
//	              9999-12-30T23:59:59Z
//	TEST_CONTROL  enabled to serve the /test endpoints, or disabled, the
//	              default
//	FIXTURES_DIR  a directory holding the files of fixtures/; by default,
//	              the fixtures built into the binary
//
// It refuses to start, exiting with status 1, on an invalid value or on
// fixtures that break the data contract's invariants, naming the file, the
// record, and the rule. The server implements the contract version of its
// built-in fixtures, which GET /v1/meta reports, so it also refuses fixtures
// that record another contract version, and fixtures with a sequence of 2^53
// or more, since spec/api.md keeps integers below 2^53. It stops on SIGINT
// or SIGTERM.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	financialdataapi "github.com/nslaughter/financial-data-api"
	"github.com/nslaughter/financial-data-api/internal/api"
	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

const (
	defaultPort       = 8080
	defaultClockStart = "2026-10-01T00:00:00Z"
	// shutdownTimeout is how long the server waits for requests in flight
	// when it stops.
	shutdownTimeout = 10 * time.Second
)

// portPattern matches a port without a sign or leading zeros.
var portPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Getenv, os.Stderr)
	stop()
	os.Exit(code)
}

// config is the server's configuration from its environment.
type config struct {
	port        int
	clockStart  time.Time
	testControl bool
	// fixturesDir is the directory of fixtures, or "" for the built-in ones.
	fixturesDir string
}

// loadConfig reads the environment through getenv. A variable set to an
// empty value is treated as unset, so its default applies.
func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{port: defaultPort, fixturesDir: getenv("FIXTURES_DIR")}
	if v := getenv("PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if !portPattern.MatchString(v) || err != nil || port > 65535 {
			return config{}, fmt.Errorf("PORT %q is not a TCP port from 1 to 65535", v)
		}
		cfg.port = port
	}
	start := defaultClockStart
	if v := getenv("CLOCK_START"); v != "" {
		start = v
	}
	t, ok := fixtures.ParseTimestamp(start)
	if !ok {
		return config{}, fmt.Errorf("CLOCK_START %q is not a timestamp in the form YYYY-MM-DDTHH:MM:SSZ", start)
	}
	if t.After(api.MaxClock) {
		return config{}, fmt.Errorf("CLOCK_START %s is later than %s, the latest the clock may be", start, api.MaxClock.Format(fixtures.TimestampLayout))
	}
	cfg.clockStart = t
	switch v := getenv("TEST_CONTROL"); v {
	case "", "disabled":
	case "enabled":
		cfg.testControl = true
	default:
		return config{}, fmt.Errorf("TEST_CONTROL %q is neither enabled nor disabled", v)
	}
	return cfg, nil
}

// newHandler loads the fixtures and returns the API they serve. The server
// implements the contract version of the built-in fixtures, and fixtures
// from FIXTURES_DIR must record the same one. Their version is checked
// before their records, since another version's records may have other
// members or rules, and refusing them by this version's would hide the
// mismatch.
func newHandler(cfg config) (*api.Server, error) {
	builtIn, err := loadFixtures(financialdataapi.Fixtures())
	if err != nil {
		return nil, err
	}
	f := builtIn
	if cfg.fixturesDir != "" {
		fsys := os.DirFS(cfg.fixturesDir)
		version, err := fixtures.ReadContractVersion(fsys)
		if err != nil {
			return nil, fmt.Errorf("the fixtures: %w", err)
		}
		if version != builtIn.ContractVersion {
			return nil, fmt.Errorf("the fixtures record contract version %s, but the server implements %s", version, builtIn.ContractVersion)
		}
		if f, err = loadFixtures(fsys); err != nil {
			return nil, err
		}
	}
	if err := fixtures.CheckIntegers(f); err != nil {
		return nil, fmt.Errorf("the fixtures hold integers the API cannot serve:\n%w", err)
	}
	return api.New(api.Config{Fixtures: f, ContractVersion: builtIn.ContractVersion, ClockStart: cfg.clockStart, TestControl: cfg.testControl})
}

// loadFixtures loads the fixtures in fsys and checks the invariants.
func loadFixtures(fsys fs.FS) (*fixtures.Fixtures, error) {
	f, err := fixtures.Load(fsys)
	if err != nil {
		var vs fixtures.Violations
		if errors.As(err, &vs) {
			return nil, fmt.Errorf("the fixtures break the data contract's invariants:\n%w", err)
		}
		return nil, fmt.Errorf("the fixtures: %w", err)
	}
	return f, nil
}

// run starts the server and serves until ctx is done. It returns the
// process's exit status.
func run(ctx context.Context, getenv func(string) string, stderr io.Writer) int {
	cfg, err := loadConfig(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "server: %v\n", err)
		return 1
	}
	handler, err := newHandler(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "server: %v\n", err)
		return 1
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.port))
	if err != nil {
		fmt.Fprintf(stderr, "server: %v\n", err)
		return 1
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	testControl := "disabled"
	if cfg.testControl {
		testControl = "enabled"
	}
	fixturesFrom := "built in"
	if cfg.fixturesDir != "" {
		fixturesFrom = "from " + cfg.fixturesDir
	}
	fmt.Fprintf(stderr, "server: listening on %s; clock %s; test control %s; fixtures %s\n",
		ln.Addr(), cfg.clockStart.Format(fixtures.TimestampLayout), testControl, fixturesFrom)

	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	select {
	case err := <-served:
		fmt.Fprintf(stderr, "server: %v\n", err)
		return 1
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		fmt.Fprintf(stderr, "server: stopping: %v\n", err)
		return 1
	}
	return 0
}
