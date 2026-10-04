# Data contract: synthetic activity index

**Status:** Version 0.2.0, tagged `contract-v0.2.0` on October 4, 2026.
Version 0.1.0 is tagged `contract-v0.1.0`. All data is synthetic and describes
no real economy, source, or provider.

This contract defines the dataset shared by the provider demonstration: the
[Python](https://github.com/nslaughter/financial-data-sdk-python),
[Go](https://github.com/nslaughter/financial-data-sdk-go), and
[TypeScript](https://github.com/nslaughter/financial-data-sdk-ts) SDKs, this API, the
[financial-data-api-monitor](https://github.com/nslaughter/financial-data-api-monitor),
and the later migration example. It specifies what the records mean. The
fixtures in [`fixtures/`](../fixtures) implement it, and
[`expected/`](../expected) records the results a correct implementation must
return. The [API specification](api.md) defines how the records are requested
and delivered, and the [conformance format](conformance.md) defines how the
expected results are executed.

## Scope

This document covers:

- the dataset and series catalogs;
- revision records, their identities, their time fields, and the invariants
  every implementation enforces;
- values and missing data;
- the rules for selecting the revision available at a cutoff, and the revision
  the source had published by a cutoff;
- the order of the change stream and the content of a snapshot;
- the release calendar;
- the demonstration credentials;
- the fixture timeline and the expected results.

The [API specification](api.md) covers endpoints, request parameters,
pagination and snapshot lifetime, exports, errors, authentication,
entitlements, the simulated clock, and test control. Where this document
describes a query, it specifies the content of the result, not the request
syntax. Where the two documents disagree, this one governs what the data
means and the API specification governs how it is delivered; report the
disagreement instead of choosing one.

## Concepts

- A **dataset** is a group of series licensed together. Entitlements, the
  change stream, and exports are per dataset. `core-indicators` is the only
  dataset in version 0.2.
- A **series** is a sequence of measurements with one meaning, unit, and
  frequency. `activity-index` is the only series in version 0.2.
- An **observation** is the series' measurement for one period. Its identity
  stays fixed while its value is revised.
- A **revision** is one version of an observation. The first release, each
  later revision or correction, and a withdrawal each create a new revision.
  Revisions are never modified or deleted.
- The **change stream** is every revision of a dataset in the order it
  became available through the API.
- A **snapshot** at position `P` is every revision of a dataset with
  `sequence` ≤ `P`.

## Dataset catalog

[`fixtures/datasets.json`](../fixtures/datasets.json)

| Field | Type | Meaning |
| --- | --- | --- |
| `dataset_id` | string | Stable identity of the dataset. Entitlements name it. |
| `name` | string | Display name. |
| `description` | string | What the dataset contains. |

## Series catalog

[`fixtures/series.json`](../fixtures/series.json)

| Field | Type | Meaning |
| --- | --- | --- |
| `series_id` | string | Stable identity of the series. Renaming the series does not change it; changing what it measures requires a new series. |
| `dataset_id` | string | The dataset the series belongs to. Entitlements are granted per dataset. |
| `name` | string | Display name. |
| `description` | string | What the series measures. |
| `frequency` | string | `monthly` for every series in version 0.2. |
| `unit` | string | Unit of every value in the series. |
| `base_period` | string | Reference period for an index. For `activity-index`, the 2025 values average 100. |
| `seasonal_adjustment` | string | `seasonally_adjusted` or `not_seasonally_adjusted`. |
| `source` | string | The organization that publishes the measurement. |
| `release_schedule` | string | The source's stated schedule, for people. The release calendar is the machine-readable form. |

## Revision records

[`fixtures/revisions.json`](../fixtures/revisions.json) holds one record per
revision, ordered by `sequence`.

| Field | Type | Meaning |
| --- | --- | --- |
| `sequence` | integer | Position in the dataset's change stream. Unique within the dataset and strictly increasing in the order revisions became available. |
| `series_id` | string | The series this revision belongs to. |
| `observation_id` | string | Stable identity of the observation; the same for every revision of one period. |
| `revision_id` | string | Identity of this revision, unique across the dataset. |
| `revision_number` | integer | Precedence among the observation's revisions, starting at 1. A higher number supersedes a lower one regardless of arrival order. |
| `change_type` | string | What created this revision. See [Change types](#change-types). |
| `period_start` | date | First day of the period measured. |
| `period_end` | date | The day after the period ends. The end is exclusive. |
| `value` | decimal string or null | The measurement. Null for a withdrawal, or for a released observation without a value. |
| `missing_reason` | string or null | Why a released observation has no value. Null otherwise. |
| `unit` | string | Unit of `value`, matching the series catalog. |
| `published_at` | timestamp | When the source made this value public. |
| `received_at` | timestamp | When the provider acquired it from the source. |
| `available_at` | timestamp | When an entitled customer could first retrieve this revision through the API. |

Identifiers are opaque. Fixture identifiers such as `obs_aug26` and
`rev_aug26_2` are readable to make review easier; clients must not parse them
or infer order from them. `sequence` and `revision_number` are the only
fields that carry order.

The provider assigns `revision_number` when it records a revision. The
source's versions of an observation are numbered in the order the provider
received them, and a provider correction takes the next number when it is
made. `sequence` is assigned later, when the revision becomes available, so
the two orders can differ: the November 2025 fixture receives its initial
release first, numbers it 1, and makes it available after revision 2.

## Invariants

Every implementation must enforce these rules. The demo API checks them when
it loads the fixtures and refuses to start if any fails, naming the record and
the rule.

1. `dataset_id`, `series_id`, `credential_id`, and `api_key` are each unique
   in their fixture; `revision_id` is unique across all datasets.
2. Every series names an existing dataset; every revision and calendar entry
   names an existing series; every credential's datasets exist.
3. Each series has at most one observation per period: `observation_id`
   corresponds one to one with (`series_id`, `period_start`).
4. All revisions of an observation share `series_id`, `period_start`,
   `period_end`, and `unit`, and `unit` equals the series' `unit`.
5. For a monthly series, `period_start` is the first day of a month and
   `period_end` is the first day of the next month.
6. Within a dataset, `sequence` starts at 1 or above, is unique, and strictly
   increases in file order. Consumers must not assume there are no gaps.
7. Within a dataset, `available_at` never decreases as `sequence` increases.
8. For every revision, `published_at` ≤ `received_at` ≤ `available_at`.
9. An observation's `revision_number` values are exactly 1 through _n_.
   Revision 1 is the only `initial_release`.
10. `value`, `missing_reason`, and `change_type` combine only as the
    [Change types](#change-types) table allows. A decimal `value` matches
    `^-?[0-9]+(\.[0-9]+)?$`.
11. A `withdrawal` does not follow another `withdrawal`, by `revision_number`.
12. A `provider_correction` with number _n_ corrects revision _n_ − 1, which
    is not a `withdrawal`, and has the same `published_at` and `received_at`
    as that revision.
13. Among an observation's revisions other than provider corrections,
    `received_at` strictly increases with `revision_number`.
14. Dates are valid `YYYY-MM-DD` calendar dates. Timestamps match
    `YYYY-MM-DDTHH:MM:SSZ` and are valid UTC instants.
15. The release calendar has at most one entry per series and period.

## Time fields answer different questions

| Question | Field |
| --- | --- |
| Which period does the value describe? | `period_start`, `period_end` |
| When did the source plan to publish it? | `scheduled_at`, in the release calendar |
| When did the source make it public? | `published_at` |
| When did the provider acquire it? | `received_at` |
| When could a customer first retrieve this revision from the API? | `available_at` |

- Dates are calendar dates (`YYYY-MM-DD`) without a time zone. Timestamps are
  RFC 3339 in UTC with a `Z` suffix and whole seconds.
- For every revision, `published_at` ≤ `received_at` ≤ `available_at`.
- A `provider_correction` keeps the `published_at` and `received_at` of the
  source value it corrects. The source published that value once; only
  `available_at` reflects the correction.
- For a `withdrawal`, `published_at` is when the source announced it.
- `available_at` records availability through the API. It does not record when
  any customer downloaded the revision. The demo API makes a fixture revision
  visible when its simulated clock reaches the revision's `available_at`.
- `scheduled_at` is a plan. It is not evidence that the source published at
  that time.

## Values and missing data

- `value` is a decimal string, such as `"102.4"`. Clients that need the exact
  representation must not convert it through binary floating point. The
  source publishes `activity-index` to one decimal place.
- A released observation without a number has a null `value` and a
  `missing_reason`. Version 0.2 uses one reason, `not_collected`: the source
  published the period without a value because the data was not collected.
  Clients must accept reasons they do not recognize.
- A null value differs from zero and from an absent observation. A query
  returns the observation with its null value and reason; an absent
  observation is not returned at all.
- A withdrawn observation is also returned, with a null value and a
  `change_type` of `withdrawal`, so a customer can tell it apart from one that
  was never released.

## Change types

| `change_type` | Created when | `value` |
| --- | --- | --- |
| `initial_release` | The source first publishes the observation. | Decimal, or null with a `missing_reason` |
| `source_revision` | The source publishes a new value for an observation it already released, including a re-release after a withdrawal. | Decimal, or null with a `missing_reason` |
| `provider_correction` | The provider fixes a value it served incorrectly. The source's value is unchanged. | Decimal |
| `withdrawal` | The source withdraws the observation without a replacement value. | Null, with a null `missing_reason` |

Keeping `provider_correction` distinct from `source_revision` lets a customer
tell a new estimate from the source apart from a fix to data the provider
served.

In version 0.2, a `provider_correction` may only fix the observation's
highest-numbered revision at the time of the correction, and it takes the next
`revision_number`. Correcting a revision the source has already superseded
requires a later contract version, which would add a field identifying the
corrected revision.

## Selecting the revision available at a cutoff

This is the `available_as_of` cutoff. For a cutoff `T` and a period range from
`start` up to `end`:

1. Consider the observations whose period lies within the range:
   `period_start` ≥ `start` and `period_end` ≤ `end`.
2. For each observation, take its revisions with `available_at` ≤ `T`. The
   boundary is inclusive.
3. Select the revision with the highest `revision_number` among them.
4. If there is none, the observation is absent from the result.
5. Otherwise, return the selected revision. A released observation without a
   value is returned with its `missing_reason`; a withdrawn observation is
   returned with a null value and a `change_type` of `withdrawal`.
6. Order the results by `period_start`, ascending.

Without a cutoff, `T` is the server's current time, which returns the latest
revision of each observation that the API has made available. In the demo API,
the current time is the [simulated clock](api.md#simulated-clock).

Precedence follows `revision_number`, not arrival. A queued release can become
available after the revision that supersedes it; the November 2025 fixture
exercises that case.

The result describes what an entitled customer could retrieve at `T`.
Revisions served in error remain in the history, so a cutoff before a provider
correction returns the value that was actually served.

## Reconstructing what the source had published

The `published_as_of` cutoff answers a different question: which values had
the source published by `T`, with the provider's processing errors corrected?
It uses the same rule with `published_at` in place of `available_at`:

1. Consider the observations whose period lies within the range, as above.
2. For each observation, take its revisions with `published_at` ≤ `T`. The
   boundary is inclusive.
3. Select the revision with the highest `revision_number` among them.
4. Omit the observation if there is none; otherwise return the selected
   revision, including a withdrawal.
5. Order the results by `period_start`, ascending.

A provider correction keeps the `published_at` of the value it corrects, so
this query applies corrections whenever they were made. On March 4, 2026, it
returns February as 101.3, while `available_as_of` returns the 1.013 the API
actually served. It also ignores the provider's delays. At 13:00 on December 3,
2025, it returns November's 100.0, which no customer could retrieve until the
next day.

The two cutoffs answer different research needs. Use `available_as_of` to
reproduce what a customer could have used. Use `published_as_of` to study the
source's own history, as with a publisher's archive of past releases. A query
uses at most one of them. Without either, the query returns the latest
revision of each observation.

The result has two limits:

- It covers only versions the provider received. If the provider began
  collecting after the source began publishing, the earlier versions are
  missing, and this query cannot recover them.
- It describes the source, not delivery. It does not establish that any
  customer could retrieve the result at `T`.

The result reflects the revisions the API has made available when the query
runs. A correction made later changes the answer for an earlier `T`, as
decision 10 accepts. With the server's clock at 00:00 on March 4, 2026, this
query returns February as 1.013, because the correction does not exist yet;
with the clock after March 5 at 15:20, the same query returns 101.3.

The stage 1 demo API implements only `available_as_of`. The full API
implements both.

## A query is evaluated at one position

Both cutoffs consider only the revisions at or before a single change-stream
position: the dataset's latest position when the query begins. The API
specification keeps that position for every page of a paged result, so pages
cannot mix states while new revisions arrive, and it returns the position with
the result. A cutoff later than the server's current time is refused, because
its answer could still change.

## Change stream

- Each dataset has its own change stream: its revisions in `sequence` order.
  Each revision is one change event, and its `sequence` is the event's
  identity. A consumer that receives an event at or below its saved position
  has already applied it.
- A position `P` means every revision with `sequence` ≤ `P` has been applied. A
  consumer continues by reading revisions with `sequence` > `P`. Position 0 is
  before the first revision.
- Consumers save the positions the API returns. They never compute a position
  by adding to a `sequence`, because sequences can have gaps.
- The position for time `T` is the highest `sequence` with `available_at` ≤
  `T`, or 0 if there is none. Because `available_at` never decreases as
  `sequence` increases (invariant 7), every revision available at `T` is at or
  before that position.
- Revisions can share an `available_at`. At 2025-07-03T12:31:10Z, the June 2025
  release (19) and the May 2025 re-release (20) became available together, so a
  timestamp alone cannot serve as a position.
- A consumer keeps every revision in its history but updates its current value
  for an observation only when the incoming `revision_number` is higher than
  the one it holds.
- A snapshot at the position for `T` contains every revision available at
  `T`. Load the snapshot, then apply revisions after its position. An
  [export](api.md#exports) delivers a snapshot with its position. Position
  retention and expiry belong to the API specification.

## Release calendar

[`fixtures/release-calendar.json`](../fixtures/release-calendar.json) holds one
entry per scheduled release, with `series_id`, `period_start`, `period_end`, and
`scheduled_at`. Every monthly value is scheduled for 12:30:00 UTC on the third
day of the following month. The synthetic calendar ignores weekends and
holidays. Revisions, corrections, and withdrawals are unscheduled. The
calendar is a published plan, so the API returns all of it whatever the
simulated clock shows, to any valid customer key without an entitlement.

## Demonstration credentials

[`fixtures/credentials.json`](../fixtures/credentials.json) holds the API keys
the demo API accepts. The keys are published synthetic values, not secrets.

| Field | Type | Meaning |
| --- | --- | --- |
| `credential_id` | string | Stable identity of the credential. Test control and expected results name it. |
| `api_key` | string | The value sent as `Authorization: Bearer <api_key>`. |
| `kind` | string | `customer` for the `/v1` API; `test_control` for the `/test` endpoints. A key is accepted only by its own kind. |
| `active` | boolean | Whether the key is accepted when the server starts. |
| `datasets` | array of strings | The datasets a customer key is entitled to. Empty for the test-control key. |
| `description` | string | What the credential is for. |

| Credential | Purpose |
| --- | --- |
| `cred_research` | Entitled to `core-indicators`. Expected results use it unless they name another. |
| `cred_unentitled` | A valid customer without entitlements: it reads the catalog and is refused data. |
| `cred_test_control` | Drives the simulated clock and the test actions. |

## Fixture timeline

The fixtures contain 32 monthly observations, January 2024 through August 2026,
and 37 revisions. Unless noted below, each observation has one initial release
published at 12:30:00 UTC on the third day of the following month, received at
12:30:04, and available at 12:31:10. The exceptions each exercise one part of
the contract:

| Observation | What happens | Revisions | Exercises |
| --- | --- | --- | --- |
| October 2024 | Released on schedule without a value: `not_collected`. | `rev_oct24_1` | A null value distinct from an absent observation |
| May 2025 | Released as 100.6 on June 3, 2025. Withdrawn by the source on June 20 at 16:00 after a collection error. Re-released as 100.1 with the June release on July 3, sharing its `available_at`. | `rev_may25_1` to `rev_may25_3` | Withdrawal in the history; ordering revisions that share a timestamp |
| November 2025 | Released as 100.0 on December 3, 2025, but held by the provider's validation until December 4 at 12:45. The source revised it to 100.2 on December 4 at 12:30, and the revision was available at 12:31:10, before the release it revises. | `rev_nov25_1`, `rev_nov25_2` | Precedence independent of arrival order; a release delayed by the provider |
| February 2026 | The provider divided the source's 101.3 by one hundred and served 1.013 on March 3, 2026. Corrected on March 5, available at 15:20:00. | `rev_feb26_1`, `rev_feb26_2` | A provider correction distinct from a source revision; reproducing what was served |
| July 2026 | Scheduled for August 3 at 12:30. The source published late, at 14:05:00; available at 14:06:10. | `rev_jul26_1` | A late source distinguished from a late provider |
| August 2026 | Released as 102.4 on September 3. Revised by the source to 102.1 on September 10: published 12:30:00, received 12:30:04, available 12:30:40. | `rev_aug26_1`, `rev_aug26_2` | Historical cutoffs; a revision arriving during an export |

The values are invented. The final 2025 values average exactly 100.0,
consistent with the base period.

## Expected results

Each file in [`expected/`](../expected) covers one scenario. A **query check**
names a query and the records a correct implementation must return; a
**scenario** is a sequence of requests and test actions with the response each
must produce. The [conformance format](conformance.md) defines how a runner
executes each kind of check and compares the results. In short: results are
compared in order and in full length, each expected entry lists the fields to
compare, and fields it omits are not compared.

The expected results were written from the timeline and the rules above. They
were not produced by running an implementation, so an implementation that
disagrees with them needs investigation, not a regenerated expectation. If the
investigation finds an error in an expected result, correct it in a new
contract version.

| File | What it checks |
| --- | --- |
| [`august-2026-at-cutoffs.json`](../expected/august-2026-at-cutoffs.json) | The August value at cutoffs around its release and revision, including the inclusive boundary |
| [`full-history.json`](../expected/full-history.json) | All 32 observations at the September 4 research date and at the latest state, with page boundaries for a page size of 10 |
| [`missing-value.json`](../expected/missing-value.json) | A null value with its reason, distinct from an absent observation |
| [`withdrawal-and-rerelease.json`](../expected/withdrawal-and-rerelease.json) | An observation returned as withdrawn, then re-released |
| [`out-of-order-arrival.json`](../expected/out-of-order-arrival.json) | A revision that supersedes a release delivered after it |
| [`provider-correction.json`](../expected/provider-correction.json) | The value as served before a correction, and the time fields the correction keeps |
| [`late-source-release.json`](../expected/late-source-release.json) | No value at the usual time, then the late release |
| [`change-stream.json`](../expected/change-stream.json) | Positions for given times, reads after a position, applying events by precedence, retention, and a position ahead of the stream |
| [`simulated-clock.json`](../expected/simulated-clock.json) | Revisions after the clock are invisible; moving the clock reveals them; resetting restores the start |
| [`pagination.json`](../expected/pagination.json) | Pages keep their snapshot while data changes, tokens are bound to their query and credential, and snapshots expire |
| [`access-control.json`](../expected/access-control.json) | Missing, unknown, and revoked keys; refusals without entitlement, including on resumed pages; an empty result distinct from a refusal |
| [`request-errors.json`](../expected/request-errors.json) | Unknown, missing, and malformed parameters, period ranges, cutoffs after the clock, unsupported versions, and the order in which errors are reported |
| [`published-as-of.json`](../expected/published-as-of.json) | What the source had published by each cutoff, compared with what the API was serving at the same instant |
| [`revision-history.json`](../expected/revision-history.json) | Every revision of an observation, including superseded and erroneous ones |
| [`release-calendar.json`](../expected/release-calendar.json) | Scheduled release times |
| [`export-handoff.json`](../expected/export-handoff.json) | Loading a snapshot and its position without losing a revision, compared with the two ways to lose it |
| [`exports.json`](../expected/exports.json) | Repeatable downloads, regeneration, expiry, and protection of export files |
| [`release-timing.json`](../expected/release-timing.json) | Source delay and availability delay for on-time, late-source, and late-provider releases |

## Implementations and conformance

| Repository | Language | Role | Stage |
| --- | --- | --- | --- |
| [financial-data-api](https://github.com/nslaughter/financial-data-api) | Go | Serves the fixtures: the demo API in stage 1, expanded into the full API in stage 2 | 1 |
| [financial-data-sdk-python](https://github.com/nslaughter/financial-data-sdk-python) | Python | The research client and the first showcase | 1 |
| [financial-data-sdk-go](https://github.com/nslaughter/financial-data-sdk-go) | Go | The same client for Go | 1, after Python |
| [financial-data-sdk-ts](https://github.com/nslaughter/financial-data-sdk-ts) | TypeScript | The same client for TypeScript | 1, after Python |
| [financial-data-api-monitor](https://github.com/nslaughter/financial-data-api-monitor) | Go | Scheduled checks with ordinary customer access | 3 |

The demo API lives in this repository from the first stage and is published as
a container image. Each image release states the contract version it
implements. The SDKs and the monitor pin an image tag, and their CI runs the
`expected/` checks for that contract version against it. The expected results
therefore act as one conformance suite: every SDK must return the same records
for the same queries, whatever its language.

Not every check applies from the first stage:

| Expected files | API | SDKs | Monitor |
| --- | --- | --- | --- |
| `august-2026-at-cutoffs`, `full-history`, `missing-value`, `withdrawal-and-rerelease`, `out-of-order-arrival`, `provider-correction`, `late-source-release`, `change-stream`, `simulated-clock`, `pagination`, `access-control`, `request-errors` | Stage 1 | Stage 1 | — |
| `published-as-of`, `revision-history`, `release-calendar`, `export-handoff`, `exports` | Stage 2 | When each SDK adds the feature | — |
| `release-timing` | Stage 2, computed from the revision history and the release calendar | — | Stage 3 |

## Versioning

The contract, fixtures, and expected results are versioned together. Each
file records `contract_version`, and each version is tagged
`contract-v<version>`, starting with `contract-v0.1.0`. The SDKs, the demo API
image, and the monitor each pin a tag. Any change to a fixture or an expected
result gets a new version, which consumers adopt deliberately.

Version 0.2.0 adds the dataset and credential fixtures, the invariants, the
per-dataset change stream, the scenario format, and the expected results for
the clock, pagination, access control, request errors, the revision history,
the release calendar, and exports. It restructures `export-handoff` as
scenarios and adds next and head positions to the change-stream reads. No
fixture record or earlier expected record changed.

## Decisions

These decisions were settled on October 4, 2026:

1. **Withdrawn observations are returned, marked as withdrawn.** A query
   returns the withdrawal revision, with a null value, so a customer can tell
   a withdrawn observation apart from one never released. A customer who
   syncs by querying, instead of reading the change stream, still learns of
   the withdrawal. Clients that want only values filter on `change_type`.
2. **Precedence uses a per-observation `revision_number`.** This handles a
   revision delivered before the release it revises. A provider correction may
   fix only the observation's highest-numbered revision and takes the next
   number. Correcting a superseded revision waits for a later contract version
   with a field identifying the corrected revision; without that rule, either
   cutoff could return a corrected old value instead of a later revision.
3. **A provider correction keeps the corrected value's `published_at` and
   `received_at`.** The source published the value once, and
   `published_as_of` depends on this rule to apply corrections.
4. **Both cutoff boundaries are inclusive**: `available_at` ≤ `T`, or
   `published_at` ≤ `T`.
5. **`available_at` values can tie.** `sequence` orders revisions that share
   one, and positions are sequence numbers, not timestamps.
6. **A null value requires a `missing_reason`; a withdrawal has neither.** Its
   `change_type` explains the missing value.
7. **The synthetic calendar ignores weekends and holidays.** Every release is
   scheduled on the third of the month. Add a moved release when the monitor
   needs to test schedule changes.
8. **One dataset and one series until a check needs more.** Version 0.1 had
   no entitlement fixtures; version 0.2 adds credentials (decision 15), and a
   valid key without entitlements exercises refusal. Revoking access is a test
   action in the API specification. Add a second dataset before the full API
   or the monitor needs to compare what different credentials can see.
9. **Period filters match observations that fall entirely within the range.**
   Overlap matching is unnecessary while every series is monthly.
10. **`published_as_of` applies corrections made after `T`.** It reconstructs
    the source's history with the provider's current processing, not the
    values the provider held at `T`. A query takes one cutoff, not both, and
    the stage 1 demo API omits this one.
11. **The demo API lives in this repository and ships as a container image.**
    The SDKs are tested against behavior they didn't define, the fixtures stay
    beside the service that serves them, and the second stage grows the same
    service instead of moving it.
12. **The API and the monitor are written in Go; the SDKs in Python, Go, and
    TypeScript.** The Python SDK comes first, and the other two cover the same
    workflow and pass the same checks.

The operator reviewed and settled these for version 0.2.0 on October 4, 2026:

13. **API v1 uses the explicit time fields from the start.** No legacy `date`
    field is specified only to be replaced, so the migration stage needs a
    different breaking change (see Open questions).
14. **The demo API runs on a simulated clock.** Revisions whose `available_at`
    is after the clock are invisible on every path. A test-control endpoint
    moves the clock forward, and scenarios in which data changes during an
    operation, and expiry checks, become deterministic.
15. **Authentication and entitlements ship in stage 1.** Static API keys from
    the credentials fixture, entitlements per dataset, checked on every
    request, including resumed pages and export downloads. Identity
    providers, key issuance, rate limits, and TLS are out of scope. Adding
    authentication later would change every SDK's constructor and errors at
    once.
16. **Positions are integers, one change stream per dataset; page tokens are
    opaque.** The history is append-only, so a position never changes
    meaning, and the integer is what the export handoff demonstrates. Streams
    are per dataset because entitlements are, and a global sequence filtered
    by entitlement would reveal activity in datasets the customer cannot see.
17. **An export contains every revision up to its position,** not only the
    latest, so a customer's local copy can answer `available_as_of` queries
    offline.
18. **Metadata is visible to every valid customer key; revisions need an
    entitlement.** The catalog lists every dataset and series with an
    `entitled` flag, and the release calendar is readable without an
    entitlement, so a refusal reveals nothing a not-found would hide.
19. **Unknown query parameters are refused.** A misspelled cutoff, or
    `published_as_of` sent to the stage 1 demo API, must not silently return
    the latest data.
20. **A cutoff later than the server's clock is refused,** because its answer
    could still change. A cutoff equal to the clock is accepted.
21. **Retention periods are fixed and run on the simulated clock:** 3,600
    seconds for a query snapshot, 1,095 days for a change-stream event, and
    86,400 seconds for an export. The change-stream period covers the whole
    fixture history at the default clock, and expiry is tested by moving the
    clock, so there is no settings endpoint.
22. **Only a reset moves the clock backwards.** `PUT /test/clock` moves it
    forward only; `POST /test/reset` may set any clock, and it discards page
    tokens and exports so that no state from a later time survives.
23. **A resumed page repeats every parameter of the first request,**
    including `page_size`, and leaves omitted parameters omitted. Any
    difference is refused, so a resumed page cannot drop a cutoff.
24. **An item expires at the instant its period ends.** Page snapshots,
    change-stream events, and exports are available while the clock is
    before that instant, which is the instant `snapshot_expires_at` and
    `expires_at` report.
25. **The release calendar ignores the simulated clock.** It is a plan
    published in advance, and a monitor needs upcoming releases to know what
    is due.

## Open questions

The operator reviewed this question on October 4, 2026, and left it open.

- **The migration stage's breaking change.** API v1 already uses the
  explicit time fields (decision 13), so stage 4 needs a different change. One
  candidate is already deferred here: correcting a superseded revision
  (decision 2) adds a field naming the corrected revision and changes the
  precedence rule, which breaks clients that select the highest
  `revision_number`. Whether the migration covers all three SDKs or only
  Python is part of the same decision. Stages 1 to 3 do not depend on it.
