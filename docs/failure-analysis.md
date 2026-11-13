# Failure and Concurrency Analysis

**Status:** Accepted

## 1. Correctness-First Principle

PostgreSQL is the source of truth for inventory, venue scheduling, reservations, bookings, payments, and refunds.

Redis, the message broker, and process memory must not bypass database invariants.

Correctness must hold across multiple API instances and worker processes.

## 2. Failure Scenarios

| Scenario | Risk | Expected Behavior |
| --- | --- | --- |
| Two customers request the final ticket | Overselling | Only one allocation succeeds |
| Same reservation request is retried | Duplicate reservation | Return original operation result |
| Same idempotency key used with different payload | Ambiguous operation | Reject key misuse |
| API crashes before transaction commit | Partial update | Database rolls back uncommitted transaction |
| API crashes after commit but before response | Client retries | Idempotency returns committed result |
| Confirmation races with expiration | Invalid state transition | Only one transition wins |
| Reservation cancellation races with expiration | Double inventory release | Only one transition wins |
| Worker crashes during expiration | Incomplete work | Work can safely resume |
| Outbox publisher crashes after broker publish | Duplicate event | Consumer handles duplicate delivery |
| Broker unavailable | Delayed event publication | Outbox retains unpublished events |
| Consumer receives duplicate event | Duplicate side effect | Consumer is idempotent |
| Redis unavailable | Cache/rate-limit degradation | Booking correctness remains intact |
| PostgreSQL unavailable | Booking unavailable | Fail closed; do not report successful reservation |
| API instance crashes | Interrupted request | Client retries safely using idempotency |
| Simulated payment fails | False confirmation | No confirmed booking is created; reservation remains Held if still eligible |
| Payment retry occurs after expiry | Invalid confirmation | Reject confirmation |
| Organizer changes ticket price | Historical price corruption | Existing reservations retain their price snapshots |
| Two organizers schedule overlapping events at one venue | Double booking of venue | Only one scheduling operation succeeds |
| Event creation races with another event creation | Venue scheduling race | Database-level protection prevents overlap |
| Ticket-category capacities exceed venue capacity | Event exceeds physical venue capacity | Reject the capacity configuration; total category capacity must not exceed venue capacity |
| Concurrent ticket-category capacity changes | Combined category capacity exceeds venue capacity | Database-level transactional protection ensures concurrent changes cannot violate the aggregate capacity invariant |
| Event cancellation races with reservation confirmation | Booking confirmed for a cancelled event | Confirmation checks event status and reservation state within a transaction; only a valid transition can succeed |
| Event cancellation races with reservation expiration | Reserved inventory released twice | Reservation state transition and inventory release are atomic; only one operation can release the inventory |
| Event cancellation races with customer cancellation | Reserved inventory released twice | Only one state transition wins and inventory is released exactly once |
| Event cancellation while Held reservations exist | Held reservations remain confirmable or inventory remains reserved | Held reservations are invalidated, cannot be confirmed, and their inventory is released exactly once |
| Event cancellation with multiple confirmed bookings | Some bookings do not receive refund work | Refund work is durably recorded for every confirmed booking |
| Refund processing succeeds but worker crashes before recording success | Duplicate refund on retry | Refund processing uses an idempotent operation and safely handles retries |
| Refund fails repeatedly | Booking remains unrefunded | Record failure details and keep refund work retryable; event remains Cancelled |
| Refund completes | Sold inventory is accidentally restored | Refund processing does not modify inventory or reservation state |
| Organizer cancels an event | Unrefunded bookings | Refund work is durably recorded and retryable |
| Refund operation fails | Incomplete refund | Record failure and allow safe retry |
| Event cancellation is retried | Duplicate refunds | Idempotency and uniqueness prevent duplicate refunds |
| Worker crashes during refund processing | Incomplete refund work | Refund remains discoverable and retryable |

## 3. Concurrency Scenarios to Test

### Test 1: Last Tickets

100 customers compete for 10 remaining tickets.

