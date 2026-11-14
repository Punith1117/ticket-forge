# High-Level Design (HLD)

**Status:** Accepted

## Architectural Goals

- Start with a modular monolith.
- Keep business logic separate from HTTP and infrastructure concerns.
- Use PostgreSQL as the source of truth.
- Separate API and worker process entry points.
- Introduce infrastructure incrementally.
- Support multiple API instances without depending on in-memory shared state.
- Enforce inventory correctness, venue scheduling, and venue capacity constraints across all API instances.
- Make asynchronous workflows recoverable and safe to retry.
- Keep the initial implementation understandable and avoid premature distributed-system complexity.

## Architecture Style

Initial architecture: **Modular Monolith**.

The application uses one Go codebase with cohesive internal modules.

It can run as separate process types:

- **API process:** Handles REST requests.
- **Worker process:** Handles background processing.
- **Outbox publisher:** Initially runs as part of the worker process.

Infrastructure:

- **PostgreSQL:** Authoritative persistent storage.
- **Redis:** Caching and rate limiting.
- **Message broker:** Asynchronous event transport; implementation choice remains open.
- **Prometheus:** Metrics collection.
- **Grafana:** Metrics visualization.

The API and worker are separate process entry points, but they share the same application codebase and domain logic.

## High-Level Architecture Diagram

```mermaid
flowchart TB
    Client[Bruno / curl / REST Clients]

    Proxy[Reverse Proxy / Load Balancer]

    API1[Go API Instance 1]
    API2[Go API Instance 2]

    subgraph Application["TicketForge Modular Monolith"]
        Identity[Identity and Access]
        Catalog[Catalog]
        Inventory[Inventory]
        Booking[Reservations and Bookings]
        Payment[Payment Simulation]
        Refund[Refund Processing]
    end

    DB[(PostgreSQL)]
    Redis[(Redis)]
    Broker[(Message Broker)]
    Worker[Go Worker Process]

    OutboxTable[(Outbox Table)]
    Obs[Prometheus / Grafana / Logs / Traces]

    Client --> Proxy
    Proxy --> API1
    Proxy --> API2

    API1 --> Application
    API2 --> Application

    Application <--> DB
    Application -. Cache and Rate Limiting .-> Redis

    DB --- OutboxTable
    Worker <--> DB
    Worker <--> Broker

    API1 --> Obs
    API2 --> Obs
    Worker --> Obs
```

The diagram represents the target architecture. Some components will be introduced only in later implementation phases.

The outbox is a table inside PostgreSQL, not a separate infrastructure service.

The worker process initially hosts:

- Reservation expiration.
- Refund processing.
- Outbox publishing.
- Other asynchronous consumers introduced later.

The broker is not required for a normal reservation transaction to commit. Business changes and their corresponding outbox records are committed together in PostgreSQL.

## Logical Modules

### Identity and Access

Responsibilities:

- User registration.
- Email verification.
- Authentication and session management.
- Organizer profile creation.
- Authorization.
- Resource ownership.

Every registered user has customer capabilities. Organizer capabilities are granted through an organizer profile.

The initial authentication model uses email/password authentication and stateful sessions.

Passwords are hashed using Argon2id. Session tokens are generated securely, while only their hashes are persisted.

Email verification is required before users can reserve tickets or activate organizer capabilities.

Organizer activation is immediate after the required verification and profile creation. There is no separate approval workflow in the initial version.

### Catalog

Responsibilities:

- Platform-managed venue catalog.
- Event creation and management.
- Event rescheduling and cancellation.
- Ticket category management.
- Booking availability controls.
- Venue scheduling validation.
- Venue capacity validation.

Organizers can select existing active venues but cannot freely create or modify venue records through the application.

Each event belongs to exactly one organizer and references one venue.

Events are created directly with lifecycle status `active`. There is no Draft or Published state.

Event lifecycle and booking availability are separate:

- Event lifecycle: `active`, `cancelled`.
- Booking availability: `open`, `paused`.

Event completion is derived from its schedule and is not persisted as a lifecycle state.

