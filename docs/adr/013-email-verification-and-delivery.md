# ADR-013: Email Verification and Transactional Email Delivery

**Status:** Accepted  
**Date:** 2026-11-14

## Context

TicketForge uses email addresses as the login identifier.

Without email verification, users can register with addresses they do not control. This can cause problems with account recovery, communication, and account abuse.

Email is an integral part of TicketForge. Users must demonstrate control of their email address before an account can be created or authenticated.

The application also requires a reliable way to send verification, password-reset, and other transactional emails.

## Decision

TicketForge will require email verification before account creation and use **Resend** as its initial transactional email delivery provider.

### Email verification and registration policy

Account registration follows an email-first verification flow:

1. The user submits their email address without providing a password.
2. The application generates a verification token and sends a verification email containing a link to the user.
3. The user opens the verification link, which directs them to the account creation page.
4. The user enters and confirms their password.
5. The client submits the verification token together with the new password.
6. The application validates the verification token, hashes the password using Argon2id, and creates the user account.
7. The application marks the email as verified and consumes the verification token.

User account creation, email verification, and verification-token consumption must happen atomically within a single database transaction.

A user account is created only after successful verification and password submission.

No password is requested, stored, or associated with an email address before email ownership has been verified.

There is no separate pending user account or registration-grant mechanism. Before successful registration, the application maintains only a registration verification challenge.

Login is permitted only for successfully created user accounts, which necessarily have verified email addresses.

Verification proves control of the email inbox. It does not establish a user's real-world identity or guarantee that the email address is permanent.

### Registration verification tokens

Registration verification tokens follow these rules:

- Tokens are cryptographically random.
- Tokens contain at least 256 bits of random entropy.
- Only SHA-256 token hashes are persisted in the verification-token table for validation.
- Raw tokens are included in verification links sent to users.
- Tokens expire after 24 hours.
- Tokens are single-use.
- A token is consumed only after successful account creation.
- Invalid, expired, and already-used tokens cannot create accounts.
- Resending verification emails is subject to a 60-second cooldown.

Verification challenges must be independently identifiable.

A new registration request for an email address must not overwrite or invalidate another outstanding verification challenge for that address merely because the email address is the same.

Multiple verification requests must not allow an unauthenticated requester to invalidate another person's valid registration link.

The verification endpoint must enforce token expiration, single-use semantics, and account uniqueness.

Verification links must not complete registration solely through an HTTP GET request. Email security scanners and link-preview systems may automatically visit URLs. The user must explicitly submit the password and verification token to complete registration.

### Registration account creation and concurrency

The application must prevent duplicate accounts and credential overwrites when multiple registration attempts target the same email address.

- Email uniqueness is enforced by a database constraint.
- Email addresses are normalized consistently before uniqueness checks and persistence.
- The verification challenge must be validated and consumed within the account-creation transaction.
- The user account must be created with the verified email address, the supplied Argon2id password hash, and a verified email status.
- A failed account-creation transaction must not consume the verification token.
- A verification token cannot be used more than once, including under concurrent requests.
- A registration attempt must never overwrite the credentials of an existing account.

If multiple valid verification challenges exist for the same email address, only one can successfully create the account. Other attempts must fail safely without modifying the existing account.

### Email address changes

Changing an account's email address requires verification of the new address.

The existing verified email remains the account's verified address until the new address has been successfully verified.

The application must not replace the existing email address merely because a user submits a new address.

The new email address must satisfy the same normalization and uniqueness requirements as account registration.

Email-change verification must not be interchangeable with registration verification.

### Password recovery emails

Password-reset emails use the same transactional email provider.

Password-reset token policy:

- Tokens are cryptographically random.
- Tokens contain at least 256 bits of random entropy.
- Only SHA-256 token hashes are persisted in the password-reset-token table for validation.
- Raw tokens are included in password-reset links sent to users.
- Tokens expire after 20 minutes.
- Tokens are single-use.
- Request cooldown is 60 seconds.
- Responses must not disclose whether an account exists for the supplied email address.

Password recovery is available only for existing user accounts.

The password-reset flow requires the user to submit a valid password-reset token together with a new password.

On successful password reset:

- The new password is hashed using Argon2id.
- The password hash is updated.
- The reset token is consumed.
- All existing sessions are revoked.

Password-reset tokens must be distinct from registration verification tokens and must never be usable to create new accounts.

### Secure token storage and email delivery

Raw registration verification and password-reset tokens must never be persisted in plaintext.

Only SHA-256 token hashes are stored in their respective token tables for token validation.

Because asynchronous email delivery requires access to the original token, the raw token must be encrypted before being included in an outbox event.

The following security requirements apply:

