# Domain Model

**Status:** Accepted

## 1. Domain Overview

TicketForge's central domain is allocating limited ticket inventory to customers while managing events at shared physical venues.

Three concepts must remain distinct:

- **Inventory:** Capacity and allocation quantities.
- **Reservation:** Temporary hold on inventory.
- **Booking:** Confirmed ticket allocation.

A reservation is not automatically a booking.

The system also manages venue scheduling. A physical venue can host multiple events, but their scheduled time intervals must not overlap.

## 2. Core Domain Concepts

### User

Represents an authenticated person.

Every user can act as a customer. A user may additionally have an OrganizerProfile.

A user does not need separate accounts to act as a customer and an organizer.

### OrganizerProfile

Grants organizer capabilities to a User.

Organizer activation is immediate in the initial version. Identity verification and organizer approval workflows are outside the MVP scope.

An organizer can create and manage their own events.

Each event belongs to exactly one organizer.

### Venue

Represents a physical location where events are hosted.

Venues are managed by the platform and maintained in a shared venue catalog.

Organizers cannot create or modify venue records through the initial application.

A venue contains:

- Name.
- Address.
- IANA timezone.
- Physical capacity.

A venue can host multiple events, provided their scheduled time intervals do not overlap.

The sum of ticket-category capacities for an event must not exceed the venue's physical capacity.

### Event

Represents a scheduled occurrence at a venue.

Each event belongs to exactly one organizer and references exactly one platform-managed venue.

It contains:

- Title and description.
- Organizer.
- Venue.
- Start and end times.
- Booking start and end times.
- Publication and lifecycle status.

An event cannot be scheduled if its time interval overlaps another non-cancelled event at the same venue.

Cancelling an event prevents further reservations and initiates refunds for its confirmed bookings.

### TicketCategory

Represents a general-admission ticket type for an event.

Examples:

- Standard.
- VIP.
- Early Bird.

Each category has:

- Name.
- Current price.
- Currency.
- Capacity.
- Active status.

An organizer may change a category's current price, including after ticket sales begin.

A price change applies only to reservations created after the change. Existing reservations retain their original price.

Automatic price tiers and scheduled early-bird transitions are deferred.

### Inventory

Tracks the allocation quantities of a ticket category.

Initial model:

- Capacity.
- Reserved quantity.
- Sold quantity.

Available quantity is derived:

available = capacity - reserved_quantity - sold_quantity

All inventory changes must be transactional.

Database constraints and concurrency control must prevent overselling.

### Reservation

Represents a temporary hold on a quantity of tickets.

It belongs to exactly one customer and one ticket category.

It has:

- Quantity.
- Status.
- Unit price snapshot.
- Currency.
- Total price snapshot.
- Creation timestamp.
- Expiration timestamp.

The initial reservation duration is two minutes.

The expiration time is determined by server-side policy. Payment retries do not extend the expiration time.

A failed simulated payment leaves the reservation Held, provided it has not expired or otherwise become ineligible.

### Booking

Represents a confirmed ticket allocation.

It originates from a successful reservation confirmation.

It stores the final quantity and price information required for historical records.

Confirmed bookings cannot be individually cancelled in the initial version.

When an organizer cancels an event, refunds are initiated for its confirmed bookings.

### PaymentAttempt

Represents an attempt to complete the simulated payment process.

It records:

- Reservation.
- Amount and currency.
- Outcome.
- Relevant timestamps.
- A reference identifying the simulated attempt.

A reservation may have multiple payment attempts.

A failed attempt does not automatically expire or cancel the reservation. The customer may retry while the reservation remains eligible.

Payment attempts do not represent real money movement.

### Refund

Represents a refund initiated because an event was cancelled.

It is associated with a confirmed booking.

It records:

- Booking.
- Amount and currency.
- Refund status.
- Creation and completion timestamps.
- Relevant failure information.

The initial implementation uses simulated refunds.

Refund failures must remain visible and retryable. A failed refund must not reactivate the cancelled event.

### IdempotencyRecord

Stores the result of a client operation.

It prevents retries from accidentally creating duplicate reservations or bookings.

It should be scoped by authenticated user, operation, and idempotency key.

A request fingerprint prevents reuse of the same key with a different payload.

### OutboxEvent

