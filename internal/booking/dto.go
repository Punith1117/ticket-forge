package booking

import "time"

type CreateReservationRequest struct {
	TicketCategoryID string `json:"ticket_category_id"`
	Quantity         int32  `json:"quantity"`
}

type CreateReservationResponse struct {
	ID               string    `json:"id"`
	EventID          string    `json:"event_id"`
	TicketCategoryID string    `json:"ticket_category_id"`
	Quantity         int32     `json:"quantity"`
	UnitPriceMinor   int64     `json:"unit_price_minor"`
	TotalPriceMinor  int64     `json:"total_price_minor"`
	Currency         string    `json:"currency"`
	Status           string    `json:"status"`
	ExpiresAt        time.Time `json:"expires_at"`
}
