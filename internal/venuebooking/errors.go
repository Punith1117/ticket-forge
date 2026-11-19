package venuebooking

import "errors"

var (
	ErrOrganizerNotFound = errors.New(
		"venuebooking: organizer profile not found",
	)

	ErrVenueNotFound = errors.New("venuebooking: venue not found")

	ErrVenueUnavailable = errors.New("venuebooking: venue is unavailable")

	ErrVenueBookingNotFound = errors.New("venuebooking: venue booking not found")

	ErrVenueBookingCancelled = errors.New("venuebooking: venue booking is cancelled")

	ErrVenueBookingHasActiveEvent = errors.New(
		"venuebooking: venue booking has an active event",
	)

	ErrVenueBookingLimitReached = errors.New(
		"venuebooking: organizer booking limit reached",
	)

	ErrVenueBookingOverlap = errors.New(
		"venuebooking: venue booking overlaps another booking",
	)

	ErrInvalidSchedule = errors.New(
		"venuebooking: invalid schedule",
	)

	ErrStartTimeTooSoon = errors.New(
		"venuebooking: booking start time is too soon",
	)

	ErrStartTimeTooFar = errors.New(
		"venuebooking: booking start time is too far in the future",
	)

	ErrInvalidDuration = errors.New(
		"venuebooking: invalid booking duration",
	)
)