Expected:

- Exactly 10 ticket units are allocated.
- No inventory becomes negative.
- No duplicate allocation occurs.

### Test 2: Duplicate Reservation Request

One customer submits the same request concurrently using the same idempotency key.

Expected:

- One logical reservation.
- Every successful retry returns the same operation result.

### Test 3: Confirmation vs Expiration

Confirmation and expiration execute concurrently for one reservation.

Expected:

- Only one valid transition wins.
- Inventory accounting remains correct.
- A reservation that has expired cannot become confirmed.

### Test 4: Reservation Cancellation vs Expiration

A customer cancels a Held reservation while a worker is expiring it.

Expected:

- Only one transition wins.
- Inventory is released exactly once.

### Test 5: Multiple Workers

Multiple workers attempt to process the same expiration workload.

Expected:

- No duplicate inventory release.
- No lost expiration work.

### Test 6: Multiple API Instances

Multiple API instances compete for the same category inventory.

Expected:

- PostgreSQL invariants hold across all instances.
- No process-local state is required for correctness.

### Test 7: Venue Scheduling Conflict

Two organizers concurrently attempt to create events at the same venue with overlapping intervals.

Expected:

- Only one event creation succeeds.
- No overlapping non-cancelled events exist for the venue.

### Test 8: Adjacent Venue Events

One event ends at 15:00 and another starts at 15:00 at the same venue.

Expected:

- Both events can be scheduled.
- The intervals are treated as non-overlapping.

### Test 9: Price Change During Reservations

A customer creates a reservation, then the organizer changes the category price.

Expected:

- The existing reservation retains its original price.
- A new reservation uses the updated price.

### Test 10: Event Cancellation and Refunds

An organizer cancels an event with multiple confirmed bookings.

Expected:

- The event becomes Cancelled.
- New reservations and confirmations are rejected.
- Refund work is created for confirmed bookings.
- Repeated cancellation requests do not create duplicate refunds.
- Failed refunds remain retryable.

### Test 11: Venue Physical Capacity

A venue has a physical capacity of 1,000.

An event already has ticket categories with a combined capacity of 700.

Two concurrent requests attempt to increase category capacities by 200 each.

Expected:

- Only a valid capacity configuration can be committed.
- The combined category capacity must never exceed 1,000.
- Concurrent updates cannot bypass the aggregate capacity invariant.

### Test 12: Event Cancellation vs Confirmation

A customer has a Held reservation for an event.

The organizer cancels the event while the customer attempts to confirm the reservation.

Expected:

- The event becomes Cancelled.
- No new confirmation succeeds after cancellation.
- The reservation cannot become Confirmed for a cancelled event.
- Inventory accounting remains correct regardless of which transaction executes first.

### Test 13: Event Cancellation vs Reservation Expiration

An event has several Held reservations.

The organizer cancels the event while an expiration worker processes those reservations.

Expected:

- Each Held reservation is invalidated or expired through a valid state transition.
- Each reservation releases its inventory exactly once.
- No inventory becomes negative.
- No inventory is released twice.
- Both operations can safely be retried.

### Test 14: Event Cancellation vs Customer Cancellation

A customer cancels a Held reservation at the same time the organizer cancels the event.

Expected:

- Only one valid reservation state transition wins.
- Inventory is released exactly once.
- The losing operation handles the already-transitioned reservation safely.

### Test 15: Event Cancellation with Multiple Confirmed Bookings

An event has multiple confirmed bookings when the organizer cancels it.

Expected:

- The event becomes Cancelled.
- Every confirmed booking gets refund work recorded.
- Repeated cancellation requests do not create duplicate refund work.
- Refund processing does not alter sold inventory.
- Refund failures remain retryable.

### Test 16: Refund Worker Crash and Retry

A refund worker processes a refund successfully but crashes before recording the successful result.

The worker later retries the same refund.

Expected:

- The retry does not cause a duplicate refund.
- Refund processing is idempotent.
- The final refund state accurately reflects the outcome.
- The event remains Cancelled regardless of refund processing outcome.

