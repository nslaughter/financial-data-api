# Data contract: synthetic activity index

**Status:** Draft for review, proposed version 0.1.0. All data is synthetic and
describes no real economy, source, or provider.

This contract defines the dataset shared by the provider demonstration: the
[financial-data-sdk](https://github.com/nslaughter/financial-data-sdk), this
API, the
[financial-data-api-monitor](https://github.com/nslaughter/financial-data-api-monitor),
and the later migration example. It specifies what the records mean. The
fixtures in [`fixtures/`](../fixtures) implement it, and
[`expected/`](../expected) records the results a correct implementation must
return.

## Scope

This document covers:

- the series catalog;
- revision records, their identities, and their time fields;
- values and missing data;
- the rules for selecting the revision available at a cutoff, and the revision
  the source had published by a cutoff;
- the order of the change stream;
- the release calendar;
- the fixture timeline and the expected results.

The API specification, to be written before the demo API, covers endpoints,
request parameters, pagination and snapshot lifetime, exports, errors,
authentication, and entitlements. Where this document describes a query, it
specifies the content of the result, not the request syntax.

## Concepts

- A **series** is a sequence of measurements with one meaning, unit, and
  frequency. `activity-index` is the only series in version 0.1.
- An **observation** is the series' measurement for one period. Its identity
  stays fixed while its value is revised.
- A **revision** is one version of an observation. The first release, each
  later revision or correction, and a withdrawal each create a new revision.
  Revisions are never modified or deleted.
- The **change stream** is every revision in the order it became available
  through the API.

## Series catalog

[`fixtures/series.json`](../fixtures/series.json)

| Field | Type | Meaning |
| --- | --- | --- |
| `series_id` | string | Stable identity of the series. Renaming the series does not change it; changing what it measures requires a new series. |
| `dataset_id` | string | The dataset the series belongs to. Entitlements are granted per dataset (specified in the API spec). |
| `name` | string | Display name. |
| `description` | string | What the series measures. |
| `frequency` | string | `monthly` for every series in version 0.1. |
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
| `sequence` | integer | Position in the change stream. Unique and strictly increasing in the order revisions became available. |
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
or infer order from them.

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
  any customer downloaded the revision.
- `scheduled_at` is a plan. It is not evidence that the source published at
  that time.

## Values and missing data

- `value` is a decimal string, such as `"102.4"`. Clients that need the exact
  representation must not convert it through binary floating point. The
  source publishes `activity-index` to one decimal place.
- A released observation without a number has a null `value` and a
  `missing_reason`. Version 0.1 uses one reason, `not_collected`: the source
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

In version 0.1, a `provider_correction` may only fix the observation's
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

Without a cutoff, `T` is the current time, which returns the latest revision of
each observation.

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

The stage 1 demo API implements only `available_as_of`. The full API
implements both.

## Change stream

- The change stream is the revisions in `sequence` order. Each revision is one
  change event.
- A position `P` means every revision with `sequence` ≤ `P` has been applied. A
  consumer continues by reading revisions with `sequence` > `P`.
- The position for time `T` is the highest `sequence` with `available_at` ≤
  `T`. Because `available_at` never decreases as `sequence` increases, every
  revision available at `T` is at or before that position.
- Revisions can share an `available_at`. At 2025-07-03T12:31:10Z, the June 2025
  release (19) and the May 2025 re-release (20) became available together, so a
  timestamp alone cannot serve as a position.
- A consumer keeps every revision in its history but updates its current value
  for an observation only when the incoming `revision_number` is higher than
  the one it holds.
- A snapshot taken at `T` and the position for `T` describe the same state:
  load the snapshot, then apply revisions after that position. Position
  retention and expiry belong to the API spec.

## Release calendar

[`fixtures/release-calendar.json`](../fixtures/release-calendar.json) holds one
entry per scheduled release, with `series_id`, `period_start`, `period_end`, and
`scheduled_at`. Every monthly value is scheduled for 12:30:00 UTC on the third
day of the following month. The synthetic calendar ignores weekends and
holidays. Revisions, corrections, and withdrawals are unscheduled.

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

Each file in [`expected/`](../expected) covers one scenario. A check names a
query and the result a correct implementation must return. Each expected entry
lists the fields to compare; fields it omits are not compared.

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
| [`published-as-of.json`](../expected/published-as-of.json) | What the source had published by each cutoff, compared with what the API was serving at the same instant |
| [`late-source-release.json`](../expected/late-source-release.json) | No value at the usual time, then the late release |
| [`release-timing.json`](../expected/release-timing.json) | Source delay and availability delay for on-time, late-source, and late-provider releases |
| [`change-stream.json`](../expected/change-stream.json) | Positions for given times, reads after a position, and applying events by precedence |
| [`export-handoff.json`](../expected/export-handoff.json) | Loading a snapshot and its position without losing a revision, compared with the two ways to lose it |

## Versioning

The contract, fixtures, and expected results are versioned together. Each
file records `contract_version`. Once this draft is approved, it will be
tagged `contract-v0.1.0`; the SDK, the demo API, and the monitor each pin a
tag. Any change to a fixture or an expected result gets a new version, which
consumers adopt deliberately.

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
8. **Version 0.1 has one series and no entitlement fixtures.** Revoking access
   is a test action specified in the API spec. Add a second dataset before the
   full API or the monitor needs to compare what different credentials can see.
9. **Period filters match observations that fall entirely within the range.**
   Overlap matching is unnecessary while every series is monthly.
10. **`published_as_of` applies corrections made after `T`.** It reconstructs
    the source's history with the provider's current processing, not the
    values the provider held at `T`. A query takes one cutoff, not both, and
    the stage 1 demo API omits this one.

## Open questions

- **The migration stage's starting point.** The migration example replaces an
  ambiguous `date` field with explicit time fields, but this contract uses the
  explicit fields from the start. Either the migration starts from a legacy
  representation created for the exercise, or stage 4 needs a different
  change.
- **Where the stage 1 demo API lives.** The SDK repository's README currently
  places it there. Serving these fixtures from this repository instead would
  make the SDK an external customer from the start.
