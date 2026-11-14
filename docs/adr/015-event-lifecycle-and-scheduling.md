# ADR-015: Event Lifecycle and Scheduling

- **Status:** Accepted
- **Date:** 2026-11-14

## Context

TicketForge needs a simple event lifecycle that supports organizers creating and managing events without introducing unnecessary publication states.

The system must also enforce reasonable event duration and scheduling boundaries. Venue scheduling must remain correct under concurrent event creation and rescheduling.

A completed event should remain part of the historical record without requiring a separate lifecycle transition to be persisted.

## Decision

### Event lifecycle

An event has one of two persisted lifecycle statuses:

- `active`
- `cancelled`

There is no `draft`, `published`, or persisted `completed` status.

An event is created directly with status `active`.

An event may transition from `active` to `cancelled`. This transition is terminal.

A cancelled event cannot be reactivated. An organizer who wants to run the event again must create a new event.

### Event completion

Completion is a derived state, not a persisted lifecycle status.

An active event is considered completed when:

`current_time >= ends_at`

The event remains stored with status `active`; its completion is derived from its end timestamp.

This avoids background jobs solely for changing an event's status when its scheduled end time passes.

### 3. Event ownership and venue

Every event:

- Belongs to exactly one organizer profile.
- References exactly one venue.
- Has a start timestamp and an end timestamp.
- Uses the venue's scheduled interval for conflict detection.

Organizers may manage only events belonging to their organizer profile.

### Event duration

An event must satisfy both duration limits:

- Minimum duration: **15 minutes**.
- Maximum duration: **7 days**.

The following must hold:

`starts_at < ends_at`

`ends_at - starts_at >= 15 minutes`

`ends_at - starts_at <= 7 days`

These duration invariants should be enforced by database constraints where possible.

### Scheduling horizon

When an event is created, its start time must be:

- At least 15 minutes after the current time.
- No more than one calendar year after the current time.

The one-year limit uses calendar arithmetic, not a fixed 365-day duration.

The same scheduling boundaries apply when an event is rescheduled.

Boundary comparisons are inclusive: a start time exactly 15 minutes ahead or exactly one calendar year ahead is permitted.

Time-dependent validation must be performed by the application at the time of the write. It must not be implemented using a PostgreSQL `CHECK` constraint involving `now()`, because such constraints are not re-evaluated as time passes.

### Time representation

Event start and end timestamps are stored using PostgreSQL `timestamptz`.

The application should use timezone-aware timestamps and normalize comparisons to instants.

The event's venue may have an IANA timezone for displaying local event times. This does not change the fact that scheduling comparisons are performed using absolute timestamps.

### Venue occupancy

An event occupies the half-open interval:

`[starts_at, ends_at)`

Two active events at the same venue must not have overlapping intervals.

An event ending at the exact instant another event begins does not overlap.

Only event start and end times determine venue occupancy. No separate setup or cleanup interval is reserved.

The database enforces the no-overlap invariant through the exclusion constraint described in ADR-007.

### 8. Active events without ticket inventory

An event may exist as active before ticket categories or inventory have been configured.

Being active does not by itself mean that the event is currently bookable. Booking availability is controlled separately, as described in ADR-016.

## Consequences

### Positive

- A small lifecycle is easier to understand and enforce.
- No background job is needed to persist an event's completion.
- Terminal cancellation preserves historical meaning.
- Duration limits and scheduling boundaries prevent unreasonable event configurations.
- Booking availability can evolve independently from event lifecycle.

### Negative

- An active event may not yet have ticket inventory or be bookable.
- The application must validate time-dependent scheduling rules on every create and reschedule operation.
- The database and application must consistently use timezone-aware timestamps.

## Alternatives considered

### Draft, published, and completed lifecycle states

Rejected for the MVP because the additional states introduce workflow complexity without being required by the current product requirements.

### Persisted completed status

Rejected because completion can be derived reliably from `ends_at`, avoiding background status updates.

### Fixed 365-day scheduling horizon

Rejected because the policy is one calendar year, not a fixed number of days.
