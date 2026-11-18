# ADR-007: Platform-Managed Venue Catalog

**Status:** Accepted  
**Date:** 2026-11-14

## Context

If every organizer can create arbitrary venue records, the system cannot reliably identify that two venue bookings refer to the same physical location.

For example, two organizers could create separate venue records for the same auditorium. The system would then fail to recognize scheduling conflicts between bookings for that location.

We need a canonical representation of physical venues that supports consistent scheduling, capacity enforcement, and controlled venue management.

## Decision

Venues are managed by the platform, not created or modified by organizers through the initial application.

The platform maintains a shared venue catalog.

Organizers can:

- Browse or search existing venues.
- Select an active venue when creating a venue booking.
- Reserve available time slots at venues in the catalog.

Each venue has a stable identity and stores relevant information, including:

- Name.
- Structured address.
- IANA timezone.
- Physical capacity.
- Activation state.

### Venue address

Venue addresses are stored using separate structured fields rather than a single unstructured address string.

The initial model includes:

- `street_address`
- `city`
- `state`
- `postal_code`
- `country_code`

Additional address fields may be introduced when required.

The `country_code` should use the ISO 3166-1 alpha-2 country code format.

The venue timezone is stored as an IANA timezone identifier, such as `Asia/Kolkata`.

Venue booking timestamps are stored using PostgreSQL `timestamptz`. The venue timezone is used for displaying and interpreting local venue times, not for replacing absolute timestamp comparisons.

### Event relationship

Each venue booking:

- References exactly one existing venue.
- Belongs to exactly one organizer profile.

Each event:

- References exactly one existing venue booking.
- Belongs to exactly one organizer profile through that booking.

A venue can have multiple venue bookings, provided their scheduled time intervals do not overlap.

A venue booking can exist without an associated event. An organizer may create an event using an existing active venue booking.

A venue booking can have multiple associated events over its lifetime, but at most one associated event may have lifecycle status `active` at any given time.

Cancelling an event does not cancel its associated venue booking. The booking remains active and can be used to create another event.

Event completion is derived from its end timestamp and does not require a separate persisted lifecycle status. Venue booking completion is likewise derived from its end timestamp.

### Venue Scheduling Invariant

For two active venue bookings A and B at the same venue, their intervals must not overlap.

An overlap exists when:

`A.starts_at < B.ends_at AND B.starts_at < A.ends_at`

Intervals use half-open semantics:

`[start, end)`

Therefore, a venue booking ending at 15:00 and another beginning at 15:00 do not overlap.

The invariant must be enforced safely under concurrent venue booking creation. An application-level availability check alone is insufficient.

The database must enforce this rule using a PostgreSQL exclusion constraint backed by GiST and `btree_gist`.

Conceptually:

- Venue bookings must reference the same `venue_id` to conflict.
- Their timestamp ranges must overlap.
- Only venue bookings with status `active` participate in the constraint.

Application-level availability checks may provide earlier feedback, but they are not the source of correctness. The database constraint is authoritative and must prevent conflicting concurrent writes.

Event lifecycle status does not determine whether a venue time slot is occupied. Cancelling an event does not release its associated venue booking's time slot.

A venue booking releases its time slot only when the venue booking itself is cancelled. Venue booking cancellation is permitted only when it has no associated active event.

Venue bookings cannot be rescheduled or modified to change their venue, start time, or end time through the initial application.

### Venue Lifecycle

A venue has an activation state represented by `is_active`.

- A newly created venue is active unless explicitly created otherwise.
- Only active venues can be selected for new venue bookings.
- An inactive venue cannot be selected for new venue bookings.
- Inactivating a venue does not cancel its existing venue bookings or associated events.
- Existing venue bookings at an inactive venue remain valid and continue to occupy their scheduled intervals, even if they have no associated active event.
- A venue can be reactivated by the platform.
- Reactivation makes it available for future venue bookings, subject to normal scheduling constraints.

Venue deactivation is not venue booking cancellation or event cancellation.

Venues are not physically deleted when they have been referenced by venue bookings. Referenced venues must be protected by database referential integrity, using restrictive deletion behavior.

### Venue Capacity Invariant

A venue has a maximum physical capacity.

For every active event associated with an active venue booking at a venue:

`SUM(ticket_category.capacity) <= venue.capacity`

The total allocated ticket inventory across all ticket categories of an active event must not exceed the physical capacity of the venue.

A venue booking can exist without an event, and an event can be created without ticket categories while it remains private. Therefore, venue capacity validation applies when ticket categories are allocated to an event, rather than requiring every venue booking to have an event or every event to have ticket inventory.

For example, an event associated with a booking at a venue with a capacity of 1,000 cannot have:

- Standard: 700 tickets.
- VIP: 500 tickets.

Because the combined capacity is 1,200, the configuration must be rejected.

The venue's physical capacity is the only global capacity ceiling. No additional platform-wide ticket inventory limit is introduced.

Capacity changes must preserve the invariant described in ADR-017.

### Venue capacity increases

Increasing venue capacity is permitted.

This does not automatically increase the allocated ticket capacity of existing events. Additional inventory must be explicitly allocated to an event.

### Venue capacity reductions

A venue's capacity cannot be reduced below the total allocated ticket capacity of any active event associated with an active venue booking at that venue.

If the proposed capacity is lower than the allocated inventory of even one such event, the reduction must be rejected.

The check applies to allocated category capacity, not merely the number of tickets currently sold.

Capacity changes must be performed transactionally and coordinated with event and ticket category modifications to prevent concurrent operations from violating the invariant.

### Operational Management

Venue records are maintained through controlled platform operations.

The initial application does not expose public venue creation or venue editing endpoints.

Only the platform can:

- Create venues.
- Edit venue details.
- Activate or deactivate venues.
- Change venue capacity.

Organizers cannot independently modify venue information or capacity.

## Consequences

### Positive

- Venue bookings at the same physical venue share one canonical venue record.
- Structured address fields support consistent display, searching, and filtering.
- IANA timezone information supports accurate local-time presentation.
- Scheduling conflicts can be detected reliably at the venue booking level.
- Venue capacity can be used to validate event ticket capacities.
- Organizers cannot create duplicate venue records through the application.
- Inactive venues can be retired without disrupting existing venue bookings or associated events.
- Venue bookings and events have separate lifecycles and responsibilities.
- A venue booking can be reused for another event after its previously associated active event is cancelled.

### Trade-offs

- The platform must maintain the venue catalog.
- Organizers cannot independently add a new venue through the application.
- Venue catalog updates require a controlled operational process.
- Venue scheduling and capacity constraints require database-level protection.
- Capacity reductions require checking all active events associated with active venue bookings at the venue.
- Venue bookings and events require separate lifecycle management.

## Future Considerations

A future version may introduce a venue onboarding and verification workflow.

That workflow must preserve canonical venue identity and prevent duplicate physical venue records.

A future version may also introduce venue-specific operational details, such as facilities, seating layouts, accessibility information, and venue imagery. These additions must not weaken the existing scheduling and capacity invariants.
