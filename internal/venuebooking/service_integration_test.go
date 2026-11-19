package venuebooking_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/idempotency"
	"github.com/Punith1117/ticket-forge/internal/venuebooking"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupIntegrationDB(t *testing.T) (
	*pgxpool.Pool,
	*venuebooking.Service,
) {
	t.Helper()

	config.Load("../../.env")

	cfg := database.ConfigFromEnv()

	testDB := os.Getenv("TEST_POSTGRES_DB")
	if testDB == "" {
		t.Fatal("TEST_POSTGRES_DB must be set")
	}
	cfg.Database = testDB

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.NewPool(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}

	t.Cleanup(pool.Close)

	seedPath := filepath.Join("..", "..", "db", "seeds", "001_venues.sql")
	seedSQL, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("failed to read venue seeds: %v", err)
	}

	if _, err := pool.Exec(ctx, string(seedSQL)); err != nil {
		t.Fatalf("failed to apply venue seeds: %v", err)
	}

	service, err := venuebooking.NewService(pool)
	if err != nil {
		t.Fatalf("failed to initialize venue booking service: %v", err)
	}

	return pool, service
}

func createTestUserAndOrganizer(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (userID string, organizerID string) {
	t.Helper()

	email := fmt.Sprintf("venuebooking_%s@example.com", uuid.NewString())

	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ($1, 'hash_placeholder', 'Venue Booking Test User')
		RETURNING id::text
	`, email).Scan(&userID)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO organizer_profiles (user_id, display_name)
		VALUES ($1, 'Test Organizer')
		RETURNING id::text
	`, userID).Scan(&organizerID)
	if err != nil {
		t.Fatalf("failed to create organizer profile: %v", err)
	}

	return userID, organizerID
}

func getActiveVenueID(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) string {
	t.Helper()

	var venueID string

	err := pool.QueryRow(ctx, `
		SELECT id::text
		FROM venues
		WHERE is_active = true
		ORDER BY id
		LIMIT 1
	`).Scan(&venueID)
	if err != nil {
		t.Fatalf("failed to retrieve active test venue: %v", err)
	}

	return venueID
}

func newCreateRequest(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	venueID string,
) venuebooking.CreateVenueBookingRequest {
	t.Helper()

	var latestEnd time.Time

	err := pool.QueryRow(ctx, `
        SELECT COALESCE(MAX(ends_at), now() + interval '48 hours')
        FROM venue_bookings
        WHERE venue_id = $1
          AND status = 'active'
    `, venueID).Scan(&latestEnd)
	if err != nil {
		t.Fatalf("failed to find next available test schedule: %v", err)
	}

	startsAt := time.Now().UTC().Add(48 * time.Hour)

	if !latestEnd.Before(startsAt) {
		startsAt = latestEnd.Add(time.Hour)
	}

	return venuebooking.CreateVenueBookingRequest{
		VenueID:         venueID,
		StartsAt:        startsAt,
		DurationMinutes: 60,
	}
}

func TestCreate_Success(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID := getActiveVenueID(t, ctx, pool)

	req := newCreateRequest(t, ctx, pool, venueID)

	response, err := service.Create(
		ctx,
		userID,
		organizerID,
		uuid.NewString(),
		req,
	)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if response.VenueBooking.ID == "" {
		t.Fatal("expected a venue booking ID")
	}

	if response.VenueBooking.OrganizerProfileID != organizerID {
		t.Errorf(
			"expected organizer ID %s, got %s",
			organizerID,
			response.VenueBooking.OrganizerProfileID,
		)
	}

	if response.VenueBooking.VenueID != venueID {
		t.Errorf(
			"expected venue ID %s, got %s",
			venueID,
			response.VenueBooking.VenueID,
		)
	}

	if response.VenueBooking.Status != "active" {
		t.Errorf(
			"expected status active, got %s",
			response.VenueBooking.Status,
		)
	}

	expectedEnd := response.VenueBooking.StartsAt.Add(
		time.Duration(req.DurationMinutes) * time.Minute,
	)

	if !response.VenueBooking.EndsAt.Equal(expectedEnd) {
		t.Errorf(
			"expected end time %s, got %s",
			expectedEnd,
			response.VenueBooking.EndsAt,
		)
	}
}

func TestCreate_IdempotencyReplay(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID := getActiveVenueID(t, ctx, pool)

	req := newCreateRequest(t, ctx, pool, venueID)
	key := uuid.NewString()

	first, err := service.Create(ctx, userID, organizerID, key, req)
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	second, err := service.Create(ctx, userID, organizerID, key, req)
	if err != nil {
		t.Fatalf("second Create failed: %v", err)
	}

	if first.VenueBooking.ID != second.VenueBooking.ID {
		t.Fatalf(
			"expected replayed booking ID %s, got %s",
			first.VenueBooking.ID,
			second.VenueBooking.ID,
		)
	}

	var count int

	err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM venue_bookings
		WHERE organizer_profile_id = $1
	`, organizerID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count venue bookings: %v", err)
	}

	if count != 1 {
		t.Errorf("expected exactly 1 booking, got %d", count)
	}
}

func TestCreate_IdempotencyKeyReuse(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID := getActiveVenueID(t, ctx, pool)

	req := newCreateRequest(t, ctx, pool, venueID)
	key := uuid.NewString()

	_, err := service.Create(ctx, userID, organizerID, key, req)
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	// Change the request while reusing the original key.
	req.DurationMinutes = 120

	_, err = service.Create(ctx, userID, organizerID, key, req)

	if !errors.Is(err, idempotency.ErrKeyReused) {
		t.Fatalf("expected ErrKeyReused, got %v", err)
	}
}

func TestCreate_RejectsOverlappingBooking(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID := getActiveVenueID(t, ctx, pool)

	firstReq := newCreateRequest(t, ctx, pool, venueID)

	_, err := service.Create(
		ctx,
		userID,
		organizerID,
		uuid.NewString(),
		firstReq,
	)
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	secondReq := firstReq
	secondReq.StartsAt = firstReq.StartsAt.Add(30 * time.Minute)

	_, err = service.Create(
		ctx,
		userID,
		organizerID,
		uuid.NewString(),
		secondReq,
	)

	if !errors.Is(err, venuebooking.ErrVenueBookingOverlap) {
		t.Fatalf("expected ErrVenueBookingOverlap, got %v", err)
	}
}
