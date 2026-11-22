# ADR-008: Two-Minute Reservation Expiration

**Status:** Accepted  
**Date:** 2026-11-14

## Context

When a customer reserves tickets, the system temporarily holds inventory while the customer completes the simulated payment process.

Without an expiration policy, customers could hold inventory indefinitely without completing payment.

We need a fixed initial reservation duration and a clear policy for failed payment attempts.

## Decision

A reservation remains Held for **two minutes** from its creation time.

The expiration timestamp is calculated by the server using the database clock:

`expires_at = reservation_created_at + 2 minutes`

The expiration timestamp is absolute and persisted in the database. The database is the source of truth for determining whether a reservation has expired.

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

Expiration is determined by comparing `expires_at` against the database's current time. The reservation is logically expired as soon as its expiration timestamp is reached, regardless of whether a background worker has processed it.

### Lazy expiration

Reservation creation must not depend on the background expiration worker running on time.

When a customer requests a reservation for a ticket category:

- The system identifies expired Held reservations belonging to that ticket category.
- It transitions eligible reservations from Held to Expired.
- It releases their held inventory.
- It performs expiration cleanup and the new inventory allocation within the same database transaction.

This ensures expired inventory can be reclaimed immediately when another customer requests tickets, even if the background worker is delayed or unavailable.

Lazy expiration is scoped to the requested ticket category rather than scanning all reservations during every reservation request.

The expiration cleanup and new allocation must be atomic. Another transaction must not be able to consume the released inventory between expiration and allocation.

### Background expiration worker

A background worker periodically identifies and processes expired Held reservations, including reservations belonging to ticket categories that receive no new reservation requests.

The worker:

- Finds reservations whose status is Held and whose expiration timestamp has been reached.
- Processes expired reservations in bounded batches.
- Uses concurrency-safe claiming, such as PostgreSQL `FOR UPDATE SKIP LOCKED`, so multiple workers can process different reservations without unnecessarily waiting on one another.
- Transitions reservations from Held to Expired.
- Releases the corresponding held inventory.
- Performs reservation transitions and inventory updates within the same database transaction.

The worker must be safe to retry and run concurrently. A reservation that has already been expired must never release its inventory again.

The background worker is responsible for proactive cleanup, not for determining whether a reservation is eligible for payment confirmation. That decision must always be made using the reservation's persisted state and expiration timestamp.

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

Reservation creation, lazy expiration, background expiration, payment confirmation, and event cancellation must coordinate through database transactions and appropriate row locking.

The implementation must use a consistent locking strategy to avoid deadlocks and prevent concurrent lifecycle operations from modifying the same reservation or inventory allocation incorrectly.

The database remains the source of truth for reservation state, expiration timestamps, and inventory.

All lifecycle transitions must be conditional on the reservation's current state. Inventory counters must only be adjusted when the corresponding state transition succeeds.

### Successful Payment

If payment succeeds while the reservation is eligible:

- The reservation transitions from Held to Confirmed.
- A Booking is created.
- Inventory moves from reserved quantity to sold quantity.

A successful payment callback received after expiration or event cancellation must not confirm the reservation. The payment must instead be handled according to the refund policy.

## Consequences

### Positive

- Prevents inventory from being held indefinitely.
- Allows payment retries without extending inventory holds.
- Customers with existing valid reservations are not disrupted by a temporary booking pause.
- Lazy expiration makes expired inventory immediately reclaimable during subsequent reservation requests.
- Background cleanup releases expired inventory even when no new reservation requests arrive.
- The system remains correct even when the background worker is delayed or unavailable.
- Idempotent expiration processing supports safe background-worker retries.
- Concurrent workers can process expired reservations safely.
- Provides a useful concurrency and background-processing challenge.

### Trade-offs

- Two minutes is shorter than many real-world ticketing payment windows.
- Customers may lose their reservation if payment takes too long.
- Maintaining inventory counters requires every lifecycle transition to update them transactionally.
- Lazy expiration adds work to reservation creation.
- The expiration worker must coordinate safely with confirmation and cancellation requests.

## Future Considerations

The reservation duration may become configurable by event or ticket category.

The initial implementation uses a fixed two-minute duration for demonstration and testing.

## Alternatives considered

### Extending expiration on payment retry

Rejected because repeated retries could hold scarce inventory indefinitely.

### Invalidating all Held reservations when bookings are paused

Rejected because pausing new bookings should not unexpectedly interrupt customers who already secured inventory.

### Relying exclusively on a background expiration worker

Rejected because delayed or unavailable workers could prevent expired inventory from being reclaimed promptly when customers request tickets.

### Relying exclusively on lazy expiration

Rejected because expired reservations belonging to inactive ticket categories would retain inventory indefinitely until another reservation request arrived.
