# ADR-017: Venue Capacity and Inventory Allocation

- **Status:** Accepted
- **Date:** 2026-11-14

## Context

TicketForge initially supports general-admission ticket categories.

Each category has an allocated capacity, and an event may have multiple categories with different prices.

Events are organized using venue bookings. A venue booking reserves a physical venue and its scheduled time interval, while an event represents an activity organized using that booking.

The system must ensure that the total number of tickets allocated across all categories of an event never exceeds the physical capacity of the venue associated with its venue booking.

It must also protect existing reservations and confirmed bookings when organizers or platform operators change capacity.

## Decision

### Venue capacity is the upper bound

Each venue has a physical capacity.

For every active event associated with an active venue booking at a venue:

`SUM(ticket_category.capacity) <= venue.capacity`

The sum of allocated capacity across all ticket categories is the event's total ticket inventory.

There is no additional global ticket inventory limit beyond the venue capacity.

A venue booking can exist without an associated event. In that case, no ticket inventory is allocated.

A venue booking can have multiple associated events over its lifetime, but at most one associated event may have lifecycle status `active` at any given time.

Capacity is evaluated against the active event associated with the venue booking, not against the number of historical events associated with it.

### Category capacity

Each ticket category has an allocated capacity.

Category capacity represents the maximum inventory allocated to that category, not the number of tickets already sold.

The sum of category capacities must never exceed the physical capacity of the venue associated with the event's venue booking.

An event may have no ticket categories configured yet. Such an event has no bookable inventory until categories are configured.

Private events do not need ticket categories. However, an event must have at least one valid ticket category before it can be published.

### Venue capacity increases

The platform may increase a venue's capacity.

Increasing capacity does not automatically increase any event's ticket category capacity. Organizers must explicitly allocate additional inventory to their events.

### Venue capacity reductions

The platform may reduce a venue's capacity only if the new capacity is greater than or equal to the total allocated ticket capacity of every active event associated with an active venue booking at that venue.

If any active event's allocated inventory exceeds the proposed venue capacity, the capacity reduction must be rejected.

The check applies to allocated category capacity, not merely the number of tickets currently sold.

Capacity reductions must account for all relevant venue bookings and their associated active events.

### Ticket category capacity reductions

An organizer may reduce a ticket category's capacity only if the new capacity is not below the quantity already committed to that category.

Committed inventory includes:

- Confirmed bookings.
- Valid, unexpired Held reservations.

Expired or otherwise released reservations do not count as committed inventory.

Capacity changes must account for reservation expiration and payment confirmation safely, so that a concurrent reservation cannot cause the new capacity to be violated.

### Capacity invariants and database enforcement

The capacity invariant spans multiple rows and cannot be safely enforced by an ordinary row-level `CHECK` constraint alone.

Capacity changes must be performed transactionally.

The implementation must coordinate:

- Venue capacity changes.
- Venue booking creation and cancellation.
- Ticket category creation.
- Ticket category capacity increases and reductions.
- Reservation creation and inventory commitment.
- Reservation expiration and payment confirmation.

Use a consistent row-locking strategy to prevent concurrent transactions from independently passing checks and exceeding capacity.

The database remains the source of truth. Application-level pre-checks may improve error messages but cannot replace transactional enforcement.

Event creation and ticket category modifications must validate the capacity of the venue associated with the event's venue booking.

Venue booking creation must preserve the venue's scheduling and booking constraints, but does not allocate ticket inventory by itself.

### Capacity and event cancellation

Cancelled events are not bookable.

Their historical ticket category and booking records must be preserved as appropriate for auditability and refunds. Cancellation must not make refunded tickets available for sale again.

Cancelling an event does not cancel its associated venue booking. The venue booking remains active and may be used to create another event.

A venue booking can only be cancelled when it has no associated active event.

## Consequences

### Positive

- The physical venue capacity is respected across all ticket categories of an event.
- Capacity reductions cannot silently invalidate an active event's allocation.
- Existing Held reservations and confirmed bookings are protected during capacity changes.
- The model supports multiple general-admission categories without introducing a separate global inventory limit.
- Venue bookings and events retain separate responsibilities.
- A venue booking can be reused for another event after its previously associated active event is cancelled.

### Negative

- Capacity updates require transactions and coordinated locking.
- Venue capacity reductions may be rejected even when the number of tickets sold is low, because the allocated inventory itself exceeds the proposed venue capacity.
- Capacity changes must coordinate with reservation creation, expiration, and payment confirmation.
- Capacity validation must account for the relationship between venue bookings and their associated events.

## Alternatives considered

### Enforce capacity only against tickets sold

Rejected because an event could have allocated inventory exceeding the physical venue capacity even before those tickets are sold.

### Use an ordinary `CHECK` constraint across ticket category rows

Rejected because PostgreSQL `CHECK` constraints are not designed to enforce aggregates across multiple rows.

### Add an independent global ticket limit

Rejected because the venue's physical capacity already provides the required upper bound.
