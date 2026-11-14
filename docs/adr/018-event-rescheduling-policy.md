# ADR-018: Event Rescheduling Policy

- **Status:** Accepted
- **Date:** 2026-11-14

## Context

Organizers may occasionally need to change an event's scheduled time or venue.

However, rescheduling after customers have confirmed bookings can create significant operational and customer-impact consequences.

Rescheduling must also preserve venue scheduling constraints and avoid races with payment confirmation and reservation creation.

## Decision

### Rescheduling before confirmed bookings

An organizer may reschedule an event while it has never had a confirmed booking.

The new schedule must satisfy all event scheduling rules in ADR-015:

- Start time must be at least 15 minutes in the future.
- Start time must be no more than one calendar year in the future.
- Duration must be at least 15 minutes.
- Duration must not exceed 7 days.
- The destination venue must be active.
- The new interval must not overlap another active event at the destination venue.

The same rules apply whether the organizer changes the time, the venue, or both.

### Rescheduling after confirmed bookings

Rescheduling is permanently prohibited once the event has had its first confirmed booking.

This remains true even if that booking is later refunded or otherwise no longer active.

The system must preserve the fact that a confirmed booking has existed. It must not infer rescheduling eligibility only from the current count of confirmed bookings.

### Rescheduling while Held reservations exist

Rescheduling is also prohibited while the event has any valid, unexpired Held reservations.

This avoids changing the event schedule while customers are actively completing checkout.

Once all Held reservations have expired or otherwise been invalidated, rescheduling may proceed if the event has never had a confirmed booking.

### Venue activation

The destination venue must be active.

An inactive venue cannot be selected for rescheduling.

If an organizer needs to move an event to an inactive venue, the platform must reactivate that venue first.

### Atomic rescheduling

Rescheduling must be performed in a database transaction.

The transaction must:

1. Verify that the event is active.
2. Verify that no confirmed booking has ever existed.
3. Verify that no valid Held reservation exists.
4. Validate the new time and duration.
5. Verify that the destination venue is active.
6. Apply the new venue and schedule.
7. Allow the database exclusion constraint to enforce venue availability.

If any condition fails, the entire operation must be rolled back.

An application-level availability check is not sufficient to prevent concurrent scheduling conflicts.

### Coordination with reservation and payment operations

Rescheduling must be coordinated with reservation creation and payment confirmation.

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

- Customers with confirmed bookings are not exposed to organizer-initiated schedule changes in the MVP.
- Existing Held reservations are not disrupted by rescheduling.
- Historical booking information remains meaningful.
- Database-level venue conflict enforcement protects against concurrent scheduling operations.

### Negative

- Organizers cannot correct the schedule after the first confirmed booking, even if the change is minor.
- Organizers must wait for valid Held reservations to expire before rescheduling.
- The application must coordinate rescheduling with reservation and payment operations through transactions and locking.

## Alternatives considered

### Allow rescheduling until the event begins

Rejected because customers may already have made plans based on a confirmed booking.

### Allow rescheduling when no confirmed bookings currently exist

Rejected because a booking that was previously confirmed and later refunded would disappear from the current count, losing important historical context.

### Allow rescheduling while Held reservations exist

Rejected because customers may be actively completing checkout against the old event schedule.
