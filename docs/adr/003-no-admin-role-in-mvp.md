# ADR-003: Do Not Add an Application Admin Role to the MVP

- Status: Accepted
- Date: 2026-11-13

## Context

The initial product has two primary business actors:

- Customer.
- Organizer.

A platform-wide Admin role would introduce additional workflows such as:

- User moderation.
- Organizer moderation.
- Manual booking overrides.
- Platform-wide resource management.
- Privileged operational APIs.

These workflows are not necessary to demonstrate the core booking-engine requirements.

Operational administration is still necessary, but it is different from a business role in the application.

## Decision

The initial application will not have an Admin role or general-purpose Admin API.

Every registered user can act as a Customer.

An optional Organizer profile grants additional organizer capabilities.

Platform operations will be handled through controlled deployment, database, and observability tooling.

## Alternatives Considered

1. Introduce a full Admin role in the first release.
2. Treat organizers as administrators.
3. Introduce a hidden hard-coded superuser.

## Consequences

### Positive

- Smaller authorization model.
- Clearer customer and organizer permissions.
- Less unnecessary privileged API surface.
- Greater focus on booking correctness.

### Negative

- No in-product moderation workflow.
- No manual administrative booking override.
- Operational interventions require controlled out-of-band procedures.

## Revisit When

A concrete requirement emerges for platform-wide moderation, customer support, compliance, or operational actions that cannot safely be handled outside the application.

If introduced, Admin must have narrowly scoped permissions and audit logging.
