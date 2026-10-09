# Financial data API

A demonstration REST API for loading a financial dataset, receiving its
corrections, and reproducing research using the data available at an earlier
point in time.

I'm [Nathan Slaughter](https://nathanslaughter.com/). My engineering work spans
fintech, data pipelines, observability, and infrastructure, informed by a
background in investment research. I help teams turn datasets into APIs whose
meaning and delivery behavior customers can depend on.

**Status:** The full API is implemented in Go and passes its conformance
suite on every pull request. Release `v0.2.0` publishes it as the container
image `ghcr.io/nslaughter/financial-data-api:0.2.0`, and release `v0.1.0`
published the smaller demo API as `0.1.0`; see [Run the API](#run-the-api).
This repository also holds the [data contract](spec/data-contract.md)
(version 0.3.0, tagged `contract-v0.3.0`) with its fixtures and expected
results, the [API specification](spec/api.md) and its
[OpenAPI form](spec/openapi.yaml), the [conformance format](spec/conformance.md),
and the [implementation plan](docs/implementation-plan.md), whose remaining
steps bring the code in line with its conventions without changing what it
serves. The SDKs and the monitor that act as the API's customers are in
development or planned; see
[Where the demonstration stands](#where-the-demonstration-stands). The
dataset is synthetic, and this is a demonstration project, not client work.

## What this project demonstrates

This is the supporting example for my REST API development and data modeling
work. It shows the contract and delivery behavior a customer needs to load a
dataset once, keep it current, and reproduce earlier research:

- **Stable identities.** Series, observations, and revisions each have their
  own identifiers, so a correction is recognizable without overwriting the
  version used in earlier work.
- **Explicit time semantics.** Observation periods, source publication times,
  and customer availability times are separate fields with documented meanings.
- **Historical queries.** One cutoff returns the versions an entitled customer
  could retrieve at that time. A second reconstructs what the source had
  published by then, with the provider's processing errors corrected.
- **Pagination that cannot mix states.** Continuation tokens are bound to the
  endpoint, the credential, every parameter, and the query's snapshot. An
  expired snapshot requires a restart.
- **Bulk delivery with a safe handoff to updates.** Each export identifies its
  snapshot and the matching position in the change stream, so a revision made
  during the export cannot fall between them.
- **Access enforced on every path.** Entitlements are checked on queries,
  resumed pages, and exports.
- **One conformance suite.** The [expected results](expected) state what
  every implementation must return from the same fixtures. The runner in this
  repository executes them over HTTP, checks the responses against the
  OpenAPI document, and runs on every pull request, against the server and
  against its container image.

The API covers the first two of four stages in a demonstration for financial
data providers. In the first, a small demo API serves the SDKs; it lives here
and grew into the full API in the second. The SDKs act as the API's
customers. In the third stage, the
[financial-data-api-monitor](https://github.com/nslaughter/financial-data-api-monitor)
will check what customers can retrieve from it, and a final stage will make a
deliberate contract change to the API and SDKs.

## A customer can make successful requests and still have the wrong dataset

A customer downloads a dataset and keeps a local copy. The provider then
corrects an old observation. If the customer's next request asks only for
dates after the last downloaded observation, it can miss the correction.
Every request succeeds, and the local copy stays wrong.

This project uses a fictional monthly activity index to make that problem
concrete. Its August 2026 value is published as 102.4 on September 3 and
revised to 102.1 on September 10. The first version is this record in the
[fixtures](fixtures/revisions.json):

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

The September 10 revision keeps `observation_id` and gets a new `revision_id`
and the next `sequence`, its position in the change stream.
The period runs from August 1 up to September 1, with the end excluded. A
decimal string preserves the value's representation, and the unit is explicit.
`published_at` records source publication, `received_at` the provider's
acquisition, and `available_at` when an entitled customer could first retrieve
the record through the API. The [data contract](spec/data-contract.md) defines
every field.

## How a customer loads, updates, and reproduces the data

1. Load a consistent snapshot from a bulk export. Its manifest carries the
   snapshot identity, the matching start position in the change stream, the
   API and contract versions, coverage, and file checksums.
2. Verify the files, then apply releases, revisions, and withdrawals from that
   position. Each page of events and its next position are saved in one local
   transaction, and event identities let a repeated page be recognized.
3. Query the versions available at an earlier cutoff. The
   [expected results](expected/august-2026-at-cutoffs.json), prepared before
   the API was written, give the answer at each cutoff:

   ```text
   GET /v1/observations?series_id=activity-index&period_start=2026-08-01&period_end=2026-09-01&available_as_of=2026-09-04T00:00:00Z
   ```

   This query returns 102.4, although the revision has since replaced it.
   Without the cutoff, current research receives 102.1.

## Two cases show where the contract protects the customer

Both are files in the [expected results](expected), and the API passes them.

The first, [`export-handoff`](expected/export-handoff.json), makes the
September 10 revision available while an export is being written. Its first
scenario shows that the manifest's position, taken with its snapshot,
delivers the revision through the change stream. The other two show how the
revision is lost otherwise. If the provider took the "start updates here"
position after the file finished, the revision would be absent from the
snapshot and already behind the update cursor. A customer who asked only for
later periods would miss it too.

The second, [`august-2026-at-cutoffs`](expected/august-2026-at-cutoffs.json),
is a set of query checks. They show a later revision entering historical
research through a query that lacks an availability cutoff, and how the
`available_as_of` query keeps the September 4 answer intact.

Step-by-step examples that tell these stories for a reader, not a test
runner, are planned.

## The contract has to cover delivery as well as field names

The [data contract](spec/data-contract.md) describes identifiers, units,
missing values, time semantics, and revision history, and the
[API specification](spec/api.md) adds access rules, pagination, retention,
and recovery. The expected results use one dataset for queries, exports, and
updates, so their results can be compared.

Several delivery rules matter as much as the schema, and each has a check in
the expected results:

- Withdrawals are events in the history. Deleting the original row would
  destroy the answer to an earlier query.
- A null observation carries a reason and stays distinct from zero.
- A query that matches nothing is distinct from a denied request, so a
  synchronization job cannot record success while its data goes stale.
- Change-stream positions have a documented retention period. An expired
  cursor reports that the customer needs a fresh snapshot instead of skipping
  ahead.
- Retrying an export download fetches the same retained file. Regenerating an
  export creates a new snapshot identity, at the head position when it is
  created.
- A token issued before access was revoked cannot grant that access on a
  resumed request, and export files are protected like query endpoints.

## Where the demonstration stands

The demonstration is complete when these criteria hold. The API meets the
four that depend on it alone, and the conformance suite checks them against
the server and its image on every pull request.

| Criterion | Status | Checked by |
| --- | --- | --- |
| The eligible version at each cutoff matches independent fixtures, including the gap between publication and customer availability. | Met | `august-2026-at-cutoffs`, `late-source-release`, `published-as-of` |
| Pagination returns a consistent result while data changes during traversal. | Met | `pagination` |
| Unauthorized queries, resumed pages, and exports are refused. | Met | `access-control`, `exports` |
| Bulk delivery reconciles with subsequent updates, including a revision published during an export. | Met | `export-handoff` |
| The SDK workflow runs against the expanded API. | Not yet | The SDKs' own runners |

The [Python SDK](https://github.com/nslaughter/financial-data-sdk-python) is
in development: it queries the catalog, observations, and the change stream,
and it doesn't run the shared checks against the API image yet. The
[Go](https://github.com/nslaughter/financial-data-sdk-go) and
[TypeScript](https://github.com/nslaughter/financial-data-sdk-ts) SDKs and the
[monitor](https://github.com/nslaughter/financial-data-api-monitor) are
project briefs so far.

## What's in this repository

| Path | Contents |
| --- | --- |
| [`spec/`](spec) | The data contract, the API specification and its OpenAPI form, and the conformance format. The API specification gives the [retention](spec/api.md#retention) periods and what a client does when one expires. |
| [`fixtures/`](fixtures) | The synthetic dataset: its catalog, revisions, release calendar, and demonstration credentials. |
| [`expected/`](expected) | The expected results: one conformance suite for the API, the SDKs, and the monitor. |
| [`cmd/server`](cmd/server) | The API server. It and the packages it uses in [`internal/`](internal) need only Go's standard library. |
| [`cmd/conformance`](cmd/conformance) | The conformance runner. |
| [`.github/workflows`](.github/workflows), [`Dockerfile`](Dockerfile) | CI, which runs the tests and the conformance suite on every pull request, and the release workflow, which builds the image, checks its labels, runs the suite against it, and publishes it for a version tag. |
| [`docs/implementation-plan.md`](docs/implementation-plan.md) | The pull requests that built the API, in order, and the decisions made along the way. |
| [`AGENTS.md`](AGENTS.md) | The rules and conventions that implementation pull requests follow. |

Still to come:

- Query, export, and update examples.
- A tagged release that names the compatible SDK version, once an SDK passes
  the shared checks.

## Run the API

The API is published as a container image with the fixtures built in.
This command starts it with the `/test` endpoints enabled, which the
conformance suite and the SDKs' tests use to set the simulated clock and
reset state:

```sh
docker run --rm -p 8080:8080 -e TEST_CONTROL=enabled ghcr.io/nslaughter/financial-data-api:0.2.0
```

From a checkout of this repository, with Go 1.22 or later,
`TEST_CONTROL=enabled go run ./cmd/server` starts the same server.

The query from the example above, with a demonstration key from the
fixtures, returns 102.4:

```sh
curl -H 'Authorization: Bearer demo-research-key' \
  'http://localhost:8080/v1/observations?series_id=activity-index&period_start=2026-08-01&period_end=2026-09-01&available_as_of=2026-09-04T00:00:00Z'
```

From a checkout of this repository, this runs the stage 2 conformance suite
against it:

```sh
go run ./cmd/conformance --base-url http://localhost:8080 --stage 2
```

[Running the server](spec/api.md#running-the-server) lists the environment
variables the server reads. The image's labels record the contract version
it implements and its stage. Each release tag `vX.Y.Z` publishes the image
tagged `X.Y.Z`, after the conformance suite of its stage passes against it.
Image `0.1.0` is the stage 1 demo API, which passes the suite with
`--stage 1`.

## What comes next

In the third stage, the
[monitor](https://github.com/nslaughter/financial-data-api-monitor) will run
scheduled checks with ordinary customer access, including the release timing
in [`release-timing`](expected/release-timing.json): how long after its
scheduled time each release was published, and how long after that it became
available.

In the fourth stage, the API will make a breaking change to its data model
and introduce v2 beside v1. API v1 already uses explicit `published_at`,
`period_start`, `period_end`, and `available_at` fields, so the change will
be a different one; choosing it is the contract's
[open question](spec/data-contract.md#open-questions). Each query and export
will select a version, and continuation tokens will keep that choice through
pagination. As in v1 today, a request for an unsupported version will fail
with an explanation instead of falling back to a different data model.

The checks will run through direct HTTP requests as well as the SDK, so a
client-side workaround cannot hide a server error. The recovery rehearsal
will start after a customer has already stored changed output: identify the
affected exports and snapshots, reissue the data, and repair the customer's
local copy.

## What the historical results establish, and what they don't

Availability here means an entitled customer could retrieve a record through
the API. It does not establish when any customer actually downloaded it. When
the provider corrects a conversion error, the API distinguishes the data it
served, which `available_as_of` returns, from the history reconstructed with
the correction, which `published_as_of` returns. The
[data contract](spec/data-contract.md#reconstructing-what-the-source-had-published)
states the limits of that reconstruction. The demonstration runs locally
against synthetic data and makes no performance or scale claims.

## Related projects and writing

- [financial-data-sdk-python](https://github.com/nslaughter/financial-data-sdk-python):
  the first of the clients that act as this API's customers, in development.
- [financial-data-sdk-go](https://github.com/nslaughter/financial-data-sdk-go) and
  [financial-data-sdk-ts](https://github.com/nslaughter/financial-data-sdk-ts):
  the same client for Go and TypeScript, planned.
- [financial-data-api-monitor](https://github.com/nslaughter/financial-data-api-monitor):
  scheduled checks of what customers retrieve from this API, planned for the
  third stage.
- *Turning a financial dataset into a dependable API* and *The timestamps that
  make financial data usable*: articles on this design, in preparation. I'll
  link them here when they are published.
- *Shipping an API change your customers can adopt confidently*: an article
  on the migration stage, also in preparation.

## License

This project is source available under a custom license, the
[Nathan Slaughter Personal Use License](LICENSE) (`LicenseRef-NSPUL-1.0`).
It is not open source.

- Anyone may read the source, including on behalf of an organization.
- An individual may run it, and change it privately, for their own personal
  use, such as learning, study, and personal projects.
- Everything else needs my written permission first: use by or for an
  organization, including evaluating this project or my services; use in a
  business; and publishing, redistributing, packaging, or hosting the
  project, or putting its code in another project.

Ask for permission at git@nathanslaughter.com. The [LICENSE](LICENSE)
controls; this summary doesn't change it.

## Work with me on programmatic access to your dataset

I can help design and build the data contract, query endpoints, bulk delivery,
and update workflows your customers need. An engagement includes agreed
acceptance cases, documentation, and handover, with maintenance available
after delivery.

[Discuss a financial data API](https://www.linkedin.com/in/nathan-slaughter)
with your dataset, intended users, and the workflows it needs to support.
