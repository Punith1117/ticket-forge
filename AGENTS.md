# TicketForge — Agent Instructions

## Project Context

TicketForge is a high-concurrency ticket booking engine
written in Go.

Architecture:

- Modular monolith
- PostgreSQL as the authoritative data store
- REST API
- Separate API and worker entry points
- SQL-first database workflow

## Engineering Principles

- Prefer simple, idiomatic Go.
- Avoid unnecessary abstractions.
- Do not introduce microservices.
- Do not introduce an ORM.
- Do not introduce Redis or a message broker prematurely.
- Preserve transactional correctness.
- Use PostgreSQL constraints to enforce data integrity.
- Keep business logic separate from transport concerns.

## Database

- PostgreSQL 18.
- Goose for SQL migrations.
- pgx/v5 for database connectivity.
- sqlc for type-safe query generation.
- SQL is the source of truth.

## Security

- Never commit secrets.
- Never store plaintext passwords.
- Use Argon2id for password hashing.
- Use opaque stateful sessions.
- Enforce resource ownership.

## Development Rules

- Read existing documentation before changing architecture.
- Do not modify accepted ADR decisions without justification.
- Never modify an already-applied migration.
- Do not generate large implementations without approval.
- Explain architectural decisions and trade-offs.
- Run relevant tests after implementation.
- Report files changed, commands executed, and verification results.

## Collaboration

Before implementing a task:

1. Inspect the existing codebase.
2. Identify relevant design documents.
3. Explain the proposed changes.
4. Identify unresolved decisions.

Wait for approval when the task involves architectural or schema changes.
