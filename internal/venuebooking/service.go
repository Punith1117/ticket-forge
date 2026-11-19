package venuebooking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/Punith1117/ticket-forge/internal/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxActiveBookings = 10

	minBookingDuration = 15 * time.Minute
	maxBookingDuration = 7 * 24 * time.Hour

	minBookingNotice = 15 * time.Minute

	createBookingOperation = "venue_booking.create"
)

type Service struct {
	pool        *pgxpool.Pool
	repository  *Repository
	idempotency *idempotency.Service
	now         func() time.Time
}

func NewService(pool *pgxpool.Pool) (*Service, error) {
	if pool == nil {
		return nil, errors.New("venuebooking: database pool cannot be nil")
	}

	repository, err := NewRepository(pool)
	if err != nil {
		return nil, fmt.Errorf("venuebooking: failed to initialize repository: %w", err)
	}

	return &Service{
		pool:        pool,
		repository:  repository,
		idempotency: idempotency.NewService(),
		now:         time.Now,
	}, nil
}

// Create creates a venue booking for an organizer.
//
// userID scopes the idempotency record.
// organizerProfileID identifies the organizer profile that owns the booking.
func (s *Service) Create(
	ctx context.Context,
	userID string,
	organizerProfileID string,
	idempotencyKey string,
	req CreateVenueBookingRequest,
) (CreateVenueBookingResponse, error) {
	var response CreateVenueBookingResponse

	parsedUserID, err := uuid.Parse(userID)
	if err != nil {
		return response, fmt.Errorf("venuebooking: invalid user ID: %w", err)
	}

	parsedOrganizerID, err := uuid.Parse(organizerProfileID)
	if err != nil {
		return response, fmt.Errorf("venuebooking: invalid organizer profile ID: %w", err)
	}

	parsedVenueID, err := uuid.Parse(req.VenueID)
	if err != nil {
		return response, ErrVenueNotFound
	}

	venueBookingID := pgtype.UUID{
		Bytes: parsedVenueID,
		Valid: true,
	}

	organizerID := pgtype.UUID{
		Bytes: parsedOrganizerID,
		Valid: true,
	}

	if err := s.validateSchedule(req); err != nil {
		return response, err
	}

	endsAt := req.StartsAt.Add(time.Duration(req.DurationMinutes) * time.Minute)

	payload, err := json.Marshal(req)
	if err != nil {
		return response, fmt.Errorf("venuebooking: failed to encode request: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return response, fmt.Errorf("venuebooking: failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	execution, err := s.idempotency.Begin(ctx, tx, idempotency.Request{
		UserID:    parsedUserID.String(),
		Operation: createBookingOperation,
		Key:       idempotencyKey,
		Payload:   payload,
	})
	if err != nil {
		return response, fmt.Errorf("venuebooking: failed to begin idempotent operation: %w", err)
	}

	// A replay returns the original successful response.
	if execution.Replay != nil {
		if err := json.Unmarshal(execution.Replay.Body, &response); err != nil {
			return response, fmt.Errorf("venuebooking: failed to decode replay response: %w", err)
		}

		if err := tx.Rollback(ctx); err != nil {
			return CreateVenueBookingResponse{}, fmt.Errorf(
				"venuebooking: failed to close replay transaction: %w", err,
			)
		}

		return response, nil
	}

	// Serialize booking creation for this organizer.
	if err := s.repository.LockOrganizerProfile(ctx, tx, organizerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return response, ErrOrganizerNotFound
		}
		return response, err
	}

	// Ensure the venue is active and prevent concurrent deactivation.
	_, err = s.repository.GetActiveVenueForBooking(ctx, tx, venueBookingID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return response, ErrVenueUnavailable
		}
		return response, err
	}

	// Enforce the organizer's maximum number of active unfinished bookings.
	count, err := s.repository.CountActiveUnfinishedVenueBookings(ctx, tx, organizerID)
	if err != nil {
		return response, err
	}

	if count >= maxActiveBookings {
		return response, ErrVenueBookingLimitReached
	}

	startsAt := pgtype.Timestamptz{
		Time:  req.StartsAt.UTC(),
		Valid: true,
	}

	bookingEndsAt := pgtype.Timestamptz{
		Time:  endsAt.UTC(),
		Valid: true,
	}

	booking, err := s.repository.CreateVenueBooking(ctx, tx, sqlc.CreateVenueBookingParams{
		OrganizerProfileID: organizerID,
		VenueID:            venueBookingID,
		StartsAt:           startsAt,
		EndsAt:             bookingEndsAt,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) &&
			pgErr.Code == "23P01" &&
			pgErr.ConstraintName == "venue_bookings_venue_no_overlap" {
			return response, ErrVenueBookingOverlap
		}

		return response, err
	}

	response = CreateVenueBookingResponse{
		Message: "Venue booking created successfully",
		VenueBooking: VenueBookingResponse{
			ID:                 booking.ID.String(),
			OrganizerProfileID: booking.OrganizerProfileID.String(),
			VenueID:            booking.VenueID.String(),
			StartsAt:           booking.StartsAt.Time,
			EndsAt:             booking.EndsAt.Time,
			Status:             booking.Status,
			CreatedAt:          booking.CreatedAt.Time,
			UpdatedAt:          booking.UpdatedAt.Time,
		},
	}

	responseBody, err := json.Marshal(response)
	if err != nil {
		return CreateVenueBookingResponse{}, fmt.Errorf(
			"venuebooking: failed to encode booking response: %w", err,
		)
	}

	if err := s.idempotency.Complete(ctx, tx, execution, 201, responseBody); err != nil {
		return CreateVenueBookingResponse{}, fmt.Errorf(
			"venuebooking: failed to complete idempotent operation: %w", err,
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateVenueBookingResponse{}, fmt.Errorf(
			"venuebooking: failed to commit booking transaction: %w", err,
		)
	}

	return response, nil
}

// Get retrieves a venue booking belonging to an organizer.
func (s *Service) Get(
	ctx context.Context,
	organizerProfileID string,
	bookingID string,
) (GetVenueBookingResponse, error) {
	var response GetVenueBookingResponse

	organizerID, err := parseUUID(organizerProfileID)
	if err != nil {
		return response, fmt.Errorf("venuebooking: invalid organizer profile ID: %w", err)
	}

	id, err := parseUUID(bookingID)
	if err != nil {
		return response, ErrVenueBookingNotFound
	}

	booking, err := s.repository.GetVenueBookingForOrganizer(
		ctx,
		organizerID,
		id,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return response, ErrVenueBookingNotFound
		}
		return response, err
	}

	response.VenueBooking = VenueBookingResponse{
		ID:                 booking.ID.String(),
		OrganizerProfileID: booking.OrganizerProfileID.String(),
		VenueID:            booking.VenueID.String(),
		StartsAt:           booking.StartsAt.Time,
		EndsAt:             booking.EndsAt.Time,
		Status:             booking.Status,
		CreatedAt:          booking.CreatedAt.Time,
		UpdatedAt:          booking.UpdatedAt.Time,
	}

	return response, nil
}

