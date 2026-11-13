# ADR-001: Start with a Modular Monolith

- Status: Accepted
- Date: 2026-11-13

## Context

TicketForge contains several related business capabilities:

- Identity and access.
- Event and venue management.
- Inventory.
- Reservations and bookings.
- Payment simulation.
- Asynchronous processing.

The project needs to demonstrate backend engineering without introducing distributed-system complexity prematurely.

## Decision

Start with a modular monolith in one Go codebase.

Keep business responsibilities separated into cohesive internal modules.

Run the HTTP API and background worker as separate process entry points when background processing is introduced.

## Alternatives Considered

1. Microservices from the beginning.
2. A single unstructured application package.
3. Separate repositories for API and worker.

## Consequences

### Positive

- Easier local development and debugging.
- Database transactions can span related business operations.
- Fewer network and deployment failure modes.
- Modules can be extracted later if justified.

### Negative

- Module boundaries require discipline.
- Modules cannot initially be scaled independently.
- Shared database ownership must be managed carefully.

## Revisit When

A module has a demonstrated independent scaling, deployment, ownership, or reliability requirement that justifies extraction.
