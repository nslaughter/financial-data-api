# Conformance format

**Status:** Version 0.2.0, tagged `contract-v0.2.0` with the
[data contract](data-contract.md).

The files in [`expected/`](../expected) are one conformance suite for the API,
the three SDKs, and the monitor. This document defines how a runner executes
them and compares results, so that every runner reaches the same verdict.

## Runners

- **The API runner** lives in this repository. It runs every check for a
  stage over HTTP against a server started with `TEST_CONTROL=enabled` and
  the default `CLOCK_START`, and it exits with a non-zero status if any check
  fails.
- **SDK runners** run the query checks and `apply_checks` through the SDK's
  own methods, and run scenarios over HTTP or through the SDK where it exposes
  the operation. Each SDK documents which scenario steps it maps to its
  methods.
- **The monitor** runs `release-timing`.

The [stage table](data-contract.md#implementations-and-conformance) says which
files each runner must pass from which stage. A runner given a stage runs
every file required at or before it.

## Rules for every check

- **Independence.** Before every check (each query check, position check,
  read check, release-timing check, and scenario), the runner calls
  `POST /test/reset`, with `{"clock": <clock>}` if the check has a `clock`
  member and `{}` otherwise. Checks therefore run in any order, but one at a
  time against a server.
- **Credential.** Requests carry
  `Authorization: Bearer demo-research-key` (`cred_research`) unless the check
  says otherwise.
- **Paging.** Where a check compares a whole result, the runner follows
  `next_page_token` until it is `null` and concatenates every page's `data`.
- **Status.** A request without an explicit expected status must return
  `200`.

### Matching

Expected values are compared with actual JSON values by this rule:

- An expected **object** matches an actual object that has every member the
  expected object names, each matching. Members the expected object omits are
  not compared.
- An expected **array** matches an actual array of the same length whose
  elements match in order.
- An expected **string, number, boolean, or `null`** matches an equal actual
  value of the same JSON type. Strings are compared exactly, so `"102.0"` does
  not match `"102"` or `102`. `null` matches only a member that is present and
  `null`.

## Query checks

Files: `august-2026-at-cutoffs`, `full-history`, `missing-value`,
`withdrawal-and-rerelease`, `out-of-order-arrival`, `provider-correction`,
`late-source-release`, and the `checks` of `published-as-of`.

Each entry of `checks` has a `query` and an `expected` list:

1. Send `GET /v1/observations` with each member of `query` as a parameter of
   the same name. A `null` member is omitted, not sent.
2. Concatenate every page.
3. `expected` must match the concatenated `data`.
4. If the entry has `contrast_available_as_of`, send the same query with
   `published_as_of` replaced by `available_as_of` at the same instant, and
   match its result the same way.

`full-history` also has `pages_with_page_size_10`. For each of its checks, the
runner sends the query with `page_size=10`, follows every page, and requires
as many pages as the list has entries; page _i_ has `count` records, and its
first and last records have the listed `observation_id` values.

## Change-stream checks

`change-stream.json` has three kinds of checks besides its scenarios:

- **`position_checks`**: reset with the clock at `at`, then
  `GET /v1/datasets/core-indicators`. Its `head_position` must equal
  `expected_position`.
- **`read_checks`**: `GET /v1/datasets/core-indicators/changes` with `after`
  set to `after_position` and `limit` to `limit` (omitted when `null`).
  `expected` must match `data`; `next_position` and `head_position` must equal
  `expected_next_position` and `expected_head_position`.
- **`apply_checks`**: for SDK runners only. Build a local copy by reading the
  stream from position 0 through `from_position`, read the events after
  `from_position` through `through_position`, and apply them by precedence.
  The local copy's current revision for `observation_id` must be
  `expected_current_revision_id`. `incorrect_if_applied_by_arrival` documents
  the result of the wrong rule.

## Release timing

For each check in `release-timing.json`, at the default clock:

1. `GET /v1/release-calendar?series_id=activity-index&period_start=<period_start>`
   and take the entry whose `period_start` equals the check's. Its
   `scheduled_at` and `period_end` are used below.
2. `GET /v1/revisions?series_id=activity-index&period_start=<period_start>&period_end=<period_end>`.
3. `first_published_at` is the earliest `published_at`. `first_available_at`
   is the earliest `available_at`; `first_available_revision_id` is the
   revision with that `available_at`, and the lowest `sequence` if several
   share it.
4. `source_delay_seconds` is `first_published_at` minus `scheduled_at`, and
   `availability_delay_seconds` is `first_available_at` minus
   `first_published_at`, both in whole seconds.
5. Every member of `expected` must equal the computed value.

## Scenarios

A file's `scenarios` array holds scenarios. A scenario has a `name`, a
`reason`, an optional `clock` for its reset, and `steps`, executed in order.
A scenario passes when every step passes; the first failing step ends it.

SDK runners may also check a scenario's optional `expected_local_copy`: after
loading the scenario's export file and applying the changes it reads, the
SDK's local copy must select those revisions. Each entry names an
`observation_id`, and the revision the local copy selects for that
observation must match the entry. Observations the list does not name are
not compared. The API runner ignores it.

### Steps

Each step has exactly one action member.

| Action | Effect | The step passes when |
| --- | --- | --- |
| `"set_clock": "<timestamp>"` | `PUT /test/clock` with `{"now": <timestamp>}` | the response is `200` |
| `"set_credential": {...}` | `PUT /test/credentials/<credential_id>` with the object's other members as the body | the response is `200` |
| `"reset": {...}` | `POST /test/reset` with the object as the body | the response is `200` |
| `"request": {...}` | sends the request below | the response meets the step's `expect` |

Test actions use the test-control key. A request step may have an `id`, which
later references use, and must have an `expect`.

### Request

| Member | Meaning |
| --- | --- |
| `method` | HTTP method; `GET` when absent. |
| `path` | Path, starting with `/`. |
| `query` | Object of query parameters. A string is sent as one parameter, `null` is omitted, and an array of strings sends the parameter once per element, in order. Values are percent-encoded by the runner's URL encoder and are otherwise sent as written. |
| `credential` | `credential_id` whose key is sent as `Authorization: Bearer <api_key>`. `null` sends no `Authorization` header. Absent means `cred_research`. |
| `authorization` | A raw `Authorization` header value. It replaces `credential`. |
| `body` | A JSON value sent with `Content-Type: application/json`. Absent means no body. |

### Expect

| Member | The response passes when |
| --- | --- |
| `status` | Required. Its status code equals this. |
| `code` | It has `Content-Type: application/problem+json`, and its body's `code` equals this and its body's `status` equals the status code. |
| `body` | Its body, parsed as JSON, matches this. |
| `headers` | Each named header (names compared case-insensitively) equals the value exactly, except that `Content-Type` compares only the media type, ignoring parameters. |
| `body_sha256` | The SHA-256 digest of its raw body, in lowercase hexadecimal, equals this. |
| `body_lines` | Its body, split after each `\n` with every line parsed as JSON, matches this array. A body that does not end with `\n` fails. |

### References

A string anywhere in a step's `request` or `expect` may contain
`${<id>.<path>}`. It refers to the parsed JSON body of the request step with
that `id`; `<path>` is a dot-separated list of member names and array indexes,
such as `files.0.url`.

- In `request`, a reference names an earlier step. In `expect`, references
  are resolved after the response arrives, so they may also name the step
  itself.
- When a string is exactly one reference, it is replaced by the referenced
  value with its JSON type. In a query parameter, a number is sent in
  decimal and `null` omits the parameter.
- When a reference is part of a longer string, the referenced value must be a
  string or a number, and it is inserted as text.
- A reference to a missing step or member fails the step.

## Failure reports

A runner reports, for each failing check, the file, the check or scenario
name, the step number, and the expected and actual values at the first
difference.
