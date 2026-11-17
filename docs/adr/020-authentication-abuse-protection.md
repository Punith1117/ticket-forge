# ADR-020: Authentication Rate Limiting and Abuse Protection

- **Status:** Accepted
- **Date:** 2026-11-17
- **Related ADRs:** ADR-012 (Authentication and Sessions), ADR-013 (Email Verification and Delivery), ADR-019 (Application Rate Limiting Infrastructure)

## Context

Authentication endpoints are attractive targets for automated abuse, including:

- Brute-force password attacks.
- Credential stuffing using leaked credentials.
- Registration email bombing.
- Automated registration attempts.
- Repeated registration verification attempts using guessed or stolen tokens.
- Password-reset email bombing.
- Automated password-reset token guessing.
- Excessive requests that consume server resources, particularly Argon2id password verification and hashing.

ADR-019 establishes reusable application-level rate-limiting infrastructure using a PostgreSQL-backed token bucket.

This ADR defines how authentication features use that infrastructure: which endpoints are protected, which identifiers are limited, the initial policies, and how authentication flows behave when limits are reached.

The design must balance abuse prevention with legitimate usage. In particular, users sharing a public IP address, such as a college network or office, must not unnecessarily prevent one another from authenticating.

## Decision

### Use the shared rate-limiting infrastructure

Authentication services must use the reusable rate-limiting infrastructure defined in ADR-019.

Authentication-specific policies and identifier construction belong to the authentication module, not the generic rate-limiting package.

The authentication module is responsible for deciding:

- Which operations require rate limiting.
- Which identifiers are used.
- Which policy applies.
- When a rate-limit check occurs.
- How a rejected request is communicated to the client.

The generic rate-limiting infrastructure is responsible for atomically evaluating and consuming tokens.

### Protect authentication endpoints

The following endpoints must be protected:

| Endpoint | Protected dimensions | When the limit applies |
| --- | --- | --- |
| `POST /api/v1/auth/login` | IP address | Every request, before password verification |
| `POST /api/v1/auth/login` | Normalized email | Failed authentication attempts |
| `POST /api/v1/auth/register` | IP address | Every request |
| `POST /api/v1/auth/register` | Normalized email | Every request |
| `POST /api/v1/auth/register/complete` | IP address | Every request |
| `POST /api/v1/auth/register/complete` | Registration token identifier | Every completion attempt |
| `POST /api/v1/auth/password-reset/request` | IP address | Every request |
| `POST /api/v1/auth/password-reset/request` | Normalized email | Every request |
| `POST /api/v1/auth/password-reset/complete` | IP address | Every request |
| `POST /api/v1/auth/password-reset/complete` | Password-reset token identifier | Every completion attempt |

### Initial rate-limit policies

The following values are initial MVP defaults. They should be centralized in authentication configuration rather than scattered throughout handlers or services.

| Policy | Capacity | Refill rate | Scope |
| --- | ---: | --- | --- |
| Login per IP | 30 | 1 token every 2 seconds | Every login request |
| Login failures per email | 10 | 1 token every 60 seconds | Failed authentication only |
| Registration per IP | 10 | 1 token every 30 seconds | Every registration request |
| Registration per email | 3 | 1 token every 10 minutes | Every registration request |
| Registration completion per IP | 20 | 1 token every 10 seconds | Every completion request |
| Registration completion per token | 5 | 1 token every 2 minutes | Every completion attempt |
| Password-reset request per IP | 10 | 1 token every 60 seconds | Every password-reset request |
| Password-reset request per email | 3 | 1 token every 10 minutes | Every password-reset request |
| Password-reset completion per IP | 20 | 1 token every 30 seconds | Every password-reset completion request |
| Password-reset completion per token | 5 | 1 token every 2 minutes | Every password-reset completion attempt |

A token bucket starts with its configured capacity when a bucket is first created. Subsequent requests consume tokens, while elapsed time replenishes them according to the configured refill rate, subject to the capacity.

These values are starting defaults, not permanent guarantees. They may be adjusted based on observed usage and abuse patterns without changing the underlying infrastructure.

### Login protection

Login requires two independent rate-limit dimensions.

#### IP-based request limit

The IP-based limit applies to every login request, regardless of whether authentication succeeds or fails.

It must be checked before performing expensive password verification.

This protects server resources against automated requests that could otherwise repeatedly trigger Argon2id verification.

#### Email-based failed-attempt limit

