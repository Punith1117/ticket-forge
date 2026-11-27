package payment

import "time"

// CheckoutSessionResponse is returned to the authenticated customer after checkout session creation.
type CheckoutSessionResponse struct {
	PaymentAttemptID     string    `json:"payment_attempt_id"`
	ReservationID        string    `json:"reservation_id"`
	Provider             string    `json:"provider"`
	ProviderSessionID    string    `json:"provider_session_id"`
	CheckoutURL          string    `json:"checkout_url"`
	AmountMinor          int64     `json:"amount_minor"`
	Currency             string    `json:"currency"`
	Status               string    `json:"status"`
	ReservationExpiresAt time.Time `json:"reservation_expires_at"`
	Reused               bool      `json:"-"`
}
