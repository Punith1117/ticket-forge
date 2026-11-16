# Failure and Concurrency Analysis

**Status:** Accepted

## Correctness-First Principle

PostgreSQL is the source of truth for inventory, venue scheduling, reservations, bookings, payments, and refunds.

Redis, the message broker, and process memory must not bypass database invariants.

Correctness must hold across multiple API instances and worker processes.

The system must remain correct when:

- Requests are retried.
- Multiple customers compete for the same inventory.
- API instances process requests concurrently.
- Workers execute the same work more than once.
- Processes crash during operations.
- Messages are delivered more than once.
- External infrastructure becomes temporarily unavailable.

## Failure Scenarios

### Implemented Authentication and Asynchronous Processing Scenarios

| Scenario | Risk | Expected Behavior | Status |
| --- | --- | --- | --- |
| Registration requested within cooldown | SMTP flooding / challenge spam | Returns 429 Too Many Requests if challenge requested within 60s | **Implemented** |
| Registration completion with expired token (>24h) | Unauthorized account creation | Returns 400 Bad Request (`token_expired`); challenge not consumed | **Implemented** |
| Registration completion with consumed token | Replay attack / duplicate user | Returns 400 Bad Request (`token_already_used`); atomic transaction prevents replay | **Implemented** |
| Registration completion with invalid/malformed token | Unauthorized account creation | Returns 400 Bad Request (`invalid_token`) | **Implemented** |
| Login with non-existent email | User enumeration via timing attack | Executes dummy Argon2id verification to equalize execution time; returns generic 401 | **Implemented** |
| Login with wrong password | Unauthorized access | Verifies password with Argon2id; returns generic 401 without revealing error details | **Implemented** |
| Request without session cookie | Unauthorized access | Middleware rejects request with generic 401 Unauthorized; next handler not called | **Implemented** |
| Session idle timeout elapsed (>30 min) | Stale session access | Atomic SQL update finds zero active rows; returns 401 Unauthorized | **Implemented** |
| Session absolute lifetime elapsed (>7 days) | Indefinite session extension | Atomic SQL update rejects request even if idle expiry was refreshed; returns 401 | **Implemented** |
| Session revoked via logout | Revoked session access | Query filters on `revoked_at IS NULL`; returns 401 Unauthorized | **Implemented** |
| PostgreSQL unavailable during session validation | False logout on infrastructure failure | Middleware catches DB error, logs it, and returns 500 Internal Server Error (never 401) | **Implemented** |
| Outbox worker crashes after claiming events | Stalled event processing | Claim lease (`claim_expires_at`) expires; another worker reclaims via `SKIP LOCKED` | **Implemented** |
| Outbox worker crashes after SMTP send before DB update | Duplicate email delivery | At-least-once delivery: email may resend; token hash in DB prevents duplicate consumption | **Implemented** |
| SMTP server temporarily unavailable | Lost verification email | Worker logs error, applies exponential backoff with full jitter, sets `next_attempt_at` | **Implemented** |
| Outbox event exceeds max retry attempts (5) | Infinite worker retry loop | Worker transitions event status to `failed` with `last_error` for dead-letter isolation | **Implemented** |

### Planned Ticketing Pipeline Scenarios

