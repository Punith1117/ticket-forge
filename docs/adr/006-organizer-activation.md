# ADR-006: Immediate Organizer Activation

**Status:** Accepted  
**Date:** 2026-11-13

## Context

TicketForge supports two capabilities:

- Customer: available to every registered user.
- Organizer: additional capabilities granted through an OrganizerProfile.

We need to decide whether a user must undergo verification or approval before becoming an organizer.

For the initial release, introducing a verification workflow would add complexity without being essential to demonstrating the booking engine's core engineering challenges.

## Decision

Organizer activation is immediate.

An authenticated user can request organizer capabilities. Once the OrganizerProfile is successfully created, the user can immediately perform organizer operations.

A user can simultaneously act as a customer and an organizer.

Each user can have at most one OrganizerProfile.

## Consequences

### Positive

- Simple onboarding and authorization model.
- No approval workflow or administrative interface is required.
- A user can organize events and book tickets using the same account.
- Keeps the initial scope focused on the booking engine.

### Trade-offs

- There is no organizer identity verification in the initial release.
- The system does not initially support organizer approval, suspension, or revocation workflows.
- Production systems may require additional trust, verification, and abuse-prevention mechanisms.

## Future Considerations

Organizer verification and moderation may be introduced later if the product requires them.

These features should not be added to the initial release without a clear requirement.
