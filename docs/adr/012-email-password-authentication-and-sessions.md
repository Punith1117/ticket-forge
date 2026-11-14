# ADR-012: Email/Password Authentication and Stateful Sessions

**Status:** Accepted  
**Date:** 2026-11-14

## Context

TicketForge requires user authentication for customers and organizers.

A user can act as a Customer and may additionally have an OrganizerProfile. Authentication must establish the user's identity, while authorization determines which resources and operations that user can access.

Two common authentication approaches were considered:

- Stateless authentication using signed JWTs.
- Stateful authentication using server-managed sessions.

JWTs can be validated without a session lookup, but immediate revocation, logout, and permission changes require additional mechanisms such as token blocklists, refresh-token records, or token-version checks.

TicketForge is initially a modular monolith with PostgreSQL as its authoritative data store. There is no requirement for independent services to validate user identities.

## Decision

TicketForge will use **email/password authentication with opaque, stateful sessions persisted in PostgreSQL**.

### 1. User credentials

- Email address is the login identifier.
- Passwords are stored only as Argon2id password hashes.
- Password hashing uses `golang.org/x/crypto/argon2`.
- Passwords are never stored or logged in plaintext.
- User identity is represented by a stable UUID, not the email address.
- Email uniqueness is enforced at the database level.
- Email verification is handled separately under ADR-013.

### 2. Session tokens

- Session tokens are cryptographically random opaque values generated using Go's `crypto/rand`.
- Tokens contain at least 256 bits of random entropy.
- The raw token is returned to the client only when the session is created.
- Only the SHA-256 hash of the token is persisted.
- The raw session token must never be logged or stored in the database.
- Session tokens are validated against persisted session records.
- Revoked and expired sessions cannot authenticate requests.

SHA-256 is appropriate for fingerprinting high-entropy random tokens. It is not used for password hashing.

### 3. Session expiration policies

Normal sessions:

- Idle timeout: 30 minutes.
- Absolute lifetime: 7 days.

Remember Me sessions:

- Idle timeout: 30 days.
- Absolute lifetime: 30 days.

A session expires when either its idle timeout or absolute lifetime is exceeded.

Idle expiration is based on the last recorded activity. Absolute expiration is based on session creation time and cannot be extended by activity.

### 4. Session lifecycle

- Login creates a new session.
- Logout revokes the current session.
- Users can revoke all their active sessions.
- Password changes revoke all existing sessions.
- Session expiration and revocation are enforced server-side.
- Session records are retained only as long as operationally necessary, with cleanup of expired records handled separately.

### 5. Token transport

For the initial REST API, clients will send the opaque session token through:

`Authorization: Bearer <session-token>`

If a browser-based client is introduced, the session credential will be transported through an appropriately configured `Secure`, `HttpOnly`, `SameSite` cookie.

Cookie-based authentication must include appropriate CSRF protection.

Long-lived authentication credentials must not be stored in browser localStorage.

### 6. Authorization

Authentication and authorization remain separate concerns.

- Authentication establishes the identity of the user.
- Authorization determines whether the user can perform an operation.
- Organizer ownership is checked against the requested resource.
- A valid session does not automatically grant access to another organizer's resources.
- Customer and OrganizerProfile capabilities are determined from persisted application state, not trusted client-provided claims.

### 7. Password reset

- Password reset tokens are cryptographically random.
- Only token hashes are stored.
- Tokens expire after 20 minutes.
- Tokens are single-use.
- Password reset requests have a 60-second cooldown.
- Successful password changes revoke existing sessions.

### 8. Abuse protection

Authentication endpoints must be protected against brute-force attacks and automated abuse.

The application will implement rate limiting for login, registration, password reset, and other sensitive authentication operations.

The resend cooldowns defined in ADR-013 are separate from general rate limiting.

## Consequences

### Positive

- Sessions can be revoked immediately.
- Logout takes effect immediately.
- Password changes can invalidate all existing sessions.
- Account and permission changes are reflected through persisted application state.
- Multiple API instances can authenticate users through the shared PostgreSQL database.
- No JWT refresh-token or token-revocation infrastructure is required.

### Negative

- Authenticated requests require a session lookup.
- PostgreSQL must be available for session validation.
- Session cleanup and expiration handling must be implemented.
- Session validation may become a measurable database workload at scale.

Redis may be considered later for session lookup acceleration, but PostgreSQL remains authoritative.

## Alternatives considered

### JWT-based authentication

Rejected for the initial architecture because its stateless validation advantage does not justify the additional token lifecycle and revocation complexity for this application.

### In-memory sessions

Rejected because sessions would not be shared across API instances and would be lost on process restarts.

### Redis-only sessions

Not selected because PostgreSQL is already the authoritative persistent store. Redis may later be introduced as an optimization, not as the only source of session truth.
