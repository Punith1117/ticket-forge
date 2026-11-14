# ADR-013: Email Verification and Transactional Email Delivery

**Status:** Accepted  
**Date:** 2026-11-14

## Context

TicketForge uses email addresses as the login identifier.

Without email verification, users can register with addresses they do not control. This can cause problems with account recovery, communication, and account abuse.

The application requires a reliable way to send verification and password-reset emails.

## Decision

TicketForge will require email verification and use **Resend** as its initial transactional email delivery provider.

### 1. Email verification policy

An account may be registered and authenticated before its email address is verified.

However, an unverified account cannot:

- Create ticket reservations.
- Activate an OrganizerProfile.
- Perform organizer-specific operations.

Public event browsing and email verification operations remain available.

Verification proves control of the email inbox. It does not establish a user's real-world identity or guarantee that the email address is permanent.

### 2. Verification tokens

- Tokens are cryptographically random.
- Tokens contain at least 256 bits of random entropy.
- Only SHA-256 token hashes are persisted.
- Raw tokens are included only in the verification link sent to the user.
- Tokens expire after 24 hours.
- Tokens are single-use.
- A successful verification marks the user's email as verified.
- Resending verification emails is subject to a 60-second cooldown.

Expired or already-used tokens cannot verify an account.

### 3. Email address changes

Changing an account's email address requires verification of the new address.

The existing verified email remains the account's verified address until the new address has been successfully verified.

### 4. Password recovery emails

Password-reset emails use the same transactional email provider.

Password-reset token policy:

- Expiration: 20 minutes.
- Single-use tokens.
- Only token hashes are persisted.
- Request cooldown: 60 seconds.
- Responses must not disclose whether an account exists for the supplied email address.

### 5. Email provider

Resend is selected as the initial provider.

The application will use the official Go SDK behind an internal email-sending boundary so that provider-specific functionality does not spread throughout the authentication domain.

The provider API key must be supplied through environment-based secrets and must never be committed to version control.

A verified sending domain and appropriate SPF, DKIM, and DMARC configuration are required before production email delivery.

### 6. Delivery reliability

Sending an email is an external side effect and must not occur while holding a database transaction open.

Account creation and token persistence must not depend on the email provider successfully accepting a request.

The system must allow verification emails to be requested again when delivery fails.

As the transactional outbox and background-worker infrastructure is introduced, email delivery will be dispatched through that mechanism, with retries and appropriate failure handling.

Email delivery failures must not silently invalidate the user's account or token state.

## Consequences

### Positive

- Users demonstrate control of their registered email addresses.
- Password recovery can use a verified communication channel.
- Unverified accounts cannot perform important business operations.
- Email provider integration is isolated from authentication business logic.
- Delivery can evolve toward asynchronous processing and retries.

### Negative

- Registration introduces an additional verification step.
- Email delivery depends on an external provider.
- Domain configuration and email deliverability require operational attention.
- Verification token creation, expiration, resend, and consumption must be implemented securely.

## Alternatives considered

### No email verification

Rejected because an account could immediately use important application capabilities with an email address it does not control.

### Self-hosted SMTP

Not selected for the initial implementation because it introduces additional email infrastructure and deliverability responsibilities.

### Other transactional email providers

Deferred. The application will keep provider-specific functionality isolated to allow a future provider change if justified.
