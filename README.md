# Financial data API

A demonstration API for loading a financial dataset, receiving its corrections,
and reproducing research using the data available at an earlier point in time.

I'm [Nathan Slaughter](https://nathanslaughter.com/). My engineering work spans
fintech, data pipelines, observability, and infrastructure, informed by a
background in investment research. I help teams turn datasets into APIs whose
meaning and delivery behavior customers can depend on.

**Status:** Project brief. This repository currently contains this README.
The API, contract, deployment, and runnable demonstrations are planned.

## A customer can make successful requests and still have the wrong dataset

A customer downloads a dataset and keeps a local copy. The provider then
corrects an old observation. If the customer's next request asks only for
dates after the last downloaded observation, it can miss the correction.

This project will use a fictional economic series to make that problem
concrete. The proposed API separates observation identity from revision
identity and observation periods from publication and availability times.
Those distinctions support both a current copy and an explanation of what
an earlier research result used.

## How a customer will load, update, and reproduce the data

1. Load a consistent snapshot whose export identifies the corresponding
   starting position in the update stream.
2. Apply releases, revisions, and withdrawals from that position, saving
   progress with the local data changes.
3. Query the versions available at an earlier cutoff and compare the answer
   with independently prepared fixture records.

The [financial-data-sdk](https://github.com/nslaughter/financial-data-sdk)
will act as a customer of the API. The demonstration will introduce a revision
during an export to examine whether the snapshot and update handoff can lose
it. Interrupted pagination, expired cursors, and changed access will supply
additional acceptance cases.

## The contract has to cover delivery as well as field names

The planned contract will describe identifiers, units, missing values, time
semantics, revision history, access rules, and recovery limits. Query, export,
and update examples will use the same dataset so their results can be compared.
A later version-change exercise will follow the consequences into the
customer's stored data.

The intended deliverables include a documented API specification, local startup
instructions, seeded fixtures, automated checks, and a versioned release. The
[delivery monitor](https://github.com/nslaughter/financial-data-api-monitor)
will independently examine responses using ordinary customer access.

## Work with me on programmatic access to your dataset

I can help design and build the data contract, query endpoints, bulk delivery,
and update workflows your customers need. An engagement includes agreed
acceptance cases, documentation, and handover, with maintenance available
after delivery.

[Discuss a financial data API](https://nathanslaughter.com/) with your
dataset, intended users, and the workflows it needs to support.
