# ADR-014: SQL-First Database Tooling

**Status:** Accepted  
**Date:** 2026-11-14

## Context

TicketForge uses PostgreSQL as its authoritative persistent store.

The project requires explicit control over database schema, constraints, indexes, transactions, locking, and query behavior.

An ORM-based approach was considered but is not selected. The project prioritizes understanding and controlling the SQL executed against PostgreSQL.

The Go application still requires reliable database connectivity, schema migration management, and type-safe query access.

## Decision

TicketForge will adopt a SQL-first database workflow using:

- PostgreSQL for persistent storage.
- Goose for database schema migrations.
- pgx for PostgreSQL connectivity.
- sqlc for generating type-safe Go code from SQL queries.

No general-purpose ORM will be introduced initially.

### 1. SQL ownership

SQL remains the source of truth for:

- Table definitions.
- Primary and foreign keys.
- Unique constraints.
- Check constraints.
- Indexes.
- Database-specific features.
- Transactions and locking behavior.
- Application queries.

The application will not rely on an ORM to implicitly generate or modify its schema.

### 2. Schema migrations

Goose will manage schema migration history.

Migrations will be written as versioned SQL files containing explicit Up and Down sections.

Each migration represents a deliberate schema change.

Rules:

- Applied migrations must not be modified to change their historical meaning.
- Subsequent schema changes require new migrations.
- Destructive migrations must be reviewed carefully.
- Migration execution is a controlled deployment operation.
- API instances must not independently race to apply migrations at startup.
- Seed data is managed separately from schema migrations unless a particular seed is required for a schema invariant.

### 3. PostgreSQL connectivity

The application will use pgx and pgxpool for database connectivity and connection pooling.

PostgreSQL-specific functionality will be used where it provides correctness or performance benefits.

Transactions, row-level locking, conditional updates, and database constraints will be explicitly controlled by application SQL.

### 4. Query generation

sqlc will generate Go code and types from application-owned SQL queries.

Generated code is not the source of truth; the SQL query files are.

Generated code must be reproducible from the checked-in SQL and sqlc configuration.

Database schema migrations and sqlc query definitions remain separate concerns.

### 5. Dependency boundaries

Database access will be organized behind appropriate application and repository boundaries.

The domain layer must not depend directly on generated database code or PostgreSQL-specific driver types.

The application will avoid unnecessary abstractions and interfaces. Abstractions will be introduced when they provide meaningful separation or testability.

## Consequences

### Positive

- Full control over PostgreSQL-specific SQL.
- Explicit schema evolution and migration history.
- Type-safe generated query interfaces.
- Clear visibility into transaction boundaries and locking.
- No hidden ORM-generated queries or schema changes.
- Strong alignment between database design and Go application behavior.

### Negative

- SQL and query definitions must be maintained deliberately.
- Schema changes require explicit migration files.
- Generated code must be regenerated when queries change.
- Developers need to understand PostgreSQL and SQL rather than relying on ORM abstractions.

## Alternatives considered

### General-purpose ORM

Rejected for the initial project because explicit SQL control is preferred for the concurrency-sensitive booking engine.

### Handwritten Go database access without sqlc

Not selected as the default because sqlc provides useful compile-time type safety and reduces repetitive database mapping code.

### Other migration tools

golang-migrate was considered a viable alternative. Goose was selected for its convenient SQL migration workflow and Go integration.
