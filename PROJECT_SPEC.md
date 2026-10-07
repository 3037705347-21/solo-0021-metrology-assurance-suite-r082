# Metrology Assurance Suite

## Project Goal

Metrology Assurance Suite is a local HTTP service that keeps a traceable record
of laboratory measurement assurance work. It registers devices, opens a
verification case for one device, records technician measurements, requests a
repeat measurement when tolerance is exceeded, and seals the final evidence
record after review.

The service is intentionally self-contained. It uses an in-memory repository
with atomic copy-on-write mutations and does not require a database or an
external service.

## Users

- Laboratory steward: registers devices, opens verification cases, and
  submits field-confirmed scope revisions.
- Measurement technician: records observed values for an open case.
- Quality verifier: reviews accepted measurements and seals the case.

## Core Entities

- Device: a laboratory asset with a unique asset tag, model, room, steward, and
  lifecycle state.
- VerificationCase: one bounded assurance activity for a device, with a nominal
  value, tolerance, current state, scope version, and assigned steward.
- CaseRevision: an immutable record of the effective scope (device, nominal
  value, tolerance) at one case version. Version 1 captures case opening; each
  later revision keeps the actor-supplied reason and basis so every superseded
  version stays traceable within the same case.
- Measurement: an observed value, the case version and scope it was recorded
  against, computed deviation, acceptance decision, technician, and timestamp.
- EvidenceSeal: the immutable closing record for a reviewed case, including the
  selected accepted measurement.
- AuditEvent: an append-only trace of meaningful case and device changes.

## Workflows

### 1. Register Device

The steward sends `POST /devices` with an asset tag, model, room, and steward.
The service validates the request, normalizes the asset tag, rejects duplicate
tags, and creates an active device.

### 2. Open Verification Case

The steward sends `POST /cases` with a device ID, nominal value, tolerance, and
owner. The service verifies that the device exists, is active, and has no
unfinished case. A valid request creates a case in `awaiting_measurement`.

### 3. Record Measurement

A technician sends `POST /cases/{id}/measurements`. The case must be
`awaiting_measurement`. The measurement is bound to the case's current scope
version and records the nominal value and tolerance in effect at that time.
The service computes the signed deviation from that nominal value. An
in-tolerance measurement moves the case to `review_ready`; an out-of-tolerance
measurement moves it to `recheck_required`.

### 4. Reopen Verification Case

The steward sends `POST /cases/{id}/reopen` after an out-of-tolerance result.
Only `recheck_required` cases may move back to `awaiting_measurement`. The
service records the actor and reason in the audit trail.

### 5. Revise Case Scope

After a case is opened but before any measurement is recorded, the assigned
device, nominal value, or tolerance may turn out to need correction based on
field confirmation. Instead of cancelling and reopening the case (which would
sever the case history and the device's unfinished-case relationship), the
steward sends `POST /cases/{id}/revisions` with the corrected scope, the
actor, a reason, a supporting basis, and the `expected_version` currently held.

A revision is accepted only when all of the following hold:

- the case exists, is not sealed, and has no recorded measurements;
- the target device exists, is active, and has no other unfinished case;
- `expected_version` equals the current scope version;
- at least one scope field changes.

The revision creates a new effective version of the **same** case. The case
state stays `awaiting_measurement`, the scope version increments by one, and
an immutable `CaseRevision` plus a `case_revised` audit event are appended in
the same repository commit. Simultaneous revisions are serialized by the
repository lock: exactly one commits; the others fail with
`version_conflict` and must retry against the new version.

### 6. Seal Verification

The verifier sends `POST /cases/{id}/seal` after an accepted measurement. Only
`review_ready` cases may be sealed. The service selects the accepted
measurement with the smallest absolute deviation from the measurements
recorded against the **current** case version, creates exactly one evidence
seal, moves the case to `sealed`, and rejects later mutations.

## State And Rules

Device state:

- `active -> retired`
- `retired` is terminal.

VerificationCase state:

- `awaiting_measurement -> review_ready`
- `awaiting_measurement -> recheck_required`
- `recheck_required -> awaiting_measurement`
- `review_ready -> sealed`
- `sealed` is terminal.

A scope revision is not a state transition: it is allowed only in
`awaiting_measurement` and leaves the state unchanged. The case scope version
starts at 1 and increments only on a committed scope revision; measurement,
reopen, and seal transitions do not change it.

Required invariants:

- Asset tags are unique after case-insensitive normalization.
- A device has at most one unfinished verification case; reassigning a case to
  another device through a scope revision moves that relationship without
  leaving either device with two unfinished cases.
- Measurements are accepted only while a case is awaiting measurement.
- Every measurement is recorded against the case's current scope version and
  the nominal value and tolerance then in effect.
- A tolerance must be positive.
- A scope revision requires an actor, a reason, and a basis, must change at
  least one scope field, and is rejected for sealed cases and cases that
  already have recorded measurements.
- Every scope version, including version 1 at opening, is retained as an
  immutable CaseRevision; superseded versions stay readable on the same case.
- Concurrent scope revisions against the same version have a definite outcome:
  exactly one commits and the others fail with a version conflict.
- A case can have at most one evidence seal.
- Sealing selects only accepted measurements recorded against the current case
  version; measurements and seals can never use a superseded scope version.
- A sealed case cannot accept measurements, revisions, reopen, or seal again.
- Every mutation that changes a case also appends an audit event in the same
  repository commit.

## Modules

- `cmd/metrology-service`: process entrypoint and graceful HTTP shutdown.
- `cmd/workflowcheck`: bounded smoke checks that call the public HTTP API.
- `internal/httpapi`: routing, request decoding, and stable error mapping.
- `internal/assurance`: workflow orchestration and atomic decisions.
- `internal/domain`: entities, validation, state transitions, and errors.
- `internal/repository`: mutex-protected in-memory persistence with copy-on-write
  commits.
- `internal/contracts`: request and response contracts shared by the HTTP layer.

## Public Interfaces

- `GET /healthz`
- `POST /devices`
- `GET /devices/{id}`
- `POST /cases`
- `GET /cases/{id}`
- `POST /cases/{id}/measurements`
- `POST /cases/{id}/reopen`
- `POST /cases/{id}/revisions`
- `POST /cases/{id}/seal`
- `GET /cases/{id}/events`

## Validation Plan

The baseline build command is `go build ./...`. Each declared workflow has a
bounded production smoke command under `cmd/workflowcheck`. The command starts
an HTTP test server in the same process, calls the public routes, and verifies
both success and failure behavior. The seal check also races two concurrent
seal requests to prove that the repository mutation lock permits exactly one
seal. The revision check verifies same-case versioning with retained history,
version-bound measurements, sealed/already-measured rejection, target-device
conflicts, and races two concurrent revisions to prove exactly one commits.

Tests are intentionally deferred for this initialization baseline. The later
engineering-task stage will add unit and integration tests, including red/green
verification for the workflows and their failure paths.