The catalog must prevent overlapping active events at the same venue. PostgreSQL exclusion constraints provide authoritative protection against concurrent scheduling conflicts.

The total allocated capacity of an event's ticket categories must not exceed its venue's physical capacity. This aggregate invariant requires transactional coordination.

### Inventory

Responsibilities:

- Availability queries.
- Inventory allocation.
- Inventory release.
- Inventory invariants.
- Concurrency control.

PostgreSQL is authoritative for inventory.

For each ticket category:

```text
available = capacity - reserved_quantity - sold_quantity
```

Inventory transitions must occur atomically with their corresponding reservation or booking state transitions.

Redis must never become the authoritative inventory store.

### Booking

Responsibilities:

- Reservation creation.
- Reservation expiration.
- Booking confirmation.
- Reservation cancellation.
- Idempotency coordination.
- Historical price snapshots.

Reservations initially have a two-minute lifetime.

A reservation can produce at most one booking.

Expired and cancelled Held reservations release their reserved inventory exactly once.

Confirmed bookings remain historical records. Individual confirmed-booking cancellation is not supported in the initial version.

### Payment Simulation

Responsibilities:

- Simulated payment attempts.
- Success and failure outcomes.
- Payment attempt persistence.
- Payment retries while a reservation remains eligible.

Failed payment attempts are retained.

A failed payment does not automatically release inventory. The reservation remains Held if it is still eligible.

The initial implementation uses a deterministic simulator rather than a real payment provider.

### Refund Processing

Responsibilities:

- Creating refund records during event cancellation.
- Processing pending refunds.
- Recording simulated refund outcomes.
- Retrying failed refunds.
- Preventing duplicate refund operations.

Event cancellation performs its critical database changes synchronously:

- Marks the event as `cancelled`.
- Invalidates outstanding Held reservations.
- Releases their reserved inventory.
- Creates pending refund records for confirmed bookings.
- Records the cancellation outbox event.

These changes commit atomically.

Actual refund execution is asynchronous. Refund processing failures do not reactivate events or restore sold inventory.

There is at most one refund record per booking. Retries operate on the existing refund record.

### Asynchronous Processing

Responsibilities:

- Transactional outbox.
- Outbox publishing.
- Reservation expiration.
- Refund processing.
- Event consumers.
- Retry handling.
- Duplicate message handling.

The transactional outbox ensures that business changes and corresponding event records are committed atomically.

The outbox publisher sends committed records to the message broker.

Delivery is treated as at least once. Consumers must therefore be idempotent.

Workers must safely claim work, revalidate state, and tolerate retries and process crashes.

Reservation expiration and event cancellation coordinate through database transactions. Event cancellation itself does not defer Held reservation invalidation to a background worker.

### Observability

Responsibilities:

- Structured logging.
- Metrics.
- Tracing.
- Health checks.
- Operational diagnostics.

Prometheus collects metrics from API instances and workers.

Grafana provides dashboards for application behavior and infrastructure health.

Important signals include:

- HTTP request count, latency, and error rates.
- Reservation attempts and outcomes.
- Inventory conflicts.
- Reservation expiration lag.
- Venue scheduling conflicts.
- Database connection pool utilization.
- Worker processing duration.
- Pending refund count and oldest pending refund age.
- Outbox backlog and oldest unpublished event age.
- Broker publishing failures.
- Consumer retries and dead-letter counts.

Sensitive information such as passwords, session tokens, and unnecessary personal information must not be logged.

## Deployment Evolution

### Initial Setup

Run PostgreSQL 18 using a local container through Compose.

The Go API can run directly on Fedora during early development.

The initial venue catalog is populated through seed data.

Database schema changes are managed through versioned SQL migrations.

The initial database access approach uses:

- `pgx/v5` and `pgxpool` for PostgreSQL connectivity.
- `sqlc` for type-safe query code generation.
- Goose for SQL migrations.

### Caching Phase

Add Redis to the local container environment.

Use it for selected caching and rate-limiting requirements.

