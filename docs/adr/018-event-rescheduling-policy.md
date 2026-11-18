# ADR-018: Event Rescheduling Policy

- **Status:** Accepted
- **Date:** 2026-11-14

## Context

Organizers may occasionally need to change an event's scheduled time or venue.

However, rescheduling after customers have confirmed bookings can create significant operational and customer-impact consequences.

The system separates event information from venue scheduling:

- A `venue_booking` reserves a physical venue and time interval.
- An `event` is organized using a venue booking and does not directly own its venue or schedule.

Rescheduling must therefore account for both the event's booking history and the associated venue booking.

The policy should preserve organizer flexibility before customers have committed to attending while protecting customers once bookings have been confirmed.

## Decision

### Rescheduling eligibility

An event may be rescheduled only if:

- It has never had a confirmed booking.
- It has no valid, unexpired Held reservations.
- It has not been cancelled.

Once an event has had its first confirmed booking, rescheduling is permanently prohibited.

This remains true even if that booking is later refunded or otherwise no longer active.

The system must preserve the fact that a confirmed booking has existed. It must not infer rescheduling eligibility only from the current count of confirmed bookings.

### Rescheduling while Held reservations exist

Rescheduling is prohibited while the event has any valid, unexpired Held reservations.

Once all Held reservations have expired or otherwise been invalidated, rescheduling may proceed if the event has never had a confirmed booking.

### Venue booking and schedule ownership

The event does not directly own its venue or scheduled time. These belong to its associated venue booking.

Consequently, rescheduling an event requires changing its associated venue booking or otherwise establishing a new venue booking for the rescheduled event.

The venue booking must continue to satisfy the scheduling and venue availability rules defined in ADR-007 and ADR-015.

The exact lifecycle and reassignment behavior of venue bookings during event rescheduling must be defined when rescheduling is brought into implementation scope.

### Venue activation

Any destination venue must be active.

An inactive venue cannot be selected for rescheduling. If an organizer needs to use an inactive venue, the platform must reactivate it first.

### MVP scope

Event rescheduling is not supported in the MVP.

Venue bookings cannot be modified or rescheduled after creation. Organizers may cancel an event under ADR-010 and may cancel its associated venue booking only when no active event remains associated with it.

The rescheduling policy defined in this ADR is retained as the intended product behavior for a future implementation. Supporting it will require revisiting venue-booking immutability and defining the associated transactional and lifecycle behavior.

### Atomic rescheduling

When implemented, rescheduling must be performed in a database transaction.

The transaction must:

1. Verify that the event is active.
2. Verify that no confirmed booking has ever existed.
3. Verify that no valid Held reservation exists.
4. Validate the proposed schedule and duration.
5. Verify that the destination venue is active.
6. Apply the venue-booking changes required by the rescheduling operation.
7. Allow the database exclusion constraint to enforce venue availability.

If any condition fails, the entire operation must be rolled back.

An application-level availability check is not sufficient to prevent concurrent scheduling conflicts.

### Coordination with reservation and payment operations

When implemented, rescheduling must be coordinated with reservation creation and payment confirmation.

The implementation must use a consistent transaction and row-locking strategy so that:

- A reservation cannot be created concurrently in a way that bypasses the rescheduling restrictions.
- A payment confirmation cannot race with rescheduling and create a confirmed booking after the rescheduling transaction has checked eligibility.
- Two concurrent rescheduling operations cannot violate venue scheduling constraints.

The event row should be part of the coordination mechanism. All relevant operations must follow a consistent lock order to reduce deadlock risk.

### Event cancellation is separate

Rescheduling does not change the event lifecycle status.

An organizer who no longer intends to run an event may cancel it under ADR-010.

A cancelled event cannot be rescheduled or reactivated. A replacement event must be created separately.

## Consequences

### Positive

- Customers with confirmed bookings are not exposed to organizer-initiated schedule changes.
- Existing Held reservations are not disrupted by rescheduling.
- Organizers retain the ability to reschedule events before customers have committed to attending.
- Historical booking information remains meaningful.
- Database-level venue conflict enforcement protects against concurrent scheduling operations.
- The policy is established without expanding MVP implementation scope.

### Negative

- Organizers cannot correct an event's schedule after the first confirmed booking, even if the change is minor.
- Organizers must wait for valid Held reservations to expire before rescheduling.
- The application will require additional transactional coordination between event, venue booking, reservation, and payment operations when rescheduling is implemented.
- Rescheduling is unavailable in the MVP, so organizers must use cancellation and replacement workflows when a schedule change is necessary.

## Alternatives considered

### Allow rescheduling until the event begins

Rejected because customers may already have made plans based on a confirmed booking.

### Allow rescheduling when no confirmed bookings currently exist

Rejected because a booking that was previously confirmed and later refunded would disappear from the current count, losing important historical context.

### Allow rescheduling while Held reservations exist

Rejected because customers may be actively completing checkout against the existing event schedule.

### Remove rescheduling support entirely

Rejected because organizers may reasonably need to correct scheduling mistakes before customers have committed to attending. Retaining a defined policy allows the capability to be introduced later without treating it as an immediate MVP requirement.
