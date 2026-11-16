# Requirements

**Status:** Accepted  
**Project:** TicketForge  
**Scope:** Initial release

## Purpose

TicketForge is a REST-based ticket booking engine built with Go.

Its primary engineering challenge is allocating limited ticket inventory correctly when many customers attempt to reserve tickets concurrently.

It also manages events at shared physical venues, preventing conflicting event schedules and ensuring that allocated ticket capacity never exceeds the venue's physical capacity.

The initial release supports general-admission tickets. Assigned seating will be introduced as a later enhancement.

## Project Goals

- Provide event and ticket-category management for organizers.
- Maintain a platform-managed venue catalog.
- Allow customers to discover events and reserve tickets.
- Support temporary reservations followed by simulated payment and booking confirmation.
- Prevent overselling under concurrent requests.
- Prevent overlapping active events at the same venue.
- Ensure event ticket allocation does not exceed venue capacity.
- Make client retries safe through idempotency.
- Support reliable asynchronous processing.
- Recover from worker and infrastructure failures.
- Support event cancellation and simulated refunds.
- Measure system behavior through testing, monitoring, and profiling.

## Scope

### Included

- User registration and authentication.
- Customer and organizer capabilities.
- Immediate organizer activation.
- Platform-managed venue catalog.
- Event management and venue scheduling.
- Event rescheduling subject to reservation-history restrictions.
- Separate event lifecycle and booking availability states.
- General-admission ticket categories.
- Ticket inventory management.
- Event discovery.
- Temporary ticket reservations.
- Two-minute reservation duration.
- Simulated payment.
- Payment retries while reservations remain eligible.
- Booking confirmation.
- Event cancellation and simulated refunds.
- PostgreSQL transactions and concurrency control.
- Idempotent REST APIs.
- Redis for selected caching and rate-limiting use cases.
- Message broker and background workers.
- Transactional outbox.
- Retry and dead-letter handling.
- Structured logging, metrics, and tracing.
- Load and failure testing.
- Dockerized local infrastructure.
- Multiple API instances behind a reverse proxy/load balancer.

### Excluded from the Initial Release

- Assigned seating and seat maps.
- Real payment gateway integration.
- Real financial refunds.
- Frontend web or mobile applications.
- Microservices.
- Multi-region deployment.
- Kubernetes.
- AI/ML features.
- Application-level Admin role.
- Organizer verification and approval workflow.
- Organizer-created venue records through the application.
- Individual cancellation of confirmed bookings.
- Automatic scheduled early-bird pricing.

## Actors and Business Roles

### Customer

A registered user who can:

- Browse active events.
- View event details and ticket availability.
- Reserve tickets.
- Attempt payment for eligible reservations.
- Retry failed payments while the reservation remains eligible.
- Cancel eligible Held reservations.
- View their own reservations.
- View their own booking history.

Every registered user can act as a customer.

Individual confirmed-booking cancellation is not supported in the initial release.

### Organizer

A registered user with an organizer profile.

Organizer activation is immediate.

An organizer can:

- Create and manage events.
- Select venues from the platform-managed catalog.
- Define ticket categories.
- Configure ticket prices and capacities.
- Change ticket prices.
- Pause or resume booking availability.
- Reschedule eligible events.
- Cancel their own events.
- View relevant booking information for their events.

An organizer can also act as a customer.

Each event has exactly one organizer.

Organizers cannot create or modify venue catalog records through the initial application.

### Background Worker

A non-human system process responsible for:

- Expiring reservations.
- Publishing asynchronous events.
- Processing refunds.
- Retrying failed jobs.
- Performing background operations.

It is not an application user or business role.

### Platform Operator

Responsible for deployment and infrastructure operations.

This responsibility does not require an application-level Admin role. Operational access is handled through controlled infrastructure and deployment tooling.

Platform-operated processes also maintain the initial venue catalog.

## Functional Requirements

### FR-01: Identity and Access Management

**Status:** Partially Implemented

