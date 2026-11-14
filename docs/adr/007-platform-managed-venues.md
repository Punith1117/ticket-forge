# ADR-007: Platform-Managed Venue Catalog

**Status:** Accepted  
**Date:** 2026-11-14

## Context

If every organizer can create arbitrary venue records, the system cannot reliably identify that two events refer to the same physical location.

For example, two organizers could create separate venue records for the same auditorium. The system would then fail to recognize scheduling conflicts between events at that location.

We need a canonical representation of physical venues that supports consistent scheduling, capacity enforcement, and controlled venue management.

## Decision

Venues are managed by the platform, not created or modified by organizers through the initial application.

The platform maintains a shared venue catalog.

Organizers can:

- Browse or search existing venues.
- Select an active venue when creating an event.
- Schedule events at venues available in the catalog.

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

Event timestamps are stored using PostgreSQL `timestamptz`. The venue timezone is used for displaying and interpreting local venue times, not for replacing absolute timestamp comparisons.

### Event relationship

Each event:

- References exactly one existing venue.
- Belongs to exactly one organizer profile.

A venue can host multiple events, provided their scheduled time intervals do not overlap.

Only events with lifecycle status `active` occupy a venue's schedule. Cancelled events no longer participate in scheduling conflict checks.

Event completion is derived from its end timestamp and does not require a separate persisted lifecycle status.

## Venue Scheduling Invariant

For two active events A and B at the same venue, their intervals must not overlap.

An overlap exists when:

`A.starts_at < B.ends_at AND B.starts_at < A.ends_at`

Intervals use half-open semantics:

`[start, end)`

Therefore, an event ending at 15:00 and another beginning at 15:00 do not overlap.

The invariant must be enforced safely under concurrent event creation and rescheduling. An application-level availability check alone is insufficient.

The database must enforce this rule using a PostgreSQL exclusion constraint backed by GiST and `btree_gist`.

Conceptually:

- Events must have the same `venue_id` to conflict.
- Their timestamp ranges must overlap.
- Only events with status `active` participate in the constraint.

Application-level availability checks may provide earlier feedback, but they are not the source of correctness. The database constraint is authoritative and must prevent conflicting concurrent writes.

## Venue Lifecycle

A venue has an activation state represented by `is_active`.

- A newly created venue is active unless explicitly created otherwise.
- Only active venues can be selected for new events or event rescheduling.
- An inactive venue cannot be selected for new events or rescheduling.
- Inactivating a venue does not cancel its existing events.
- Existing events at an inactive venue remain valid and continue to occupy their scheduled intervals.
- A venue can be reactivated by the platform.
- Reactivation makes it available for future event creation and rescheduling, subject to normal scheduling constraints.

Venue deactivation is not event cancellation.

Venues are not physically deleted when they have been referenced by events. Referenced venues must be protected by database referential integrity, using restrictive deletion behavior.

## Venue Capacity Invariant

A venue has a maximum physical capacity.

For every active event at a venue:

`SUM(ticket_category.capacity) <= venue.capacity`

The total allocated ticket inventory across all ticket categories must not exceed the venue's capacity.

For example, an event at a venue with a capacity of 1,000 cannot have:

- Standard: 700 tickets.
- VIP: 500 tickets.

Because the combined capacity is 1,200, the configuration must be rejected.

The venue's physical capacity is the only global capacity ceiling. No additional platform-wide ticket inventory limit is introduced.

Capacity changes must preserve the invariant described in ADR-017.

### Venue capacity increases

Increasing venue capacity is permitted.

This does not automatically increase the allocated ticket capacity of existing events. Additional inventory must be explicitly allocated to an event.

### Venue capacity reductions

A venue's capacity cannot be reduced below the total allocated ticket capacity of any active event associated with that venue.

If the proposed capacity is lower than the allocated inventory of even one active event, the reduction must be rejected.

The check applies to allocated category capacity, not merely the number of tickets currently sold.

Capacity changes must be performed transactionally and coordinated with event and ticket category modifications to prevent concurrent operations from violating the invariant.

## Operational Management

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

- Events at the same physical venue share one canonical venue record.
- Structured address fields support consistent display, searching, and filtering.
- IANA timezone information supports accurate local-time presentation.
- Scheduling conflicts can be detected reliably.
- Venue capacity can be used to validate event ticket capacities.
- Organizers cannot create duplicate venue records through the application.
- Inactive venues can be retired without disrupting existing events.
- The initial domain model remains relatively simple.

### Trade-offs

- The platform must maintain the venue catalog.
- Organizers cannot independently add a new venue through the application.
- Venue catalog updates require a controlled operational process.
- Venue scheduling and capacity constraints require database-level protection.
- Capacity reductions require checking all active events associated with a venue.

## Future Considerations

A future version may introduce a venue onboarding and verification workflow.

That workflow must preserve canonical venue identity and prevent duplicate physical venue records.

A future version may also introduce venue-specific operational details, such as facilities, seating layouts, accessibility information, and venue imagery. These additions must not weaken the existing scheduling and capacity invariants.
