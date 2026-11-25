package booking

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNewService_NilPool(t *testing.T) {
	_, err := NewService(nil)

	if err == nil {
		t.Fatal("expected error for nil database pool")
	}
}

func TestCreateReservation_Validation(t *testing.T) {
	validCustomerID := uuid.NewString()
	validEventID := uuid.NewString()
	validCategoryID := uuid.NewString()

	tests := []struct {
		name    string
		userID  string
		eventID string
		req     CreateReservationRequest
		wantErr error
	}{
		{
			name:    "zero quantity",
			userID:  validCustomerID,
			eventID: validEventID,
			req: CreateReservationRequest{
				TicketCategoryID: validCategoryID,
				Quantity:         0,
			},
			wantErr: ErrInvalidQuantity,
		},
		{
			name:    "negative quantity",
			userID:  validCustomerID,
			eventID: validEventID,
			req: CreateReservationRequest{
				TicketCategoryID: validCategoryID,
				Quantity:         -1,
			},
			wantErr: ErrInvalidQuantity,
		},
		{
			name:    "missing ticket category ID",
			userID:  validCustomerID,
			eventID: validEventID,
			req: CreateReservationRequest{
				Quantity: 2,
			},
			wantErr: ErrMissingTicketCategoryID,
		},
		{
			name:    "invalid customer ID",
			userID:  "invalid-user-id",
			eventID: validEventID,
			req: CreateReservationRequest{
				TicketCategoryID: validCategoryID,
				Quantity:         2,
			},
			wantErr: nil,
		},
		{
			name:    "invalid event ID",
			userID:  validCustomerID,
			eventID: "invalid-event-id",
			req: CreateReservationRequest{
				TicketCategoryID: validCategoryID,
				Quantity:         2,
			},
			wantErr: ErrInvalidEventID,
		},
		{
			name:    "invalid ticket category ID",
			userID:  validCustomerID,
			eventID: validEventID,
			req: CreateReservationRequest{
				TicketCategoryID: "invalid-category-id",
				Quantity:         2,
			},
			wantErr: ErrInvalidTicketCategoryID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Validation happens before the service needs a database.
			service := &Service{}

			_, err := service.CreateReservation(
				context.Background(),
				tt.userID,
				tt.eventID,
				tt.req,
				uuid.NewString(),
			)

			if tt.name == "invalid customer ID" {
				if err == nil {
					t.Fatal("expected an error for invalid customer ID")
				}
				return
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf(
					"expected error %v, got %v",
					tt.wantErr,
					err,
				)
			}
		})
	}
}

func TestGetReservationStatus_Validation(t *testing.T) {
	service := &Service{}

	if _, err := service.GetReservationStatus(context.Background(), "bad-customer", uuid.NewString()); err == nil {
		t.Fatal("expected error for invalid customer ID")
	}

	if _, err := service.GetReservationStatus(context.Background(), uuid.NewString(), "bad-reservation"); !errors.Is(err, ErrInvalidReservationID) {
		t.Fatalf("expected ErrInvalidReservationID, got %v", err)
	}
}