- Users can register using an email-first challenge flow. *(Implemented)*
- Password hashing using Argon2id with memory-hard parameters. *(Implemented)*
- Users can authenticate with email and password to receive a stateful session cookie. *(Implemented)*
- Stateful sessions in PostgreSQL with sliding 30-minute idle expiration and 7-day absolute lifetime. *(Implemented)*
- Session authentication middleware attaches user identity to request context. *(Implemented)*
- Users can log out, revoking their session and clearing their cookie. *(Implemented)*
- Every registered user can perform customer operations. *(Implemented in domain model)*
- Users can obtain an organizer profile. *(Schema-only: table exists, endpoint pending)*
- Organizer activation is immediate in the initial version. *(Pending)*
- Organizer-only operations require an organizer profile. *(Pending)*
- Organizers can manage only their own events. *(Pending)*
- Customers can access only their own private reservations and bookings. *(Pending)*

### FR-02: Venue Catalog

**Status:** Schema-Only

- The platform maintains a catalog of venues. *(Schema and sqlc queries exist)*
- Venue records contain:
  - Name.
  - Slug (unique URL identifier).
  - Structured address fields, including street address, city, state, postal code, and country code.
  - IANA timezone.
  - Physical capacity.
  - Active/inactive state (`is_active`).
- Country codes use ISO 3166-1 alpha-2 (`'IN'`).
- Organizers can browse and select existing active venues. *(Pending)*
- Organizers cannot create or modify venue records through the initial application.
- Venue catalog entries are maintained through controlled operational processes.
- A venue can host multiple events, provided their scheduled intervals do not overlap.
- Only active venues can be selected when creating or rescheduling events.
- Deactivating a venue does not automatically cancel its existing events.
- Existing active events continue to occupy the venue's schedule even if the venue is deactivated.
- Referenced venues cannot be physically deleted.
- The combined capacity of an event's ticket categories must not exceed the venue's physical capacity.
- A venue capacity reduction must be rejected if it would cause any active event's allocated ticket capacity to exceed the proposed venue capacity.
- Increasing venue capacity does not automatically increase existing event ticket-category capacities.

### FR-03: Event Management and Scheduling

**Status:** Schema-Only (Table with exclusion constraint and sqlc queries exist; API routes pending)

- Organizers can create events at existing active venues.
- Each event belongs to exactly one organizer.
- Events contain title, description, venue, schedule, and booking-window information.
- Events are created directly with the `active` lifecycle status.
- The initial release does not have persisted Draft, Published, or Completed event states.
- An active event can exist without ticket categories and does not necessarily have to be bookable.
- Organizers can update their own events, subject to the applicable business rules.
- Customers can discover active events.
- Cancelling an event prevents further reservations and confirmations.

#### Event lifecycle

The persisted event lifecycle has two states:

- `active`
- `cancelled`

Cancellation is terminal. A cancelled event cannot be reactivated.

Event completion is derived from time: an active event is considered completed when the current time reaches or passes `ends_at`. Completion is not a persisted event status.

#### Event scheduling constraints

- Event start must be at least 15 minutes in the future.
- Event start must be no more than one calendar year in the future.
- Minimum event duration is 15 minutes.
- Maximum event duration is 7 days.
- The same scheduling boundaries apply when rescheduling.
- One calendar year is calculated using calendar arithmetic, not a fixed 365-day duration.
- Time-dependent scheduling rules are validated by the application at write time.

The scheduled interval is half-open:

`[starts_at, ends_at)`

The end is exclusive. Two events at the same venue may be scheduled consecutively without a gap.

Only active events occupy a venue's schedule.

Non-cancelled events at the same venue must not have overlapping scheduled intervals. This guarantee must be enforced with database-level concurrency control rather than relying solely on an application availability check.

#### Event rescheduling

