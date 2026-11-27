package event

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
			name:           "invalid request",
			err:            ErrInvalidRequest,
			wantHandled:    true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "venue booking not found",
			err:            ErrVenueBookingNotFound,
			wantHandled:    true,
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "venue booking not owned",
			err:            ErrVenueBookingNotOwned,
			wantHandled:    true,
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "venue booking unavailable",
			err:            ErrVenueBookingUnavailable,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:           "active event already exists",
			err:            ErrActiveEventAlreadyExists,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:           "ticket capacity exceeded",
			err:            ErrTicketCapacityExceeded,
			wantHandled:    true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "event not found",
			err:            ErrEventNotFound,
			wantHandled:    true,
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "event not active",
			err:            ErrEventNotActive,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:           "event already cancelled",
			err:            ErrEventAlreadyCancelled,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:           "invalid booking status",
			err:            ErrInvalidBookingStatus,
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

func TestCreateEvent_RequiresAuthentication(t *testing.T) {
	handler := NewHandler(nil, nil, nil)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/events",
		strings.NewReader(`{}`),
	)

	recorder := httptest.NewRecorder()

	handler.CreateEvent(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusUnauthorized,
			recorder.Code,
		)
	}
}

func TestUpdateBookingStatus_RequiresAuthentication(t *testing.T) {
	handler := NewHandler(nil, nil, nil)

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/events/test-event-id/booking-status",
		strings.NewReader(`{"booking_status":"paused"}`),
	)

	recorder := httptest.NewRecorder()

	handler.UpdateBookingStatus(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusUnauthorized,
			recorder.Code,
		)
	}
}
