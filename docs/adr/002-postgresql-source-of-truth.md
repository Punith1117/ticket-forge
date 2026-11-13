# ADR-002: PostgreSQL Is the Source of Truth for Inventory

- Status: Accepted
- Date: 2026-11-13

## Context

TicketForge allocates scarce inventory under concurrent requests.

Correctness must hold across multiple API instances and process restarts.

## Decision

Use PostgreSQL as the authoritative store for:

- Ticket category capacity.
- Inventory allocation.
- Reservation state.
- Booking state.

Use database transactions, constraints, and an explicit concurrency-control strategy to enforce inventory correctness.

Redis must not be the authoritative inventory store.

## Alternatives Considered

1. Redis as the primary inventory store.
2. In-memory inventory counters.
3. Eventual-consistency-only allocation.

## Consequences

### Positive

- Durable transactional state.
- Database constraints.
- Row-level concurrency controls.
- Shared correctness boundary across API instances.

### Negative

- Contention can create lock waits under flash-sale traffic.
- Transaction and query design require careful measurement.
- PostgreSQL may become a throughput bottleneck.

## Revisit When

Performance testing identifies a specific bottleneck.

Any optimization must preserve the invariant that inventory cannot be oversold.