The email-based limit applies only to failed authentication attempts for the normalized email address.

A failed attempt includes:

- An unknown email address.
- An incorrect password.
- An account that is not eligible to authenticate, such as an unverified account.

A successful authentication must not consume a failure token.

The email-based failure limit must not act as a hard pre-authentication lockout.

In particular, if the email failure bucket is depleted, the server must still verify the supplied credentials. A correct password must be allowed to authenticate, provided the request passes the IP-based limit.

If authentication fails while the email failure bucket is depleted, the request must be rejected without consuming another failure token.

This prevents an attacker from permanently locking another user's account merely by submitting incorrect passwords.

All authentication failures must continue to use the generic response defined by ADR-012. Rate-limit behavior must not disclose whether an email address belongs to an existing account.

### Registration request protection

Registration requests must be limited independently by IP address and normalized email address.

The IP-based limit reduces automated registration traffic originating from a single source.

The email-based limit reduces repeated verification email requests targeting a particular address.

The existing 60-second registration verification resend cooldown defined in ADR-013 remains in effect.

The rate limiter supplements this cooldown; it does not replace it.

Both limits must be evaluated before creating a registration challenge or corresponding outbox event.

When a request is rejected by rate limiting:

- No registration challenge is created.
- No email outbox event is created.
- No verification token is generated unnecessarily.
- The response must not disclose whether the email address is already registered.

The existing generic registration response behavior defined by ADR-013 must be preserved.

### Registration completion protection

Registration completion must be protected independently by IP address and registration token identifier.

The IP-based limit reduces automated completion attempts from a single source.

The token-based limit restricts repeated attempts against an individual registration challenge.

The token identifier must be derived using a secure, non-reversible representation of the submitted token. The raw token must never be stored in rate-limiting records or used directly as a persisted bucket key.

The rate-limit checks must occur before password hashing and before consuming or modifying a registration challenge.

A request rejected by rate limiting must not consume, invalidate, or otherwise modify a valid registration token.

The existing token validation, expiration, single-use, and transactional account-creation guarantees defined by ADR-013 must remain unchanged.

### Password-reset request protection

Password-reset requests must be limited independently by IP address and normalized email address.

The IP-based limit constrains automated password-reset traffic originating from a single source.

The email-based limit protects an individual email address against repeated password-reset requests, including attempts originating from multiple IP addresses.

The existing 60-second password-reset request cooldown defined by ADR-013 remains in effect. The rate limiter supplements this cooldown rather than replacing it.

Both rate-limit dimensions must be evaluated before generating a password-reset token or creating an outbox event.

The request handler must preserve account-enumeration protection:

- Existing and nonexistent email addresses must receive the same generic response.
- A request must not disclose whether an account exists.
- Cooldown and eligibility behavior must not expose account existence through different response bodies or status codes.

A request rejected by rate limiting must not:

- Generate a password-reset token.
- Create a password-reset challenge.
- Create an email outbox event.

The email-specific bucket must be based on the normalized email address, not a user ID. This ensures the same identifier is used regardless of whether an account exists.

### Password-reset completion protection

Password-reset completion must be protected independently by IP address and password-reset token identifier.

The IP-based limit reduces automated completion attempts from a single source.

The token-based limit restricts repeated attempts against an individual password-reset challenge.

The token identifier must be derived using a secure, non-reversible representation of the submitted token. The raw token must never be stored in rate-limiting records or used directly as a persisted bucket key.

The rate-limit checks must occur before expensive Argon2id password hashing and before consuming or modifying a password-reset challenge.

A request rejected by rate limiting must not:

- Consume or invalidate the reset token.
- Modify the user's password.
- Revoke any existing sessions.
- Otherwise modify password-reset state.

The existing token validation, 20-minute expiration, single-use, and transactional password-update guarantees defined by ADR-013 must remain unchanged.

A password-reset token must never be accepted as a registration verification token, and a registration verification token must never be accepted for password reset.

### Client IP identification and trusted proxies

The server must use the remote address from the incoming network connection as the default client IP identifier.

Forwarded headers such as `X-Forwarded-For` and `X-Real-IP` must not be trusted blindly because clients can supply or manipulate them.

Forwarded client IP information may only be used when the immediate connecting proxy is explicitly configured as trusted.

When trusted proxy configuration is absent, the application must use the connection's remote address.

Proxy trust configuration must be explicit and documented for deployment environments.

