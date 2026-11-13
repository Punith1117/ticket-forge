# Initial Entity-Relationship Model

**Status:** Accepted  
**Scope:** General-admission ticketing

## 1. ER Diagram

```mermaid
erDiagram
    users {
        uuid id PK
        text email UK
        text password_hash
        text display_name
        timestamptz created_at
        timestamptz updated_at
    }

    organizer_profiles {
        uuid id PK
        uuid user_id FK,UK
        text display_name
        text status
        timestamptz created_at
    }

    venues {
        uuid id PK
        text name
        text address
        text city
        text timezone
        integer capacity
        timestamptz created_at
        timestamptz updated_at
    }

    events {
        uuid id PK
        uuid organizer_profile_id FK
        uuid venue_id FK
        text title
        text description
        text status
        timestamptz starts_at
        timestamptz ends_at
        timestamptz sales_start_at
        timestamptz sales_end_at
        timestamptz created_at
        timestamptz updated_at
    }

    ticket_categories {
        uuid id PK
        uuid event_id FK
        text name
        bigint price_minor
        char currency
        integer capacity
        boolean active
        timestamptz created_at
        timestamptz updated_at
    }

    inventory {
        uuid ticket_category_id PK,FK
        integer reserved_quantity
        integer sold_quantity
        timestamptz updated_at
    }

    reservations {
        uuid id PK
        uuid customer_id FK
        uuid ticket_category_id FK
        integer quantity
        bigint unit_price_minor
        bigint total_price_minor
        char currency
        text status
        text cancellation_reason
        timestamptz expires_at
        timestamptz created_at
        timestamptz updated_at
    }

    payment_attempts {
        uuid id PK
        uuid reservation_id FK
        bigint amount_minor
        char currency
        text status
        text provider_reference
        text failure_reason
        timestamptz attempted_at
        timestamptz completed_at
    }

    bookings {
        uuid id PK
        uuid reservation_id FK,UK
        uuid customer_id FK
        integer quantity
        bigint unit_price_minor
        bigint total_price_minor
        char currency
        text status
        timestamptz confirmed_at
        timestamptz created_at
    }

    refunds {
        uuid id PK
        uuid booking_id FK
        bigint amount_minor
        char currency
        text status
        text failure_reason
        timestamptz created_at
        timestamptz completed_at
    }

    idempotency_records {
        uuid id PK
        uuid user_id FK
        text operation
        text idempotency_key
        text request_hash
        integer response_status
        jsonb response_body
        timestamptz created_at
        timestamptz expires_at
    }

    outbox_events {
        uuid id PK
        text aggregate_type
        uuid aggregate_id
        text event_type
        jsonb payload
        timestamptz occurred_at
        timestamptz published_at
        integer attempts
    }

    users ||--o| organizer_profiles : may_have
    organizer_profiles ||--o{ events : organizes
    venues ||--o{ events : hosts
    events ||--|{ ticket_categories : offers
    ticket_categories ||--|| inventory : tracks
    users ||--o{ reservations : places
    ticket_categories ||--o{ reservations : allocates
    reservations ||--o{ payment_attempts : has
    reservations ||--o| bookings : confirms_into
    users ||--o{ bookings : owns
    bookings ||--o{ refunds : has
    users ||--o{ idempotency_records : creates
```

## 2. Modeling Notes

### Users and Organizer Profiles

A user can have zero or one organizer profile.

Every registered user can act as a customer.

We do not need separate customer and organizer account tables.

Organizer activation is immediate in the initial version. A user can act as an organizer as soon as their organizer profile is created.

A user may act as both a customer and an organizer using the same account.

### Venues

Venues are platform-managed catalog entries.

They are not owned by organizers.

Organizers select an existing venue when creating an event. The event references the venue through `venue_id`.

The initial catalog can be populated using seed data and maintained through controlled operational scripts.

A venue stores its physical capacity. This represents the maximum number of people the physical location can accommodate.

For every event:

- The sum of its ticket-category capacities must not exceed the venue's physical capacity.
- Each ticket category has its own capacity.
- The combined capacity of all ticket categories represents the event's maximum ticket allocation.

