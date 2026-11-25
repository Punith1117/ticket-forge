package booking

import "errors"

var (
	ErrMissingTicketCategoryID = errors.New("ticket_category_id is required")

	ErrInvalidEventID          = errors.New("booking: invalid event ID")
	ErrInvalidReservationID    = errors.New("booking: invalid reservation ID")
	ErrInvalidTicketCategoryID = errors.New("booking: invalid ticket category ID")
	ErrInvalidQuantity         = errors.New("booking: quantity must be greater than zero")

	ErrEventNotBookable          = errors.New("booking: event is not currently accepting reservations")
	ErrTicketCategoryUnavailable = errors.New("booking: ticket category is unavailable")
	ErrInsufficientInventory     = errors.New("booking: insufficient tickets available")

	ErrInventoryUnavailable = errors.New("booking: inventory record is missing")

	ErrReservationNotFound    = errors.New("booking: reservation not found")
	ErrReservationNotHeld     = errors.New("booking: reservation is not in held status")
	ErrReservationExpired     = errors.New("booking: reservation has expired")
	ErrEventCancelled         = errors.New("booking: event has been cancelled")
	ErrEventNotEligible       = errors.New("booking: event is not eligible for confirmation")
	ErrPaymentAttemptMismatch = errors.New("booking: payment attempt does not match reservation")
	ErrInventoryInconsistent       = errors.New("booking: inventory counters are inconsistent with reservation")
	ErrInconsistentReservationState = errors.New("booking: reservation state is inconsistent")
	ErrBookingAlreadyExists        = errors.New("booking: booking already exists for reservation")
)
