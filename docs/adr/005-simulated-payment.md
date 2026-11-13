# ADR-005: Use Simulated Payment in the Initial Release

- Status: Accepted
- Date: 2026-11-13

## Context

The initial project focuses on:

- Inventory correctness.
- Reservation lifecycle.
- Transaction management.
- Idempotency.
- Reliable asynchronous processing.

A real payment gateway introduces external credentials, provider callbacks, financial reconciliation, and additional failure scenarios.

## Decision

Use a simulated payment component with controllable success and failure outcomes.

Do not move real money or integrate a real payment provider in the initial release.

## Alternatives Considered

1. Integrate a real payment gateway immediately.
2. Confirm bookings without modeling payment.

## Consequences

### Positive

- Repeatable success and failure tests.
- Focus on booking and inventory correctness.
- No real payment credentials or money movement.

### Negative

- Does not demonstrate real payment-provider integration.
- Future integration requires redesigning the confirmation workflow around external calls, callbacks, uncertain outcomes, and reconciliation.

## Revisit When

Real payment integration becomes an explicit product requirement.
