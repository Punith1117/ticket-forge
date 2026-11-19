package venuebooking

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/idempotency"
)

func TestHandleServiceError(t *testing.T) {
	handler := NewHandler(nil, nil, nil)

	tests := []struct {
		name           string
		err            error
		wantHandled    bool
		wantStatusCode int
	}{
		{
			name:           "organizer not found",
			err:            ErrOrganizerNotFound,
			wantHandled:    true,
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "venue not found",
			err:            ErrVenueNotFound,
			wantHandled:    true,
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "venue unavailable",
			err:            ErrVenueUnavailable,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:           "booking not found",
			err:            ErrVenueBookingNotFound,
			wantHandled:    true,
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "booking already cancelled",
			err:            ErrVenueBookingCancelled,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:           "booking has active event",
			err:            ErrVenueBookingHasActiveEvent,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:           "booking limit reached",
			err:            ErrVenueBookingLimitReached,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:           "booking overlap",
			err:            ErrVenueBookingOverlap,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:           "invalid schedule",
			err:            ErrInvalidSchedule,
			wantHandled:    true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "invalid duration",
			err:            ErrInvalidDuration,
			wantHandled:    true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "start time too soon",
			err:            ErrStartTimeTooSoon,
			wantHandled:    true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "start time too far",
			err:            ErrStartTimeTooFar,
			wantHandled:    true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "invalid idempotency request",
			err:            idempotency.ErrInvalidRequest,
			wantHandled:    true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "idempotency key reused",
			err:            idempotency.ErrKeyReused,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:        "incomplete idempotency record",
			err:         idempotency.ErrIncompleteRecord,
			wantHandled: false,
		},
		{
			name:        "unexpected error",
			err:         errors.New("database connection failed"),
			wantHandled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			handled := handler.handleServiceError(recorder, tt.err)

			if handled != tt.wantHandled {
				t.Fatalf(
					"expected handled=%v, got %v",
					tt.wantHandled,
					handled,
				)
			}

			if tt.wantHandled && recorder.Code != tt.wantStatusCode {
				t.Fatalf(
					"expected status %d, got %d",
					tt.wantStatusCode,
					recorder.Code,
				)
			}
		})
	}
}