- Organizers can reschedule their own events only if the event has never had any reservation or booking associated with it.
- Any reservation record blocks rescheduling, regardless of whether it is currently Held, expired, or cancelled.
- Any confirmed booking also blocks rescheduling, including bookings that have subsequently been refunded.
- Reservation records must be retained as historical records and must not be physically deleted when they expire or are cancelled.
- The destination venue must be active.
- The new schedule must satisfy all event scheduling constraints.
- The new schedule must not conflict with another active event at the destination venue.
- Rescheduling must be transactional and coordinated with reservation creation to prevent a reservation from being created concurrently with a reschedule.
- Cancelled events cannot be rescheduled. An organizer who wants to run the event again must create a new event.

### FR-04: Ticket Category Management

**Status:** Schema-Only

- Organizers can create ticket categories for an event.
- An event can have zero or more ticket categories.
- Each category has a name, current price, currency, and allocated capacity.
- Categories can be activated or deactivated according to event lifecycle and booking rules.
- The system tracks inventory independently for each category.
- Organizers can change ticket category prices, including after sales begin.
- A price change applies only to reservations created after the change.
- Existing reservations and bookings retain their historical price information.
- The sum of an event's ticket-category capacities must not exceed its venue's physical capacity.
- A category's allocated capacity cannot be reduced below its committed inventory: reserved quantity plus sold quantity.
- Capacity changes must be coordinated transactionally with reservation and booking operations.

### FR-05: Booking Availability Controls

**Status:** Schema-Only

Booking availability is separate from the event lifecycle.

The persisted booking availability state has two values:

- `open`
- `paused`

The default state for a new event is `open`.

Organizers can pause or resume booking availability for their own active events.

When booking availability is paused:

- New reservations are rejected.
- Existing Held reservations remain eligible for payment until their original expiry, provided all other confirmation conditions are satisfied.
- Pausing does not extend reservation expiry.
- Reservation expiration continues normally.

Effective booking availability is derived from:

1. Event cancellation.
2. Booking availability state.
3. Sales start time.
4. Sales end time.
5. Remaining inventory.

An event being `open` does not guarantee that reservations can be created. For example, sales may not have started, may have ended, or inventory may be exhausted.

Event cancellation overrides booking availability and prevents new reservations and confirmations.

### FR-06: Event Discovery

**Status:** Pending

- Customers can browse active events.
- Customers can view event details.
- Customers can view ticket categories and their availability.
- Customers can identify sold-out, paused, not-yet-open, and ended booking availability.
- Customers must not be able to reserve tickets for cancelled events.

### FR-07: Reservation Management

**Status:** Schema-Only

- Customers can reserve a positive quantity of tickets.
- Reservations temporarily hold inventory.
- The initial reservation duration is two minutes.
- Each reservation has an expiration timestamp determined by server-side policy.
- Customers can view their reservations.
- Customers can cancel eligible Held reservations.
- Payment retries do not extend reservation expiry.
- Expired reservations cannot be confirmed.
- New reservations are rejected when the event is cancelled or booking availability does not permit new reservations.
- New reservations are rejected when the requested inventory is unavailable.
- Reservation creation must be safe under concurrent requests.
- Reservation records are retained after expiration or cancellation for auditing and historical business-rule enforcement.

### FR-08: Payment and Booking

**Status:** Schema-Only

- Customers can attempt to confirm eligible reservations through simulated payment.
- Simulated payment can produce success or failure.
- Failed payment attempts are retained.
- A reservation can have multiple payment attempts.
- Failed payment leaves an otherwise eligible reservation Held.
- Customers can retry payment until the reservation expires or otherwise becomes ineligible.
- Successful payment results in a confirmed booking.
- Failed payment must not create a confirmed booking.
- A reservation can produce at most one booking.
- Confirmation must be coordinated with reservation expiry, event cancellation, and reservation state transitions.
- Customers can view their booking history.
- Individual confirmed bookings cannot be cancelled in the initial release.

### FR-09: Event Cancellation and Refunds

**Status:** Schema-Only

