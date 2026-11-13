# Phase 0 — System Design

## Purpose

Define TicketForge's requirements, domain model, architecture, data model, workflows, failure behavior, and architectural decisions before implementation.

## Document Index

| Document | Purpose |
| --- | --- |
| [requirements.md](requirements.md) | Functional and non-functional requirements |
| [use-cases.md](use-cases.md) | Actors, permissions, and use cases |
| [domain-model.md](domain-model.md) | Core business concepts and relationships |
| [hld.md](hld.md) | High-level architecture |
| [er-diagram.md](er-diagram.md) | Initial relational data model |
| [state-machines.md](state-machines.md) | Reservation, booking, and payment lifecycles |
| [sequence-diagrams.md](sequence-diagrams.md) | Critical system workflows |
| [failure-analysis.md](failure-analysis.md) | Failure scenarios and recovery behavior |
| [adr/](adr/) | Architecture Decision Records |

## Design Workflow

1. Review one document at a time.
2. Resolve open questions before approving a design.
3. Record significant architectural choices as ADRs.
4. Update documentation when implementation reveals a better approach.
5. Keep the Git history honest. Never fabricate decisions or dates.

## Document Status

- Draft: Proposed, not yet approved.
- Accepted: Reviewed and agreed.
- Superseded: Replaced by a later decision.
