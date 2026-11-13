# ADR-009: Immutable Reservation and Booking Price Snapshots

**Status:** Accepted  
**Date:** 2026-11-13

## Context

Organizers may change a ticket category's price after creating it.

For example:

- Standard tickets initially cost INR 500.
- The organizer later changes the price to INR 700.

The system must determine whether this change affects customers who have already reserved or purchased tickets.

Historical transaction amounts must remain accurate even when current ticket prices change.

## Decision

Changing a ticket category's current price affects only new reservations created after the change.

When a reservation is created, the system stores a snapshot of the applicable price and currency.

That snapshot is used throughout the reservation and confirmation process.

If the reservation is successfully confirmed, the resulting booking stores the same historical price information.

A later change to the ticket category's current price must not modify:

- Existing Held reservations.
- Existing Confirmed reservations.
- Existing bookings.

The reservation's price snapshot determines the amount used for simulated payment.

## Example

1. Standard ticket price is INR 500.
2. Customer A creates a reservation.
3. Customer A's reservation stores INR 500.
4. Organizer changes the current ticket price to INR 700.
5. Customer B creates a reservation and receives the INR 700 price.
6. Customer A confirms the existing reservation at INR 500.
7. Customer B confirms at INR 700.

## Consequences

### Positive

- Historical reservation and booking amounts remain consistent.
- Organizers can change prices without modifying existing customer commitments.
- Payment calculations use a stable price.
- The system can demonstrate price snapshots and historical data integrity.

### Trade-offs

- A customer may hold a reservation at an older price until it expires.
- The system must persist price and currency snapshots rather than relying on the current ticket category price.
- Reservation and booking records contain historical monetary information.

## Future Considerations

Scheduled price tiers, early-bird offers, promotional pricing, and discounts may be introduced later.

They must preserve the same historical price-snapshot guarantee.