| Scenario | Risk | Expected Behavior | Status |
| --- | --- | --- | --- |
| Two customers request the final ticket | Overselling | Only one allocation succeeds | *Planned* |
| Same reservation request is retried | Duplicate reservation | Return the original operation result | *Planned* |
| Same idempotency key is used with a different payload | Ambiguous operation | Reject key misuse | *Planned* |
| API crashes before transaction commit | Partial update | Database rolls back uncommitted transaction | *Planned* |
| API crashes after commit but before response | Client retries | Idempotency returns the committed result | *Planned* |
| Confirmation races with expiration | Invalid state transition | Only one transition wins | *Planned* |
| Reservation cancellation races with expiration | Double inventory release | Only one transition wins | *Planned* |
| Worker crashes during expiration | Incomplete work | Work remains safely retryable | *Planned* |
| Outbox publisher crashes after broker publish | Duplicate event | Consumer handles duplicate delivery | *Planned* |
| Broker unavailable | Delayed event publication | Outbox retains unpublished events | *Planned* |
| Consumer receives duplicate event | Duplicate side effect | Consumer is idempotent | *Planned* |
| Redis unavailable | Cache/rate-limit degradation | Booking correctness remains intact | *Planned* |
| PostgreSQL unavailable | Booking unavailable | Fail closed; do not report successful reservation | *Planned* |
| API instance crashes | Interrupted request | Client retries safely using idempotency | *Planned* |
| Simulated payment fails | False confirmation | No booking is created; reservation remains Held if still eligible | *Planned* |
| Payment retry occurs after expiry | Invalid confirmation | Reject confirmation | *Planned* |
| Organizer changes ticket price | Historical price corruption | Existing reservations retain their price snapshots | *Planned* |
| Two organizers schedule overlapping events at one venue | Double booking of venue | Only one scheduling operation succeeds | *Planned* |
| Event creation races with another event creation | Venue scheduling race | Database-level protection prevents overlap | *Planned* |
| Event rescheduling races with reservation creation | Event schedule changes after reservations begin | Transactional coordination ensures either rescheduling succeeds before a reservation exists, or the reservation prevents rescheduling | *Planned* |
| Ticket-category capacities exceed venue capacity | Event exceeds physical venue capacity | Reject the capacity configuration | *Planned* |
| Concurrent ticket-category capacity changes | Combined category capacity exceeds venue capacity | Transactional coordination ensures the aggregate capacity invariant holds | *Planned* |
| Venue capacity is reduced below an active event's allocated capacity | Event exceeds physical venue capacity | Reject the venue capacity reduction | *Planned* |
| Venue is deactivated while events reference it | Existing events become invalid unexpectedly | Existing events remain scheduled; deactivation prevents new event selection | *Planned* |
| Event cancellation races with reservation creation | Reservation created for cancelled event | Event-level coordination prevents a reservation after cancellation takes effect | *Planned* |
| Event cancellation races with reservation confirmation | Booking confirmed for a cancelled event | Only a valid transition can succeed | *Planned* |
| Event cancellation races with reservation expiration | Reserved inventory released twice | State transition and inventory release are atomic | *Planned* |
| Event cancellation races with customer cancellation | Reserved inventory released twice | Only one state transition wins | *Planned* |
| Event cancellation while Held reservations exist | Held reservations remain confirmable or inventory remains reserved | Held reservations are invalidated and inventory is released in the cancellation transaction | *Planned* |
| Event cancellation with multiple confirmed bookings | Some bookings do not receive refund work | Refund work is durably recorded for every confirmed booking | *Planned* |
| Refund processing succeeds but worker crashes before recording success | Duplicate refund on retry | Idempotent refund operation safely handles retries | *Planned* |
| Refund fails repeatedly | Booking remains unrefunded | Record failure details and keep refund work retryable; event remains Cancelled | *Planned* |
| Refund completes | Sold inventory is accidentally restored | Refund processing does not modify inventory | *Planned* |
| Event cancellation is retried | Duplicate refunds | Idempotency and uniqueness prevent duplicate refund records | *Planned* |
| Worker crashes during refund processing | Incomplete refund work | Refund remains discoverable and retryable | *Planned* |
| Booking availability is paused during reservation creation | Reservation created after pause | Transactional coordination ensures a reservation is accepted only if booking availability is open at the relevant validation point | *Planned* |
| Booking availability is paused while a Held reservation exists | Existing reservation becomes unconfirmable | Existing Held reservation remains eligible for confirmation until its original expiry, if all other conditions hold | *Planned* |

## Concurrency Scenarios to Test

### Test 1: Last Tickets

100 customers compete for 10 remaining tickets.

Expected:

- Exactly 10 ticket units are allocated.
- No inventory becomes negative.
- No duplicate allocation occurs.
- All other requests fail with an appropriate insufficient-inventory response.

### Test 2: Duplicate Reservation Request

One customer submits the same request concurrently using the same idempotency key.

Expected:

- One logical reservation.
- Every successful retry returns the same operation result.
- No additional inventory is allocated.

### Test 3: Idempotency Key Misuse

One customer submits a reservation request using an idempotency key, then reuses that key with a different payload.

Expected:

- The second request is rejected.
- The original reservation remains unchanged.
- No additional inventory is allocated.

### Test 4: Confirmation vs Expiration

Confirmation and expiration execute concurrently for one reservation.

Expected:

- Only one valid transition wins.
- Inventory accounting remains correct.
- A reservation that has expired cannot become confirmed.
- A confirmed reservation cannot subsequently be expired.

### Test 5: Reservation Cancellation vs Expiration

A customer cancels a Held reservation while a worker is expiring it.

Expected:

