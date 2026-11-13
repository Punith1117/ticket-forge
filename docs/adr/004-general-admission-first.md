# ADR-004: Implement General Admission Before Assigned Seating

- Status: Accepted
- Date: 2026-11-13

## Context

Assigned seating introduces:

- Seat maps.
- Individual seat inventory.
- Seat selection.
- Seat-level reservation holds.
- Contention for individual seats.

The initial project should first establish reliable quantity-based inventory allocation.

## Decision

The initial release supports general-admission ticket categories with finite capacities.

Assigned seating will be implemented as a later enhancement.

## Alternatives Considered

1. Implement general admission and assigned seating together.
2. Implement assigned seating only.

## Consequences

### Positive

- Smaller initial domain model.
- Simpler initial database design.
- Focus on transactional inventory allocation.
- Assigned seating can build on the reservation lifecycle and idempotency concepts.

### Negative

- Initial release does not support seat selection.
- Later assigned-seating implementation will require schema and workflow extensions.

## Revisit When

The general-admission booking flow is correct, tested under concurrency, and stable enough to support a separate assigned-seating design.
