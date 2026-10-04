# Implementation plan

This plan divides the API's implementation into pull requests that can be
reviewed one at a time. Each pull request names what it builds, what it
leaves out, and the checks that prove it done. The specifications are
[`spec/data-contract.md`](../spec/data-contract.md),
[`spec/api.md`](../spec/api.md), [`spec/openapi.yaml`](../spec/openapi.yaml),
and [`spec/conformance.md`](../spec/conformance.md), at contract version
0.2.0. Read [`AGENTS.md`](../AGENTS.md) before starting any of them.

Pull requests 1 to 7 complete stage 1, the demo API the SDKs use. Pull
requests 8 to 10 complete stage 2, the full API.

## Package layout

A pull request may refine this layout if it explains why.

| Path | Contents |
| --- | --- |
| `fixtures.go` | A root package that embeds `fixtures/*.json`, so the binary carries the fixtures of its contract version. |
| `internal/fixtures` | Fixture types, loading, and the invariant checks. |
| `internal/history` | The data rules as pure functions: visibility, head positions, both cutoffs, the revision history, change-stream reads and retention, and the canonical export bytes. No HTTP. |
| `internal/api` | Routing, request parsing, errors, authentication, entitlements, pagination tokens, handlers, and test control. |
| `internal/exports` | The export store (stage 2). |
| `internal/conformance` | The conformance runner's logic: loading `expected/`, executing checks, matching, and references. |
| `cmd/server` | The server binary: environment, startup validation, and serving. |
| `cmd/conformance` | The runner binary. |

## Stage 1

### 1. Load the fixtures and enforce the invariants

- Create the Go module `github.com/nslaughter/financial-data-api` and a CI
  workflow that runs `gofmt` (failing on unformatted files), `go vet ./...`,
  and `go test ./...`.
- Embed the fixtures and load them into typed records. Nullable fields
  (`value`, `missing_reason`) are pointers, not empty strings.
