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

type ConfirmationStatus string

const (
	ConfirmationStatusConfirmed  ConfirmationStatus = "confirmed"
	ConfirmationStatusIneligible ConfirmationStatus = "ineligible"
)

type ConfirmationResult struct {
	Status           ConfirmationStatus
	BookingID        string
	ReservationID    string
	IneligibleReason error
}

type ReservationOutcome string

const (
	OutcomePending       ReservationOutcome = "PENDING"
	OutcomeConfirmed     ReservationOutcome = "CONFIRMED"
	OutcomeExpired       ReservationOutcome = "EXPIRED"
	OutcomeCancelled     ReservationOutcome = "CANCELLED"
	OutcomeRefundPending ReservationOutcome = "REFUND_PENDING"
	OutcomeRefunded      ReservationOutcome = "REFUNDED"
	OutcomeRefundFailed  ReservationOutcome = "REFUND_FAILED"
)

type ReservationStatusResponse struct {
	Outcome     ReservationOutcome `json:"outcome"`
	IsTerminal  bool               `json:"is_terminal"`
	Reservation ReservationSummary `json:"reservation"`
	Payment     *PaymentSummary    `json:"payment,omitempty"`
	Booking     *BookingSummary    `json:"booking,omitempty"`
	Refund      *RefundSummary     `json:"refund,omitempty"`
}

type ReservationSummary struct {
	ID                 string    `json:"id"`
	EventID            string    `json:"event_id"`
	TicketCategoryID   string    `json:"ticket_category_id"`
	Quantity           int32     `json:"quantity"`
	UnitPriceMinor     int64     `json:"unit_price_minor"`
	TotalPriceMinor    int64     `json:"total_price_minor"`
	Currency           string    `json:"currency"`
	Status             string    `json:"status"`
	CancellationReason *string   `json:"cancellation_reason,omitempty"`
	ExpiresAt          time.Time `json:"expires_at"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type PaymentSummary struct {
	AttemptID     string     `json:"attempt_id"`
	Status        string     `json:"status"`
	Provider      string     `json:"provider"`
	AmountMinor   int64      `json:"amount_minor"`
	Currency      string     `json:"currency"`
	FailureReason *string    `json:"failure_reason,omitempty"`
	AttemptedAt   time.Time  `json:"attempted_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type BookingSummary struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	ConfirmedAt time.Time `json:"confirmed_at"`
}

type RefundSummary struct {
	ID            string     `json:"id"`
	Status        string     `json:"status"`
	AmountMinor   int64      `json:"amount_minor"`
	Currency      string     `json:"currency"`
	FailureReason *string    `json:"failure_reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}
