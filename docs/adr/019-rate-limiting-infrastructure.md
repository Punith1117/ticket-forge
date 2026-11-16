# ADR-019: Application Rate Limiting Infrastructure

- **Status:** Accepted
- **Date:** 2026-11-16
- **Related ADRs:** ADR-012, ADR-013

## Context

The application needs a reusable rate-limiting mechanism to protect against excessive requests, automated abuse, brute-force attempts, and resource exhaustion.

Although the initial use case is authentication protection, rate limiting may eventually be required across other application areas, including booking creation, reservation requests, email delivery, and public API endpoints.

The application follows a modular-monolith architecture and uses PostgreSQL as its primary database. It may also run multiple API instances in the future.

Rate-limiting state must therefore be shared across application instances rather than maintained in process-local memory.

Introducing Redis solely for rate limiting would add another infrastructure dependency. PostgreSQL already provides the transactional and concurrency-control capabilities needed for the application's initial requirements.

We want a reusable mechanism that separates rate-limiting infrastructure from feature-specific policies, allowing different application areas to define their own limits without implementing their own algorithms or persistence mechanisms.

## Decision

### Reusable rate-limiting infrastructure

Rate limiting will be implemented as a reusable internal package:

`internal/ratelimit`

The package will provide a common mechanism for evaluating and enforcing rate limits.

It must remain independent of specific application features and must not contain authentication-, booking-, or other domain-specific business logic.

Individual application modules are responsible for:

- Selecting the applicable rate-limit policy.
- Constructing identifiers for protected operations.
- Deciding when to evaluate a limit.
- Handling the result according to their business requirements.

The rate-limiting package is responsible for:

- Evaluating policies.
- Managing token buckets.
- Persisting bucket state.
- Ensuring atomic token consumption.
- Calculating retry durations.
- Returning consistent results.

### Token bucket algorithm

The application will use the token bucket algorithm as its initial rate-limiting mechanism.

Each bucket consists of:

- A maximum token capacity.
- A current available token count.
- A token refill rate.
- A timestamp representing the last refill.

Each permitted operation consumes one or more tokens according to its policy.

Tokens replenish continuously based on elapsed time since the last refill, up to the configured bucket capacity.

When insufficient tokens are available, the operation is denied.

The algorithm must ensure:

- Available tokens never exceed bucket capacity.
- Token consumption never produces a negative balance.
- Refill calculations account for elapsed time accurately.
- Denied operations do not consume additional tokens.
- Concurrent operations cannot consume the same available tokens.

Token calculations should use integer token units and suitable time precision rather than floating-point arithmetic.

The algorithm is selected because it permits controlled bursts while limiting sustained request rates. It also avoids the boundary-burst behavior associated with fixed-window counters.

### PostgreSQL-backed persistence

PostgreSQL will be the initial persistence backend for rate-limiting state.

- All application instances must share the same bucket state.
- The implementation must not depend on in-memory counters or process-local maps.
- Rate-limit state must be persisted across application restarts.
- PostgreSQL must provide the atomicity and concurrency control required for safe token consumption.

The implementation may use atomic SQL statements, conditional updates, or transactions with appropriate row locking.

Separate read-then-write operations that can race under concurrent requests must not be used.

The database schema should store only the information required to identify and maintain buckets.

The initial implementation should avoid unnecessary persistence complexity or introducing additional infrastructure.

### Atomic token consumption and concurrency

Token refill and consumption must be atomic.

For any individual bucket, concurrent requests must observe a consistent token balance and must not collectively consume more tokens than are available.

The implementation must ensure that:

- Concurrent bucket initialization cannot create duplicate buckets.
- Concurrent requests cannot overspend available tokens.
- Refill calculations cannot be applied multiple times to the same elapsed interval.
- Bucket timestamps and token balances remain consistent.
- A denied operation does not incorrectly advance the bucket state.

PostgreSQL uniqueness constraints and atomic database operations should enforce these guarantees.

Concurrency correctness must be verified through integration tests against PostgreSQL, not only unit tests.

### Identifiers and privacy

Buckets must be associated with identifiers representing the protected client or operation.

Examples include:

- Client IP addresses.
- User identifiers.
- Normalized email identifiers.
- Registration-token identifiers.
- Other feature-specific identifiers.

The rate-limiting infrastructure must not assume that all identifiers are public or non-sensitive.

Sensitive identifiers must be transformed using keyed HMAC before persistence.

- Raw email addresses must not be stored as bucket identifiers.
- Raw authentication tokens must never be stored as bucket identifiers.
- Raw IP addresses should be transformed using keyed HMAC where practical.
- Different identifier categories should use domain-separated HMAC inputs.
- HMAC secrets must be independent of encryption, session, and other application secrets.

