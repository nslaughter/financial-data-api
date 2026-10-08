# Working in this repository

This repository holds the data contract for a synthetic financial dataset and
the Go API that serves it. The specifications come first; the code
implements them.

## Read these first

| Document | Governs |
| --- | --- |
| [`spec/data-contract.md`](spec/data-contract.md) | What the records mean: identities, time fields, selection rules, the change stream, invariants, and decisions. |
| [`spec/api.md`](spec/api.md) | How the API behaves: endpoints, parameters, errors and their order, authentication, the simulated clock, pagination, retention, exports, and test control. |
| [`spec/openapi.yaml`](spec/openapi.yaml) | The same requests and responses in machine-readable form. It must agree with `api.md`. |
| [`spec/conformance.md`](spec/conformance.md) | How the files in `expected/` are executed and compared. |
| [`docs/implementation-plan.md`](docs/implementation-plan.md) | The order of pull requests and what each must deliver. |

The README describes the project for people; it is not a specification.

## Rules

- **Do not change `spec/`, `fixtures/`, or `expected/` in an implementation
  pull request.** They are versioned together as the contract, and the SDKs
  and the monitor pin them. If the implementation disagrees with an expected
  result, investigate the implementation first. If you conclude a
  specification is wrong, ambiguous, or self-contradictory, stop and report
  it, quoting the passages, instead of choosing an interpretation or editing
  an expectation to match the code.
- **Follow the plan.** Do one pull request from the implementation plan at a
  time, in order, within its stated scope, as described in
  [Doing the next item](#doing-the-next-item). Note anything you deferred in
  the pull request description.
- **Done means verified.** A pull request is done when `gofmt -l .` prints
  nothing, `go vet ./...` and `go test ./...` pass, and the conformance files
  the plan lists for it pass against the real server. Report results as they
  are; never skip or weaken a check to make it pass.

## Implementation guidance

- Go, standard library first. The server needs no third-party dependencies.
  For OpenAPI response validation, the conformance runner uses two: a JSON
  Schema validator and a YAML parser. The owner chose these over a single
  OpenAPI library because none validates OpenAPI 3.1 on Go 1.22.
- Response structs keep the field order of the contract's tables and never
  use `omitempty`; nullable fields are pointers, so absent values encode as
  `null`.
- `value` is a string copied exactly from the fixture. Never parse it into a
  float.
- Export files use the canonical form in `spec/api.md`: in Go, an encoder with
  `SetEscapeHTML(false)`, the struct fields in contract order, and one record
  per line. The recipe needs Go 1.22 or later; earlier versions write `\b` and
  `\f` as `\u0008` and `\u000c`.
- Every error is `application/problem+json`, including routing errors, so do
  not let `net/http` write its default 404 or 405 bodies.
- All time comes from the simulated clock. Do not call `time.Now()` outside
  the clock's initialization.
- State is in memory behind a mutex. A request reads the clock once.
- Keep `internal/history` free of HTTP so its rules can be tested directly
  against `expected/`.

## Implementation conventions

### Errors

- Only `internal/api` builds problems. Every other package, and the
  server's state within `internal/api`, returns errors.
- Return a sentinel error, such as `history.ErrPositionAhead`, when the
  problem's detail needs no data, and a typed error when it needs a value,
  such as the dataset a request named.
- The handler that receives an error maps it to a problem with `errors.Is`
  or `errors.As`. Only the handler knows which parameter the problem names,
  so there is no shared table from errors to problems.

### Request contracts

- The route table declares each endpoint's whole request contract: its
  method, the credential kind it requires, its query parameters, and its
  body fields. An endpoint that takes a body declares its fields even when
  it has none, so that any field is refused; an endpoint that takes no body
  never reads one.
- The server checks parameter and field names, repeated and empty
  parameters, and the form of the body before it calls the handler, in the
  error order of `spec/api.md`. Handlers receive the parsed parameters and
  fields and convert their values.

### Interfaces

- A package defines the narrowest seam it needs from a dependency: a
  function type for one method, or an interface of its own for two or more.
- Add a seam only when there is a second implementation or a test needs a
  fake. Never define an interface in the package that implements it.

### One owner for each contract term

- `internal/fixtures` owns the data contract's vocabulary: the date and
  timestamp layouts and their parsers, the change types, the credential
  kinds, and `MaxInteger`. Other packages use these instead of defining
  their own.
- A default that the API applies belongs to the package that applies it:
  page sizes and limits to `internal/api`, and `CLOCK_START` to
  `cmd/server`.
- `internal/expected` owns the format of `expected/`: the file types,
  loading the files from `financialdataapi.Expected()`, their
  well-formedness checks, the matching rule of `spec/conformance.md`, and
  the values it gives for running the checks, such as the dataset whose
  change stream `change-stream.json` reads. It has no HTTP. The conformance
  runner and the tests that read `expected/` use it.

### Response types

- A `fixtures` type that is a table of the data contract is served as it
  is, or embedded in a response struct that adds the API's fields after it,
  as a series adds `entitled`. It is never restated field by field.
- Other domain types carry no JSON tags; `internal/api` declares their
  response types.

### Duplication

- Each contract rule has one implementation. A second one is allowed only
  as an independent check of the first, such as the conformance runner's
  path matching or the escaping test of the canonical export form, and a
  comment says it is deliberate.
- Code that reads the contract's files uses that one implementation: a test
  never carries its own reader or matcher for `expected/`.
- Tests may repeat small setup helpers, such as loading the fixtures.

## Commands

| Task | Command |
| --- | --- |
| Format check | `gofmt -l .` |
| Vet and test | `go vet ./... && go test ./...` |
| Run the server with test control | `TEST_CONTROL=enabled go run ./cmd/server` |
| Run the conformance suite | `go run ./cmd/conformance --base-url http://localhost:8080 --stage 2` |

## Doing the next item

When asked to do the next item:

1. Update `main` (`git checkout main && git pull --ff-only`) and read the
   **Progress** table in
   [`docs/implementation-plan.md`](docs/implementation-plan.md). The next
   item is the first step whose status is not `Done`.
2. Stop and report instead of starting if any of these holds:
   - `gh pr list --state open` shows a pull request for that step; report its
     state, because the owner reviews and merges it;
   - the step's status is `Needs owner decision`; name the decision.
3. Create a branch named `step-<N>-<short-slug>`, such as
   `step-1-fixtures`, and implement the step within its scope.
4. Run every check the step lists. If a check fails because a specification
   seems wrong or ambiguous, stop and report it as the
   [Rules](#rules) require; do not open a pull request built on a guess.
5. In the same branch, set the step's status to `Done` in the Progress table.
6. Push, and open a pull request as described below. Then add its number to
   the step's row in a follow-up commit on the same branch.
7. Do not merge. Report the pull request's link, the checks you ran and their
   results, and anything deferred.

## Commits and pull requests

Write commit subjects as short imperative sentences in sentence case, such as
"Serve observations with pagination", with a body that says what changed and
why. A pull request description names its step in the implementation plan,
lists the conformance files it turns on, and lists anything deferred or any
specification question raised.
