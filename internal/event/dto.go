package event

import "time"

type CreateEventRequest struct {
	VenueBookingID   string                        `json:"venue_booking_id"`
	Title            string                        `json:"title"`
	Description      string                        `json:"description"`
	TicketCategories []CreateTicketCategoryRequest `json:"ticket_categories"`
}

type CreateTicketCategoryRequest struct {
	Name       string `json:"name"`
	PriceMinor int64  `json:"price_minor"`
	Capacity   int32  `json:"capacity"`
}

type EventResponse struct {
	ID                 string    `json:"id"`
	OrganizerProfileID string    `json:"organizer_profile_id"`
	VenueBookingID     string    `json:"venue_booking_id"`
	Title              string    `json:"title"`
	Description        string    `json:"description"`
	Status             string    `json:"status"`
	BookingStatus      string    `json:"booking_status"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type TicketCategoryResponse struct {
	ID         string    `json:"id"`
	EventID    string    `json:"event_id"`
	Name       string    `json:"name"`
	PriceMinor int64     `json:"price_minor"`
	Currency   string    `json:"currency"`
	Capacity   int32     `json:"capacity"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type CreateEventResponse struct {
	Message          string                   `json:"message"`
	Event            EventResponse            `json:"event"`
	TicketCategories []TicketCategoryResponse `json:"ticket_categories"`
}

type UpdateBookingStatusRequest struct {
	BookingStatus string `json:"booking_status"`
}

type UpdateBookingStatusResponse struct {
	Message string        `json:"message"`
	Event   EventResponse `json:"event"`
}
