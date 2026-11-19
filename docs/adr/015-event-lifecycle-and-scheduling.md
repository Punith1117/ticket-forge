# ADR-015: Event Lifecycle and Scheduling

- **Status:** Accepted
- **Date:** 2026-11-14

## Context

TicketForge needs a simple event lifecycle that supports organizers creating and managing events without introducing unnecessary lifecycle complexity.

The system must preserve historical event records after cancellation and completion.

Physical venue scheduling is managed independently through venue bookings. A venue booking reserves a physical venue and its time interval, while an event represents an activity organized using that booking.

This separation allows organizers to cancel an event without releasing the venue booking, and to create another event using the same active venue booking.

## Decision

### Event lifecycle

An event has one of two persisted lifecycle statuses:

- `active`
- `cancelled`

There is no persisted `draft`, `published`, or `completed` lifecycle status.

An event is created directly with status `active` and is immediately publicly visible.

An event may transition from `active` to `cancelled`. This transition is terminal.

A cancelled event cannot be reactivated. An organizer who wants to run the event again must create a new event.

An active event appears in public event listings. A cancelled event does not.

### Event booking availability

An event has one of two persisted booking availability states:

- `open`
- `paused`

A newly created event starts with booking availability `open`.

Booking availability is independent of event lifecycle.

An event may be paused or reopened by its organizer while it remains active.

A public event may be paused without being cancelled or removed from public listings.

The effective booking availability of an event requires:

- Lifecycle status is `active`.
- Booking availability is `open`.
- The applicable ticket sales windows and inventory conditions permit reservations.

Pausing an event prevents new reservations but does not invalidate existing confirmed bookings or eligible unexpired held reservations. Existing eligible held reservations may still complete payment confirmation, provided the event has not been cancelled and the reservation remains valid.

Cancellation overrides booking availability. A cancelled event is no longer publicly visible or bookable.

### Event completion

Completion is a derived state, not a persisted lifecycle status.

An active event is considered completed when:

`current_time >= venue_booking.ends_at`

The event remains stored with lifecycle status `active`; its completion is derived from the end timestamp of its associated venue booking.

This avoids background jobs solely for changing an event's status when its scheduled end time passes.

### Event ownership and venue

Every event:

- Belongs to exactly one organizer profile.
- References exactly one existing venue booking.
- Uses the venue and scheduled interval associated with that venue booking.

An event does not directly reference a venue or maintain its own start and end timestamps.

The venue booking owns the physical venue reservation and its scheduled time interval.

A venue booking may exist without an associated event.

A venue booking can have multiple associated events over its lifetime, but at most one associated event may have lifecycle status `active` at any given time.

Cancelling an event does not cancel its associated venue booking. The venue booking remains active and may be used to create another event.

Organizers may manage only events belonging to their organizer profile.

An organizer cannot change the venue, start time, or end time of an event. These properties are determined by its associated venue booking, and venue booking rescheduling or modification is not supported in the initial application.

### Event duration

Event duration is determined by its associated venue booking.

These duration invariants should be enforced by database constraints where possible.

The duration constraints apply to venue bookings, not independently to events.

### Scheduling horizon

When a venue booking is created, its start time must be:

- At least 15 minutes after the current time.
- No more than one calendar year after the current time.

The one-year limit uses calendar arithmetic, not a fixed 365-day duration.

Boundary comparisons are inclusive: a start time exactly 15 minutes ahead or exactly one calendar year ahead is permitted.

Time-dependent validation must be performed by the application at the time of the write. It must not be implemented using a PostgreSQL `CHECK` constraint involving `now()`, because such constraints are not re-evaluated as time passes.

Venue bookings cannot be rescheduled in the initial application. Therefore, the scheduling horizon is evaluated when a venue booking is created, not when an event is created or modified.

### Time representation

Venue booking start and end timestamps are stored using PostgreSQL `timestamptz`.

The application should use timezone-aware timestamps and normalize comparisons to instants.

The associated venue has an IANA timezone for displaying local venue booking and event times. This does not change the fact that scheduling comparisons are performed using absolute timestamps.

### Venue occupancy

A venue booking occupies the half-open interval:

`[starts_at, ends_at)`

Two active venue bookings at the same venue must not have overlapping intervals.

A venue booking ending at the exact instant another venue booking begins does not overlap.

Only venue booking start and end times determine venue occupancy. No separate setup or cleanup interval is reserved.

The database enforces the no-overlap invariant through the exclusion constraint described in ADR-007.

Only venue bookings with status `active` participate in scheduling conflict checks.

Cancelling an event does not release the associated venue booking's time slot. The venue booking continues to occupy its scheduled interval until the venue booking itself is cancelled.

A venue booking may only be cancelled when it has no associated active event.

## Consequences

### Positive

- A small event lifecycle is easier to understand and enforce.
- Event creation has a straightforward meaning: the event becomes publicly visible.
- Booking availability remains independent of event lifecycle.
- No separate publication operation or visibility state is needed.
- No background job is needed to persist an event's completion.
- Terminal cancellation preserves historical meaning.
- Venue bookings and events have separate responsibilities and lifecycles.
- Cancelling an event does not unnecessarily release its venue booking.
- A venue booking can be reused for another event after its previously associated active event is cancelled.
- Physical venue scheduling remains independently enforceable at the database level.

### Negative

- Organizers must create an event only when they are ready for it to become publicly visible.
- The application must validate time-dependent scheduling rules when creating venue bookings.
- The application must maintain consistent lifecycle and booking availability transitions.
- The database and application must consistently use timezone-aware timestamps.
- Venue booking and event lifecycle management require separate domain logic.

## Alternatives considered

### Draft, published, and completed lifecycle states

Rejected for the MVP because publication and completion do not need to be represented as lifecycle states.

Events are created directly as active and publicly visible. Completion is derived from the associated venue booking's end timestamp.

### Separate private and public visibility

Rejected because organizers are expected to create events only when they are ready to make them public.

A separate visibility state and publication transition would introduce additional complexity without providing a required product capability.

### Persisted completed status

Rejected because completion can be derived reliably from the associated venue booking's `ends_at`, avoiding background status updates.

### Fixed 365-day scheduling horizon

Rejected because the policy is one calendar year, not a fixed number of days.
