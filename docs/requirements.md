# Requirements

**Status:** Accepted  
**Project:** TicketForge  
**Scope:** Initial release

## 1. Purpose

TicketForge is a REST-based ticket booking engine built with Go.

Its primary engineering challenge is allocating limited ticket inventory correctly when many customers attempt to reserve tickets concurrently.

It also manages events at shared physical venues, preventing conflicting event schedules.

The initial release supports general-admission tickets. Assigned seating will be introduced as a later enhancement.

## 2. Project Goals

- Provide event and ticket-category management for organizers.
- Maintain a platform-managed venue catalog.
- Allow customers to discover events and reserve tickets.
- Support temporary reservations followed by simulated payment and booking confirmation.
- Prevent overselling under concurrent requests.
- Prevent overlapping events at the same venue.
- Make client retries safe through idempotency.
- Support reliable asynchronous processing.
- Recover from worker and infrastructure failures.
- Support event cancellation and simulated refunds.
- Measure system behavior through testing, monitoring, and profiling.

## 3. Scope

### Included

- User registration and authentication.
- Customer and organizer capabilities.
- Immediate organizer activation.
- Platform-managed venue catalog.
- Event management and venue scheduling.
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

## 4. Actors and Business Roles

### Customer

A registered user who can:

- Browse published events.
- View ticket availability.
- Reserve tickets.
- Attempt payment for eligible reservations.
- Retry failed payments while the reservation remains eligible.
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
- Configure prices and capacities.
- Change ticket prices.
- Publish and cancel events.
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

## 5. Functional Requirements

### FR-01: Identity and Access Management

- Users can register and authenticate.
- Every registered user can perform customer operations.
- Users can obtain an organizer profile.
- Organizer activation is immediate in the initial version.
- Organizer-only operations require an organizer profile.
- Organizers can manage only their own events.
- Customers can access only their own private reservations and bookings.

### FR-02: Venue Catalog

- The platform maintains a catalog of venues.
- Venue records contain relevant location, timezone, and capacity information.
- Organizers can browse and select existing venues.
- Organizers cannot create or modify venue records through the initial application.
- Venue catalog entries are maintained through controlled operational processes.
- A venue can host multiple events, provided their scheduled intervals do not overlap.

### FR-03: Event Management

- Organizers can create events at existing venues.
- Each event belongs to exactly one organizer.
- Events contain title, description, venue, schedule, and booking-window information.
- Events must have valid start and end times.
- Non-cancelled events at the same venue must not have overlapping scheduled intervals.
- Organizers can publish, update, and cancel their events.
- Customers can discover published events.
- Cancelling an event prevents further reservations and confirmations.

### FR-04: Ticket Category Management

- Organizers can create ticket categories for an event.
- Each category has a name, price, currency, and capacity.
- Categories can be activated or deactivated according to event lifecycle rules.
- The system tracks inventory independently for each category.
- Organizers can change ticket category prices, including after sales begin.
- A price change applies only to reservations created after the change.
- Existing reservations and bookings retain their historical price information.

### FR-05: Event Discovery

- Customers can browse published events.
- Customers can view event details.
- Customers can view ticket categories and availability.
- Customers can identify sold-out or unavailable categories.

### FR-06: Reservation Management

- Customers can reserve a positive quantity of tickets.
- Reservations temporarily hold inventory.
- The initial reservation duration is two minutes.
- Each reservation has an expiration timestamp determined by server-side policy.
- Customers can view their reservations.
- Customers can cancel eligible Held reservations.
- Payment retries do not extend reservation expiry.
- Expired reservations cannot be confirmed.

### FR-07: Payment and Booking

- Customers can attempt to confirm eligible reservations through simulated payment.
- Simulated payment can produce success or failure.
- Failed payment attempts are retained.
- A reservation can have multiple payment attempts.
- Failed payment leaves an otherwise eligible reservation Held.
- Customers can retry payment until the reservation expires or otherwise becomes ineligible.
- Successful payment results in a confirmed booking.
- Failed payment must not create a confirmed booking.
- Customers can view their booking history.
- Individual confirmed bookings cannot be cancelled in the initial release.

### FR-08: Event Cancellation and Refunds

- Organizers can cancel their own events.
- Cancelling an event prevents new reservations and confirmations.
- The system initiates a refund for each confirmed booking associated with the cancelled event.
- Refund processing is simulated in the initial version.
- Refund outcomes are persisted.
- Failed refunds can be retried safely.
- Repeated event cancellation requests must not create duplicate refunds.
- Event cancellation remains effective even if refund processing fails temporarily.

### FR-09: Inventory Correctness

