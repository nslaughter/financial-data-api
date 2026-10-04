# API specification: financial data API v1

**Status:** Version 0.2.0, tagged `contract-v0.2.0` with the
[data contract](data-contract.md).

This document specifies how the demo API (stage 1) and the full API (stage 2)
are called and how they behave. The [data contract](data-contract.md) defines
what the records mean, and [`openapi.yaml`](openapi.yaml) gives the same
requests and responses in machine-readable form. This document governs
behavior; the OpenAPI document must agree with it. If an implementation finds
a disagreement between the three, it reports the disagreement instead of
choosing one.

The key words MUST, MUST NOT, SHOULD, and MAY are used as described in RFC 2119
when they appear in capitals.

## Stages

The demo API implements stage 1. The full API adds stage 2. A stage 2 endpoint
does not exist in a stage 1 server, which returns `404 not_found` for it, and a
stage 2 parameter is unknown to a stage 1 server, which returns
`400 unknown_parameter`.

| Feature | Stage |
| --- | --- |
| `GET /v1/meta` | 1 |
| `GET /v1/datasets`, `GET /v1/datasets/{dataset_id}` | 1 |
| `GET /v1/series`, `GET /v1/series/{series_id}` | 1 |
| `GET /v1/observations` with `available_as_of` | 1 |
| `GET /v1/datasets/{dataset_id}/changes` | 1 |
| Authentication and entitlements on every endpoint | 1 |
| Simulated clock and every `/test` endpoint | 1 |
| `published_as_of` on `GET /v1/observations` | 2 |
| `GET /v1/revisions` | 2 |
| `GET /v1/release-calendar` | 2 |
| Exports: `POST /v1/datasets/{dataset_id}/exports`, `GET /v1/exports/{export_id}`, `GET /v1/exports/{export_id}/files/{file_name}` | 2 |

## Running the server

The server is published as a container image with the fixtures from the same
contract version built in. It reads these environment variables:

