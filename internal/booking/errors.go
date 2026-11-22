package booking

import "errors"

var (
	ErrMissingTicketCategoryID = errors.New("ticket_category_id is required")

	ErrInvalidEventID          = errors.New("booking: invalid event ID")
	ErrInvalidTicketCategoryID = errors.New("booking: invalid ticket category ID")
	ErrInvalidQuantity         = errors.New("booking: quantity must be greater than zero")

	ErrEventNotBookable          = errors.New("booking: event is not currently accepting reservations")
	ErrTicketCategoryUnavailable = errors.New("booking: ticket category is unavailable")
	ErrInsufficientInventory     = errors.New("booking: insufficient tickets available")

	ErrInventoryUnavailable = errors.New("booking: inventory record is missing")
)
