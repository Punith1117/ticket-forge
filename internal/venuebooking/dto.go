package venuebooking

import "time"

type CreateVenueBookingRequest struct {
	VenueID         string    `json:"venue_id"`
	StartsAt        time.Time `json:"starts_at"`
	DurationMinutes int       `json:"duration_minutes"`
}

type VenueBookingResponse struct {
	ID                 string    `json:"id"`
	OrganizerProfileID string    `json:"organizer_profile_id"`
	VenueID            string    `json:"venue_id"`
	StartsAt           time.Time `json:"starts_at"`
	EndsAt             time.Time `json:"ends_at"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type CreateVenueBookingResponse struct {
	Message      string               `json:"message"`
	VenueBooking VenueBookingResponse `json:"venue_booking"`
}

type GetVenueBookingResponse struct {
	VenueBooking VenueBookingResponse `json:"venue_booking"`
}
type ListVenueBookingsResponse struct {
	VenueBookings []VenueBookingResponse `json:"venue_bookings"`
}

type CancelVenueBookingResponse struct {
	Message      string               `json:"message"`
	VenueBooking VenueBookingResponse `json:"venue_booking"`
}