The HMAC secret must be provided through environment configuration or an appropriate secrets manager and must never be committed to version control.

The implementation must document that changing the HMAC secret changes derived identifiers and effectively resets the associated rate-limit state unless an explicit migration strategy is provided.

### Policy configuration

Rate-limit policies must be defined centrally rather than scattered throughout application handlers.

Each policy must define:

- A unique policy identifier.
- Bucket capacity.
- Refill rate.
- Refill time unit.
- Any other configuration required to determine token consumption.

Policies should be supplied by the calling application module rather than embedded inside the rate-limiting infrastructure.

Configuration must be validated. Invalid capacities, refill rates, or durations must not result in silently disabled rate limiting.

The infrastructure should support different policies for different endpoints and operations without requiring changes to its core implementation.

### Retry calculation and result

The rate-limiting mechanism must provide enough information for callers to determine whether an operation is permitted.

The result should communicate:

- Whether the operation is allowed.
- The number of remaining tokens, where appropriate.
- The time until the next token becomes available when denied.

The infrastructure must not impose a universal HTTP response or business-level error behavior.

The calling module is responsible for translating a denied operation into the appropriate application response.

For HTTP endpoints, callers may use this information to return HTTP `429 Too Many Requests` with an appropriate `Retry-After` header.

### Expired bucket cleanup

Inactive buckets must not accumulate indefinitely.

The implementation must provide a cleanup strategy for buckets that have not been accessed within an appropriate retention period.

Cleanup must:

- Avoid deleting active buckets.
- Be efficient for PostgreSQL.
- Avoid interfering with concurrent bucket operations.
- Not require a dedicated background worker solely for rate-limit cleanup in the MVP.

Cleanup scheduling may use the existing application infrastructure.

### Storage abstraction and future Redis adoption

The rate-limiting implementation must separate its public interface from the underlying persistence mechanism.

PostgreSQL is the initial storage backend. Redis will not be introduced for the MVP.

The design should allow a future Redis implementation without requiring changes to the calling application modules or their business logic.

Such a replacement should preserve policy semantics and rate-limiting behavior where practical.

A storage migration must explicitly consider existing bucket state. Resetting rate-limit state or migrating active buckets must be an intentional operational decision.

Redis should only be considered if measured PostgreSQL contention, latency, throughput, or infrastructure requirements justify the additional operational complexity.

## Consequences

### Positive

- A single reusable rate-limiting mechanism can serve multiple application modules.
- Rate-limit state is shared across API instances.
- PostgreSQL avoids introducing another infrastructure dependency for the MVP.
- Token bucket supports controlled bursts while restricting sustained request rates.
- Atomic database operations provide consistent behavior under concurrency.
- Sensitive identifiers are not persisted in plaintext.
- Feature-specific policies remain separate from infrastructure implementation.
- The storage backend can potentially be replaced without redesigning calling modules.

### Negative

- Rate-limit evaluation introduces additional PostgreSQL operations.
- PostgreSQL becomes a dependency for rate-limit evaluation.
- Token bucket requires more complex calculations and concurrency handling than fixed-window counters.
- Bucket cleanup introduces additional operational responsibilities.
- HMAC key management and rotation require care.
- Moving to Redis later requires an explicit decision about existing bucket state and operational migration.

## Out of Scope

The following are not part of this decision:

- Authentication-specific rate-limit thresholds and policies.
- Booking-specific or reservation-specific rate-limit thresholds and policies.
- General HTTP middleware behavior or endpoint-specific response handling.
- CAPTCHA or external bot-detection services.
- Redis implementation for the MVP.
- A distributed rate-limiting service independent of the application.
- Concurrency limiting or request queue management.
- A universal policy requiring every application endpoint to use token bucket.

Feature-specific rate-limiting requirements should be documented separately where they introduce meaningful architectural or security decisions.

## Implementation Notes

The initial implementation should include:

- A reusable `internal/ratelimit` package.
- A small public interface for evaluating rate limits.
- PostgreSQL-backed token bucket persistence.
- Atomic token refill and consumption.
- A Goose migration for bucket storage, if required.
- sqlc-generated database access code where appropriate.
- Centralized policy definitions and configuration validation.
- HMAC-based identifier derivation using a dedicated secret.
- Retry-duration calculation.
- An efficient cleanup strategy for inactive buckets.
- Unit, PostgreSQL integration, concurrency, and failure-handling tests.

The implementation must remain independent of authentication and other feature-specific business logic.
