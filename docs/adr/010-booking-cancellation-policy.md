# ADR-010: No Individual Booking Cancellation in the Initial Release

**Status:** Accepted  
**Date:** 2026-11-14

## Context

Ticket cancellation policies vary across events.

Some events do not permit individual booking cancellations, while others allow cancellations under specific conditions.

Implementing a complete cancellation and refund policy would introduce additional business rules and state transitions.

The initial release should keep individual cancellation of confirmed bookings out of scope while still supporting:

- Customer cancellation of their own Held reservations.
- Event cancellation by organizers.
- Full refunds for confirmed bookings when an event is cancelled.

## Decision

### Individual booking cancellation

Customers cannot individually cancel Confirmed bookings in the initial release.

Customers can cancel their own Held reservations.

When a customer cancels a Held reservation:

- The reservation transitions out of Held state.
- Its reserved inventory is released exactly once.
- The reservation cannot subsequently be confirmed.

Confirmed bookings are treated as terminal from the customer's perspective.

### Organizer event cancellation

An organizer may cancel an event they own, even if it has confirmed bookings.

Event cancellation is a terminal lifecycle transition:

`active -> cancelled`

A cancelled event cannot be reactivated. An organizer who wants to run the event again must create a new event.

When an organizer cancels an event:

1. The event transitions to `cancelled`.
2. New reservations and payment confirmations are prevented.
3. All outstanding Held reservations are invalidated and transitioned out of Held state.
4. Inventory associated with those Held reservations is released exactly once.
5. Refund operations are initiated for all confirmed bookings associated with the event.

Event cancellation takes precedence over booking availability and reservation expiration.

A delayed payment callback must not confirm a reservation belonging to a cancelled event.

### Inventory accounting

Refund processing is separate from ordinary inventory accounting.

- Inventory released from Held reservations becomes available to the event's inventory accounting, although the cancelled event cannot accept new reservations.
- Refunded confirmed bookings do not restore sold inventory.
- A refund does not make previously sold tickets available again.
- Historical booking records must be preserved.

The booking remains a historical record of the confirmed allocation. Refund status is tracked separately from booking status.

## Refund Processing

The initial release uses simulated refunds.

Refund operations must:

- Be persisted independently of the booking record.
- Be safe to retry.
- Avoid duplicate successful refunds.
- Record failure details.
- Remain retryable after failure.
- Support full refunds for confirmed bookings when an event is cancelled.

Refund processing must be asynchronous and reliable.

Event cancellation must not depend on successful refund completion. The event cancellation transaction must establish the cancelled state and prevent subsequent booking confirmations without waiting for refund operations to finish.

A failed refund does not reactivate the event or restore sold inventory.

## Concurrency and Idempotency

Event cancellation, reservation expiration, customer cancellation of Held reservations, and payment confirmation must be coordinated through database transactions and appropriate row locking.

The implementation must guarantee:

- A Held reservation cannot release its inventory more than once.
- A cancelled reservation cannot subsequently be confirmed.
- A cancelled event cannot receive new reservations or payment confirmations.
- Repeated event cancellation or refund processing cannot cause duplicate successful refunds.
- Concurrent payment confirmation and event cancellation cannot produce an invalid confirmed booking.

The database remains the source of truth for booking, reservation, and inventory state.

## Consequences

### Positive

- Avoids implementing a general-purpose individual booking cancellation policy.
- Allows customers to release their own Held reservations.
- Keeps booking lifecycle and refund lifecycle separate.
- Supports event cancellation even after confirmed bookings exist.
- Demonstrates asynchronous processing, retries, and idempotency.
- Preserves historical booking records and inventory correctness.

### Trade-offs

- Customers cannot voluntarily cancel confirmed bookings.
- Refund processing requires its own persistent state and failure handling.
- Event cancellation can create substantial background refund work.
- Cancellation and payment confirmation require careful concurrency control.

## Future Considerations

A later version may introduce individual cancellation of confirmed bookings.

That feature will require explicit decisions about:

- Cancellation deadlines.
- Organizer-configured policies.
- Partial cancellation.
- Inventory restoration.
- Refund amounts.
- Refund failure and retry behavior.

These rules must be designed before introducing confirmed-booking cancellation.
