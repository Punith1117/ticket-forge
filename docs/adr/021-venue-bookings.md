# ADR-021: Venue Bookings

- **Status:** Accepted
- **Date:** 2026-11-19

## Context

TicketForge needs a way for organizers to reserve physical venues independently of creating and managing events.

A venue booking represents the reservation of a physical venue for a specific time interval. An event represents an activity organized using that reservation.

Separating these responsibilities allows organizers to:

- Reserve a venue before deciding what event to organize.
- Create an event using an existing venue booking.
- Cancel an event without automatically releasing its venue booking.
- Create another event using the same active venue booking after cancelling the previous event.

Venue bookings must also preserve physical venue scheduling constraints, organizer booking limits, and historical records.

## Decision

### Venue booking ownership

Every venue booking:

- Belongs to exactly one organizer profile.
- References exactly one platform-managed venue.
- Owns its scheduled start and end timestamps.
- Has an independent lifecycle status.

Organizers may manage only venue bookings belonging to their organizer profile.

A venue booking is independent of any event. It may exist without an associated event.

An event must reference exactly one existing venue booking, as defined in ADR-015.

### Venue booking information

A venue booking requires only:

- A destination venue.
- A start time.
- A duration.

The end time is derived from the start time and duration.

No event-specific information, such as event name, description, or ticket configuration, belongs to a venue booking.

The application stores the resulting start and end timestamps as `starts_at` and `ends_at`.

### Venue booking lifecycle

A venue booking has one of two persisted lifecycle statuses:

- `active`
- `cancelled`

A newly created venue booking starts with status `active`.

An active venue booking may transition to `cancelled` under the cancellation rules defined in this ADR.

Cancellation is terminal. A cancelled venue booking cannot be reactivated.

An organizer who needs the same venue and time interval again must create a new venue booking, subject to the applicable scheduling rules and availability constraints.

There is no persisted `completed` status.

A venue booking is considered finished when:

`current_time >= ends_at`

The venue booking remains stored with status `active`. Its completion is derived from its end timestamp.

### Venue booking creation

An organizer may create a venue booking by selecting an active venue and providing a valid start time and duration.

The organizer must have an eligible organizer profile.

The venue must be active when the venue booking is created.

The requested schedule must satisfy all duration and scheduling horizon constraints defined in ADR-015.

The requested interval must not overlap another active venue booking at the same venue.

Venue booking creation must also respect the organizer's active unfinished venue booking limit.

The booking must be created transactionally so that organizer limits and venue scheduling constraints cannot be bypassed by concurrent requests.

### Organizer venue booking limit

An organizer profile may have at most **10 active, unfinished venue bookings** at any given time.

A venue booking counts toward this limit when:

- Its status is `active`.
- Its `ends_at` timestamp is later than the current time.

The limit is evaluated when creating a new venue booking.

Once a venue booking has finished, it no longer counts toward the organizer's limit, even though its persisted status remains `active`.

The limit must be enforced transactionally to prevent concurrent booking creation from exceeding the allowed number.

The organizer profile row should participate in the locking mechanism used to coordinate concurrent venue booking creation.

Because each venue booking may have at most one associated active event, this limit also indirectly limits an organizer to at most 10 active, unfinished events.

### Venue scheduling constraints

A venue booking must satisfy the following duration limits:

- Minimum duration: **15 minutes**.
- Maximum duration: **7 days**.

The following must hold:

`starts_at < ends_at`

`ends_at - starts_at >= 15 minutes`

`ends_at - starts_at <= 7 days`

When a venue booking is created, its start time must be:

- At least 15 minutes after the current time.
- No more than one calendar year after the current time.

The one-year limit uses calendar arithmetic, not a fixed 365-day duration.

Boundary comparisons are inclusive: a start time exactly 15 minutes ahead or exactly one calendar year ahead is permitted.

The application validates time-dependent scheduling rules at the time of creation. Such rules must not be implemented using PostgreSQL `CHECK` constraints involving `now()`.

These scheduling constraints are defined in ADR-015.

### Venue occupancy

A venue booking occupies the half-open interval:

`[starts_at, ends_at)`

Two active venue bookings at the same venue must not have overlapping intervals.

A venue booking ending at the exact instant another venue booking begins does not overlap.

