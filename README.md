# Financial data API

A demonstration REST API for loading a financial dataset, receiving its
corrections, and reproducing research using the data available at an earlier
point in time.

I'm [Nathan Slaughter](https://nathanslaughter.com/). My engineering work spans
fintech, data pipelines, observability, and infrastructure, informed by a
background in investment research. I help teams turn datasets into APIs whose
meaning and delivery behavior customers can depend on.

**Status:** The full API, stage 2, with exports, the revision history, and
the release calendar, is implemented in Go and passes the stage 2 conformance
suite. Release `v0.2.0` publishes it as the container image
`ghcr.io/nslaughter/financial-data-api:0.2.0`, and release `v0.1.0` published
the stage 1 demo API as `0.1.0`; see [Run the API](#run-the-api). This
repository also contains the
[data contract](spec/data-contract.md) (version 0.3.0, tagged
`contract-v0.3.0`) with its
fixtures and expected results, the [API specification](spec/api.md) and its
[OpenAPI form](spec/openapi.yaml), the [conformance format](spec/conformance.md),
and the [implementation plan](docs/implementation-plan.md).
The runnable demonstrations are planned. The dataset is synthetic, and this
is a demonstration project, not client work.

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
  original snapshot, filters, API version, and position. An expired snapshot
  requires a restart.
- **Bulk delivery with a safe handoff to updates.** Each export identifies its
  snapshot and the matching position in the change stream, so a revision made
  during the export cannot fall between them.
- **Access enforced on every path.** Entitlements are checked on queries,
  resumed pages, and exports.
- **Working customers.** The [Python](https://github.com/nslaughter/financial-data-sdk-python),
  [Go](https://github.com/nslaughter/financial-data-sdk-go), and
  [TypeScript](https://github.com/nslaughter/financial-data-sdk-ts) SDKs
  keep running against the API as it grows, and all three pass the same
  contract checks.

The API is the second of four stages in a demonstration for financial data
providers. The small demo API the SDKs use in the first stage lives here from
the start and grows into the full API in the second stage, and the
[financial-data-api-monitor](https://github.com/nslaughter/financial-data-api-monitor)
then checks what customers can retrieve from it. A final stage makes a
deliberate contract change to the API and SDKs.

## A customer can make successful requests and still have the wrong dataset

A customer downloads a dataset and keeps a local copy. The provider then
corrects an old observation. If the customer's next request asks only for
dates after the last downloaded observation, it can miss the correction.
Every request succeeds, and the local copy stays wrong.

This project uses a fictional monthly activity index to make that problem
concrete. Its August 2026 value is published as 102.4 on September 3 and
revised to 102.1 on September 10. A proposed record looks like this:

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

## How a customer will load, update, and reproduce the data

1. Load a consistent snapshot from a bulk export. Its manifest carries the
   snapshot identity, the matching start position in the change stream, the
   schema version, coverage, and file checksums.
2. Verify the files, then apply releases, revisions, and withdrawals from that
   position. Each page of events and its next position are saved in one local
   transaction, and event identities let a repeated page be recognized.
3. Query the versions available at an earlier cutoff and compare the answer
   with independently prepared fixture records:

   ```text
   GET /v1/observations?series_id=activity-index&period_start=2026-08-01&period_end=2026-09-01&available_as_of=2026-09-04T00:00:00Z
   ```

   After the revision, this query should still return 102.4. Without the
   cutoff, current research receives 102.1.

## Two walkthroughs show where the contract protects the customer

The first introduces the September 10 revision while an export is being
written. If the provider took the "start updates here" position after the file
finished, the revision would be absent from the snapshot and already behind
the update cursor. The walkthrough shows that failure, then shows how the
manifest's consistent snapshot and position prevent it.

The second shows a later revision entering historical research through a
query that lacks an availability cutoff, and how the `available_as_of` query
keeps the September 4 answer intact.

## The contract has to cover delivery as well as field names

The planned contract will describe identifiers, units, missing values, time
semantics, revision history, access rules, and recovery limits. Query, export,
and update examples will use the same dataset so their results can be compared.

Several delivery rules matter as much as the schema:

- Withdrawals are events in the history. Deleting the original row would
  destroy the answer to an earlier query.
- A null observation carries a reason and stays distinct from zero.
- A query that matches nothing is distinct from a denied request, so a
  synchronization job cannot record success while its data goes stale.
- Change-stream positions have a documented retention period. An expired
  cursor reports that the customer needs a fresh snapshot instead of skipping
  ahead.
- Retrying an export download fetches the same retained files. Regenerating an
  export creates a new snapshot identity and update position.
- A token issued before access was revoked cannot grant that access on a
  resumed request, and export files are protected like query endpoints.

## The demonstration is complete when

- The SDK workflow runs against the expanded API.
- The eligible version at each cutoff matches independent fixtures, including
  the gap between publication and customer availability.
- Pagination returns a consistent result while data changes during traversal.
- Unauthorized queries, resumed pages, and exports are refused.
- Bulk delivery reconciles with subsequent updates, including a revision
  published during an export.

## What the repository will contain

- An API specification and data dictionary. These are written: see
  [`spec/`](spec).
- A local startup command and seeded fixtures. These are here: see
  [Run the API](#run-the-api).
- Query, export, and update examples.
- Contract and authorization checks in CI. The conformance suite runs against
  the server on every pull request, and the suite of the image's stage runs
  against its container image on each pull request that changes the image and
  before each release.
- A tagged release that names the compatible SDK version.
- Documented retention and recovery policies, and the limits of the historical
  availability claims.

## Run the API

The API is published as a container image with the fixtures built in.
This command starts it with the `/test` endpoints enabled, which the
conformance suite and the SDKs' tests use to set the simulated clock and
reset state:

```sh
docker run --rm -p 8080:8080 -e TEST_CONTROL=enabled ghcr.io/nslaughter/financial-data-api:0.2.0
```

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

## A later contract change will test the maintenance work

In the fourth stage, the API makes a breaking change to its data model and
introduces v2 beside v1. API v1 already uses explicit `published_at`,
`period_start`, `period_end`, and `available_at` fields, so the change will be
a different one; choosing it is the contract's
[open question](spec/data-contract.md#open-questions). Each query and export
selects a version, and continuation tokens keep that choice through
pagination. A request for an unsupported version fails with an explanation
instead of falling back to a different data model.

The checks run through direct HTTP requests as well as the SDK, so a
client-side workaround cannot hide a server error. The recovery rehearsal
starts after a customer has already stored changed output: identify the
affected exports and snapshots, reissue the data, and repair the customer's
local copy.

## What the historical results will and will not establish

Availability here means an entitled customer could retrieve a record through
the API. It does not establish when any customer actually downloaded it. If the
provider later corrects a conversion error, the API must distinguish the data
it served from history reconstructed with the correction. The demonstration
runs locally against synthetic data and makes no performance or scale claims.

## Related projects and writing

- [financial-data-sdk-python](https://github.com/nslaughter/financial-data-sdk-python),
  [financial-data-sdk-go](https://github.com/nslaughter/financial-data-sdk-go), and
  [financial-data-sdk-ts](https://github.com/nslaughter/financial-data-sdk-ts):
  the clients that act as this API's customers.
- [financial-data-api-monitor](https://github.com/nslaughter/financial-data-api-monitor):
  scheduled checks of what customers retrieve from this API.
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