| Variable | Default | Meaning |
| --- | --- | --- |
| `PORT` | `8080` | TCP port for HTTP. |
| `CLOCK_START` | `2026-10-01T00:00:00Z` | The simulated clock at startup and after a reset without a clock. Must be a timestamp in the format below, no later than `9999-12-30T23:59:59Z` (see [Simulated clock](#simulated-clock)); otherwise the server refuses to start. |
| `TEST_CONTROL` | `disabled` | `enabled` serves the `/test` endpoints, and `disabled` does not. Any other value is refused at startup. |
| `FIXTURES_DIR` | the fixtures built into the image | Directory containing the files from `fixtures/`. |

At startup the server loads every fixture file and checks the
[invariants](data-contract.md#invariants). If any check fails, it MUST exit
with a non-zero status and a message naming the file, the record, and the
rule. It serves plain HTTP; TLS is out of scope.

All state is in memory: the clock, changes made to credentials, exports, and
the key that signs page tokens. A restart returns the server to its startup
state, like a reset.

The default `CLOCK_START` is after every fixture revision and before the
September 2026 release would be due, so a server started without test control
serves the whole fixture history.

## Conventions

### Versioning

Every customer endpoint is under `/v1`. A request whose first path segment is
`v` followed by digits, other than `v1`, returns
`404 unsupported_api_version`; the error's `detail` lists the supported
versions. The server never answers one version's request with another
version's data model.

Within v1, changes are additive: new endpoints, new optional parameters, and
new response fields. Clients MUST ignore response fields they do not
recognize. `GET /v1/meta` reports the contract version the server implements.

### Requests

- Paths match exactly and are case-sensitive. A trailing slash is a different
  path and returns `404 not_found`.
- A query parameter name may appear at most once; repeating it returns
  `400 invalid_parameter`. A parameter with an empty value returns
  `400 invalid_parameter`. A parameter the endpoint does not define returns
  `400 unknown_parameter`, so a misspelled cutoff can never fall back to the
  latest data.
- Request bodies, where an endpoint takes one, are JSON objects. An absent or
  empty body is treated as `{}`. A body that is not a JSON object returns
  `400 invalid_parameter`; a field the endpoint does not define returns
  `400 unknown_parameter`, and a required field that is absent returns
  `400 missing_parameter`. A body sent with a `GET` is ignored.
- Formats, which requests and responses share:

| Type | Format |
| --- | --- |
| date | `YYYY-MM-DD`, a valid calendar date. |
| timestamp | Exactly `YYYY-MM-DDTHH:MM:SSZ`: UTC, uppercase `T` and `Z`, whole seconds from `00` to `59`, a valid instant. Fractional seconds, offsets such as `+00:00`, and dates without a time return `400 invalid_parameter`. |
| integer parameter | ASCII digits matching `^(0\|[1-9][0-9]*)$`, within the parameter's range. Signs, leading zeros, and decimals return `400 invalid_parameter`. |

### Responses

- Successful responses are JSON with `Content-Type: application/json`, except
  export files. Every field documented for a response is present; a field
  without a value is `null`, never omitted.
- A **revision record** has the same 14 fields everywhere it appears: query
  results, the revision history, the change stream, and export files. The
  fields are those of the [revision records](data-contract.md#revision-records)
  table, with the same names and meanings. `value` is a JSON string copied
  exactly from the fixture (`"102.0"` stays `"102.0"`) or `null`, never a JSON
  number. Integers are JSON numbers and stay below 2^53.
- A list response is an object whose `data` member holds the items, with any
  metadata beside it.

Example revision record:

```json
{
  "sequence": 36,
  "series_id": "activity-index",
  "observation_id": "obs_aug26",
  "revision_id": "rev_aug26_1",
  "revision_number": 1,
  "change_type": "initial_release",
  "period_start": "2026-08-01",
  "period_end": "2026-09-01",
  "value": "102.4",
  "missing_reason": null,
  "unit": "index_points",
  "published_at": "2026-09-03T12:30:00Z",
  "received_at": "2026-09-03T12:30:04Z",
  "available_at": "2026-09-03T12:31:10Z"
}
```

### Errors

An error response has `Content-Type: application/problem+json` and this body:

```json
{
  "status": 400,
  "code": "unknown_parameter",
  "title": "Unknown parameter",
  "detail": "available_asof is not a parameter of GET /v1/observations.",
  "parameter": "available_asof"
}
```

`code` is stable and is what clients act on. `title` is fixed for each code.
`detail` explains this occurrence and may change between versions.
`parameter` names the query parameter, body field, or path parameter at
fault:

- `unknown_parameter`, `missing_parameter`, `invalid_parameter`, and
  `cutoff_in_future` name the parameter or field. An empty or reversed period
  range names `period_end`. A malformed body names `null`.
- `invalid_page_token`, `page_token_mismatch`, and `page_token_expired` name
  `page_token`.
- `position_ahead` and `position_expired` name `after`.
- `clock_backwards` names `now`.
- Every other code, including `conflicting_cutoffs`, has `null`.

| Status | `code` | `title` | When |
| --- | --- | --- | --- |
| 400 | `unknown_parameter` | Unknown parameter | A query parameter or body field the endpoint does not define. |
| 400 | `missing_parameter` | Missing parameter | A required parameter or body field is absent. |
| 400 | `invalid_parameter` | Invalid parameter | A value is malformed, out of range, empty, or repeated; a period range is empty or reversed; a body is malformed. |
| 400 | `conflicting_cutoffs` | Conflicting cutoffs | Both `available_as_of` and `published_as_of` are given. |
| 400 | `cutoff_in_future` | Cutoff in the future | A cutoff is later than the server's clock. |
| 400 | `invalid_page_token` | Invalid page token | The token is malformed, altered, or was issued before the last reset or restart. |
| 400 | `page_token_mismatch` | Page token mismatch | The token was issued for a different endpoint, different parameters, or a different credential. |
| 400 | `position_ahead` | Position ahead of the stream | `after` is greater than the dataset's head position. |
| 401 | `unauthenticated` | Unauthenticated | The `Authorization` header is missing or malformed, or the key is unknown, inactive, or of the wrong kind for the endpoint. |
| 403 | `not_entitled` | Not entitled | The key is valid but not entitled to the dataset the request reads. |
| 404 | `not_found` | Not found | No such path, or no such series, dataset, export, file, or credential. |
| 404 | `unsupported_api_version` | Unsupported API version | The path names an API version the server does not serve. |
| 405 | `method_not_allowed` | Method not allowed | The path exists but not with this method. The `Allow` header lists the path's methods in the order `GET`, `PUT`, `POST`, separated by a comma and a space, such as `GET, PUT`. |
| 409 | `clock_backwards` | Clock cannot move backwards | `PUT /test/clock` names a time before the current clock. |
| 410 | `page_token_expired` | Page token expired | The token's snapshot is older than its lifetime. Restart the query. |
| 410 | `position_expired` | Position expired | Events after the position are past retention. Load a fresh export or query, then continue from its position. |
| 410 | `export_expired` | Export expired | The export is past retention. Create a new export. |
| 500 | `internal` | Internal error | An unexpected server failure. |

When a request has several faults, the server reports the first in this order:

1. **Routing.** `404 unsupported_api_version`, `404 not_found` for an unknown
   path (including `/test` paths when test control is disabled), or
   `405 method_not_allowed`.
2. **Authentication.** `401 unauthenticated`.
3. **Request validation.** Any `400` except `position_ahead` and the two
   checks of `PUT /test/credentials/{credential_id}` described below. Where
   several validation faults apply, the server reports any one of them.
4. **Lookup.** `404 not_found` for a series, dataset, export, file, or
   credential named by the request.
5. **Entitlement.** `403 not_entitled`.
6. **State.** `400 position_ahead`, `409 clock_backwards`, and the `410`
   errors.

`PUT /test/credentials/{credential_id}` checks the credential's kind and the
datasets in its body after it looks up the credential, and reports a
test-control credential or an unknown dataset as `400 invalid_parameter`,
not `404 not_found`, as that endpoint describes.

### Authentication and entitlements

- Every `/v1` endpoint except `GET /v1/meta` requires a customer key:
  `Authorization: Bearer <api_key>`. The scheme name is case-insensitive; the
  key is compared exactly. The keys are in
  [`fixtures/credentials.json`](../fixtures/credentials.json).
- Every `/test` endpoint requires the test-control key. A customer key is
  refused under `/test`, and the test-control key is refused under `/v1`, both
  with `401 unauthenticated`.
- A `401` response includes `WWW-Authenticate: Bearer`.
- A customer key may read metadata without entitlements: the catalog
  (`GET /v1/datasets`, `GET /v1/datasets/{dataset_id}`, `GET /v1/series`,
  and `GET /v1/series/{series_id}`) and the release calendar
  (`GET /v1/release-calendar`). Every other `/v1` endpoint reads one dataset's
  revisions and requires the key to be entitled to it; otherwise it returns
  `403 not_entitled`.
- The server checks the key and its entitlements on every request, using
  their state at that moment. Page tokens, positions, and export identifiers
  never carry access: a request with a token issued before a key was revoked
  is refused like any other request with that key.

### Simulated clock

The server keeps one simulated clock, which is its current time for every
purpose:

- **Visibility.** A revision is visible when its `available_at` is at or
  before the clock. Revisions after the clock do not exist on any customer
  path: queries, the revision history, the change stream, exports, and head
  positions. Because `available_at` never decreases as `sequence` increases,
  a dataset's visible revisions are exactly those with `sequence` at or below
  its head position.
- **Default cutoff.** A query without a cutoff uses the clock.
- **Cutoff validation.** A cutoff later than the clock returns
  `400 cutoff_in_future`. A cutoff equal to the clock is accepted.
- **Expiry.** Page snapshots, change-stream retention, and exports expire on
  the clock.
- **Timestamps the server creates**, such as `server_time`, `created_at`, and
  `expires_at`, come from the clock.

The clock starts at `CLOCK_START` and does not advance by itself. Only
`PUT /test/clock` moves it forward, and only `POST /test/reset` moves it
anywhere else. A request reads the clock once and uses that value throughout.

The clock is never later than `9999-12-30T23:59:59Z`, the latest instant
whose derived timestamps still fit the timestamp format: an export's
`expires_at` is the clock plus 86,400 seconds, and a query's
`snapshot_expires_at` is the clock plus 3,600 seconds. A server whose
`CLOCK_START` is later refuses to start, and a later `now` for
`PUT /test/clock` or `clock` for `POST /test/reset` returns
`400 invalid_parameter` naming that field.

### Positions and snapshots

- A dataset's **head position** is the highest `sequence` among its visible
  revisions, or 0 when none is visible.
- Positions are JSON integers. Each dataset has its own change stream and its
  own positions. Clients save the positions the server returns and MUST NOT
  compute positions from sequence numbers.
- A query is evaluated at a **snapshot position**: the dataset's head position
  when the query's first page is served. Every page of the query considers
  only revisions with `sequence` at or below it, whatever arrives later. The
  response returns it as `position`. For a query without a cutoff, the result
  equals the state after applying every revision up to `position`, so a
  client can continue from that position in the change stream.

### Pagination

`GET /v1/observations` and `GET /v1/revisions` are paged:

- `page_size` is an integer from 1 to 1000. The default is 100.
- A response carries `next_page_token`: an opaque string when more results
  remain, and `null` when this page holds the last result. A page is never
  empty unless the whole result is empty.
- To continue, a client sends `page_token` with **every other parameter
  identical** to the first request, including `page_size` and including
  omitted parameters left omitted. Any difference returns
  `400 page_token_mismatch`, as does a token issued for another endpoint or
  to another credential.
- Each page is computed from the result at the snapshot position, so pages
  never mix states.
- A snapshot lives for 3,600 seconds of simulated time from its first page.
  Every page returns `snapshot_expires_at`. A token is accepted while the
  clock is before that instant; at or after it, the server returns
  `410 page_token_expired`, and the client restarts the query.
- Tokens are opaque. Clients MUST NOT parse, construct, or alter them.

A token SHOULD be a signed encoding of the endpoint, the credential, the
parameters, the snapshot position and time, and the offset of the next
result, signed with a key the server generates at startup and at every reset.
A token that fails the signature check returns `400 invalid_page_token`.

### Retention

| What | Kept for | Measured from | After it expires |
| --- | --- | --- | --- |
| A query snapshot | 3,600 seconds | The query's first page | `410 page_token_expired` |
| A change-stream event | 1,095 days (94,608,000 seconds) | The event's `available_at` | `410 position_expired` for a position that still needs it |
| An export | 86,400 seconds | The export's `created_at` | `410 export_expired` |

Each item expires at its start plus its period: it is available while the
clock is before that instant, and expired from that instant on. These periods
are fixed in v1. The change-stream period covers the whole fixture history at
the default clock, so the expected results run without setup; expiry is
tested by moving the clock.

## Customer endpoints

### `GET /v1/meta`

Describes the server. It requires no key and ignores the `Authorization`
header, so a missing, unknown, or wrong-kind key still receives `200`. It
serves as the readiness check.

```json
{
  "api_version": "v1",
  "supported_api_versions": ["v1"],
  "contract_version": "0.2.0",
  "server_time": "2026-10-01T00:00:00Z"
}
```

`server_time` is the simulated clock.

### `GET /v1/datasets`

Lists every dataset, ordered by `dataset_id`. Not paged.

```json
{
  "data": [
    {
      "dataset_id": "core-indicators",
      "name": "Core indicators (synthetic)",
      "description": "Synthetic economic indicators created for this demonstration. Entitlements are granted per dataset.",
      "entitled": true,
      "head_position": 37
    }
  ]
}
```

`entitled` says whether the requesting key may read the dataset's data.
`head_position` is the dataset's head position, or `null` when the key is not
entitled.

### `GET /v1/datasets/{dataset_id}`

Returns one dataset object in the form above, or `404 not_found`.

### `GET /v1/series`

Lists every series, ordered by `series_id`. Not paged. Each item has the
fields of the [series catalog](data-contract.md#series-catalog) and
`entitled`.

```json
{
  "data": [
    {
      "series_id": "activity-index",
      "dataset_id": "core-indicators",
      "name": "Activity index (synthetic)",
      "description": "A fictional monthly index of economic activity created for this demonstration. It describes no real economy, source, or provider.",
      "frequency": "monthly",
      "unit": "index_points",
      "base_period": "2025 = 100",
      "seasonal_adjustment": "seasonally_adjusted",
      "source": "Fictional statistics office (synthetic)",
      "release_schedule": "Third day of the following month at 12:30 UTC",
      "entitled": true
    }
  ]
}
```

### `GET /v1/series/{series_id}`

Returns one series object in the form above, or `404 not_found`.

### `GET /v1/observations`

Returns the selected revision of each observation, following
[Selecting the revision available at a cutoff](data-contract.md#selecting-the-revision-available-at-a-cutoff)
or, with `published_as_of`,
[Reconstructing what the source had published](data-contract.md#reconstructing-what-the-source-had-published).
Requires entitlement to the series' dataset.

| Parameter | Required | Type | Meaning |
| --- | --- | --- | --- |
| `series_id` | yes | string | The series to query. An unknown series returns `404 not_found`. |
| `period_start` | no | date | Include observations with `period_start` at or after this date. |
| `period_end` | no | date | Include observations with `period_end` at or before this date. If both bounds are given, `period_start` MUST be before `period_end`. |
| `available_as_of` | no | timestamp | Select among revisions with `available_at` at or before this instant. At most the clock. |
| `published_as_of` | no | timestamp | Stage 2. Select among revisions with `published_at` at or before this instant. At most the clock. Cannot be combined with `available_as_of`. |
| `page_size` | no | integer | 1 to 1000; default 100. |
| `page_token` | no | string | Continues a query. |

Without a cutoff, the query selects among all visible revisions. Either way,
only revisions at or below the snapshot position are considered. Results are
ordered by `period_start`, ascending.

```json
{
  "data": [
    {
      "sequence": 36,
      "series_id": "activity-index",
      "observation_id": "obs_aug26",
      "revision_id": "rev_aug26_1",
      "revision_number": 1,
      "change_type": "initial_release",
      "period_start": "2026-08-01",
      "period_end": "2026-09-01",
      "value": "102.4",
      "missing_reason": null,
      "unit": "index_points",
      "published_at": "2026-09-03T12:30:00Z",
      "received_at": "2026-09-03T12:30:04Z",
      "available_at": "2026-09-03T12:31:10Z"
    }
  ],
  "position": 37,
  "snapshot_expires_at": "2026-10-01T01:00:00Z",
  "next_page_token": null
}
```

That is the response to
`GET /v1/observations?series_id=activity-index&period_start=2026-08-01&period_end=2026-09-01&available_as_of=2026-09-04T00:00:00Z`
at the default clock. A query that matches nothing returns `200` with an
empty `data`, which a client can tell apart from a refusal.

### `GET /v1/revisions`

Stage 2. Returns every visible revision of the matching observations,
including superseded, erroneous, and withdrawn ones. It serves vintage
research and lets the monitor measure release timing. Requires entitlement to
the series' dataset.

| Parameter | Required | Type | Meaning |
| --- | --- | --- | --- |
| `series_id` | yes | string | As for observations. |
| `period_start` | no | date | As for observations. |
| `period_end` | no | date | As for observations. |
| `available_as_of` | no | timestamp | Include only revisions with `available_at` at or before this instant. At most the clock. |
| `page_size` | no | integer | 1 to 1000; default 100. |
| `page_token` | no | string | Continues a query. |

Results are ordered by `period_start`, then `revision_number`, both
ascending. The response has the same members as `GET /v1/observations`:
`data`, `position`, `snapshot_expires_at`, and `next_page_token`.

### `GET /v1/release-calendar`

Stage 2. Returns the scheduled releases of a series from the
[release calendar](data-contract.md#release-calendar), whatever the clock
shows. Any valid customer key may read it; it requires no entitlement. Not
paged.

| Parameter | Required | Type | Meaning |
| --- | --- | --- | --- |
| `series_id` | yes | string | The series. An unknown series returns `404 not_found`. |
| `period_start` | no | date | Include releases with `period_start` at or after this date. |
| `period_end` | no | date | Include releases with `period_end` at or before this date. |

Results are ordered by `period_start`, ascending.

```json
{
  "data": [
    {
      "series_id": "activity-index",
      "period_start": "2026-07-01",
      "period_end": "2026-08-01",
      "scheduled_at": "2026-08-03T12:30:00Z"
    }
  ]
}
```

### `GET /v1/datasets/{dataset_id}/changes`

Reads the dataset's change stream: the visible revisions with `sequence`
greater than `after`, in `sequence` order. Requires entitlement to the
dataset.

| Parameter | Required | Type | Meaning |
| --- | --- | --- | --- |
| `after` | yes | integer | The client's saved position. 0 reads from the start. |
| `limit` | no | integer | 1 to 1000; default 100. The most events to return. |

```json
{
  "data": [
    {
      "sequence": 37,
      "series_id": "activity-index",
      "observation_id": "obs_aug26",
      "revision_id": "rev_aug26_2",
      "revision_number": 2,
      "change_type": "source_revision",
      "period_start": "2026-08-01",
      "period_end": "2026-09-01",
      "value": "102.1",
      "missing_reason": null,
      "unit": "index_points",
      "published_at": "2026-09-10T12:30:00Z",
      "received_at": "2026-09-10T12:30:04Z",
      "available_at": "2026-09-10T12:30:40Z"
    }
  ],
  "next_position": 37,
  "head_position": 37
}
```

- `next_position` is the `sequence` of the last event returned, or `after`
  when none is returned. The client saves it together with the events it
  applied, in one local transaction.
- `head_position` is the dataset's head position. The client has caught up
  when `next_position` equals `head_position`.
- `after` greater than `head_position` returns `400 position_ahead`. That
  happens only when the client's position came from a later clock, before a
  reset.
- An event expires 1,095 days after its `available_at`. If the first visible
  event after `after` has expired, the server returns
  `410 position_expired`. Events expire in `sequence` order, so if the first
  is retained, the rest are too. A position equal to the head position never
  expires.
- An event's `sequence` is its identity. A client that receives an event at or
  below its saved position has already applied it.

### Exports

Stage 2. An export delivers a snapshot of one dataset and the position that
matches it, so a client can load the snapshot and continue from the change
stream without losing a revision.

#### `POST /v1/datasets/{dataset_id}/exports`

Creates an export of the dataset at its current head position. Requires
entitlement to the dataset. The body is empty or `{}`; any field returns
`400 unknown_parameter`. The response is `201 Created` with the manifest as
its body and a `Location` header holding the manifest's path,
`/v1/exports/{export_id}`:

```json
{
  "export_id": "exp_5f0c9a7e2b14d6c3",
  "dataset_id": "core-indicators",
  "position": 36,
  "created_at": "2026-09-10T12:30:20Z",
  "expires_at": "2026-09-11T12:30:20Z",
  "api_version": "v1",
  "contract_version": "0.2.0",
  "coverage": {
    "series_ids": ["activity-index"],
    "observation_count": 32,
    "revision_count": 36,
    "period_start": "2024-01-01",
    "period_end": "2026-09-01"
  },
  "files": [
    {
      "name": "revisions.jsonl",
      "url": "/v1/exports/exp_5f0c9a7e2b14d6c3/files/revisions.jsonl",
      "media_type": "application/x-ndjson",
      "record_count": 36,
      "size_bytes": 13695,
      "sha256": "e75e2d61e8ded1e54e210e7d854c394c9e880dcacafd2c6423ddfe5b81652469"
    }
  ]
}
```

- `export_id` identifies the snapshot. It is opaque, and the server MUST NOT
  reuse one, even after a reset. Creating another export, even at the same
  position, creates a new identity.
- `position` is the dataset's head position when the export is created. The
  snapshot is every visible revision with `sequence` at or below it. Revisions
  that become visible later are never in this export, whenever its file is
  generated or downloaded.
- `coverage` counts what the file contains: the series with at least one
  revision (ascending), the observations, the revisions, the earliest
  `period_start`, and the latest `period_end`. An export of an empty snapshot
  has empty `series_ids`, zero counts, and `null` periods.
- `expires_at` is `created_at` plus 86,400 seconds.
- `files[].url` is a path on this server. In v1 an export has exactly one
  file, `revisions.jsonl`.

#### `GET /v1/exports/{export_id}`

Returns the manifest above. Requires entitlement to the export's dataset.
An unknown export returns `404 not_found`; an expired one returns
`410 export_expired`.

#### `GET /v1/exports/{export_id}/files/{file_name}`

Returns the file with `Content-Type: application/x-ndjson` and its
`Content-Length`. The same checks apply as for the manifest, at the time of
the download. Every download of a file returns identical bytes, whose length
and SHA-256 digest match the manifest.

The file has one revision record per line, ordered by `sequence`, in this
canonical form so that every implementation produces the same bytes:

- each record is a JSON object with the 14 fields in the order of the
  [revision records](data-contract.md#revision-records) table, starting with
  `sequence` and ending with `available_at`;
- no whitespace outside strings;
- strings escape only `"`, `\`, and control characters, and are UTF-8;
  characters such as `<`, `>`, and `&` are not escaped;
- every record, including the last, ends with a single `\n`, and an empty
  snapshot is an empty file.

The first line of every export of the fixture dataset at position 1 or later
is:

```text
{"sequence":1,"series_id":"activity-index","observation_id":"obs_jan24","revision_id":"rev_jan24_1","revision_number":1,"change_type":"initial_release","period_start":"2024-01-01","period_end":"2024-02-01","value":"97.1","missing_reason":null,"unit":"index_points","published_at":"2024-02-03T12:30:00Z","received_at":"2024-02-03T12:30:04Z","available_at":"2024-02-03T12:31:10Z"}
```

An export at position 36 is 13,695 bytes with SHA-256
`e75e2d61e8ded1e54e210e7d854c394c9e880dcacafd2c6423ddfe5b81652469`; at
position 37 it is 14,076 bytes with SHA-256
`66d6cd910b83cf9ae974f14665f64551a5f3ce7d4f59d1297ce22d7e418024fd`.

## Test control

The `/test` endpoints exist only when the server starts with
`TEST_CONTROL=enabled`; otherwise every `/test` path returns `404 not_found`.
They require the test-control key. They change server state for every client,
so a test suite that uses them runs its scenarios one at a time against its
own server.

### `GET /test/clock`

Returns the clock: `{"now": "2026-10-01T00:00:00Z"}`.

### `PUT /test/clock`

Moves the clock forward. Body: `{"now": "<timestamp>"}`. `now` is required.
A time later than `9999-12-30T23:59:59Z` returns `400 invalid_parameter`
naming `now`. A time before the current clock returns `409 clock_backwards`;
the current time is accepted and changes nothing. Returns the new clock, as
for `GET /test/clock`.

Revisions whose `available_at` the clock passes become visible at once, in
`sequence` order, and expiry is evaluated against the new time.

### `POST /test/reset`

Returns the server to its startup state, with an optional different clock.
Body: empty, `{}`, or `{"clock": "<timestamp>"}`. Any valid timestamp up to
`9999-12-30T23:59:59Z` is accepted, including one earlier than the current
clock; a later one returns `400 invalid_parameter` naming `clock`. The reset:

- sets the clock to `clock`, or to `CLOCK_START` when absent;
- restores every credential from the fixture;
- deletes every export, so their identifiers return `404 not_found`;
- replaces the page-token signing key, so earlier tokens return
  `400 invalid_page_token`.

Positions held by clients are not invalidated; a position past the new head
returns `400 position_ahead`. Returns the clock, as for `GET /test/clock`.

### `PUT /test/credentials/{credential_id}`

Changes a customer credential until the next reset. Body:
`{"active": <boolean>, "datasets": [<dataset_id>, ...]}`; both fields are
optional, and an absent field is unchanged. The server checks, in this order:

1. the body's form, as for any request (`400`);
2. that the credential exists (`404 not_found`);
3. that it is a customer credential (`400 invalid_parameter`, naming
   `credential_id`) and that every dataset exists (`400 invalid_parameter`,
   naming `datasets`).

Returns the credential without its key:

```json
{
  "credential_id": "cred_research",
  "kind": "customer",
  "active": false,
  "datasets": ["core-indicators"]
}
```

`active: false` makes the key fail with `401 unauthenticated`. Removing a
dataset makes requests for it fail with `403 not_entitled`. Both take effect
on the next request, including resumed pages and export downloads.

## Out of scope for v1

- Identity providers, OAuth, key issuance or rotation, and per-user accounts.
- Rate limits, quotas, and usage metering.
- TLS, CORS, compression, `HEAD`, `OPTIONS`, and conditional requests.
- Endpoints that load or correct data. Data comes from the fixtures and
  becomes visible as the clock moves.
- More than one series per query, overlapping period matches, and filters on
  the change stream.
- Persistence across restarts.
