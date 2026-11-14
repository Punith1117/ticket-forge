# ADR-008: Two-Minute Reservation Expiration

**Status:** Accepted  
**Date:** 2026-11-14

## Context

When a customer reserves tickets, the system temporarily holds inventory while the customer completes the simulated payment process.

Without an expiration policy, customers could hold inventory indefinitely without completing payment.

We need a fixed initial reservation duration and a clear policy for failed payment attempts.

## Decision

A reservation remains Held for two minutes from its creation time.

The expiration timestamp is calculated by the server:

`expires_at = created_at + 2 minutes`

The customer must successfully complete payment before the reservation expires.

### Failed Payment

If simulated payment fails:

- A failed PaymentAttempt is persisted.
- The reservation remains Held if it is still eligible.
- The held inventory is not released.
- The customer may retry payment before expiration.
- Payment retries do not extend the original expiration time.

### Reservation expiration

A reservation is eligible for payment confirmation only while it is still valid and has not expired.

When a reservation expires:

- It must no longer be eligible for confirmation.
- Its held inventory must be released exactly once.
- Repeated expiration processing must not release the same inventory more than once.
- Expiration processing must be safe to execute concurrently and repeatedly.
- The reservation transitions from Held to Expired.

The expiration worker performs this transition safely and transactionally.

### Interaction with booking pauses

An event may temporarily pause new bookings through its separate booking availability state.

Pausing bookings:

- Rejects the creation of new reservations.
- Does not invalidate existing Held reservations.
- Does not extend existing reservation expiration timestamps.
- Does not prevent an otherwise eligible Held reservation from completing payment.
- Does not stop normal expiration processing or inventory release.

This means a customer who obtained a valid reservation before the pause can still complete payment within the original reservation lifetime.

### Event cancellation takes precedence

Event cancellation is different from a booking pause.

When an event is cancelled:

- All outstanding Held reservations are invalidated.
- Their held inventory is released exactly once.
- They can no longer be confirmed through payment callbacks or retries.
- Confirmed bookings follow the full-refund policy in ADR-010.
- A delayed payment callback must not confirm a reservation after its event has been cancelled.

### Concurrency and correctness

Reservation creation, expiration, and payment confirmation must coordinate through database transactions and appropriate row locking.

The database remains the source of truth for reservation state and inventory.

Any background expiration worker must be idempotent. Reprocessing a reservation must not release inventory multiple times or produce inconsistent state.

### Successful Payment

If payment succeeds while the reservation is eligible:

- The reservation transitions from Held to Confirmed.
- A Booking is created.
- Inventory moves from reserved quantity to sold quantity.

## Consequences

### Positive

- Prevents inventory from being held indefinitely.
- Allows payment retries without extending inventory holds.
- Customers with existing valid reservations are not disrupted by a temporary booking pause.
- Idempotent expiration processing supports safe background-worker retries.
- Provides a useful concurrency and background-processing challenge.

### Trade-offs

- Two minutes is shorter than many real-world ticketing payment windows.
- Customers may lose their reservation if payment takes too long.
- The expiration worker must coordinate safely with confirmation and cancellation requests.

## Future Considerations

The reservation duration may become configurable by event or ticket category.

The initial implementation uses a fixed two-minute duration for demonstration and testing.

## Alternatives considered

### Extending expiration on payment retry

Rejected because repeated retries could hold scarce inventory indefinitely.

### Invalidating all Held reservations when bookings are paused

Rejected because pausing new bookings should not unexpectedly interrupt customers who already secured inventory.
