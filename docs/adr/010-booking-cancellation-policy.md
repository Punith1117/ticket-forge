# ADR-010: No Individual Booking Cancellation in the Initial Release

**Status:** Accepted  
**Date:** 2026-11-13

## Context

Ticket cancellation policies vary across events.

Some events do not permit individual booking cancellations, while others allow cancellations under specific conditions.

Implementing a complete cancellation and refund policy would introduce additional business rules and state transitions.

The initial release should keep individual booking cancellation out of scope while still supporting event cancellation by organizers.

## Decision

Customers cannot individually cancel Confirmed bookings in the initial release.

Customers can cancel their own Held reservations.

When an organizer cancels an event:

1. The event transitions to Cancelled.
2. New reservations and confirmations are prevented.
3. Existing Held reservations are cancelled or otherwise transitioned out of Held state.
4. Reserved inventory associated with those Held reservations is released exactly once.
5. Refund operations are initiated for confirmed bookings associated with the event.

Refund processing is separate from ordinary inventory accounting.

A refund does not automatically make sold tickets available again.

The booking remains a historical record of the confirmed allocation. Refund status is tracked separately.

## Refund Processing

The initial release uses simulated refunds.

Refund operations must:

- Be persisted.
- Be safe to retry.
- Avoid duplicate successful refunds.
- Record failure details.
- Remain retryable after failure.

A failed refund does not reactivate the event.

Event cancellation must not depend on successful refund completion.

## Consequences

### Positive

- Avoids implementing a general-purpose individual booking cancellation policy.
- Keeps booking lifecycle and refund lifecycle separate.
- Still demonstrates event cancellation, asynchronous processing, retries, and idempotency.
- Preserves historical booking records.

### Trade-offs

- Customers cannot voluntarily cancel confirmed bookings.
- Refund processing requires its own persistent state and failure handling.
- Event cancellation can create substantial background refund work.

## Future Considerations

A later version may introduce individual booking cancellation.

That feature will require explicit decisions about:

- Cancellation deadlines.
- Organizer-configured policies.
- Partial cancellation.
- Inventory restoration.
- Refund amounts.
- Refund failure and retry behavior.

These rules must be designed before introducing confirmed-booking cancellation.