- Organizers can cancel their own active events.
- Event cancellation is terminal.
- Cancelling an event prevents new reservations and confirmations.
- Existing Held reservations are invalidated as part of the cancellation transaction.
- Reserved inventory for invalidated Held reservations is released exactly once.
- Held reservation invalidation must be coordinated with reservation expiration and payment confirmation to prevent duplicate inventory release or late confirmation.
- The system initiates a full refund for each confirmed booking associated with the cancelled event.
- Refund processing is simulated in the initial version.
- Refund records and outcomes are persisted independently of booking records.
- Failed refunds can be retried safely.
- Repeated event cancellation requests must not create duplicate refunds.
- Refund retries operate on the existing refund record.
- Event cancellation remains effective even if refund processing fails temporarily.
- Refund processing is asynchronous and must not require the cancellation transaction to wait for the refund operation to finish.
- Refunding a booking does not restore sold inventory.
- A booking remains a historical record after it has been refunded.

### FR-10: Inventory Correctness

**Status:** Schema-Only

- The system must prevent overselling.
- Concurrent reservation attempts must be handled safely.
- Inventory must be released after reservation expiration.
- Inventory must be released after eligible Held reservation cancellation.
- Event cancellation must invalidate outstanding Held reservations and release their reserved inventory.
- Confirmation moves inventory from reserved to sold.
- Inventory must not be released more than once for the same reservation transition.
- Reserved quantity plus sold quantity must never exceed ticket-category capacity.
- The combined allocated capacity of all categories must never exceed the venue's physical capacity.
- Refund processing must not modify inventory.

### FR-11: Idempotency

**Status:** Schema-Only

- Reservation and confirmation endpoints support idempotency keys.
- Repeating an operation with the same key must not create duplicate logical operations.
- Reusing a key with a different request payload must be rejected.
- Event cancellation and refund initiation must also be safe against duplicate processing.
- Refund retries must not create duplicate refund records or duplicate refund operations.

### FR-12: Asynchronous Processing

**Status:** Partially Implemented

- Transactional outbox persistence and worker claiming with leases (`claim_token`, `claim_expires_at`). *(Implemented)*
- Exponential backoff retry with full jitter and terminal failure marking. *(Implemented)*
- Transactional email dispatch via Mailpit/SMTP. *(Implemented)*
- Background workers process reservation expiration. *(Pending)*
- Business events are recorded for reliable publication. *(Outbox implemented; message broker transport pending)*
- Event cancellation durably records the work required for asynchronous refund processing. *(Pending)*
- Event cancellation and Held reservation invalidation must be committed consistently. *(Pending)*
- Failed asynchronous operations can be retried. *(Implemented for outbox worker)*
- Repeatedly failing messages can be isolated. *(Implemented via `status = 'failed'` dead-letter)*
- Consumers safely handle duplicate message delivery. *(Pending)*
- Worker restarts must not cause duplicate inventory release, booking confirmation, or refunds. *(Pending)*

### FR-13: Operations and Observability

**Status:** Partially Implemented

- The API exposes health endpoints (`GET /healthz`). *(Implemented)*
- The system provides structured JSON logs via `log/slog`. *(Implemented)*
- Requests can be correlated across API and worker processes. *(Pending)*
- The system exposes relevant metrics. *(Prometheus/Grafana pending)*
- Operators can inspect failed asynchronous work through controlled operational mechanisms. *(Pending)*

## Non-Functional Requirements

These are initial engineering targets. They will be refined using measurements.

| ID | Attribute | Requirement |
| --- | --- | --- |
| NFR-01 | Correctness | Never oversell inventory |
| NFR-02 | Scheduling | Never allow overlapping active events at one venue |
| NFR-03 | Capacity | Never allocate more tickets than the venue's physical capacity |
| NFR-04 | Consistency | PostgreSQL is the source of truth |
| NFR-05 | Idempotency | Repeated requests produce one logical operation |
| NFR-06 | Performance | Initial target: p95 below 500 ms under a defined baseline load |
| NFR-07 | Concurrency | Demonstrate correctness under hundreds of concurrent attempts |
| NFR-08 | Reliability | Recover safely from worker restarts |
| NFR-09 | Scalability | Support multiple API instances |
| NFR-10 | Security | Authentication, authorization, validation, secure password storage |
| NFR-11 | Maintainability | Clear separation of business and infrastructure concerns |
| NFR-12 | Observability | Logs, metrics, request IDs, and traces where appropriate |
| NFR-13 | Portability | Reproducible local environment using Docker Compose |
| NFR-14 | Testability | Unit, integration, API, concurrency, failure, and load tests |