// List retrieves all venue bookings belonging to an organizer.
func (s *Service) List(
	ctx context.Context,
	organizerProfileID string,
) (ListVenueBookingsResponse, error) {
	var response ListVenueBookingsResponse

	organizerID, err := parseUUID(organizerProfileID)
	if err != nil {
		return response, fmt.Errorf("venuebooking: invalid organizer profile ID: %w", err)
	}

	bookings, err := s.repository.ListOrganizerVenueBookings(ctx, organizerID)
	if err != nil {
		return response, err
	}

	response.VenueBookings = make([]VenueBookingResponse, 0, len(bookings))

	for _, booking := range bookings {
		response.VenueBookings = append(response.VenueBookings, VenueBookingResponse{
			ID:                 booking.ID.String(),
			OrganizerProfileID: booking.OrganizerProfileID.String(),
			VenueID:            booking.VenueID.String(),
			StartsAt:           booking.StartsAt.Time,
			EndsAt:             booking.EndsAt.Time,
			Status:             booking.Status,
			CreatedAt:          booking.CreatedAt.Time,
			UpdatedAt:          booking.UpdatedAt.Time,
		})
	}

	return response, nil
}

// Cancel cancels a venue booking if it has no associated active event.
func (s *Service) Cancel(
	ctx context.Context,
	organizerProfileID string,
	bookingID string,
) (CancelVenueBookingResponse, error) {
	var response CancelVenueBookingResponse

	organizerID, err := parseUUID(organizerProfileID)
	if err != nil {
		return response, fmt.Errorf("venuebooking: invalid organizer profile ID: %w", err)
	}

	id, err := parseUUID(bookingID)
	if err != nil {
		return response, ErrVenueBookingNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return response, fmt.Errorf("venuebooking: failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	booking, err := s.repository.LockVenueBookingForOrganizer(
		ctx,
		tx,
		organizerID,
		id,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return response, ErrVenueBookingNotFound
		}
		return response, err
	}

	if booking.Status == "cancelled" {
		return response, ErrVenueBookingCancelled
	}

	hasActiveEvent, err := s.repository.HasActiveEventForVenueBooking(ctx, tx, id)
	if err != nil {
		return response, err
	}

	if hasActiveEvent {
		return response, ErrVenueBookingHasActiveEvent
	}

	cancelled, err := s.repository.CancelVenueBooking(
		ctx,
		tx,
		organizerID,
		id,
	)
	if err != nil {
		return response, err
	}

	response = CancelVenueBookingResponse{
		Message: "Venue booking cancelled successfully",
		VenueBooking: VenueBookingResponse{
			ID:                 cancelled.ID.String(),
			OrganizerProfileID: cancelled.OrganizerProfileID.String(),
			VenueID:            cancelled.VenueID.String(),
			StartsAt:           cancelled.StartsAt.Time,
			EndsAt:             cancelled.EndsAt.Time,
			Status:             cancelled.Status,
			CreatedAt:          cancelled.CreatedAt.Time,
			UpdatedAt:          cancelled.UpdatedAt.Time,
		},
	}

	if err := tx.Commit(ctx); err != nil {
		return CancelVenueBookingResponse{}, fmt.Errorf(
			"venuebooking: failed to commit cancellation: %w", err,
		)
	}

	return response, nil
}

func (s *Service) validateSchedule(req CreateVenueBookingRequest) error {
	if req.StartsAt.IsZero() {
		return ErrInvalidSchedule
	}

	duration := time.Duration(req.DurationMinutes) * time.Minute

	if duration < minBookingDuration || duration > maxBookingDuration {
		return ErrInvalidDuration
	}

	now := s.now().UTC()
	startsAt := req.StartsAt.UTC()

	if startsAt.Before(now.Add(minBookingNotice)) {
		return ErrStartTimeTooSoon
	}

	if startsAt.After(now.AddDate(1, 0, 0)) {
		return ErrStartTimeTooFar
	}

	return nil
}

func parseUUID(value string) (pgtype.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}

	return pgtype.UUID{
		Bytes: id,
		Valid: true,
	}, nil
}