func TestDeriveReservationOutcome(t *testing.T) {
	tests := []struct {
		name              string
		reservationStatus string
		paymentStatus     string
		refundStatus      string
		wantOutcome       ReservationOutcome
		wantTerminal      bool
		wantErr           bool
	}{
		// Valid outcomes
		{
			name:              "held and unpaid",
			reservationStatus: "held",
			paymentStatus:     "",
			wantOutcome:       OutcomePending,
			wantTerminal:      false,
		},
		{
			name:              "held and payment pending",
			reservationStatus: "held",
			paymentStatus:     "pending",
			wantOutcome:       OutcomePending,
			wantTerminal:      false,
		},
		{
			name:              "held and payment failed",
			reservationStatus: "held",
			paymentStatus:     "failed",
			wantOutcome:       OutcomePending,
			wantTerminal:      false,
		},
		{
			name:              "confirmed and payment succeeded",
			reservationStatus: "confirmed",
			paymentStatus:     "succeeded",
			wantOutcome:       OutcomeConfirmed,
			wantTerminal:      true,
		},
		{
			name:              "expired and unpaid",
			reservationStatus: "expired",
			paymentStatus:     "",
			wantOutcome:       OutcomeExpired,
			wantTerminal:      true,
		},
		{
			name:              "expired and payment pending",
			reservationStatus: "expired",
			paymentStatus:     "pending",
			wantOutcome:       OutcomeExpired,
			wantTerminal:      true,
		},
		{
			name:              "expired and payment failed",
			reservationStatus: "expired",
			paymentStatus:     "failed",
			wantOutcome:       OutcomeExpired,
			wantTerminal:      true,
		},
		{
			name:              "cancelled and unpaid",
			reservationStatus: "cancelled",
			paymentStatus:     "",
			wantOutcome:       OutcomeCancelled,
			wantTerminal:      true,
		},
		{
			name:              "cancelled and payment failed",
			reservationStatus: "cancelled",
			paymentStatus:     "failed",
			wantOutcome:       OutcomeCancelled,
			wantTerminal:      true,
		},
		{
			name:              "expired and payment succeeded without refund row yet",
			reservationStatus: "expired",
			paymentStatus:     "succeeded",
			wantOutcome:       OutcomeRefundPending,
			wantTerminal:      false,
		},
		{
			name:              "cancelled and payment succeeded without refund row yet",
			reservationStatus: "cancelled",
			paymentStatus:     "succeeded",
			wantOutcome:       OutcomeRefundPending,
			wantTerminal:      false,
		},
		{
			name:              "expired with pending refund",
			reservationStatus: "expired",
			paymentStatus:     "succeeded",
			refundStatus:      "pending",
			wantOutcome:       OutcomeRefundPending,
			wantTerminal:      false,
		},
		{
			name:              "expired with processing refund",
			reservationStatus: "expired",
			paymentStatus:     "succeeded",
			refundStatus:      "processing",
			wantOutcome:       OutcomeRefundPending,
			wantTerminal:      false,
		},
		{
			name:              "expired with succeeded refund",
			reservationStatus: "expired",
			paymentStatus:     "succeeded",
			refundStatus:      "succeeded",
			wantOutcome:       OutcomeRefunded,
			wantTerminal:      true,
		},
		{
			name:              "expired with failed refund",
			reservationStatus: "expired",
			paymentStatus:     "succeeded",
			refundStatus:      "failed",
			wantOutcome:       OutcomeRefundFailed,
			wantTerminal:      true,
		},
		{
			name:              "confirmed with pending refund (event cancelled)",
			reservationStatus: "confirmed",
			paymentStatus:     "succeeded",
			refundStatus:      "pending",
			wantOutcome:       OutcomeRefundPending,
			wantTerminal:      false,
		},
		{
			name:              "confirmed with succeeded refund",
			reservationStatus: "confirmed",
			paymentStatus:     "succeeded",
			refundStatus:      "succeeded",
			wantOutcome:       OutcomeRefunded,
			wantTerminal:      true,
		},

		// Inconsistent / unexpected states
		{
			name:              "held with succeeded payment",
			reservationStatus: "held",
			paymentStatus:     "succeeded",
			wantErr:           true,
		},
		{
			name:              "confirmed without payment",
			reservationStatus: "confirmed",
			paymentStatus:     "",
			wantErr:           true,
		},
		{
			name:              "confirmed with failed payment",
			reservationStatus: "confirmed",
			paymentStatus:     "failed",
			wantErr:           true,
		},
		{
			name:              "refund without succeeded payment",
			reservationStatus: "expired",
			paymentStatus:     "failed",
			refundStatus:      "pending",
			wantErr:           true,
		},
		{
			name:              "refund while reservation still held",
			reservationStatus: "held",
			paymentStatus:     "succeeded",
			refundStatus:      "pending",
			wantErr:           true,
		},
		{
			name:              "unknown reservation status",
			reservationStatus: "mystery",
			paymentStatus:     "",
			wantErr:           true,
		},
		{
			name:              "unknown payment status",
			reservationStatus: "held",
			paymentStatus:     "mystery",
			wantErr:           true,
		},
		{
			name:              "unknown refund status",
			reservationStatus: "expired",
			paymentStatus:     "succeeded",
			refundStatus:      "mystery",
			wantErr:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, terminal, err := deriveReservationOutcome(
				tt.reservationStatus,
				tt.paymentStatus,
				tt.refundStatus,
			)

			if tt.wantErr {
				if !errors.Is(err, ErrInconsistentReservationState) {
					t.Fatalf("expected ErrInconsistentReservationState, got outcome=%q err=%v", outcome, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if outcome != tt.wantOutcome {
				t.Errorf("expected outcome %q, got %q", tt.wantOutcome, outcome)
			}
			if terminal != tt.wantTerminal {
				t.Errorf("expected is_terminal=%v, got %v", tt.wantTerminal, terminal)
			}
		})
	}
}
