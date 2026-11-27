# ADR-005: Use a Simulated Payment Provider in the Initial Release

- Status: Accepted
- Date: 2026-11-27

## Context

The initial release of TicketForge prioritizes correctness and reliability in its reservation and booking lifecycle.

The primary engineering concerns are:

- Inventory allocation and consistency.
- Reservation creation, expiration, and lifecycle management.
- Transaction boundaries and concurrency control.
- Idempotent operations.
- Reliable handling of asynchronous payment outcomes.

Integrating a real payment gateway at this stage would introduce additional complexity, including provider-specific APIs, credentials, webhook verification, external service failures, payment reconciliation, and refund handling.

These concerns are important for a production ticketing platform, but introducing them before the core reservation and payment lifecycle is established would increase the scope of the initial release.

At the same time, simply bypassing payment or directly modifying reservation state to simulate successful payments would fail to exercise the asynchronous nature of real payment processing.

## Decision

TicketForge will use a simulated payment provider for the initial release, behind a provider-independent payment abstraction.

The following architectural principles will govern the implementation:

1. **Provider independence:** The payment domain will depend on a provider abstraction rather than a specific payment gateway. Provider-specific communication and verification details will remain isolated within their respective adapters.

2. **Simulated provider:** The initial implementation will provide a simulated payment provider capable of creating checkout sessions and delivering payment outcomes. It will exercise the same application-facing integration boundary expected of a real provider.

3. **Asynchronous payment outcomes:** Payment outcomes will be communicated through the webhook integration rather than by directly modifying application state from the checkout-session creation flow. This preserves the asynchronous nature of payment processing.

4. **Reservation lifecycle independence:** Creating a checkout session will not extend, reset, or otherwise modify the reservation's expiration deadline. Payment attempts and reservation holds have separate lifecycles.

5. **Checkout session reuse and single active attempt:** A reservation may have multiple historical payment attempts, but it must have at most one active, potentially payable checkout session at a time (`initializing` or `pending`). Calling the checkout initiation endpoint while a pending session exists returns the existing session rather than creating duplicate payable sessions. A new attempt is only permitted once the previous attempt is definitively `failed` or `expired`.

6. **Durable session initialization and recovery:** Checkout session creation uses a durable `initializing` state with claim tokens and lease expirations. Abandoned or ambiguous initialization attempts are never expired solely by timeout; they are recovered and reconciled with the provider using the payment attempt ID as the idempotency key.

7. **No real money movement:** The simulated provider will not initiate actual financial transactions. Its purpose is to exercise the application's payment integration and lifecycle handling.

8. **Separation of payment and booking confirmation:** Recording a successful payment outcome and confirming a booking are distinct operations. Payment success alone must not implicitly bypass the reservation's eligibility checks or inventory consistency requirements. Confirmed bookings explicitly record the settling `payment_attempt_id`.

9. **Late payment reconciliation and automatic refunds:** A verified payment success notification for an expired, cancelled, or otherwise ineligible reservation must still record the payment collection as `succeeded` (including `expired -> succeeded` reconciliation). It must not confirm the reservation or create a booking; instead, it automatically triggers an idempotent refund against that successful payment.

10. **Refund lifecycle, retry, and over-refund prevention:** Refunds are associated with a payment attempt rather than strictly one-per-booking, supporting multiple partial refunds or late-payment refunds without a booking. The invariant `SUM(successful and pending refunds) <= collected amount` is enforced transactionally. Asynchronous refund processing supports worker lease claiming (`FOR UPDATE SKIP LOCKED`), retry with backoff, and terminal state tracking without altering the payment attempt's `succeeded` status.

## Scope

The simulated-payment implementation establishes the provider abstraction, checkout-session creation and reuse, asynchronous payment outcome delivery, idempotent webhook processing, late-payment reconciliation, and asynchronous refund handling.

Production payment gateway integrations with real external financial institutions will be introduced in subsequent milestones using additional provider adapters conforming to this architecture.

## Alternatives Considered

### Integrate a real payment gateway immediately

Rejected for the initial release.

This would introduce external dependencies and financial integration complexity before the core reservation lifecycle has been sufficiently established.

### Confirm bookings without modeling payment

Rejected.

This would avoid payment integration complexity but would not exercise asynchronous payment outcomes or establish an architecture suitable for integrating real payment providers later.

### Simulate payment outcomes through direct in-process callbacks

Rejected.

Direct callbacks would bypass the external communication boundary and would not exercise webhook authentication, delivery, or duplicate-event handling. The simulated provider should exercise the same webhook integration used by future real providers.

## Consequences

### Positive

- The core reservation and inventory lifecycle can be developed without introducing real financial transactions.
- Payment integration remains independent of the specific provider.
- Asynchronous payment processing can be exercised from the beginning.
- Webhook delivery, authentication, and idempotency can be tested under controlled conditions.
- Future payment gateway integrations can be introduced through additional provider adapters.
- The architecture establishes a clear separation between payment outcomes and booking confirmation.

### Negative

- The simulated provider and its supporting infrastructure must be maintained.
- Simulated payment outcomes do not reproduce every failure mode or operational characteristic of real payment gateways.
- A real provider integration will still require provider-specific security, reconciliation, and operational work.
- Payment success and booking confirmation require separate lifecycle handling, adding complexity that must be addressed in subsequent milestones.