A durable event record written in the same database transaction as the business change that requires publication.

An asynchronous publisher later forwards it to the message broker.

The outbox is a database table, not a separate infrastructure service.

## 3. Domain Relationships

```mermaid
erDiagram
    USER ||--o| ORGANIZER_PROFILE : may_have
    ORGANIZER_PROFILE ||--o{ EVENT : organizes
    VENUE ||--o{ EVENT : hosts
    EVENT ||--|{ TICKET_CATEGORY : offers
    TICKET_CATEGORY ||--|| INVENTORY : tracks
    USER ||--o{ RESERVATION : places
    TICKET_CATEGORY ||--o{ RESERVATION : allocates
    RESERVATION ||--o{ PAYMENT_ATTEMPT : has
    RESERVATION ||--o| BOOKING : confirms_into
    USER ||--o{ BOOKING : owns
    BOOKING ||--o{ REFUND : has
    USER ||--o{ IDEMPOTENCY_RECORD : creates
```

## 4. Domain Rules

### Identity and Organizer Rules

1. Every registered user can act as a customer.
2. A user may have zero or one organizer profile.
3. Organizer activation is immediate in the initial version.
4. An organizer can manage only their own events.
5. Each event belongs to exactly one organizer.

### Venue and Event Rules

1. Venues are platform-managed catalog entries.
2. Organizers select existing venues when creating events.
3. An event must reference an existing venue.
4. An event must have a valid start and end time.
5. A venue cannot host overlapping non-cancelled events.
6. A venue may host multiple events on the same day if their intervals do not overlap.
7. A cancelled event no longer occupies its venue schedule.

### Ticket and Pricing Rules

 1. A published event must have at least one bookable ticket category.
 2. Ticket category capacity cannot be negative.
 3. Reservation quantity must be positive.
 4. Available inventory is derived from capacity, reserved quantity, and sold quantity.
 5. Changing a ticket category's current price affects only subsequently created reservations.
 6. Existing reservation and booking prices must not change when the current category price changes.

### Reservation and Booking Rules

 1. A reservation belongs to exactly one customer.
 2. A reservation has a server-determined expiration time.
 3. The initial reservation duration is two minutes.
 4. Payment retries do not extend reservation expiry.
 5. A failed payment attempt leaves an otherwise eligible reservation Held.
 6. A reservation can produce at most one confirmed booking.
 7. A reservation cannot be confirmed after it expires.
 8. Confirmed bookings cannot be individually cancelled in the initial version.
 9. Event cancellation initiates refunds for confirmed bookings.

### Inventory Rules

 1. Inventory modifications must enforce the inventory invariants.
 2. Inventory must be released exactly once when a Held reservation expires or is cancelled.
 3. Confirmation moves quantity from reserved to sold without changing total allocated quantity.
 4. Event cancellation must not result in duplicate inventory releases or duplicate refunds.

### Payment and Refund Rules

 1. Failed payment attempts must be retained.
 2. A reservation may have multiple payment attempts.
 3. A successful payment can create at most one booking.
 4. Event cancellation initiates a refund for each confirmed booking.
 5. Refund failures must be recorded and safely retryable.

## 5. Suggested Logical Modules

These are conceptual boundaries, not a requirement to create one package for every entity.

- **Identity and Access:** Users, authentication, organizer profiles.
- **Catalog:** Platform-managed venues, events, ticket categories.
- **Inventory:** Capacity, allocation, release.
- **Booking:** Reservations, confirmations, booking records.
- **Payment Simulation:** Simulated payment attempts.
- **Refund Processing:** Simulated refunds and refund outcomes.
- **Async Processing:** Outbox, publisher, consumers, expiration and refund workers.

## 6. Deferred Decisions

The following decisions are intentionally not finalized:

1. Are ticket quantities limited per customer?
2. Can ticket categories share a common venue capacity constraint?
3. How long should idempotency records be retained?
4. What is the retention policy for expired reservations?
5. What is the retention policy for published outbox events?
6. What buffer, if any, should be required between events at the same venue?
7. Should organizers be able to request new venues through a future workflow?
8. Should organizer verification be introduced in a future version?
9. Should automatic early-bird and scheduled price tiers be supported?
10. What operational workflow should be used to inspect and retry failed refunds?
