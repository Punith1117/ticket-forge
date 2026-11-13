# Critical Sequence Diagrams

**Status:** Accepted

## 1. Create Event and Validate Venue Availability

```mermaid
sequenceDiagram
    actor O as Organizer
    participant API as Go API
    participant DB as PostgreSQL

    O->>API: POST /events
    API->>API: Authenticate and authorize organizer
    API->>API: Validate event details

    API->>DB: Begin transaction
    API->>DB: Verify venue exists
    API->>DB: Check for overlapping non-cancelled events

    alt No scheduling conflict
        API->>DB: Insert event
        API->>DB: Commit
        API-->>O: 201 Event created

    else Scheduling conflict
        API->>DB: Rollback
        API-->>O: 409 Venue unavailable
    end
```

**Important design consideration:**

The availability check and event insertion must be protected against concurrent event creation.

A simple `SELECT` followed by `INSERT` is insufficient because two organizers could simultaneously observe that a venue is available.

The database must enforce the non-overlapping schedule invariant. PostgreSQL range types and exclusion constraints are one possible solution.

The exact database implementation will be decided during SQL schema design.

## 2. Reserve Tickets

```mermaid
sequenceDiagram
    actor C as Customer
    participant API as Go API
    participant DB as PostgreSQL

    C->>API: POST /reservations + Idempotency-Key

    API->>API: Authenticate and validate request

    API->>DB: Begin transaction
    API->>DB: Check idempotency record

    alt Existing key with same request
        DB-->>API: Stored result
        API-->>C: Return original result

    else New operation
        API->>DB: Validate event and category eligibility
        API->>DB: Read current category price
        API->>DB: Atomically allocate inventory

        alt Inventory available
            API->>DB: Insert Held reservation with price snapshot
            API->>DB: Insert idempotency result
            API->>DB: Commit

            API-->>C: 201 Reservation + expires_at + amount

        else Insufficient inventory or unavailable category
            API->>DB: Rollback or persist defined failure result
            API-->>C: 409 Reservation unavailable
        end
    end
```

The reservation stores the price applicable at creation time.

A later ticket category price change must not affect this reservation.

The initial reservation expiry is two minutes after creation.

## 3. Confirm Reservation

```mermaid
sequenceDiagram
    actor C as Customer
    participant API as Go API
    participant DB as PostgreSQL
    participant Pay as Payment Simulator

    C->>API: POST /reservations/{id}/confirm
    Note over C,API: Include Idempotency-Key header

    API->>API: Authenticate and validate ownership

    API->>DB: Begin transaction
    API->>DB: Check idempotency record
    API->>DB: Lock and validate reservation
    API->>DB: Check expiry and event status

    alt Reservation is eligible
        API->>Pay: Execute simulated payment
        Pay-->>API: Payment result

        alt Payment succeeds
            API->>DB: Mark reservation Confirmed
            API->>DB: Move inventory from reserved to sold
            API->>DB: Insert Booking using price snapshot
            API->>DB: Insert successful PaymentAttempt
            API->>DB: Insert OutboxEvent
            API->>DB: Store idempotency result
            API->>DB: Commit transaction

            API-->>C: Booking confirmed

        else Payment fails
            API->>DB: Insert failed PaymentAttempt
            API->>DB: Keep reservation Held
            API->>DB: Store idempotency result
            API->>DB: Commit transaction

            API-->>C: Payment failed. Retry allowed before expiry
        end

    else Reservation expired or event unavailable
        API->>DB: Rollback transaction
        API-->>C: Reservation no longer eligible
    end
```

**Important design considerations:**

- A failed payment attempt is retained.
- A failed attempt does not release inventory.
- The reservation remains Held if it is still eligible.
- Payment retries do not extend the original expiration time.
- Only one successful confirmation can create a booking.
- The reservation price snapshot determines the amount charged.

The initial simulator is local. We will finalize whether its execution occurs inside the database transaction during low-level design.

Do not hold a database transaction open across a real external payment provider call in a future integration. A real payment workflow would need to handle callbacks, uncertain outcomes, and reconciliation.

## 4. Expire Reservations

```mermaid
sequenceDiagram
    participant W as Expiration Worker
    participant DB as PostgreSQL
    participant B as Message Broker

    W->>DB: Find and claim expired Held reservations
    DB-->>W: Claimed reservation IDs

    loop Each claimed reservation
        W->>DB: Begin transaction
        W->>DB: Lock and recheck reservation state and expiry
        W->>DB: Transition Held -> Expired
        W->>DB: Release reserved inventory
        W->>DB: Insert ReservationExpired outbox event
        W->>DB: Commit
    end

    W->>DB: Claim unpublished outbox events
    DB-->>W: Event payloads

    W->>B: Publish events
    B-->>W: Acknowledge

    W->>DB: Mark events published
```

The worker must safely handle multiple instances processing the same expiration workload.

The reservation state transition and inventory release must occur in one transaction.

## 5. Cancel Reservation

```mermaid
sequenceDiagram
    actor C as Customer
    participant API as Go API
    participant DB as PostgreSQL

    C->>API: DELETE /reservations/{id}

    API->>API: Authenticate and validate ownership

    API->>DB: Begin transaction
    API->>DB: Lock and check reservation state

    alt Reservation is Held and cancellable
        API->>DB: Mark reservation Cancelled
        API->>DB: Release reserved inventory
        API->>DB: Insert outbox event
        API->>DB: Commit

        API-->>C: Cancellation successful

    else Not cancellable
        API->>DB: Rollback
        API-->>C: Conflict or not found
    end
```

Confirmed bookings cannot be individually cancelled in the initial version.

## 6. Cancel Event and Initiate Refunds

```mermaid
sequenceDiagram
    actor O as Organizer
    participant API as Go API
    participant DB as PostgreSQL
    participant W as Refund Worker
    participant R as Refund Simulator

    O->>API: POST /events/{id}/cancel

    API->>API: Authenticate and authorize organizer
    API->>DB: Begin transaction
    API->>DB: Lock and validate event

    API->>DB: Mark event Cancelled
    API->>DB: Prevent further reservations and confirmations
    API->>DB: Record EventCancelled outbox event
    API->>DB: Commit

    API-->>O: Event cancelled

    W->>DB: Find confirmed bookings requiring refunds
    DB-->>W: Refund work

    loop Each eligible booking
        W->>DB: Create or claim refund operation
        W->>R: Execute simulated refund
        R-->>W: Success or failure

        alt Refund succeeds
            W->>DB: Mark refund Succeeded
            W->>DB: Commit
        else Refund fails
            W->>DB: Mark refund Failed
            W->>DB: Record failure details
            W->>DB: Commit
        end
    end
```

**Important design considerations:**

- Event cancellation must be committed independently of refund completion.
- Refund processing can happen asynchronously.
- A failed refund must be retryable.
- Repeated event cancellation or message delivery must not create duplicate refunds.
- A cancelled event must remain cancelled even when refund processing fails.
- The exact refund uniqueness and retry strategy will be finalized during low-level design.

## 7. Important Implementation Questions

- Which concurrency-control strategy protects inventory?
- How are idempotency records committed with business operations?
- How do multiple workers safely claim expiration jobs?
- How do we enforce venue non-overlap at the database level?
- What happens if an event is published but the publisher crashes before marking it published?
- How do refund workers claim and retry refund operations safely?
- What API status codes and error formats should be standardized?
- How will simulated payment and refund outcomes be deterministic in tests?
