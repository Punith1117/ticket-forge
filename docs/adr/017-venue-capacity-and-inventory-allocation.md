# ADR-017: Venue Capacity and Inventory Allocation

- **Status:** Accepted
- **Date:** 2026-11-14

## Context

TicketForge initially supports general-admission ticket categories.

Each category has an allocated capacity, and an event may have multiple categories with different prices.

The system must ensure that the total number of tickets allocated across all categories never exceeds the physical capacity of the venue.

It must also protect existing reservations and confirmed bookings when organizers or platform operators change capacity.

## Decision

### Venue capacity is the upper bound

Each venue has a physical capacity.

For every active event at that venue:

`SUM(ticket_category.capacity) <= venue.capacity`

The sum of allocated capacity across all ticket categories is the event's total ticket inventory.

There is no additional global ticket inventory limit beyond the venue capacity.

### Category capacity

Each ticket category has an allocated capacity.

Category capacity represents the maximum inventory allocated to that category, not the number of tickets already sold.

The sum of category capacities must never exceed the venue capacity.

An event may have no ticket categories configured yet. Such an event has no bookable inventory until categories are configured.

### Venue capacity increases

The platform may increase a venue's capacity.

Increasing capacity does not automatically increase any event's ticket category capacity. Organizers must explicitly allocate additional inventory to their events.

### Venue capacity reductions

The platform may reduce a venue's capacity only if the new capacity is greater than or equal to the total allocated ticket capacity of every active event at that venue.

If any active event's allocated inventory exceeds the proposed venue capacity, the capacity reduction must be rejected.

The check applies to allocated category capacity, not merely the number of tickets currently sold.

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
- Event venue assignments and rescheduling.
- Ticket category creation.
- Ticket category capacity increases and reductions.
- Reservation creation and inventory commitment.

Use a consistent row-locking strategy to prevent concurrent transactions from independently passing checks and exceeding capacity.

The database remains the source of truth. Application-level pre-checks may improve error messages but cannot replace transactional enforcement.

### Capacity and event cancellation

Cancelled events are not bookable.

Their historical ticket category and booking records must be preserved as appropriate for auditability and refunds. Cancellation must not make refunded tickets available for sale again.

## Consequences

### Positive

- The physical venue capacity is respected across all ticket categories.
- Capacity reductions cannot silently invalidate an active event's allocation.
- Existing Held reservations and confirmed bookings are protected during capacity changes.
- The model supports multiple general-admission categories without introducing a separate global inventory limit.

### Negative

- Capacity updates require transactions and coordinated locking.
- Venue capacity reductions may be rejected even when the number of tickets sold is low, because the allocated inventory itself exceeds the proposed capacity.
- Capacity changes must coordinate with reservation creation, expiration, and payment confirmation.

## Alternatives considered

### Enforce capacity only against tickets sold

Rejected because an event could have allocated inventory exceeding the physical venue capacity even before those tickets are sold.

### Use an ordinary `CHECK` constraint across ticket category rows

Rejected because PostgreSQL `CHECK` constraints are not designed to enforce aggregates across multiple rows.

### Add an independent global ticket limit

Rejected because the venue's physical capacity already provides the required upper bound.
