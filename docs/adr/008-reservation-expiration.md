# ADR-008: Two-Minute Reservation Expiration

**Status:** Accepted  
**Date:** 2026-11-13

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

### Successful Payment

If payment succeeds while the reservation is eligible:

- The reservation transitions from Held to Confirmed.
- A Booking is created.
- Inventory moves from reserved quantity to sold quantity.

### Expiration

If the reservation reaches its expiration time without successful confirmation:

- The reservation transitions from Held to Expired.
- Reserved inventory is released exactly once.

The expiration worker performs this transition safely and transactionally.

## Consequences

### Positive

- Prevents inventory from being held indefinitely.
- Gives customers a clear payment deadline.
- Allows payment retries without extending inventory holds.
- Provides a useful concurrency and background-processing challenge.

### Trade-offs

- Two minutes is shorter than many real-world ticketing payment windows.
- Customers may lose their reservation if payment takes too long.
- The expiration worker must coordinate safely with confirmation and cancellation requests.

## Future Considerations

The reservation duration may become configurable by event or ticket category.

The initial implementation uses a fixed two-minute duration for demonstration and testing.