- Only one transition wins.
- Inventory is released exactly once.
- The losing operation handles the already-transitioned reservation safely.

### Test 6: Multiple Workers

Multiple workers attempt to process the same expiration workload.

Expected:

- No duplicate inventory release.
- No lost expiration work.
- Workers can safely retry unfinished work.

### Test 7: Multiple API Instances

Multiple API instances compete for the same category inventory.

Expected:

- PostgreSQL invariants hold across all instances.
- No process-local state is required for correctness.
- Inventory cannot be oversold.

### Test 8: Venue Scheduling Conflict

Two organizers concurrently attempt to create events at the same venue with overlapping intervals.

Expected:

- Only one event creation succeeds.
- No overlapping active events exist for the venue.
- The PostgreSQL exclusion constraint prevents the conflict.

### Test 9: Adjacent Venue Events

One event ends at 15:00 and another starts at 15:00 at the same venue.

Expected:

- Both events can be scheduled.
- The intervals are treated as non-overlapping because they are half-open: `[start, end)`.

### Test 10: Price Change During Reservations

A customer creates a reservation, then the organizer changes the category price.

Expected:

- The existing reservation retains its original price.
- A new reservation uses the updated price.
- Existing bookings retain their historical price.

### Test 11: Event Cancellation and Refunds

An organizer cancels an event with multiple confirmed bookings.

Expected:

- The event becomes `cancelled`.
- New reservations and confirmations are rejected.
- Outstanding Held reservations are invalidated and their inventory released in the cancellation transaction.
- Refund work is created for confirmed bookings.
- Repeated cancellation requests do not create duplicate refunds.
- Failed refunds remain retryable.

### Test 12: Venue Physical Capacity

A venue has a physical capacity of 1,000.

An event already has ticket categories with a combined capacity of 700.

Two concurrent requests attempt to increase category capacities by 200 each.

Expected:

- Only a valid capacity configuration can be committed.
- Combined category capacity must never exceed 1,000.
- Concurrent updates cannot bypass the aggregate capacity invariant.

### Test 13: Event Cancellation vs Confirmation

A customer has a Held reservation for an event.

The organizer cancels the event while the customer attempts to confirm the reservation.

Expected:

- The event becomes `cancelled`.
- No confirmation succeeds after cancellation has taken effect.
- The reservation cannot become Confirmed for a cancelled event.
- If confirmation commits first, the resulting booking is included in the cancellation's refund processing.
- Inventory accounting remains correct regardless of which transaction executes first.

### Test 14: Event Cancellation vs Reservation Expiration

An event has several Held reservations.

The organizer cancels the event while an expiration worker processes those reservations.

Expected:

- Each Held reservation is invalidated through a valid state transition.
- Each reservation releases its inventory exactly once.
- No inventory becomes negative.
- No inventory is released twice.
- Both operations can safely be retried.

### Test 15: Event Cancellation vs Customer Cancellation

A customer cancels a Held reservation at the same time the organizer cancels the event.

Expected:

- Only one valid reservation state transition wins.
- Inventory is released exactly once.
- The losing operation handles the already-transitioned reservation safely.

### Test 16: Event Cancellation with Multiple Confirmed Bookings

An event has multiple confirmed bookings when the organizer cancels it.

Expected:

- The event becomes `cancelled`.
- Every confirmed booking gets refund work recorded.
- Repeated cancellation requests do not create duplicate refund records.
- Refund processing does not alter sold inventory.
- Refund failures remain retryable.

### Test 17: Refund Worker Crash and Retry

A refund worker processes a refund successfully but crashes before recording the successful result.

The worker later retries the same refund.

Expected:

- The retry does not cause a duplicate successful refund.
- Refund processing is idempotent.
- The final refund state accurately reflects the outcome.
- The event remains `cancelled` regardless of refund processing outcome.

### Test 18: Event Rescheduling vs Reservation Creation

An organizer attempts to reschedule an event while a customer attempts to reserve tickets for it.

Expected:

- Both operations coordinate through the event.
- If reservation creation succeeds first, the event can no longer be rescheduled.
- If rescheduling succeeds first, the reservation uses the updated event information.
- No reservation is created against an inconsistent schedule.

### Test 19: Booking Availability vs Reservation Creation

An organizer pauses bookings while customers are attempting to reserve tickets.

Expected:

- A reservation cannot be created if the pause takes effect before its booking-availability validation.
- A reservation that successfully commits before the pause remains valid.
- Existing Held reservations remain eligible for confirmation until their original expiry, provided other conditions hold.

