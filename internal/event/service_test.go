package event

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestValidateCreateEventRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     CreateEventRequest
		wantErr error
	}{
		{
			name: "valid request",
			req: CreateEventRequest{
				VenueBookingID: "booking-id",
				Title:          "Go Conference",
				Description:    "A Go conference",
				TicketCategories: []CreateTicketCategoryRequest{
					{
						Name:       "Prime",
						PriceMinor: 50000,
						Capacity:   100,
					},
					{
						Name:       "Basic",
						PriceMinor: 20000,
						Capacity:   150,
					},
				},
			},
		},
		{
			name: "empty booking ID",
			req: CreateEventRequest{
				Title: "Go Conference",
				TicketCategories: []CreateTicketCategoryRequest{
					{Name: "Prime", PriceMinor: 50000, Capacity: 100},
				},
			},
			wantErr: ErrInvalidRequest,
		},
		{
			name: "empty title",
			req: CreateEventRequest{
				VenueBookingID: "booking-id",
				Title:          "   ",
				TicketCategories: []CreateTicketCategoryRequest{
					{Name: "Prime", PriceMinor: 50000, Capacity: 100},
				},
			},
			wantErr: ErrInvalidRequest,
		},
		{
			name: "no ticket categories",
			req: CreateEventRequest{
				VenueBookingID: "booking-id",
				Title:          "Go Conference",
			},
			wantErr: ErrInvalidRequest,
		},
		{
			name: "empty category name",
			req: CreateEventRequest{
				VenueBookingID: "booking-id",
				Title:          "Go Conference",
				TicketCategories: []CreateTicketCategoryRequest{
					{Name: "   ", PriceMinor: 50000, Capacity: 100},
				},
			},
			wantErr: ErrInvalidRequest,
		},
		{
			name: "negative price",
			req: CreateEventRequest{
				VenueBookingID: "booking-id",
				Title:          "Go Conference",
				TicketCategories: []CreateTicketCategoryRequest{
					{Name: "Prime", PriceMinor: -1, Capacity: 100},
				},
			},
			wantErr: ErrInvalidRequest,
		},
		{
			name: "zero capacity",
			req: CreateEventRequest{
				VenueBookingID: "booking-id",
				Title:          "Go Conference",
				TicketCategories: []CreateTicketCategoryRequest{
					{Name: "Prime", PriceMinor: 50000, Capacity: 0},
				},
			},
			wantErr: ErrInvalidRequest,
		},
		{
			name: "negative capacity",
			req: CreateEventRequest{
				VenueBookingID: "booking-id",
				Title:          "Go Conference",
				TicketCategories: []CreateTicketCategoryRequest{
					{Name: "Prime", PriceMinor: 50000, Capacity: -1},
				},
			},
			wantErr: ErrInvalidRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCreateEventRequest(&tt.req)

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

func TestValidateCreateEventRequest_TrimsWhitespace(t *testing.T) {
	req := CreateEventRequest{
		VenueBookingID: "booking-id",
		Title:          "  Go Conference  ",
		Description:    "  A Go conference  ",
		TicketCategories: []CreateTicketCategoryRequest{
			{
				Name:       "  Prime  ",
				PriceMinor: 50000,
				Capacity:   100,
			},
		},
	}

	if err := validateCreateEventRequest(&req); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}

	if req.Title != "Go Conference" {
		t.Errorf("expected trimmed title, got %q", req.Title)
	}

	if req.Description != "A Go conference" {
		t.Errorf("expected trimmed description, got %q", req.Description)
	}

	if req.TicketCategories[0].Name != "Prime" {
		t.Errorf(
			"expected trimmed category name, got %q",
			req.TicketCategories[0].Name,
		)
	}
}

func TestIsActiveEventUniqueViolation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "matching unique violation",
			err: &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "uq_events_active_venue_booking",
			},
			want: true,
		},
		{
			name: "different unique constraint",
			err: &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "some_other_constraint",
			},
			want: false,
		},
		{
			name: "different postgres error code",
			err: &pgconn.PgError{
				Code:           "23503",
				ConstraintName: "uq_events_active_venue_booking",
			},
			want: false,
		},
		{
			name: "ordinary error",
			err:  errors.New("database unavailable"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isActiveEventUniqueViolation(tt.err)

			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestNewService_NilPool(t *testing.T) {
	_, err := NewService(nil)

	if err == nil {
		t.Fatal("expected error for nil database pool")
	}
}