## 4. Event Cancellation: Transactional and Concurrency Guarantees

Event cancellation affects event availability, reservations, inventory, and refunds. It must be handled as a coordinated business operation.

### 4.1 Cancellation vs Confirmation

A reservation confirmation must verify:

- The reservation is still Held.
- The reservation has not expired.
- The associated event is not Cancelled.

These checks must be protected by appropriate transactional concurrency control.

If event cancellation and confirmation execute concurrently, the database must serialize the conflicting operations so that a reservation cannot be confirmed for an event that has already been cancelled.

### 4.2 Cancellation vs Reservation Expiration

Event cancellation and the expiration worker may attempt to invalidate the same Held reservation.

Both operations must use guarded state transitions.

Inventory must be released only when the reservation successfully transitions out of Held.

The reservation state transition and corresponding inventory release must happen atomically within a database transaction.

This guarantees that only one operation releases the reserved quantity.

### 4.3 Cancellation vs Customer Cancellation

Customer cancellation and event cancellation may target the same Held reservation.

Both operations must follow the same state-transition and inventory-accounting rules.

If one operation has already transitioned the reservation out of Held, the other must not release its inventory again.

### 4.4 Cancellation and Refund Processing

Event cancellation must durably record refund work for every confirmed booking associated with the event.

Refund processing is independent of inventory accounting.

A refund worker failure must not:

- Reactivate the event.
- Change a Confirmed reservation back to Held.
- Restore sold inventory.
- Cause duplicate refunds when processing is retried.

Refund operations must be idempotent and safely retryable.

### 4.5 Transactional and Asynchronous Boundaries

The event cancellation workflow must guarantee that:

1. The event is marked Cancelled.
2. New reservations and confirmations are prevented.
3. Existing Held reservations are invalidated and their reserved inventory is released exactly once.
4. Refund work is durably recorded for every confirmed booking.

The event cancellation and the durable recording of required follow-up work must be coordinated through database transactions and the transactional outbox pattern where appropriate.

Background workers may process reservation invalidation and refunds asynchronously.

Regardless of when workers execute, the event must remain Cancelled immediately after the cancellation transaction commits. Confirmation requests must reject the cancelled event even if some Held reservations have not yet been processed by a worker.

The exact division between synchronous database operations and asynchronous worker processing will be finalized during application and sequence-diagram design.

## 5. Observability Signals

At minimum, collect:

- HTTP request count.
- HTTP latency.
- HTTP error rates.
- Reservation attempts and outcomes.
- Inventory conflicts.
- Insufficient inventory responses.
- Reservation expiration lag.
- Venue scheduling conflicts.
- Event cancellation count.
- Refund attempts and outcomes.
- Pending refund count.
- Age of oldest pending refund.
- Outbox pending count.
- Age of oldest unpublished event.
- Broker publish failures.
- Consumer retry count.
- Dead-letter count.
- Database connection pool utilization.
- Database query latency.
- Worker processing duration.

Avoid logging passwords, authentication tokens, or unnecessary personal information.

## 6. Recovery Expectations

- PostgreSQL rolls back uncommitted transactions.
- Committed outbox records remain available for publication.
- Workers can restart and reclaim unfinished work.
- Consumers tolerate duplicate message delivery.
- Failed messages can be inspected and replayed through a controlled procedure.
- Refund processing can resume without duplicating completed refunds.
- Graceful shutdown avoids abandoning work unnecessarily.
- Venue scheduling constraints remain enforced after API restarts and across multiple instances.

## 7. Failure-Test Acceptance

A failure test is successful only when business invariants are verified after recovery.

For example:

Killing a worker is not sufficient.

We must also verify that after restart:

- Expired reservations are processed.
- Inventory is restored exactly once.
- No confirmed booking is lost.
- No duplicate event processing corrupts state.
- Pending refunds are eventually retried.
- Completed refunds are not repeated.
- No overlapping events are created at the same venue.
