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

- Laboratory steward: registers devices and opens verification cases.
- Measurement technician: records observed values for an open case.
- Quality verifier: reviews accepted measurements and seals the case.

## Core Entities

- Device: a laboratory asset with a unique asset tag, model, room, steward, and
  lifecycle state.
- VerificationCase: one bounded assurance activity for a device, with a nominal
  value, tolerance, current state, scope version, and assigned steward.
- ScopeRevision: one effective version of a case's scope (device, nominal,
  tolerance), versioned from 1. Later revisions carry the revision reason,
  supporting basis, and actor; the full revision list stays traceable.
- Measurement: an observed value, computed deviation, acceptance decision,
  technician, the scope version it was evaluated against, and timestamp.
- EvidenceSeal: the immutable closing record for a reviewed case, including the
  selected accepted measurement and the sealed scope version.
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
`awaiting_measurement`. The service computes the signed deviation from the
nominal value. An in-tolerance measurement moves the case to
`review_ready`; an out-of-tolerance measurement moves it to
`recheck_required`.

### 4. Reopen Verification Case

The steward sends `POST /cases/{id}/reopen` after an out-of-tolerance result.
Only `recheck_required` cases may move back to `awaiting_measurement`. The
service records the actor and reason in the audit trail.

### 4a. Revise Case Scope

After opening a case but before any measurement is recorded, on-site
confirmation may show that the nominal value, tolerance, or associated device
was entered incorrectly. Instead of cancelling and reopening (which would
break the case history and the device's open-case relationship), the
responsible steward sends `POST /cases/{id}/revisions` with the current
`expected_version`, the corrected nominal, tolerance, and optionally device,
plus a reason and the supporting basis (document, certificate, or observation).

The service appends a new `ScopeRevision` to the same case and bumps the
scope version. The prior revision is retained verbatim and stays traceable in
the case view and audit trail. The case remains `awaiting_measurement`;
measurements recorded afterward and the eventual seal use only the current
effective version (each measurement and seal records its `case_version`).

A revision request gets a deterministic result:

- A sealed case is rejected (`state_conflict`).
- A case that already has any recorded measurement is rejected
  (`state_conflict`), including rejected measurements from the recheck path;
  their evaluated scope must not change underneath them.
- A request whose `expected_version` is not the current version is rejected
  (`state_conflict`), so concurrent revisions resolve as exactly one success
  and one conflict under the repository mutation lock.
- Missing actor/reason/basis or non-positive tolerance are `invalid_input`;
  the actor must be the case's responsible steward.
- A referenced device must exist and be active; rebinding onto a device that
  already has another unfinished case is `active_case_exists`.

### 5. Seal Verification

The verifier sends `POST /cases/{id}/seal` after an accepted measurement. Only
`review_ready` cases may be sealed. The service selects the accepted
measurement with the smallest absolute deviation, creates exactly one evidence
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

A scope revision is not a state transition: it stays in
`awaiting_measurement` and only advances the scope version.

Required invariants:

- Asset tags are unique after case-insensitive normalization.
- A device has at most one unfinished verification case, including when a
  revision rebinds a case to another device.
- Measurements are accepted only while a case is awaiting measurement.
- A tolerance must be positive, on case creation and on every revision.
- A scope revision requires the responsible steward, a reason, a basis, and
  the current `expected_version`, and is permitted only while no measurement
  exists on the case and the case is not sealed.
- Every case keeps an ordered revision history; revision 1 is written when the
  case opens and later revisions only append, never overwrite.
- Each measurement and seal records the scope version it used; after a
  revision only the current version's accepted measurement can be sealed.
- A case can have at most one evidence seal.
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
both success and failure behavior. The seal check and the scope revision check
also race two concurrent mutating requests to prove that the repository
mutation lock permits exactly one success.

Tests are intentionally deferred for this initialization baseline. The later
engineering-task stage will add unit and integration tests, including red/green
verification for the workflows and their failure paths.