Only venue booking start and end times determine venue occupancy. No separate setup or cleanup interval is reserved.

The database enforces the no-overlap invariant through the exclusion constraint described in ADR-007.

Only venue bookings with status `active` participate in scheduling conflict checks.

Cancelling a venue booking releases its reserved time interval.

### Venue activation

Only active venues may be selected when creating new venue bookings.

Deactivating a venue does not automatically cancel or invalidate its existing venue bookings.

Existing active venue bookings remain valid and continue to occupy their scheduled intervals, even if their associated venue later becomes inactive.

A venue must be active again before it can be selected for new venue bookings.

### Venue booking and event relationship

A venue booking may exist without an associated event.

An event must reference exactly one existing venue booking.

A venue booking may have multiple associated events over its lifetime, but at most one associated event may have lifecycle status `active` at any given time.

This allows an organizer to cancel an event and create another event using the same active venue booking.

Creating or cancelling an event does not automatically change the lifecycle status of its associated venue booking.

The venue booking continues to reserve its scheduled interval until the venue booking itself is cancelled.

Event lifecycle, visibility, and booking availability are managed independently according to ADR-015 and ADR-016.

### Venue booking cancellation

An organizer may cancel an active venue booking only when it has no associated event with lifecycle status `active`.

If an active event is associated with the venue booking, the organizer must cancel that event first under ADR-010.

Cancelling an event does not automatically cancel the venue booking.

Once the associated active event has been cancelled, the organizer may separately cancel the venue booking.

A cancelled venue booking:

- Releases its reserved venue time interval.
- Cannot be reactivated.
- Cannot be used to create another event.
- Remains stored for historical and audit purposes.

If the organizer wants to organize another event, a new venue booking must be created.

### Venue booking modifications and rescheduling

Venue bookings cannot be modified or rescheduled after creation in the MVP.

Organizers cannot change:

- The destination venue.
- The start time.
- The end time.
- The duration.

If an organizer needs a different venue or schedule, the existing venue booking must first be eligible for cancellation under the cancellation rules.

The organizer may then create a new venue booking with the desired venue and schedule, subject to availability and scheduling constraints.

Event rescheduling is also not supported in the MVP, as defined in ADR-018.

### Venue capacity

Venue capacity represents the maximum physical capacity of the venue.

Ticket inventory allocated to an event associated with a venue booking must respect the physical capacity of the venue.

The sum of ticket category capacities for an active event associated with an active venue booking must not exceed the capacity of the associated venue.

Venue bookings without an associated event do not allocate ticket inventory.

Venue capacity and ticket inventory allocation rules are defined in ADR-017.

## Consequences

### Positive

- Venue reservations and events have clearly separated responsibilities.
- Organizers can reserve venues independently of event creation.
- Organizers can prepare events using existing venue bookings.
- Cancelling an event does not unnecessarily release its venue reservation.
- An active venue booking can be reused for another event after its previously associated active event is cancelled.
- Venue scheduling constraints are enforced independently of event lifecycle.
- The organizer booking limit is enforced at the venue booking level.
- Historical venue booking records remain available after cancellation and completion.
- Minimal venue booking information avoids mixing event-specific metadata with physical venue reservations.

### Negative

- Venue booking and event lifecycle management require separate domain logic.
- Organizers must manage venue booking cancellation separately from event cancellation.
- Organizers cannot modify or reschedule venue bookings in the MVP.
- The application must enforce organizer booking limits transactionally.
- Creating an event requires an existing venue booking.
- The system must maintain consistency between venue bookings, associated events, and ticket inventory.

## Alternatives considered

### Store venue and schedule directly on events

Rejected because venue reservation and event organization are separate responsibilities.

An organizer may need to reserve a venue before creating an event, and cancelling an event should not necessarily release the venue reservation.

### Automatically cancel venue bookings when events are cancelled

Rejected because venue bookings may be reused for another event.

Event cancellation and venue booking cancellation are separate operations with independent lifecycle rules.

### Allow multiple active events per venue booking

Rejected because a single venue booking represents one reserved physical venue and time interval. Allowing multiple active events to use the same booking would introduce unnecessary complexity around scheduling and physical capacity.

### Allow venue booking modifications in the MVP

Rejected to keep the initial implementation simple and avoid the additional transactional coordination required for rescheduling venue reservations.
