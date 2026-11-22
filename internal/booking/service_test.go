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