## Event Cancellation: Transactional and Concurrency Guarantees

Event cancellation affects event availability, reservations, inventory, and refunds. It must be handled as a coordinated business operation.

### Cancellation vs Confirmation

A reservation confirmation must verify:

- The reservation is still Held.
- The reservation has not expired.
- The associated event is not Cancelled.

These checks must be protected by appropriate transactional concurrency control.

If event cancellation and confirmation execute concurrently, the database must serialize the conflicting operations so that:

- If confirmation commits first, the booking is created and must be included in event cancellation's refund processing.
- If cancellation commits first, confirmation must fail.

A reservation cannot be confirmed for an event that has already been cancelled.

### Cancellation vs Reservation Expiration

Event cancellation and the expiration worker may attempt to invalidate the same Held reservation.

Both operations must use guarded state transitions.

Inventory must be released only when the reservation successfully transitions out of Held.

The reservation state transition and corresponding inventory release must happen atomically within a database transaction.

This guarantees that only one operation releases the reserved quantity.

### Cancellation vs Customer Cancellation

Customer cancellation and event cancellation may target the same Held reservation.

Both operations must follow the same state-transition and inventory-accounting rules.

If one operation has already transitioned the reservation out of Held, the other must not release its inventory again.

### Cancellation and Refund Processing

Event cancellation must durably record refund work for every confirmed booking associated with the event.

Refund processing is independent of inventory accounting.

A refund worker failure must not:

- Reactivate the event.
- Change a Confirmed reservation back to Held.
- Restore sold inventory.
- Cause duplicate refund records.
- Cause duplicate successful refund execution when processing is retried.

Refund operations must be idempotent and safely retryable.

### Transactional and Asynchronous Boundaries

The event cancellation workflow must guarantee that, within one database transaction:

1. The event is marked `cancelled`.
2. New reservations and confirmations are prevented through event-level coordination.
3. Existing Held reservations are locked and invalidated.
4. Reserved inventory is released for each invalidated reservation exactly once.
5. Pending refund records are created for every confirmed booking requiring a refund.
6. The event cancellation outbox record is created.

The transaction commits only when all these changes succeed.

Refund execution happens asynchronously after commit.

**Held reservation invalidation is not deferred to a background worker.** Workers may handle other asynchronous follow-up work, but the cancellation transaction itself must invalidate Held reservations and release their inventory.

This guarantees that immediately after cancellation commits:

- The event is cancelled.
- No new reservation can be created.
- No Held reservation can be confirmed.
- No reserved inventory remains allocated to invalidated Held reservations.
- Refund work is durably recorded.

### Refund Execution and Retry

Refund records are persisted before the cancellation transaction commits.

The Refund Worker:

1. Claims pending or retryable refund records.
2. Processes the refund operation.
3. Persists the outcome.
4. Makes failed operations eligible for controlled retry.

The initial implementation uses simulated refunds. The simulation must support deterministic idempotent processing so that a retry cannot produce duplicate successful effects.

For a future real payment provider, external idempotency keys and reconciliation will be required to handle a successful provider-side refund followed by a worker crash before local success is recorded.

## Observability Signals

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

## Recovery Expectations

- PostgreSQL rolls back uncommitted transactions.
- Committed outbox records remain available for publication.
- Workers can restart and reclaim unfinished work.
- Consumers tolerate duplicate message delivery.
- Failed messages can be inspected and replayed through a controlled procedure.
- Refund processing can resume without duplicating completed refunds.
- Graceful shutdown avoids abandoning work unnecessarily.
- Venue scheduling constraints remain enforced after API restarts and across multiple instances.
- Inventory accounting remains correct after API and worker restarts.
- Event cancellation remains effective even when refund processing is unavailable.

## Failure-Test Acceptance

A failure test is successful only when business invariants are verified after recovery.

For example, killing a worker is not sufficient.

We must also verify that after restart:

- Expired reservations are processed.
- Inventory is restored exactly once.
- No confirmed booking is lost.
- No duplicate event processing corrupts state.
- Pending refunds are eventually retried.
- Completed refunds are not executed successfully more than once.
- No overlapping active events are created at the same venue.
- Venue capacity is never exceeded by an event's allocated ticket categories.
- Cancelled events cannot accept new reservations or confirmations.
- Held reservations invalidated by event cancellation do not retain reserved inventory.
- Refunded bookings remain historical records and do not restore sold inventory.
