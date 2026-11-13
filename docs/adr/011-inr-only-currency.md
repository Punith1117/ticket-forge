# ADR-011: Indian Rupee as the Only Supported Currency

**Status:** Accepted  
**Date:** 2026-11-13

## Context

TicketForge needs a consistent monetary representation for ticket prices, reservations, bookings, payment attempts, and refunds.

Supporting multiple currencies introduces additional concerns such as currency-specific precision, currency conversion, and payment-provider behavior.

The initial release does not require international ticket sales.

## Decision

The initial release supports Indian Rupees (INR) only.

All monetary amounts are stored as integer minor units.

For INR:

- 1 rupee = 100 paise.
- INR 100.50 is stored as 10,050 paise.
- INR 500.00 is stored as 50,000 paise.

Floating-point types must not be used for monetary calculations.

The currency code is stored explicitly with relevant monetary records, using `INR`.

The API accepts and returns monetary values using a clearly documented representation.

## Consequences

### Positive

- Avoids floating-point precision problems.
- Keeps initial payment and refund calculations simple.
- Avoids currency conversion and exchange-rate handling.
- Provides a consistent monetary representation across the system.

### Trade-offs

- International ticket sales are not supported.
- The system cannot represent transactions in other currencies.
- API clients must understand that stored amounts use paise.

## Future Considerations

Multi-currency support may be introduced later.

That change will require explicit currency-specific precision rules, conversion policies where applicable, and corresponding updates to payment and refund workflows.