- The system must prevent overselling.
- Concurrent reservation attempts must be handled safely.
- Inventory must be released after reservation expiration.
- Inventory must be released after eligible Held reservation cancellation.
- Confirmation moves inventory from reserved to sold.
- Inventory must not be released more than once for the same reservation transition.

### FR-10: Idempotency

- Reservation and confirmation endpoints support idempotency keys.
- Repeating an operation with the same key must not create duplicate logical operations.
- Reusing a key with a different request payload must be rejected.
- Event cancellation and refund initiation must also be safe against duplicate processing.

### FR-11: Asynchronous Processing

- Background workers process reservation expiration.
- Business events are recorded for reliable publication.
- Event cancellation can trigger asynchronous refund processing.
- Failed asynchronous operations can be retried.
- Repeatedly failing messages can be isolated.
- Consumers safely handle duplicate message delivery.

### FR-12: Operations and Observability

- The API exposes health endpoints.
- The system provides structured logs.
- Requests can be correlated across API and worker processes.
- The system exposes relevant metrics.
- Operators can inspect failed asynchronous work through controlled operational mechanisms.

## 6. Non-Functional Requirements

These are initial engineering targets. They will be refined using measurements.

| ID | Attribute | Requirement |
| --- | --- | --- |
| NFR-01 | Correctness | Never oversell inventory |
| NFR-02 | Scheduling | Never allow overlapping non-cancelled events at one venue |
| NFR-03 | Consistency | PostgreSQL is the source of truth |
| NFR-04 | Idempotency | Repeated requests produce one logical operation |
| NFR-05 | Performance | Initial target: p95 below 500 ms under a defined baseline load |
| NFR-06 | Concurrency | Demonstrate correctness under hundreds of concurrent attempts |
| NFR-07 | Reliability | Recover safely from worker restarts |
| NFR-08 | Scalability | Support multiple API instances |
| NFR-09 | Security | Authentication, authorization, validation, secure password storage |
| NFR-10 | Maintainability | Clear separation of business and infrastructure concerns |
| NFR-11 | Observability | Logs, metrics, request IDs, and traces where appropriate |
| NFR-12 | Portability | Reproducible local environment using Docker Compose |
| NFR-13 | Testability | Unit, integration, API, concurrency, failure, and load tests |

The latency target excludes simulated payment delays and must always be reported with the test environment and load profile.

## 7. Business Invariants

These are fundamental correctness guarantees.

1. Available inventory must never become negative.
2. Reserved quantity plus sold quantity must never exceed category capacity.
3. A reservation cannot be confirmed after it expires.
4. Inventory must be released exactly once when a Held reservation expires or is cancelled.
5. A confirmed booking must not be duplicated by retries.
6. Failed simulated payments must not create confirmed bookings.
7. Customers cannot access another customer's private booking data.
8. Organizers cannot manage another organizer's events.
9. An event must reference an existing platform-managed venue.
10. Non-cancelled events at the same venue must not have overlapping time intervals.
11. A cancelled event cannot accept new reservations or confirmations.
12. Existing reservation and booking prices must not change when an organizer edits current ticket prices.
13. Event cancellation must initiate refunds for confirmed bookings.
14. A failed refund must not reactivate a cancelled event.
15. Business changes requiring event publication must remain publishable after process failure.
16. Redis and broker failures must not bypass PostgreSQL inventory or venue scheduling guarantees.
17. The sum of ticket-category capacities for an event must not exceed the venue's physical capacity.

## 8. Initial Assumptions

- Every user can act as a customer.
- A user may additionally have an organizer profile.
- Organizer activation is immediate.
- Each event has exactly one organizer.
- Venues are platform-managed catalog entries.
- Organizers select existing venues.
- A venue can host multiple events at different, non-overlapping times.
- General admission is the only supported ticket allocation model initially.
- Currency is INR only in the initial release.
- Monetary values are stored as integer minor units.
- Reservation duration is two minutes.
- Failed payment attempts leave eligible reservations Held.
- Payment retries do not extend reservation expiry.
- Historical reservation and booking prices must not change when organizers edit current ticket prices.
- Individual confirmed-booking cancellation is unsupported.
- Event cancellation initiates simulated refunds for confirmed bookings.
- Event times are stored as timezone-aware instants.

## 9. Phase 0 Acceptance Criteria

- Actors and permissions are documented.
- Core domain concepts are identified.
- Venue ownership and scheduling rules are documented.
- High-level architecture is documented.
- Initial relational model is reviewed.
- Reservation, booking, payment, and refund lifecycles are defined.
- Critical workflows are documented.
- Concurrency and failure scenarios are identified.
- Architectural decisions are recorded.
