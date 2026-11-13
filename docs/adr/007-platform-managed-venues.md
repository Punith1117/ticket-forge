# ADR-007: Platform-Managed Venue Catalog

**Status:** Accepted  
**Date:** 2026-11-13

## Context

If every organizer can create arbitrary venue records, the system cannot reliably identify that two events refer to the same physical location.

For example, two organizers could create separate venue records for the same auditorium. The system would then fail to recognize scheduling conflicts between events at that location.

We need a simple way to represent physical venues consistently.

## Decision

Venues are managed by the platform, not created or modified by organizers through the initial application.

The platform maintains a shared venue catalog.

Organizers can:

- Browse or search existing venues.
- Select a venue when creating an event.
- Schedule events at venues available in the catalog.

Each venue has a stable identity and stores relevant information, including:

- Name.
- Address.
- IANA timezone.
- Physical capacity.

Each event references exactly one existing venue and belongs to exactly one organizer.

A venue can host multiple events, provided their scheduled time intervals do not overlap.

Only non-cancelled events occupy a venue's schedule.

## Venue Scheduling Invariant

For two events A and B at the same venue, their intervals must not overlap.

An overlap exists when:

`A.starts_at < B.ends_at AND B.starts_at < A.ends_at`

Intervals use half-open semantics:

`[start, end)`

Therefore, an event ending at 15:00 and another beginning at 15:00 do not overlap.

The invariant must be enforced safely under concurrent event creation. An application-level availability check alone is insufficient.

PostgreSQL range types and exclusion constraints are a possible implementation.

## Venue Capacity Invariant

The sum of ticket-category capacities for an event must not exceed the physical capacity of its venue.

For example, an event at a venue with a capacity of 1,000 cannot have:

- Standard: 700 tickets.
- VIP: 500 tickets.

Because the combined capacity is 1,200, the configuration must be rejected.

## Operational Management

Venue records are maintained through controlled platform operations.

The initial application does not expose public venue creation or venue editing endpoints.

## Consequences

### Positive

- Events at the same physical venue share one canonical venue record.
- Scheduling conflicts can be detected reliably.
- Venue capacity can be used to validate event ticket capacities.
- Organizers cannot create duplicate venue records through the application.
- The initial domain model remains relatively simple.

### Trade-offs

- The platform must maintain the venue catalog.
- Organizers cannot independently add a new venue through the application.
- Venue catalog updates require a controlled operational process.
- Venue scheduling and capacity constraints require database-level protection.

## Future Considerations

A future version may introduce a venue onboarding and verification workflow.

That workflow must preserve canonical venue identity and prevent duplicate physical venue records.