### Rate-limit rejection responses

When a request is rejected because a required rate limit is exhausted, the API must return:

- HTTP `429 Too Many Requests`.
- A generic JSON error response.
- A `Retry-After` header indicating the estimated number of seconds until the request can be retried.

The response must not expose internal bucket keys, raw identifiers, or implementation details.

The retry duration must be derived from the rate-limiting result provided by ADR-019 rather than calculated independently by each handler.

Existing endpoint-specific response semantics must otherwise remain unchanged.

### Rate-limiter infrastructure failures

Authentication endpoints must fail closed when a required rate-limit operation cannot be evaluated because of a database or infrastructure failure.

The application must not silently bypass a required limit.

Such failures must return an appropriate server error rather than incorrectly reporting that the user has exceeded a rate limit.

Internal error details must be logged safely without exposing credentials, raw tokens, session tokens, or other sensitive information.

### Shared IP addresses and legitimate users

IP-based policies are intended to constrain traffic from a single source, not to identify individual users.

Multiple legitimate users may share an IP address through NAT, college networks, offices, or public Wi-Fi.

Therefore:

- IP limits must remain reasonably generous.
- Email-specific limits must provide an independent protection dimension.
- Login failures must be tracked by normalized email rather than relying exclusively on IP-based failure limits.
- IP-based limits must not be treated as account-specific lockouts.

The initial policy values should be revisited if legitimate shared-network traffic is unnecessarily restricted.

### Identifier privacy

Authentication rate-limit identifiers must follow the privacy requirements established by ADR-019.

Normalized email addresses and registration and password-reset token identifiers must not be stored in plaintext as bucket keys.

Use the dedicated keyed HMAC mechanism defined by ADR-019 for sensitive identifiers.

IP addresses should also be represented using the privacy-preserving identifier strategy established by ADR-019.

Rate-limit records must not become an alternative storage location for authentication credentials or raw user identifiers.

### Policy changes

Authentication rate-limit policies must be centrally configurable.

Changing a policy's capacity or refill rate must not require modifying the generic rate-limiting infrastructure.

Policy identifiers must remain stable and distinguish different operations and identifier dimensions.

Any future policy changes should consider the effects on legitimate users, abuse resistance, and existing bucket state.

## Consequences

### Positive

- Protects authentication endpoints against common automated attacks.
- Reduces unnecessary Argon2id work from excessive login requests.
- Provides independent protection against attacks distributed across IP addresses.
- Reduces registration email bombing.
- Protects registration completion tokens from repeated guessing attempts.
- Reduces password-reset email bombing.
- Protects password-reset challenges against repeated token guessing.
- Avoids permanent account lockout caused by failed login attempts.
- Supports users sharing public IP addresses.
- Reuses the generic infrastructure from ADR-019 without coupling it to authentication-specific behavior.
- Keeps policy changes independent of the rate-limiter implementation.

### Negative

- Adds database operations to authentication requests.
- Requires careful policy tuning to avoid restricting legitimate users.
- Shared IP addresses can still exhaust an IP-based bucket under heavy traffic.
- HMAC key rotation can affect existing bucket identifiers, as described in ADR-019.
- Additional policies will be needed as authentication functionality expands.

## Out of Scope

The following are not included in this ADR:

- Logout and session revocation rate limiting.
- OAuth or social authentication.
- Multi-factor authentication.
- CAPTCHA or other human-verification mechanisms.
- General API rate limiting.
- Redis or other alternative rate-limit storage implementations.
- Distributed rate limiting across independent services.
- Concurrent-request or resource-concurrency limiting.

## Implementation Notes

The implementation should:

1. Use `internal/ratelimit` as defined by ADR-019.
2. Keep authentication policy definitions in the authentication module.
3. Apply all required dimensions for each protected endpoint.
4. Ensure rate-limit checks are atomic and safe under concurrent requests.
5. Use normalized email identifiers and secure token identifiers.
6. Respect trusted proxy configuration when determining client IP addresses.
7. Return consistent `429` responses with appropriate `Retry-After` headers.
8. Preserve existing authentication, registration, and password-reset transaction guarantees.
9. Avoid logging sensitive identifiers or credentials.
10. Include unit, PostgreSQL integration, concurrency, and HTTP handler tests.

The initial implementation must use PostgreSQL-backed rate limiting. No Redis dependency or in-memory fallback is required for the MVP.
