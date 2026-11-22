package booking

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/idempotency"
)

func TestHandleServiceError(t *testing.T) {
	handler := NewHandler(nil, nil)

	tests := []struct {
		name           string
		err            error
		wantHandled    bool
		wantStatusCode int
	}{
		{
			name:           "invalid quantity",
			err:            ErrInvalidQuantity,
			wantHandled:    true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "missing ticket category ID",
			err:            ErrMissingTicketCategoryID,
			wantHandled:    true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "invalid event ID",
			err:            ErrInvalidEventID,
			wantHandled:    true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "invalid ticket category ID",
			err:            ErrInvalidTicketCategoryID,
			wantHandled:    true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "event not bookable",
			err:            ErrEventNotBookable,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:           "ticket category unavailable",
			err:            ErrTicketCategoryUnavailable,
			wantHandled:    true,
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "insufficient inventory",
			err:            ErrInsufficientInventory,
			wantHandled:    true,
			wantStatusCode: http.StatusConflict,
		},
		{
			name:        "inventory unavailable",
			err:         ErrInventoryUnavailable,
			wantHandled: false,
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

func TestCreateReservation_RequiresAuthentication(t *testing.T) {
	handler := NewHandler(nil, nil)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/events/test-event-id/reservations",
		strings.NewReader(`{"ticket_category_id":"test","quantity":2}`),
	)

	recorder := httptest.NewRecorder()

	handler.CreateReservation(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusUnauthorized,
			recorder.Code,
		)
	}
}
