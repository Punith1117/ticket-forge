package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/Punith1117/ticket-forge/internal/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	createEventOperation = "event.create"

	eventStatusActive = "active"
	bookingStatusOpen = "open"
)

type Service struct {
	pool        *pgxpool.Pool
	repository  *Repository
	idempotency *idempotency.Service
}

func NewService(pool *pgxpool.Pool) (*Service, error) {
	if pool == nil {
		return nil, errors.New("event: database pool cannot be nil")
	}

	repository, err := NewRepository(pool)
	if err != nil {
		return nil, fmt.Errorf("event: failed to initialize repository: %w", err)
	}

	return &Service{
		pool:        pool,
		repository:  repository,
		idempotency: idempotency.NewService(),
	}, nil
}

// Create creates an event and its ticket categories atomically.
//
// userID scopes the idempotency record.
// organizerProfileID identifies the organizer who owns the venue booking.
func (s *Service) Create(
	ctx context.Context,
	userID string,
	organizerProfileID string,
	idempotencyKey string,
	req CreateEventRequest,
) (CreateEventResponse, error) {
	var response CreateEventResponse

	parsedUserID, err := uuid.Parse(userID)
	if err != nil {
		return response, fmt.Errorf("event: invalid user ID: %w", err)
	}

	parsedOrganizerID, err := uuid.Parse(organizerProfileID)
	if err != nil {
		return response, fmt.Errorf("event: invalid organizer profile ID: %w", err)
	}

	parsedBookingID, err := uuid.Parse(req.VenueBookingID)
	if err != nil {
		return response, ErrVenueBookingNotFound
	}

	organizerIDParam := pgtype.UUID{
		Bytes: parsedOrganizerID,
		Valid: true,
	}

	bookingIDParam := pgtype.UUID{
		Bytes: parsedBookingID,
		Valid: true,
	}

	if err := validateCreateEventRequest(&req); err != nil {
		return response, err
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return response, fmt.Errorf("event: failed to encode request: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return response, fmt.Errorf("event: failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	execution, err := s.idempotency.Begin(ctx, tx, idempotency.Request{
		UserID:    parsedUserID.String(),
		Operation: createEventOperation,
		Key:       idempotencyKey,
		Payload:   payload,
	})
	if err != nil {
		return response, fmt.Errorf("event: failed to begin idempotent operation: %w", err)
	}

	// Return the original response for a successful replay.
	if execution.Replay != nil {
		if err := json.Unmarshal(execution.Replay.Body, &response); err != nil {
			return response, fmt.Errorf("event: failed to decode replay response: %w", err)
		}

		if err := tx.Rollback(ctx); err != nil {
			return CreateEventResponse{}, fmt.Errorf(
				"event: failed to close replay transaction: %w", err,
			)
		}

		return response, nil
	}

	// Lock the booking and verify that it belongs to this organizer.
	booking, err := s.repository.LockVenueBookingForOrganizer(
		ctx,
		tx,
		bookingIDParam,
		organizerIDParam,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return response, ErrVenueBookingNotFound
		}
		return response, err
	}

	if booking.Status != "active" {
		return response, ErrVenueBookingUnavailable
	}

	// The venue may have been deactivated after the booking was confirmed.
	// Existing active bookings remain valid for event creation.
	venueCapacity, err := s.repository.GetVenueCapacityForBooking(
		ctx,
		tx,
		bookingIDParam,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return response, ErrVenueBookingNotFound
		}
		return response, err
	}

	// Check whether this booking already has an active event.
	hasActiveEvent, err := s.repository.HasActiveEventForVenueBooking(
		ctx,
		tx,
		bookingIDParam,
	)
	if err != nil {
		return response, err
	}

	if hasActiveEvent {
		return response, ErrActiveEventAlreadyExists
	}

	// Ensure the requested ticket capacity does not exceed venue capacity.
	var totalCapacity int64
	for _, category := range req.TicketCategories {
		totalCapacity += int64(category.Capacity)
	}

	if totalCapacity > int64(venueCapacity) {
		return response, ErrTicketCapacityExceeded
	}

	createdEvent, err := s.repository.CreateEvent(
		ctx,
		tx,
		sqlc.CreateEventParams{
			OrganizerProfileID: organizerIDParam,
			VenueBookingID:     bookingIDParam,
			Title:              req.Title,
			Description:        req.Description,
			Status:             eventStatusActive,
			BookingStatus:      bookingStatusOpen,
		},
	)
	if err != nil {
		if isActiveEventUniqueViolation(err) {
			return response, ErrActiveEventAlreadyExists
		}
		return response, err
	}

	response = CreateEventResponse{
		Message: "Event created successfully",
		Event: EventResponse{
			ID:                 createdEvent.ID.String(),
			OrganizerProfileID: createdEvent.OrganizerProfileID.String(),
			VenueBookingID:     createdEvent.VenueBookingID.String(),
			Title:              createdEvent.Title,
			Description:        createdEvent.Description,
			Status:             createdEvent.Status,
			BookingStatus:      createdEvent.BookingStatus,
			CreatedAt:          createdEvent.CreatedAt.Time,
			UpdatedAt:          createdEvent.UpdatedAt.Time,
		},
		TicketCategories: make([]TicketCategoryResponse, 0, len(req.TicketCategories)),
	}

	for _, category := range req.TicketCategories {
		createdCategory, err := s.repository.CreateTicketCategory(
			ctx,
			tx,
			sqlc.CreateTicketCategoryParams{
				EventID:    createdEvent.ID,
				Name:       category.Name,
				PriceMinor: category.PriceMinor,
				Capacity:   category.Capacity,
			},
		)
		if err != nil {
			return CreateEventResponse{}, err
		}

		response.TicketCategories = append(
			response.TicketCategories,
			TicketCategoryResponse{
				ID:         createdCategory.ID.String(),
				EventID:    createdCategory.EventID.String(),
				Name:       createdCategory.Name,
				PriceMinor: createdCategory.PriceMinor,
				Currency:   createdCategory.Currency,
				Capacity:   createdCategory.Capacity,
				IsActive:   createdCategory.IsActive,
				CreatedAt:  createdCategory.CreatedAt.Time,
				UpdatedAt:  createdCategory.UpdatedAt.Time,
			},
		)
	}

	responseBody, err := json.Marshal(response)
	if err != nil {
		return CreateEventResponse{}, fmt.Errorf(
			"event: failed to encode event response: %w", err,
		)
	}

	if err := s.idempotency.Complete(
		ctx,
		tx,
		execution,
		201,
		responseBody,
	); err != nil {
		return CreateEventResponse{}, fmt.Errorf(
			"event: failed to complete idempotent operation: %w", err,
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateEventResponse{}, fmt.Errorf(
			"event: failed to commit event transaction: %w", err,
		)
	}

	return response, nil
}

func validateCreateEventRequest(req *CreateEventRequest) error {
	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)

	if req.Title == "" || strings.TrimSpace(req.VenueBookingID) == "" {
		return ErrInvalidRequest
	}

	if len(req.TicketCategories) == 0 {
		return ErrInvalidRequest
	}

	for i := range req.TicketCategories {
		category := &req.TicketCategories[i]
		category.Name = strings.TrimSpace(category.Name)

		if category.Name == "" ||
			category.PriceMinor < 0 ||
			category.Capacity <= 0 {
			return ErrInvalidRequest
		}
	}

	return nil
}

func isActiveEventUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "uq_events_active_venue_booking"
}
