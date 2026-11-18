# ADR-016: Booking Availability Controls

- **Status:** Accepted
- **Date:** 2026-11-14

## Context

An event's lifecycle, visibility, and booking availability represent different concerns.

An event can exist and remain active while its organizer temporarily stops accepting new reservations. For example, the organizer may need to pause sales while reviewing event arrangements.

An event may also remain private while its organizer prepares it for publication.

Using event lifecycle status to represent booking availability or visibility would mix separate responsibilities and make it harder to manage these states independently.

The system must allow organizers to pause and resume new reservations without cancelling an event or changing its visibility.

## Decision

### Separate booking availability state

Each event has a separate persisted booking availability state:

- `open`
- `paused`

The default state is `open`.

A newly created event starts with:

- Lifecycle status: `active`
- Visibility: `private`
- Booking availability: `open`

Booking availability is independent of event lifecycle status and visibility.

Pausing bookings does not cancel the event or change its visibility.

A private event may have booking availability set to `open`. This does not make it publicly bookable.

### Meaning of open

`open` means the organizer has not manually paused bookings.

It does not guarantee that reservations can currently be created.

Effective booking availability is derived from multiple conditions:

1. The event must be active.
2. The event must have public visibility.
3. Booking availability must be `open`.
4. The sales window must have started.
5. The sales window must not have ended.
6. Sufficient inventory must be available.

If any of these conditions is not satisfied, a new reservation must not be created.

Publishing an event is a single user-facing action that changes its visibility to `public` and sets its booking availability to `open`.

Publication requires the event to satisfy the applicable publication requirements described in ADR-015.

Once an event becomes public, it cannot become private again. Booking availability can subsequently be paused or reopened independently.

### Meaning of paused

When booking availability is `paused`:

- New reservation creation is rejected.
- Existing Held reservations remain valid until their original expiration.
- Eligible Held reservations may still complete payment.
- Reservation expiration continues normally.
- Inventory release continues normally.
- Pausing does not extend reservation expiration timestamps.
- Event visibility remains unchanged.
- The event itself remains active.

This allows an organizer to stop new customers from entering checkout without disrupting customers who already obtained a valid reservation.

A public event with paused booking availability remains publicly visible but does not accept new reservations.

### Effective availability

The application should derive an appropriate availability result using the following precedence:

| Condition | Effective availability |
| --- | --- |
| Event is cancelled | Cancelled |
| Event is private | Private |
| Booking state is paused | Paused |
| Current time is before sales start | Not started |
| Current time is at or after sales end | Ended |
| No inventory remains | Sold out |
| Otherwise | Available |

The exact API response may use suitable names, but it must preserve these distinctions.

The event's persisted lifecycle status, visibility, and booking availability state must not be overwritten merely to represent derived availability.

The effective availability is not itself a persisted event state.

### Event cancellation precedence

Event cancellation overrides visibility and booking availability.

A cancelled event:

- Is no longer publicly visible.
- Cannot accept new reservations.
- Cannot confirm existing Held reservations, even if its booking state is `open` and a reservation has not expired.

Outstanding Held reservations are invalidated according to ADR-008 and ADR-010.

Existing confirmed reservations remain part of the event's booking history and are handled according to the cancellation and refund policies.

Cancelling an event does not automatically cancel its associated venue booking. The venue booking remains active.

### Payment confirmation

Payment confirmation must validate the reservation's eligibility and event lifecycle state.

It must not reject an otherwise eligible Held reservation merely because the event's booking availability is `paused`.

It must reject confirmation if:

- The event has been cancelled.
- The reservation has expired.
- The reservation has otherwise become ineligible.

Event visibility and booking availability must not independently invalidate an already valid Held reservation.

In particular, pausing bookings does not prevent an eligible Held reservation from completing payment.

### Concurrency

Changing booking availability and creating reservations must be coordinated so that a reservation cannot slip through a pause transition because of an unchecked application-level race.

The application must use appropriate transactional locking and state validation.

The reservation creation transaction must validate the event's lifecycle status, visibility, booking availability, sales window, and inventory before creating a reservation.

The pause transition and reservation creation must coordinate through consistent locking so that their outcomes are deterministic.

## Consequences

### Positive

- Organizers can temporarily stop new bookings without cancelling an event.
- Organizers can prepare an event privately after creating a venue booking, without making it publicly visible or bookable.
- Public events can remain visible while bookings are paused.
- Existing customers retain their original checkout window.
- Event lifecycle, visibility, and booking availability remain independent.
- Effective availability can communicate useful states such as private, not started, paused, sold out, and ended.

### Negative

- The application must derive effective availability from multiple fields and inventory.
- Reservation creation and payment confirmation require careful state validation.
- API clients must distinguish persisted event state from effective availability.
- Event publication must update visibility and booking availability together.
- Reservation creation and booking availability changes require transactional coordination.

## Alternatives considered

### Use event status to represent a booking pause

Rejected because pausing sales does not mean that the event itself has been cancelled or completed.

### Invalidate Held reservations when bookings are paused

Rejected because pausing should affect new reservations only.

### Treat `open` as always bookable

Rejected because event visibility, sales windows, inventory exhaustion, and event cancellation can independently prevent new reservations.

### Force private events to have paused booking availability

Rejected because visibility and booking availability represent separate concerns.

A private event can retain `open` as its booking availability state while remaining unavailable for public reservations due to its private visibility.
