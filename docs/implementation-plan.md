# Implementation plan

This plan divides the API's implementation into pull requests that can be
reviewed one at a time. Each pull request names what it builds, what it
leaves out, and the checks that prove it done. The specifications are
[`spec/data-contract.md`](../spec/data-contract.md),
[`spec/api.md`](../spec/api.md), [`spec/openapi.yaml`](../spec/openapi.yaml),
and [`spec/conformance.md`](../spec/conformance.md), at contract version
0.3.0. Read [`AGENTS.md`](../AGENTS.md) before starting any of them.

Pull requests 1 to 7 complete stage 1, the demo API the SDKs use. Pull
requests 8 to 10 complete stage 2, the full API. Pull requests 11 to 13
bring the code in line with the implementation conventions in `AGENTS.md`.

## Progress

Each step's pull request changes its own row: it sets **Status** to `Done`,
and after the pull request is opened, a follow-up commit on the same branch
fills in **Pull request**. A row reads `Done` on `main` only once its pull
request is merged. A step whose status is `Needs operator decision` cannot
start until the operator records the decision here.

| Step | Status | Pull request |
| --- | --- | --- |
| 1. Load the fixtures and enforce the invariants | Done | [#5](https://github.com/nslaughter/financial-data-api/pull/5) |
| 2. Implement the data rules | Done | [#6](https://github.com/nslaughter/financial-data-api/pull/6) |
| 3. Build the conformance runner | Done | [#7](https://github.com/nslaughter/financial-data-api/pull/7) |
| 4. Serve the foundation | Done | [#8](https://github.com/nslaughter/financial-data-api/pull/8), [#9](https://github.com/nslaughter/financial-data-api/pull/9) |
| 5. Serve observations with pagination | Done | [#10](https://github.com/nslaughter/financial-data-api/pull/10) |
| 6. Serve the change stream | Done | [#11](https://github.com/nslaughter/financial-data-api/pull/11) |
| 7. Publish the demo API image | Done | [#14](https://github.com/nslaughter/financial-data-api/pull/14) |
| 8. Add `published_as_of`, the revision history, and the release calendar | Done | [#15](https://github.com/nslaughter/financial-data-api/pull/15) |
| 9. Add exports | Done | [#16](https://github.com/nslaughter/financial-data-api/pull/16) |
| 10. Publish the full API image | Done | [#17](https://github.com/nslaughter/financial-data-api/pull/17) |
| 11. Give each contract term one owner | Not started | |
| 12. Map errors and declare request contracts in `internal/api` | Not started | |
| 13. Embed the fixture types and remove the smaller repeats | Not started | |

## Package layout

A pull request may refine this layout if it explains why.

| Path | Contents |
| --- | --- |
| `fixtures.go`, `conformance.go` | A root package that embeds `fixtures/*.json`, so the binary carries the fixtures of its contract version, and `expected/*.json` and `spec/openapi.yaml`, which the conformance runner checks a server against. `go:embed` cannot reach a parent directory, and the contract directories hold no Go files, so these embeds belong to the root package. |
| `internal/fixtures` | Fixture types, loading, and the invariant checks. |
| `internal/history` | The data rules as pure functions: visibility, head positions, both cutoffs, the revision history, change-stream reads and retention, and the canonical export bytes. No HTTP. |
| `internal/api` | Routing, request parsing, errors, authentication, entitlements, pagination tokens, handlers, and test control. |
| `internal/exports` | The export store (stage 2). |
| `internal/conformance` | The conformance runner's logic: loading `expected/`, executing checks, matching, and references. |
| `cmd/server` | The server binary: environment, startup validation, and serving. |
| `cmd/conformance` | The runner binary. |

## Stage 1

### 1. Load the fixtures and enforce the invariants

- Create the Go module `github.com/nslaughter/financial-data-api`, requiring
  Go 1.22 or later as the canonical export form does, and a CI workflow that
  runs `gofmt` (failing on unformatted files), `go vet ./...`, and
  `go test ./...`.
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
- A test checks the canonical form's string escaping, which the fixtures do
  not exercise: strings containing `"`, `\`, every character from U+0000 to
  U+001F, U+007F, U+0080 to U+009F, U+2028, U+2029, `<`, `>`, and `&` encode
  as [`spec/api.md`](../spec/api.md#exports) specifies.

Out of scope: HTTP, parameters, errors, and tokens.

### 3. Build the conformance runner

- Implement [`spec/conformance.md`](../spec/conformance.md) completely:
  - query checks, `pages_with_page_size_10`, position and read checks, release
    timing, and scenarios, including a scenario's `stages` member;
  - the matching rule and references;
  - failure reports.
- Validate every JSON response against `spec/openapi.yaml`, in addition to
  the expected values. The subset match ignores fields an expectation does
  not name. Schema validation is what catches a field that is omitted
  instead of `null`. A response to a request that matches no operation (an
  unknown path, or a method the path does not support) follows the
  OpenAPI document's rules for every path: validate its body against
  `#/components/schemas/Problem`, and a `405` also against
  `#/components/responses/MethodNotAllowed`, whose `Allow` header is
  required.
- Flags:
  - `--base-url`;
  - `--stage` (required; runs every file the stage table requires of the API
    at that stage, skipping a scenario whose `stages` member does not list
    it);
  - `--file` (repeatable; runs only the named files, still at the stage that
    `--stage` gives);
  - `--check` (runs checks whose name contains a substring).
- Tests use a fake server built with `net/http/httptest`, and cover:
  - matching, including type-sensitive comparison and array length;
  - every step action;
  - reference resolution, including references to the current step in
    `expect`;
  - repeated query parameters;
  - NDJSON bodies;
  - a scenario with `stages`, which runs only at a stage it lists.

Out of scope: running against the real server; nothing serves the API yet.

### 4. Serve the foundation: routing, errors, the clock, authentication, test control, and the catalog

- Read `PORT`, `CLOCK_START`, `TEST_CONTROL`, and `FIXTURES_DIR`, treating a
  variable set to an empty value as unset. Refuse to start on invalid values,
  including a `CLOCK_START` later than `9999-12-30T23:59:59Z`, or failed
  invariants.
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
  `PUT /test/clock` and `POST /test/reset` refuse a clock later than
  `9999-12-30T23:59:59Z` with `400 invalid_parameter`; `simulated-clock`
  checks both from step 6.
- Implement `GET /v1/meta` and the catalog endpoints: datasets and series,
  with `entitled` and `head_position`.
- Tests:
  - `httptest` tests for routing, error bodies and order, parsing,
    authentication, and test control;
  - an `httptest` test that a `GET` sent with a body, including a body that
    is not a JSON object, is answered as if it had none; no conformance file
    checks this;
  - the server refuses to start with a `CLOCK_START` later than
    `9999-12-30T23:59:59Z`;
  - each of the four variables set to an empty value takes its default.

Out of scope: observations, pagination, and the change stream.

The operator settled three questions raised in this step's review on
October 5, 2026, and [#9](https://github.com/nslaughter/financial-data-api/pull/9)
applies them:

- The `Authorization` header may separate `Bearer` from the key with one or
  more spaces, as RFC 6750 allows.
- `GET /v1/meta` reports the contract version built into the binary, and the
  server refuses to start when the files in `FIXTURES_DIR` record another.
- The server refuses to start on a fixture `sequence` above
  9007199254740991, citing the rule in `spec/api.md` that integers stay
  below 2^53. The next contract version adds this upper bound to invariant
  6, and the check then cites the invariant instead.

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
  and runs the runner with `--stage 1` on these files:
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
- Switch the CI job to `--stage 1` without `--file`, which adds
  `change-stream`, `simulated-clock`, `access-control`, and `request-errors`,
  including the stage 1 scenario in which `published_as_of` and
  `GET /v1/revisions` are refused. Done when every stage 1 file passes.

### 7. Publish the demo API image

Before this step starts, the operator chooses the image name and the tag
scheme that triggers a release. Release tags must not match `contract-v*`,
which marks contract versions. The decision is recorded here and the step's
status changes to `Not started`.

The operator chose both on October 6, 2026:

- **Image name:** `ghcr.io/nslaughter/financial-data-api`.
- **Release tags:** the server's own semantic version, `vX.Y.Z`, which is
  also the Go module's version. This stage 1 image is `v0.1.0`, and the
  stage 2 image is `v0.2.0`. The release workflow triggers on tags matching
  `v[0-9]*`, which no `contract-v*` tag matches. The image's tag is the
  version without its `v`, such as `0.1.0`. The contract version is in the
  image's labels and in `GET /v1/meta`, not in the tag.

- A `Dockerfile` that builds a static binary into a minimal image, with the
  fixtures embedded and the default port exposed. Labels record the contract
  version (`0.3.0`) and the stage.
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
  and `release-timing`. Done when those pass with every stage 1 file. The
  CI job runs with `--stage 2` and a `--file` for each of these files and
  every stage 1 file: the server now serves `published_as_of` and
  `GET /v1/revisions`, so the stage 1 scenario in `request-errors` no longer
  applies.

The operator confirmed on October 6, 2026, a correction made in this step's
review: the Commands table in [`AGENTS.md`](../AGENTS.md) runs the
conformance suite with `--stage 2` and the `--file` flags of the CI job until
step 9 adds exports. The `--stage 1` command it gave before fails against a
server that serves `published_as_of` and `GET /v1/revisions`.

### 9. Add exports

- Create, read, and download exports:
  - the snapshot is fixed at creation;
  - the canonical file bytes, stored or regenerated identically;
  - expiry;
  - entitlement checks on every download;
  - identifiers with at least 64 bits from `crypto/rand`, never reused after
    a reset.
- The runner adds `export-handoff` and `exports`. Done when every file passes
  with `--stage 2`.

### 10. Publish the full API image

- Update the image labels and the README for stage 2, and run the release
  workflow with `--stage 2`.

The operator settled two questions raised in this step's review on
October 7, 2026, and [#17](https://github.com/nslaughter/financial-data-api/pull/17)
applies them:

- The Release workflow runs on every pull request, with no `paths` filter.
  The filter kept a stage 1 suite from running against the servers of steps
  8 and 9, and that reason ended with this step. Each pull request builds
  the image, checks its labels, and runs the suite of the image's stage
  against the container, so a failure only the image shows fails the pull
  request instead of the next version tag.
- A pull request that changes the contract version in `fixtures/` changes
  the Dockerfile's `contract-version` label in the same pull request. The
  Release workflow's label check enforces it.

## Implementation conventions

The operator settled the
[implementation conventions](../AGENTS.md#implementation-conventions) on
October 7, 2026, after reviewing the code of steps 1 to 10. Steps 11 to 13
bring that code in line with them, one area at a time, without changing what
the server or the runner does: every problem keeps its status, code,
parameter, and detail, and the runner reports the same results. None of them
changes `spec/`, `fixtures/`, or `expected/`.

Each step is done when `gofmt -l .` prints nothing, `go vet ./...` and
`go test ./...` pass, every file passes with `--stage 2` against the real
server, and the Release workflow passes on its pull request.

### 11. Give each contract term one owner

- Add `internal/expected`, moving into it from `internal/conformance`:
  - the file types and `Load`;
  - the well-formedness checks, with the syntax of references that they
    need;
  - the matching rule;
  - the values `spec/conformance.md` gives for running the checks, such as
    the dataset whose change stream `change-stream.json` reads.

  The runner keeps executing checks, resolving references, validating
  responses against `spec/openapi.yaml`, and reporting.
- In the tests of `internal/history`, replace the file types, the loader
  that reads `../../expected` from disk, and `match` with
  `internal/expected`. Numbers then compare as `json.Number`, as the runner
  compares them.
- Add the credential kinds to `internal/fixtures`, and use them in
  `internal/api` and `internal/conformance`. `internal/conformance` uses
  `fixtures.TimestampLayout`, and `internal/history` parses with
  `fixtures.ParseDate` and `fixtures.ParseTimestamp` instead of its own
  layouts.
- Add `internal/expected` to the [package layout](#package-layout), and
  remove loading `expected/` and matching from the row of
  `internal/conformance`.
- Tests: `internal/expected` takes the tests of loading, well-formedness,
  and matching from `internal/conformance`, and the tests that stay with the
  runner pass without changes to what they expect.

Out of scope: errors and request contracts in `internal/api` (step 12), and
response types and seams (step 13).

### 12. Map errors and declare request contracts in `internal/api`

- `state.setClock` and `state.changeCredential` return errors, and their
  handlers map them to the problems they return now.
- `endpoint` declares each endpoint's query parameters and body fields.
  `serve` parses them into `call`, and the handlers drop their `parseQuery`
  and `parseBody` calls.
- One catalog type holds the datasets and series and looks each up by its
  identifier, replacing `findSeries`, `datasetExists`, and the loop in
  `getDataset`.
- One helper looks up a dataset and then checks entitlement, for
  `readChanges` and `createExport`.
- Tests:
  - each error the state returns has a test;
  - the existing `httptest` tests of parsing, error bodies, and error order
    pass without changes to what they expect.

Out of scope: response types and seams (step 13), and an interface between
`internal/api` and `internal/history`, which the conventions leave concrete
until a test needs a fake.

### 13. Embed the fixture types and remove the smaller repeats

- The dataset and series responses embed `fixtures.Dataset` and
  `fixtures.Series`.
- `exports.NewStore` takes a function that returns a dataset's snapshot at a
  position, instead of `*history.History`.
- One function writes a response's bytes, for JSON and for export files.
- `internal/history` converts its revisions to `fixtures.Revision` in one
  place.
- A comment on the runner's path matching says it is deliberately separate
  from the server's routing.
- Tests: the existing catalog, export, and history tests pass without
  changes to what they expect.

Out of scope: the repeated test setup helpers, which the conventions allow.

## After stage 2

- The monitor (stage 3) uses the published image and runs `release-timing`.
- Contract 0.4.0 adds a request ID header to every response, errors
  included. The owner decided this on October 5, 2026, in
  [decision D7](https://github.com/nslaughter/financial-data-sdk-python/blob/main/spec/client.md#d7-request-ids)
  of the Python SDK's client contract, which assumes the header is named
  `Request-Id`. The SDK's step 11 waits for a `contract-v0.4.0` tag and an
  image that implements it. The change needs that contract version and a
  step in this plan before any work starts.
- The migration stage's breaking change is an open question in the
  [data contract](../spec/data-contract.md#open-questions); it needs a
  decision and a new contract version before any work starts. The operator
  reviewed it again on October 7, 2026, and left it open until stage 3 is
  underway; stages 1 to 3 do not depend on it.