Redis is optional for business correctness. Its unavailability must not allow inventory overselling or violate other PostgreSQL invariants.

### Messaging Phase

Add a message broker.

Introduce the outbox publisher and worker process.

Use the transactional outbox pattern to reliably publish events resulting from committed database transactions.

The specific broker remains an implementation decision.

### Observability Phase

Add Prometheus and Grafana.

Instrument the API, database interactions, and workers.

Introduce structured logging and tracing as needed.

### Multi-Instance Phase

Run multiple API instances behind a reverse proxy.

All API instances share PostgreSQL and the required infrastructure.

Correctness must not depend on sticky sessions or process-local memory.

Stateful authentication sessions are persisted in PostgreSQL, allowing requests to be handled by different API instances.

## Important Architectural Rules

1. PostgreSQL is the source of truth for business-critical state.
2. PostgreSQL enforces venue scheduling correctness through exclusion constraints.
3. Inventory allocation and release must be transactionally correct.
4. Aggregate venue-capacity constraints must be protected through transactional coordination.
5. Redis is not the authoritative inventory store.
6. Broker availability must not be required to commit a normal reservation.
7. Outbox events are committed atomically with their corresponding business changes.
8. Consumers assume at-least-once delivery.
9. Consumers and workers must be safe to retry.
10. API shutdown must allow in-flight requests to finish within a configured deadline.
11. Worker shutdown must stop claiming new work and safely finish or release claimed work.
12. Event cancellation must be durable even if refund processing is temporarily unavailable.
13. Event cancellation synchronously invalidates Held reservations and releases their inventory.
14. Refund processing must be idempotent.
15. Refund processing must not restore sold inventory.
16. Price changes must not modify existing reservation or booking amounts.
17. Authorization must enforce organizer ownership at the resource level.
18. Multiple API instances and workers must coordinate through shared persistent state, not process-local locks.

## Initial Technology Direction

| Concern | Technology | Decision |
| --- | --- | --- |
| Language | Go | Selected |
| API | REST over HTTP | Selected |
| Database | PostgreSQL 18 | Selected |
| PostgreSQL driver | pgx/v5 and pgxpool | Selected |
| Query generation | sqlc | Selected |
| Migrations | Goose | Selected |
| Local infrastructure | Compose | Selected |
| Cache and rate limiting | Redis | Selected |
| Messaging | Message broker | To be selected |
| Authentication | Email/password and stateful sessions | Selected |
| Password hashing | Argon2id | Selected |
| Email delivery | Resend | Selected |
| API testing | Bruno and curl | Selected |
| Metrics | Prometheus | Selected |
| Dashboards | Grafana | Selected |
| Load testing | To be selected |
| Reverse proxy | To be selected |

## Decisions Deferred Until Implementation

The following decisions remain open and should be made when their corresponding implementation phases approach:

- Go HTTP router.
- Message broker.
- Redis client.
- Reverse proxy and load balancer.
- Exact deployment topology.
- Load-testing tool.
- Detailed worker claiming, retry, and backoff policies.
- Outbox retention and cleanup policy.
- Consumer deduplication strategy.
- Detailed database locking strategy for aggregate venue capacity.
- Detailed database locking and lock-order strategy across reservation, confirmation, cancellation, and expiration operations.
- Operational procedures for failed refunds and dead-letter messages.

Consequential architectural decisions should be recorded as ADRs.

## Implementation Philosophy

Build the system incrementally.

Start with PostgreSQL-backed business functionality and establish correctness before introducing additional infrastructure.

A sensible implementation progression is:

1. Database schema, migrations, and query generation.
2. Identity and access.
3. Venue and event management.
4. Ticket categories and inventory.
5. Reservations and booking confirmation.
6. Simulated payments and refunds.
7. Transactional outbox and background workers.
8. Redis integration where justified.
9. Message broker integration.
10. Observability and load testing.
11. Multiple API instances behind a reverse proxy.

Each phase should preserve the established business invariants rather than introducing infrastructure merely for architectural appearance.