- Implement all 15 [invariants](../spec/data-contract.md#invariants). A
  failure names the file, the record, and the rule number.
- Tests: the fixtures pass. For each invariant, a test alters one record so
  that only that invariant fails, and checks the reported rule.

Out of scope: HTTP, the clock, and any query logic.

### 2. Implement the data rules

- In `internal/history`, implement the rules of the data contract over a
  clock and a snapshot position: visible revisions, the head position, the
  position for a time, selection by `available_as_of` and by
  `published_as_of`, the revision history, change-stream reads with
  retention, and the canonical export bytes.
- Tests run the expected results that need no HTTP directly against the
  package:
  - every query check in `expected/` (including `published-as-of`, whose rule
    is pure even though its endpoint is stage 2);
  - `position_checks` and `read_checks` in `change-stream.json`;
  - the two export digests in [`spec/api.md`](../spec/api.md#exports).
  The tests read the files from `expected/`; they do not copy their values.

Out of scope: HTTP, parameters, errors, and tokens.

### 3. Build the conformance runner

- Implement [`spec/conformance.md`](../spec/conformance.md) completely:
  - query checks, `pages_with_page_size_10`, position and read checks, release
    timing, and scenarios;
  - the matching rule and references;
  - failure reports.
- Validate every JSON response against `spec/openapi.yaml`, in addition to
  the expected values. The subset match ignores fields an expectation does
  not name. Schema validation is what catches a field that is omitted
  instead of `null`.
- Flags:
  - `--base-url`;
  - `--stage` (runs every file the stage table requires of the API);
  - `--file` (repeatable);
  - `--check` (runs checks whose name contains a substring).
- Tests use a fake server built with `net/http/httptest`, and cover:
  - matching, including type-sensitive comparison and array length;
  - every step action;
  - reference resolution, including references to the current step in
    `expect`;
  - repeated query parameters;
  - NDJSON bodies.

Out of scope: running against the real server; nothing serves the API yet.

### 4. Serve the foundation: routing, errors, the clock, authentication, test control, and the catalog

- Read `PORT`, `CLOCK_START`, `TEST_CONTROL`, and `FIXTURES_DIR`. Refuse to
  start on invalid values or failed invariants.
- Implement routing, using the error order in
  [Errors](../spec/api.md#errors):
  - `404 unsupported_api_version`;
  - `404 not_found`;
  - `405 method_not_allowed` with the `Allow` header.
  `net/http`'s `ServeMux` writes its own plain-text 404 and 405 responses
  and redirects some paths, so wrap it or route manually; every error must
  be `application/problem+json`.
- Write strict parsing helpers for query parameters and bodies:
  - unknown, repeated, and empty parameters;
  - exact dates, timestamps, and integers;
  - a body that is absent or empty counts as `{}`.
- Implement the simulated clock, read once per request and safe for
  concurrent requests.
- Implement authentication for both credential kinds, with
  `WWW-Authenticate` on every 401.
- Implement every `/test` endpoint, served only when `TEST_CONTROL=enabled`.
- Implement `GET /v1/meta` and the catalog endpoints: datasets and series,
  with `entitled` and `head_position`.
- Tests: `httptest` tests for routing, error bodies and order, parsing,
  authentication, and test control.

Out of scope: observations, pagination, and the change stream.

### 5. Serve observations with pagination

- `GET /v1/observations` with `available_as_of`, built on `internal/history`.
- Pagination as specified:
  - a snapshot position fixed by the first page;
  - signed opaque tokens bound to the endpoint, credential, and parameters;
  - a signing key replaced at every reset;
  - `snapshot_expires_at` and expiry on the clock;
  - no empty final page.
- Entitlement checks on every page.
- Add a CI job that builds the server, starts it with `TEST_CONTROL=enabled`,
  and runs the runner on these files:
  - `august-2026-at-cutoffs`, `full-history`, `missing-value`,
    `withdrawal-and-rerelease`, `out-of-order-arrival`,
    `provider-correction`, `late-source-release`;
  - `pagination`.
- Done when that job passes.

Out of scope: `published_as_of`, which a stage 1 server refuses as an unknown
parameter.

### 6. Serve the change stream

- `GET /v1/datasets/{dataset_id}/changes`, with retention, `position_ahead`,
  `next_position`, and `head_position`.
- Switch the CI job to `--stage 1`, which adds `change-stream`,
  `simulated-clock`, `access-control`, and `request-errors`. Done when every
  stage 1 file passes.

### 7. Publish the demo API image

- A `Dockerfile` that builds a static binary into a minimal image, with the
  fixtures embedded and the default port exposed. Labels record the contract
  version (`0.2.0`) and the stage.
- A release workflow, triggered by a version tag, that:
  - builds the image;
  - runs the stage 1 conformance suite against the container, not only the
    binary;
  - publishes the image to the GitHub Container Registry.
  The repository owner pushes the tag; the pull request adds the workflow
  only.
- Update the README's status and add the command that starts the container
  with test control enabled.
- Done when the workflow passes on a pull request (build and conformance,
  without publishing).

Stage 1 is complete here. The SDKs can pin the image.

## Stage 2

### 8. Add `published_as_of`, the revision history, and the release calendar

- `published_as_of` on `GET /v1/observations`, with `conflicting_cutoffs`.
- `GET /v1/revisions`, paged like observations, with tokens that are not
  interchangeable between the two endpoints.
- `GET /v1/release-calendar`.
- The runner adds `published-as-of`, `revision-history`, `release-calendar`,
  and `release-timing`. Done when those pass with every stage 1 file.

### 9. Add exports

- Create, read, and download exports:
  - the snapshot is fixed at creation;
  - the canonical file bytes, stored or regenerated identically;
  - expiry;
  - entitlement checks on every download;
  - identifiers never reused after a reset.
- The runner adds `export-handoff` and `exports`. Done when every file passes
  with `--stage 2`.

### 10. Publish the full API image

- Update the image labels and the README for stage 2, and run the release
  workflow with `--stage 2`.

## After stage 2

- The monitor (stage 3) uses the published image and runs `release-timing`.
- The migration stage's breaking change is an open question in the
  [data contract](../spec/data-contract.md#open-questions); it needs a
  decision and a new contract version before any work starts.
