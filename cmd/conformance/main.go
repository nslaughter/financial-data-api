// Command conformance is the API runner of spec/conformance.md. It runs the
// checks of expected/ over HTTP against a server started with
// TEST_CONTROL=enabled and the default CLOCK_START, and validates every
// response against spec/openapi.yaml. The expected results, the OpenAPI
// document, and the fixture credentials are built in, so the runner checks
// the contract version it was built with.
//
// Usage:
//
//	conformance --stage 1 [--base-url http://localhost:8080] [--file pagination]... [--check text]
//
// --stage runs every file the stage table requires of the API at that stage,
// skipping the scenarios whose stages member does not list it. --file, which
// may be repeated, runs only the files named, still at that stage. --check
// runs only the checks whose name contains the text.
//
// It prints one line for each check and the first difference of each
// failing one, and exits with status 1 if a check fails and 2 if it cannot
// run.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	financialdataapi "github.com/nslaughter/financial-data-api"
	"github.com/nslaughter/financial-data-api/internal/conformance"
	"github.com/nslaughter/financial-data-api/internal/expected"
	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// files collects the values of a repeated flag.
type files []string

func (f *files) String() string { return strings.Join(*f, ",") }

func (f *files) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("conformance", flag.ContinueOnError)
	flags.SetOutput(stderr)
	baseURL := flags.String("base-url", "http://localhost:8080", "the server's `URL`")
	stage := flags.Int("stage", 0, "the `stage` the server serves, 1 or 2 (required)")
	var named files
	flags.Var(&named, "file", "run only this `file` of expected/, such as pagination; may be repeated")
	check := flags.String("check", "", "run only the checks whose name contains this `text`")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "conformance: unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}
	if *stage == 0 {
		fmt.Fprintln(stderr, "conformance: --stage is required")
		flags.Usage()
		return 2
	}

	runner, selected, err := setup(*baseURL, *stage, named)
	if err != nil {
		fmt.Fprintf(stderr, "conformance: %v\n", err)
		return 2
	}
	var passed, failed, skipped int
	runner.Progress = func(r conformance.Result) {
		switch {
		case r.Skipped:
			skipped++
			fmt.Fprintf(stdout, "skip  %s: its stages do not list stage %d\n", r, *stage)
		case r.Failure != nil:
			failed++
			fmt.Fprintf(stdout, "FAIL  %s\n      %s\n", r, strings.ReplaceAll(r.Failure.String(), "\n", "\n      "))
		default:
			passed++
			fmt.Fprintf(stdout, "ok    %s\n", r)
		}
	}
	results := runner.Run(ctx, selected, *check)
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "conformance: interrupted")
		return 2
	}
	if len(results) == 0 {
		fmt.Fprintf(stderr, "conformance: no check's name contains %q\n", *check)
		return 2
	}
	fmt.Fprintf(stdout, "\n%d passed, %d failed, %d skipped\n", passed, failed, skipped)
	if failed > 0 {
		return 1
	}
	return 0
}

// setup loads the built-in contract and returns a runner for the server and
// the files to run.
func setup(baseURL string, stage int, named []string) (*conformance.Runner, []*expected.File, error) {
	all, err := expected.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("expected/: %w", err)
	}
	selected, err := conformance.Select(all, stage, named)
	if err != nil {
		return nil, nil, err
	}
	doc, err := conformance.ParseOpenAPI(financialdataapi.OpenAPI())
	if err != nil {
		return nil, nil, fmt.Errorf("spec/openapi.yaml: %w", err)
	}
	f, err := fixtures.Load(financialdataapi.Fixtures())
	if err != nil {
		return nil, nil, fmt.Errorf("fixtures/: %w", err)
	}
	runner, err := conformance.New(conformance.Config{
		BaseURL:     baseURL,
		Stage:       stage,
		Credentials: f.Credentials,
		OpenAPI:     doc,
	})
	if err != nil {
		return nil, nil, err
	}
	return runner, selected, nil
}
