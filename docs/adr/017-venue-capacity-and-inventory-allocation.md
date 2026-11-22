# ADR-017: Venue Capacity and Inventory Allocation

- **Status:** Accepted
- **Date:** 2026-11-22

## Context

TicketForge initially supports general-admission ticket categories.

Each category has an allocated capacity, and an event may have multiple categories with different prices.

Events are organized using venue bookings. A venue booking reserves a physical venue and its scheduled time interval, while an event represents an activity organized using that booking.

The system must ensure that the total number of tickets allocated across all categories of an event never exceeds the physical capacity of the venue associated with its venue booking.

It must also protect existing reservations and confirmed bookings when organizers or platform operators change capacity.

Additionally, the system needs an efficient and concurrency-safe way to determine ticket availability.

Two approaches were considered:

### Approach A: Calculate availability from reservations

Availability could be calculated by aggregating reservations:

```sql
SELECT
    capacity
    - confirmed_quantity
    - active_held_quantity AS available
FROM ...
```

Active held quantity would include reservations whose status is `held` and whose expiration timestamp is still in the future.

Advantages:

- No separate inventory counters that can become inconsistent.
- Expired reservations automatically stop contributing to availability.
- Conceptually straightforward.

Disadvantages:

- Availability queries become more expensive as reservation history grows.
- Concurrent allocation still requires transaction coordination and locking.
- Repeatedly aggregating reservations becomes increasingly expensive at scale.

### Approach B: Maintain inventory counters

The system maintains an inventory row for each ticket category.

The row tracks:

- `reserved_quantity`: quantity currently allocated to held reservations.
- `sold_quantity`: quantity allocated to confirmed bookings.

Available inventory is:

```text
available_quantity =
    category_capacity
    - reserved_quantity
    - sold_quantity
```

Advantages:

- Fast availability queries.
- Atomic inventory allocation.
- Avoids repeatedly aggregating reservation history.
- Scales better for high-demand events.

Disadvantages:

- Counter consistency becomes critical.
- Expiration must release inventory, not merely change a timestamp or reservation status.
- Every lifecycle transition must update inventory transactionally.

## Decision

TicketForge will use **inventory counters**, combined with lazy expiration and background cleanup.

The database remains the source of truth.

Reservation records represent the individual reservation lifecycle, while inventory counters maintain the aggregate allocation state for each ticket category.

All changes to reservation states and their corresponding inventory counters must happen within the same database transaction.

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

### Inventory counters

Each ticket category has exactly one corresponding inventory row.

The inventory row maintains:

- `reserved_quantity`: quantity held by currently allocated reservations.
- `sold_quantity`: quantity committed to confirmed bookings.

The following invariant must always hold:

`reserved_quantity >= 0`

`sold_quantity >= 0`

`reserved_quantity + sold_quantity <= ticket_category.capacity`

Available inventory is calculated as:

`available_quantity = capacity - reserved_quantity - sold_quantity`

The database must protect these invariants through transactional updates, conditional updates, and appropriate constraints.

Application-level availability checks alone are insufficient.

### Inventory allocation during reservation creation

When a customer requests tickets:

1. Begin a database transaction.
2. Lock the relevant event row.
3. Validate that the event is active and its booking status is open.
4. Lock and validate the requested ticket category.
5. Reclaim expired reservations belonging to that category.
6. Release their reserved inventory.
7. Lock the inventory row.
8. Calculate available inventory using the counters.
9. Reject the request if insufficient inventory remains.
10. Create the new held reservation.
11. Increment `reserved_quantity` by the requested quantity.
12. Commit the transaction.

The expiration cleanup and new inventory allocation must occur in the same transaction.

This ensures that another request cannot consume inventory between releasing expired holds and allocating the new reservation.

### Reservation confirmation

When an eligible reservation is successfully paid:

- The reservation transitions from `held` to `confirmed`.
- A booking is created.
- `reserved_quantity` decreases by the reservation quantity.
- `sold_quantity` increases by the same quantity.

These changes must occur atomically.

The total allocated inventory must remain unchanged during confirmation.

### Reservation expiration

When a reservation expires:

- Its status transitions from `held` to `expired`.
- Its reserved inventory is released.
- `reserved_quantity` decreases by the reservation quantity.

Expiration must release inventory exactly once.

Repeated or concurrent expiration attempts must not decrement inventory more than once.

See ADR-008 for the complete reservation expiration policy.

### Lazy expiration and background cleanup

TicketForge uses two complementary expiration mechanisms.

**Lazy expiration**

When a new reservation request needs inventory for a ticket category, the transaction first reclaims expired held reservations belonging to that category.

This ensures that expired reservations do not permanently consume inventory simply because a background worker has not processed them yet.

Lazy expiration is scoped to the requested category. A reservation request must not perform a global scan of every expired reservation in the system.

**Background cleanup**

A background worker periodically processes expired held reservations across all categories.

Its purpose is to release inventory proactively, even when no new reservation requests arrive.

The worker must:

- Identify overdue held reservations.
- Coordinate with other lifecycle operations through database transactions and row locks.
- Transition reservations to expired.
- Release their reserved inventory.
- Avoid duplicate processing and double decrements.

The worker must not be required for the correctness of new reservation allocation.

See ADR-008 for expiration processing and worker coordination.

### Inventory locking and concurrency

Concurrent requests for the same ticket category must not allocate more inventory than the category's capacity.

Inventory allocation must use database row locking and transactional updates.

For example, if only two tickets remain and ten customers request two tickets simultaneously:

- The first transaction locks the relevant inventory row.
- It allocates the two remaining tickets and commits.
- Subsequent transactions observe zero available inventory and are rejected.

All reservation lifecycle operations must follow a consistent locking strategy.

The event row is acquired first. Relevant reservation or category rows are then locked, followed by inventory rows.

When multiple inventory rows must be locked, they should be acquired in a deterministic order.

The database remains the source of truth. Application-level pre-checks may improve error messages but cannot replace transactional enforcement.

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
- Valid, unexpired held reservations.

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
- Existing held reservations and confirmed bookings are protected during capacity changes.
- Availability queries do not require repeatedly aggregating reservation history.
- Inventory allocation is efficient and transactionally controlled.
- Lazy expiration allows expired inventory to be reclaimed even if the background worker is unavailable.
- Background cleanup proactively releases inventory when no new reservation requests arrive.
- The model supports multiple general-admission categories without introducing a separate global ticket inventory limit.
- Venue bookings and events retain separate responsibilities.
- A venue booking can be reused for another event after its previously associated active event is cancelled.

### Negative

- Capacity updates require transactions and coordinated locking.
- Inventory counters must remain consistent with reservation and booking states.
- Every lifecycle transition must update inventory transactionally.
- Expiration requires both lazy cleanup and a background worker.
- Venue capacity reductions may be rejected even when the number of tickets sold is low, because the allocated inventory itself exceeds the proposed venue capacity.
- Capacity validation must account for the relationship between venue bookings and their associated events.

## Alternatives considered

### Calculate availability from reservation records

Rejected because repeatedly aggregating reservation history makes availability queries increasingly expensive as reservation volume grows.

### Use inventory counters without expiration cleanup

Rejected because expired reservations would continue consuming inventory until some other process released it.

### Depend exclusively on a background expiration worker

Rejected because worker downtime or processing delays could temporarily prevent customers from reserving inventory that should already be available.

### Use an ordinary `CHECK` constraint across ticket category rows

Rejected because PostgreSQL `CHECK` constraints are not designed to enforce aggregates across multiple rows.

### Add an independent global ticket limit

Rejected because the venue's physical capacity already provides the required upper bound.
