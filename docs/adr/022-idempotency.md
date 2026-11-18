# ADR-022: Idempotency Infrastructure

- **Status:** Accepted
- **Date:** 2026-11-19

## Context

TicketForge exposes operations that create or modify resources. Clients may retry requests because of network failures, timeouts, or uncertainty about whether the server successfully processed an earlier request.

Without idempotency guarantees, retrying a request may create duplicate resources or repeat an operation that has already succeeded.

For example, an organizer may submit a venue booking creation request, experience a network timeout, and retry the request without knowing whether the original request succeeded.

The system needs reusable idempotency infrastructure that allows supported operations to safely handle retries and concurrent duplicate requests.

Idempotency is a cross-cutting concern. The infrastructure should be reusable across multiple operations, while individual domain decisions determine which operations require idempotency.

## Decision

### Idempotency keys

Clients must provide an `Idempotency-Key` header for operations that require idempotency.

The client generates a UUID for each logical operation attempt.

The same key must be reused when retrying the same logical operation.

A new key must be generated when the client intends to perform a new logical operation, even if the request payload is identical.

The server validates the key before processing the operation.

Missing or invalid keys are rejected for operations that require idempotency.

### Idempotency scope

An idempotency key is scoped to:

- The authenticated organizer profile.
- The operation being performed.
- The idempotency key itself.

The same key may be used independently by different organizer profiles or for different operations.

For example, a key used for venue booking creation does not conflict with the same key used for another operation.

Idempotency records must not be shared across unrelated organizers or operations.

### Request fingerprinting

The system stores a fingerprint of the request payload associated with each idempotency key.

The fingerprint is calculated from a deterministic representation of the operation's relevant request data.

The representation must be consistent so that semantically identical retries produce the same fingerprint.

The fingerprint must include all request fields that affect the operation's outcome.

If the same idempotency key is reused with a different request fingerprint, the server rejects the request with HTTP `409 Conflict`.

The original operation must not be executed again.

### Successful request replay

When a request succeeds, the system stores enough information to reproduce the original successful HTTP response.

The stored result includes:

- The request fingerprint.
- The created resource identifier.
- The original HTTP status code.
- The original response body.

When a request arrives with an existing idempotency key and matching fingerprint, the server returns the previously stored successful result instead of executing the operation again.

The replayed response must preserve the original status code and response body.

The resource must not be created again.

### Failed operations

Only successful operations permanently consume an idempotency key.

If an operation fails before successfully completing, its idempotency record must not permanently prevent the client from retrying with the same key.

For example, venue booking creation may fail because:

- The requested venue is unavailable.
- The organizer has reached the venue booking limit.
- The requested schedule violates scheduling constraints.
- Another business rule prevents creation.

The client may retry using the same key after the underlying condition has been resolved.

Failed operations must not leave behind a successful idempotency result.

### Transactional persistence

The idempotency result and the resource created by the operation must be committed in the same database transaction.

The transaction must ensure that:

- A successful resource creation cannot commit without its corresponding idempotency result.
- An idempotency result cannot commit without the corresponding successful resource creation.
- A rolled-back operation leaves neither a created resource nor a successful idempotency result.

PostgreSQL remains the source of truth for idempotency records.

No external cache or Redis dependency is introduced for the MVP.

### Concurrent requests

The infrastructure must handle concurrent requests using the same idempotency key.

If multiple identical requests arrive concurrently:

- At most one request may execute the underlying operation successfully.
- Other requests must not create duplicate resources.
- Requests that encounter an in-progress operation must wait for its outcome or receive a clearly defined retryable response.
- Once the original operation succeeds, subsequent matching requests must receive the stored successful result.

The database must enforce uniqueness for the idempotency scope.

The implementation must use database transactions and appropriate locking or conflict handling to guarantee these properties.

### Idempotency record retention

Successful idempotency records are retained for the lifetime of the resource created by the operation.

The system does not introduce a time-based expiration policy for successful idempotency records in the MVP.

This avoids allowing a delayed retry to create a duplicate resource after an idempotency record has expired.

Idempotency records must remain available for as long as the corresponding resource is retained.

No background cleanup job or Redis-based expiration mechanism is required.

### Operation-specific policies

The reusable idempotency infrastructure does not automatically require every endpoint to support idempotency.

Individual domain decisions determine:

- Whether an operation requires an idempotency key.
- Which authenticated identity scopes the operation.
- Which request fields participate in fingerprinting.
- Which successful response must be stored and replayed.
- How the operation handles domain-specific failures.

The infrastructure must be reusable without embedding venue booking or event-specific business rules.

Venue booking creation requires idempotency under ADR-021.

Other operations may adopt the infrastructure when their domain policies require it.

## Consequences

### Positive

- Retrying supported operations does not create duplicate resources.
- Clients can safely retry requests after uncertain network outcomes.
- Idempotency behavior is consistent across supported operations.
- Domain packages can reuse common infrastructure without duplicating implementation.
- PostgreSQL provides durable, transactional idempotency guarantees.
- Concurrent duplicate requests cannot successfully execute the same logical operation multiple times.
- Operation-specific policies remain independent of the reusable infrastructure.
- Successful idempotency records cannot expire while their associated resources are retained.

### Negative

- Supported operations require additional database records and transactional coordination.
- Request fingerprinting requires a deterministic representation of relevant request data.
- Concurrent requests may need to wait for an in-progress transaction.
- Successful idempotency records accumulate over time and require a retention strategy tied to resource lifecycle.
- Each operation must explicitly integrate with the infrastructure and define its own idempotency policy.

## Alternatives considered

### Client-side duplicate prevention only

Rejected because clients cannot reliably determine whether a request succeeded when a network failure occurs.

Client-side prevention alone cannot guarantee that retries will not create duplicate resources.

### Time-based idempotency key expiration

Rejected for the MVP because an expired key could allow a delayed retry to execute an operation again and create a duplicate resource.

Retaining successful records for the lifetime of their associated resources provides a simpler guarantee.

### Redis-based idempotency storage

Rejected because PostgreSQL is already the source of truth for application data and can persist idempotency results transactionally with resource creation.

Introducing Redis would add infrastructure without providing a necessary guarantee that PostgreSQL cannot provide.

### Implement idempotency separately in every endpoint

Rejected because it would duplicate key validation, fingerprinting, concurrency handling, and response replay logic.

A reusable infrastructure package provides consistent behavior while allowing individual operations to define their own policies.

### Require idempotency for every endpoint

Rejected because not every operation needs the same guarantees.

Idempotency requirements should be determined by individual domain policies rather than imposed indiscriminately across the API.
