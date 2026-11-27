package event

import "errors"

var (
	ErrInvalidRequest = errors.New(
		"event: invalid request",
	)

	ErrVenueBookingNotFound = errors.New(
		"event: venue booking not found",
	)

	ErrVenueBookingNotOwned = errors.New(
		"event: venue booking does not belong to organizer",
	)

	ErrVenueBookingUnavailable = errors.New(
		"event: venue booking is unavailable",
	)

	ErrActiveEventAlreadyExists = errors.New(
		"event: an active event already exists for this venue booking",
	)

	ErrTicketCapacityExceeded = errors.New(
		"event: total ticket category capacity exceeds venue capacity",
	)

	ErrEventNotFound = errors.New(
		"event not found",
	)

	ErrEventNotActive = errors.New(
		"event is not active",
	)

	ErrEventAlreadyCancelled = errors.New(
		"event: event is already cancelled",
	)

	ErrInvalidBookingStatus = errors.New(
		"invalid booking status",
	)
)