For example, if a venue has a physical capacity of 1,000, an event may have:

| Ticket category | Capacity |
| --- | ---: |
| General | 500 |
| Premium | 300 |
| VIP | 100 |
| **Total** | **900** |

The configuration is valid because 900 does not exceed the venue's physical capacity of 1,000.

This is an aggregate invariant across multiple ticket-category rows. A simple SQL `CHECK` constraint cannot enforce it. The implementation must enforce it transactionally, with an appropriate database-level concurrency strategy.

### Events and Scheduling

Each event references exactly one organizer profile and one venue.

An event must reference an existing platform-managed venue.

The event's scheduled interval is:

`[starts_at, ends_at)`

The end is exclusive. Therefore, an event ending at 15:00 and another starting at 15:00 do not overlap.

Only non-cancelled events participate in venue conflict checks.

Two events at the same venue must not have overlapping scheduled intervals.

The application must not rely solely on a separate availability query followed by an insert. Concurrent event creation must be protected by database-level concurrency control.

PostgreSQL range types and exclusion constraints are a possible implementation. The exact schema and constraint will be finalized during SQL design.

A cancelled event no longer occupies its venue schedule.

### Ticket Categories

Each ticket category belongs to exactly one event.

A category has a current price, currency, and capacity.

Organizers may change the current price, including after sales begin.

A price change applies only to reservations created after the change.

Existing reservations retain their original price snapshots. Existing bookings retain their historical price information.

The sum of an event's ticket-category capacities must not exceed the physical capacity of its venue.

### Inventory

The available quantity is derived:

`available = capacity - reserved_quantity - sold_quantity`

We should avoid storing redundant availability values unless later measurements justify it.

Database constraints must prevent:

- Negative reserved quantity.
- Negative sold quantity.
- Reserved plus sold quantity exceeding category capacity.

Additionally, the sum of ticket-category capacities for an event must not exceed its venue's physical capacity.

Inventory changes must be atomic with their corresponding reservation or booking state transitions.

Confirmation moves quantity from reserved to sold without changing the total allocated quantity.

Expiration and cancellation release reserved inventory exactly once.

Event cancellation must invalidate existing Held reservations and release their reserved inventory exactly once. This must be coordinated with the reservation expiration worker so that concurrent processing cannot release the same quantity twice.

Refund processing must not modify inventory.

### Reservations

A reservation belongs to exactly one customer and one ticket category.

A reservation stores a snapshot of the ticket price at creation time.

It must not depend on the current ticket-category price when calculating an existing reservation's payment amount.

The initial reservation duration is two minutes.

The expiration timestamp is determined by server-side policy. Payment retries do not extend it.

A failed payment attempt leaves an otherwise eligible reservation Held.

A reservation cannot be confirmed after it expires.

A reservation can produce at most one confirmed booking. A unique constraint on `bookings.reservation_id` enforces this relationship.

When an event is cancelled:

- New reservations for the event are rejected.
- Existing Held reservations are invalidated.
- Existing Held reservations cannot be confirmed.
- Their reserved inventory is released exactly once.
- The invalidation and release must be safe when racing with the expiration worker.

The reservation state transition and inventory release must be protected by transactional state checks.

A `cancellation_reason` can distinguish customer cancellation from event cancellation. The exact reservation state names and transition rules are defined in `state-machines.md`.

An index should support finding expired reservations efficiently, for example:

`(status, expires_at)`

### Payment Attempts

A reservation may have multiple payment attempts.

Each attempt stores its amount and currency so that the historical payment operation is independent of later category price changes.

Failed attempts are retained rather than overwritten.

A failed payment attempt must not create a confirmed booking.

A successful payment can create at most one booking through the reservation's unique booking relationship.

### Bookings

A booking originates from a reservation.

A unique constraint on `reservation_id` prevents multiple bookings from being created from one reservation.

The booking stores historical price information.

Individual confirmed-booking cancellation is not supported in the initial version.