The latency target excludes simulated payment delays and must always be reported with the test environment and load profile.

## Business Invariants

These are fundamental correctness guarantees.

1. Available inventory must never become negative.
2. Reserved quantity plus sold quantity must never exceed category capacity.
3. The sum of ticket-category capacities for an event must never exceed the venue's physical capacity.
4. A reservation cannot be confirmed after it expires.
5. Inventory must be released exactly once when a Held reservation expires or is cancelled.
6. A confirmed booking must not be duplicated by retries.
7. Failed simulated payments must not create confirmed bookings.
8. Customers cannot access another customer's private booking data.
9. Organizers cannot manage another organizer's events.
10. An event must reference an existing platform-managed venue.
11. Only active venues can be selected for new events or event rescheduling.
12. Active events at the same venue must not have overlapping time intervals.
13. A cancelled event cannot accept new reservations or confirmations.
14. Event cancellation invalidates outstanding Held reservations and releases their inventory exactly once.
15. Existing reservation and booking prices must not change when an organizer edits current ticket prices.
16. Event cancellation must initiate full refunds for confirmed bookings.
17. A failed refund must not reactivate a cancelled event.
18. Refund processing must not automatically restore sold inventory.
19. Business changes requiring asynchronous publication must remain publishable after process failure.
20. Redis and broker failures must not bypass PostgreSQL inventory or venue scheduling guarantees.
21. An event cannot be rescheduled if any reservation or booking record has ever existed for it.
22. Reservation and booking records must be retained to preserve historical evidence and support auditing.
23. Pausing booking availability prevents new reservations but does not invalidate otherwise eligible Held reservations.
24. Payment retries must not extend reservation expiry.

## Initial Assumptions

- Every user can act as a customer.
- A user may additionally have an organizer profile.
- Organizer activation is immediate.
- Each event has exactly one organizer.
- Venues are platform-managed catalog entries.
- Organizers select existing active venues.
- A venue can host multiple events at different, non-overlapping times.
- Only active events occupy venue schedules.
- Event creation immediately produces an active event.
- Event completion is derived from time rather than persisted as a status.
- Event cancellation is terminal.
- Booking availability is independent of event lifecycle.
- An active event may exist without ticket categories.
- General admission is the only supported ticket allocation model initially.
- Currency is INR only in the initial release.
- Monetary values are stored as integer minor units.
- Reservation duration is two minutes.
- Failed payment attempts leave eligible reservations Held.
- Payment retries do not extend reservation expiry.
- Historical reservation and booking prices must not change when organizers edit current ticket prices.
- Reservation records are retained after expiration or cancellation.
- An event can be rescheduled only if it has never had a reservation or booking.
- Individual confirmed-booking cancellation is unsupported.
- Event cancellation initiates simulated full refunds for confirmed bookings.
- Refunds do not restore sold inventory.
- Event times are stored as timezone-aware instants.
- Venue timezones use IANA timezone identifiers.

## Phase 0 Acceptance Criteria

- Actors and permissions are documented.
- Core domain concepts are identified.
- Venue ownership, lifecycle, capacity, and scheduling rules are documented.
- Event lifecycle and booking availability are documented separately.
- Event creation, rescheduling, and cancellation rules are documented.
- High-level architecture is documented.
- Initial relational model is reviewed.
- Reservation, booking, payment, and refund lifecycles are defined.
- Critical workflows are documented.
- Concurrency and failure scenarios are identified.
- Architectural decisions are recorded.

## Assigned Seating Extension

Assigned seating is intentionally excluded.

A later design may introduce:

- Seat maps.
- Seats.
- Seat-level allocation.
- Seat-level reservation holds.

We should not add these requirements or tables before their requirements are established.