- Authenticated encryption must be used, specifically AES-256-GCM.
- Encryption keys must be supplied through environment-based secrets or a dedicated secrets manager.
- Encryption keys must never be stored in PostgreSQL or committed to version control.
- Encryption keys must be separate from token hashes and must support controlled rotation.
- The outbox payload must contain the encrypted token, not the plaintext token.
- The background worker decrypts the token only when processing the corresponding email-delivery event.
- Decrypted tokens must not be logged or unnecessarily retained in memory.
- After successful email delivery, the encrypted token payload must be removed or securely discarded from the outbox event.
- Failed delivery attempts may retain the encrypted payload for retries, subject to token expiration.
- The same requirements apply to registration verification and password-reset tokens.

The encryption key must remain accessible only to application components that require token encryption or decryption.

Encryption protects persisted delivery payloads against database-only exposure. It does not protect against compromise of both the database and the encryption key.

### Email provider

Resend is selected as the initial provider.

The application will use the official Go SDK behind an internal email-sending boundary so that provider-specific functionality does not spread throughout the authentication domain.

The provider API key must be supplied through environment-based secrets and must never be committed to version control.

A verified sending domain and appropriate SPF, DKIM, and DMARC configuration are required before production email delivery.

The email-sending boundary should support the transactional email types required by the application, including:

- Account verification.
- Password reset.
- Email address change.
- Future transactional notifications.

### Delivery reliability

Sending an email is an external side effect and must not occur while holding a database transaction open.

Creating and persisting verification challenges or password-reset tokens must not depend on the email provider successfully accepting a request.

Creating a token record and its corresponding outbox event must happen atomically.

Email delivery will be performed asynchronously through the transactional outbox and background-worker infrastructure.

The system must support:

- Reliable persistence of email delivery requests.
- Background processing of pending email events.
- Decryption of token payloads when processing email events.
- Retry handling for transient delivery failures.
- Appropriate handling of permanent delivery failures.
- Tracking delivery attempts and final delivery status.

Email delivery failures must not silently invalidate a verification challenge or password-reset token.

The system must allow verification emails to be requested again when delivery fails, subject to the applicable cooldown and rate limits.

External email delivery must happen outside database transactions.

Outbox processing must account for worker crashes and retries. Email delivery may occur more than once if a worker crashes after the provider accepts a message but before the event is marked as processed. The system must use appropriate idempotency and retry handling where supported by the provider.

Expired token events must not result in sending links that can no longer be used.

### Abuse protection

Registration, verification, resend, login, and password-reset operations must be protected against automated abuse.

The application will implement rate limiting for:

- Registration requests.
- Verification email requests.
- Verification submissions.
- Login attempts.
- Password-reset requests.
- Password-reset submissions.
- Other sensitive authentication operations.

Responses must avoid unnecessarily disclosing whether an email address belongs to an existing account.

Verification requests must not allow an unauthenticated requester to invalidate another person's outstanding valid verification challenges.

The application must also avoid logging raw verification tokens, password-reset tokens, session tokens, encryption keys, or passwords.

## Consequences

### Positive

- Users demonstrate control of their email addresses before accounts are created.
- Unverified email addresses cannot occupy the user account namespace.
- Passwords are never collected or stored before email ownership is established.
- Users cannot authenticate before completing registration.
- Someone cannot take over another person's pending registration by replacing its password.
- Account creation and email verification have a clear transactional boundary.
- Password recovery uses a verified communication channel.
- Registration and password-reset tokens have distinct purposes and cannot be interchanged.
- Only token hashes are stored in token-validation tables.
- Encrypted outbox payloads allow asynchronous email delivery without storing raw tokens in plaintext.
- Email provider integration is isolated from authentication business logic.
- Email delivery can be retried independently of account and token persistence.
- The transactional outbox provides reliable integration between database state changes and email delivery.

### Negative

- Registration introduces an additional verification step before password creation.
- Registration verification challenges require persistence, expiration, and cleanup.
- Verification and account creation require careful transactional and concurrency handling.
- Encrypted outbox payloads introduce encryption-key management and rotation responsibilities.
- Background email delivery requires worker infrastructure and retry handling.
- Email delivery depends on an external provider.
- Domain configuration and email deliverability require operational attention.
- Verification and password-reset token creation, expiration, resend, and consumption must be implemented securely.
- Outbox processing requires careful handling of retries, duplicate delivery, and expired tokens.

## Alternatives considered

### Create accounts before email verification

Rejected because unverified email addresses could occupy the user account namespace and allow unauthorized individuals to register using addresses they do not control.

### Collect passwords before email verification

Rejected because storing passwords against unverified email addresses introduces credential replacement and account-takeover risks when multiple people attempt registration using the same address.

### Store raw tokens in plaintext outbox payloads

Rejected because a database compromise would expose usable verification and password-reset tokens.

### Generate tokens only in the background worker

Rejected because worker crashes and retries would complicate reliable token delivery and validation. The original token must remain recoverable for retries, requiring secure persistence.

### No email verification

Rejected because an account could immediately use important application capabilities with an email address the user does not control.

### Self-hosted SMTP

Not selected for the initial implementation because it introduces additional email infrastructure and deliverability responsibilities.

### Other transactional email providers

Deferred. The application will keep provider-specific functionality isolated to allow a future provider change if justified.