Event cancellation initiates refunds for confirmed bookings.

A booking remains a historical record of the confirmed allocation even when a refund is processed. Refund status is modeled independently and does not require changing the booking back to an unconfirmed state.

### Refunds

Refunds are associated with confirmed bookings.

The initial implementation simulates refund processing.

Refund records preserve the amount, currency, status, failure reason, and relevant timestamps.

A failed refund must remain visible and safely retryable.

Event cancellation initiates a refund for each confirmed booking associated with the event.

Refund processing is separate from inventory accounting:

- A refund does not transition a Confirmed reservation back to Held or another inventory state.
- A refund does not automatically restore sold inventory.
- A failed refund does not reactivate a cancelled event.
- Retrying a refund must not create a duplicate refund operation.

The initial policy is a full refund for event cancellation. The implementation must prevent duplicate full refunds for the same booking and cancellation operation.

The exact refund uniqueness constraint and retry strategy will be finalized during SQL and application design.

### Idempotency Records

A unique constraint should enforce the agreed idempotency scope:

`(user_id, operation, idempotency_key)`

The request hash prevents the same key from being reused with a different payload.

Retention duration and handling of stored failure responses will be finalized before implementation.

### Outbox Events

Outbox records are persisted in the same transaction as the business change.

An index should support efficient retrieval of unpublished events.

The outbox is important for event cancellation and refund processing. The event cancellation transaction must durably record the work that needs to happen after the transaction commits.

For example, the application may publish events representing:

- Event cancellation.
- Held reservation invalidation.
- Refund requests for confirmed bookings.

The exact event types and payloads will be finalized during the asynchronous processing design.

The table may later need fields such as:

- `next_attempt_at`
- `locked_at`
- `last_error`
- `aggregate_version`

These fields can be added when the publisher and retry design are finalized.

### Money

Store money using integer minor units rather than floating-point numbers.

For example, INR 100.50 is stored as 10050 paise.

The initial version supports INR only.

Store the currency code explicitly with monetary values.

Use a consistent currency representation, such as `INR`, in the application and database. The SQL column can use `char(3)` or an appropriate constrained text type.

### Time

Use PostgreSQL `timestamptz` for actual instants.

Store the venue's IANA timezone separately for event presentation and local scheduling rules.

Reservation expiry should be evaluated using authoritative server/database time.

## 3. Important Database Invariants

The following rules must be enforced by the database, transactional application logic, or both:

1. Every event references an existing venue.
2. Non-cancelled events at the same venue cannot have overlapping time intervals.
3. The sum of ticket-category capacities for an event cannot exceed the venue's physical capacity.
4. Available inventory must never become negative.
5. Reserved quantity plus sold quantity must never exceed category capacity.
6. A reservation cannot be confirmed after expiry.
7. A reservation can produce at most one confirmed booking.
8. Inventory must be released exactly once when a Held reservation expires or is cancelled.
9. Event cancellation prevents new reservations and confirmations.
10. Event cancellation invalidates existing Held reservations and releases their inventory exactly once.
11. Event cancellation initiates refunds for confirmed bookings.
12. Refund processing is separate from inventory accounting.
13. A refund does not automatically restore sold inventory.
14. Refund retries must not create duplicate refunds for the same cancellation operation.

## 4. Questions Before SQL Schema Approval

1. Are ticket quantities limited per customer?
2. How long should idempotency records be retained?
3. Should failed idempotency responses be stored?
4. What is the retention policy for expired reservations?
5. What is the retention policy for published outbox events?
6. Should a buffer be required between events at the same venue?
7. Should venue capacity reductions be allowed after events have been created?
8. Should ticket-category capacity reductions be allowed after reservations or sales have begun?
9. What exact uniqueness and retry policy should apply to refunds?
10. Should event cancellation process Held reservation invalidation synchronously or through a background worker?

## 5. Assigned Seating Extension

Assigned seating is intentionally excluded.

A later design may introduce:

- Seat maps.
- Seats.
- Seat-level allocation.
- Seat-level reservation holds.

We should not add these tables before their requirements are established.
