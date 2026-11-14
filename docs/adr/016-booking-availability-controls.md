# ADR-016: Booking Availability Controls

- **Status:** Accepted
- **Date:** 2026-11-14

## Context

An event's lifecycle and its booking availability represent different concerns.

An event can exist and remain active while its organizer temporarily stops accepting new reservations. For example, the organizer may need to pause sales while reviewing event arrangements.

Using event lifecycle status for this purpose would mix event existence with sales availability and make it harder to resume bookings without changing the event's lifecycle.

## Decision

### Separate booking availability state

Each event has a separate persisted booking availability state:

- `open`
- `paused`

The default state is `open`.

This state is independent of the event lifecycle status.

Pausing bookings does not cancel the event.

### Meaning of open

`open` means the organizer has not manually paused bookings.

It does not guarantee that reservations can currently be created.

Effective booking availability is derived from multiple conditions:

1. The event must be active.
2. Booking availability must be `open`.
3. The sales window must have started.
4. The sales window must not have ended.
5. Sufficient inventory must be available.

If any of these conditions is not satisfied, a new reservation must not be created.

### Meaning of paused

When booking availability is `paused`:

- New reservation creation is rejected.
- Existing Held reservations remain valid until their original expiration.
- Eligible Held reservations may still complete payment.
- Reservation expiration continues normally.
- Inventory release continues normally.
- Pausing does not extend reservation expiration timestamps.

This allows an organizer to stop new customers from entering checkout without disrupting customers who already obtained a valid reservation.

### Effective availability

The application should derive an appropriate availability result using the following precedence:

| Condition | Effective availability |
| --- | --- |
| Event is cancelled | Cancelled |
| Booking state is paused | Paused |
| Current time is before sales start | Not started |
| Current time is at or after sales end | Ended |
| No inventory remains | Sold out |
| Otherwise | Available |

The exact API response may use suitable names, but it must preserve these distinctions.

The event's persisted lifecycle status and booking availability state must not be overwritten merely to represent derived availability.

### Event cancellation precedence

Event cancellation overrides booking availability.

A cancelled event cannot accept new reservations or confirm existing Held reservations, even if its booking state is `open` and a reservation has not expired.

Outstanding Held reservations are invalidated according to ADR-008 and ADR-010.

### Payment confirmation

Payment confirmation must validate the reservation's eligibility and event lifecycle state.

It must not reject an otherwise eligible Held reservation merely because the event's booking availability is `paused`.

It must reject confirmation if the event has been cancelled or the reservation has expired or otherwise become ineligible.

### Concurrency

Changing booking availability and creating reservations must be coordinated so that a reservation cannot slip through a pause transition because of an unchecked application-level race.

The application must use appropriate transactional locking and state validation.

## Consequences

### Positive

- Organizers can temporarily stop new bookings without cancelling an event.
- Existing customers retain their original checkout window.
- Event lifecycle remains independent from sales operations.
- Effective availability can communicate useful states such as not started, paused, sold out, and ended.

### Negative

- The application must derive effective availability from multiple fields and inventory.
- Reservation creation and payment confirmation require careful state validation.
- API clients must distinguish persisted booking state from effective availability.

## Alternatives considered

### Use event status to represent a booking pause

Rejected because pausing sales does not mean that the event itself has been cancelled or completed.

### Invalidate Held reservations when bookings are paused

Rejected because pausing should affect new reservations only.

### Treat `open` as always bookable

Rejected because sales windows, inventory exhaustion, and event cancellation can independently prevent new reservations.
