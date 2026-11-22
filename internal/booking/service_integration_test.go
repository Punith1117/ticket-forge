package booking_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/booking"
	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/event"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type bookingTestFixture struct {
	pool       *pgxpool.Pool
	service    *booking.Service
	customerID string
	eventID    string
	categoryID string
}

func setupBookingIntegrationDB(t *testing.T) *bookingTestFixture {
	t.Helper()

	config.Load("../../.env")

	cfg := database.ConfigFromEnv()

	testDB := os.Getenv("TEST_POSTGRES_DB")
	if testDB == "" {
		t.Fatal("TEST_POSTGRES_DB must be set")
	}
	cfg.Database = testDB

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	customerID := createBookingTestUser(t, ctx, pool)
	organizerID := createBookingTestOrganizer(t, ctx, pool, customerID)

	venueID, venueCapacity := getBookingTestVenue(t, ctx, pool)
	venueBookingID := createBookingTestVenueBooking(
		t,
		ctx,
		pool,
		organizerID,
		venueID,
	)

	eventService, err := event.NewService(pool)
	if err != nil {
		t.Fatalf("failed to initialize event service: %v", err)
	}

	eventResponse, err := eventService.Create(
		ctx,
		customerID,
		organizerID,
		uuid.NewString(),
		event.CreateEventRequest{
			VenueBookingID: venueBookingID,
			Title:          "Booking Integration Test",
			Description:    "Test event for reservation lifecycle",
			TicketCategories: []event.CreateTicketCategoryRequest{
				{
					Name:       "General",
					PriceMinor: 50000,
					Capacity:   min(10, venueCapacity),
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("failed to create test event: %v", err)
	}

	categoryID := eventResponse.TicketCategories[0].ID

	bookingService, err := booking.NewService(pool)
	if err != nil {
		t.Fatalf("failed to initialize booking service: %v", err)
	}

	return &bookingTestFixture{
		pool:       pool,
		service:    bookingService,
		customerID: customerID,
		eventID:    eventResponse.Event.ID,
		categoryID: categoryID,
	}
}

func createBookingTestUser(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) string {
	t.Helper()

	email := fmt.Sprintf("booking_%s@example.com", uuid.NewString())

	var userID string
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ($1, 'hash_placeholder', 'Booking Test User')
		RETURNING id::text
	`, email).Scan(&userID)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	return userID
}

func createBookingTestOrganizer(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	userID string,
) string {
	t.Helper()

	var organizerID string
	err := pool.QueryRow(ctx, `
		INSERT INTO organizer_profiles (user_id, display_name)
		VALUES ($1, 'Booking Test Organizer')
		RETURNING id::text
	`, userID).Scan(&organizerID)
	if err != nil {
		t.Fatalf("failed to create test organizer: %v", err)
	}

	return organizerID
}

func getBookingTestVenue(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (string, int32) {
	t.Helper()

	var venueID string
	var capacity int32

	err := pool.QueryRow(ctx, `
		SELECT id::text, capacity
		FROM venues
		WHERE is_active = true
		ORDER BY id
		LIMIT 1
	`).Scan(&venueID, &capacity)
	if err != nil {
		t.Fatalf("failed to retrieve active venue: %v", err)
	}

	return venueID, capacity
}

func createBookingTestVenueBooking(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	organizerID string,
	venueID string,
) string {
	t.Helper()

	var startsAt time.Time

	err := pool.QueryRow(ctx, `
		SELECT
			GREATEST(
				COALESCE(MAX(ends_at), now()),
				now() + interval '30 days'
			) + interval '1 hour'
		FROM venue_bookings
		WHERE venue_id = $1
		  AND status = 'active'
	`, venueID).Scan(&startsAt)
	if err != nil {
		t.Fatalf("failed to calculate booking schedule: %v", err)
	}

	var bookingID string

	err = pool.QueryRow(ctx, `
		INSERT INTO venue_bookings (
			organizer_profile_id,
			venue_id,
			starts_at,
			ends_at
		)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text
	`,
		organizerID,
		venueID,
		startsAt,
		startsAt.Add(time.Hour),
	).Scan(&bookingID)
	if err != nil {
		t.Fatalf("failed to create venue booking: %v", err)
	}

	return bookingID
}

func (f *bookingTestFixture) createReservation(
	t *testing.T,
	quantity int32,
) booking.CreateReservationResponse {
	t.Helper()

	response, err := f.service.CreateReservation(
		context.Background(),
		f.customerID,
		f.eventID,
		booking.CreateReservationRequest{
			TicketCategoryID: f.categoryID,
			Quantity:         quantity,
		},
		uuid.NewString(),
	)
	if err != nil {
		t.Fatalf("failed to create reservation: %v", err)
	}

	return response
}

func TestCreateReservation_LazyExpirationReleasesInventory(t *testing.T) {
	f := setupBookingIntegrationDB(t)
	ctx := context.Background()

	// First customer request reserves 2 tickets.
	first := f.createReservation(t, 2)

	if first.Status != "held" {
		t.Fatalf("expected held status, got %s", first.Status)
	}

	// Simulate the passage of five minutes without sleeping.
	_, err := f.pool.Exec(ctx, `
		UPDATE reservations
		SET expires_at = clock_timestamp() - interval '1 second'
		WHERE id = $1
	`, first.ID)
	if err != nil {
		t.Fatalf("failed to expire test reservation: %v", err)
	}

	// A new request for the same category should reclaim the expired hold.
	second := f.createReservation(t, 3)

	if second.Status != "held" {
		t.Fatalf("expected second reservation to be held, got %s", second.Status)
	}

	// Verify the old reservation was actually expired.
	var firstStatus string

	err = f.pool.QueryRow(ctx, `
		SELECT status
		FROM reservations
		WHERE id = $1
	`, first.ID).Scan(&firstStatus)
	if err != nil {
		t.Fatalf("failed to retrieve first reservation: %v", err)
	}

	if firstStatus != "expired" {
		t.Fatalf(
			"expected first reservation status expired, got %s",
			firstStatus,
		)
	}

	// The old quantity (2) must have been released.
	// Only the new reservation quantity (3) should remain reserved.
	var reservedQuantity int32
	var soldQuantity int32

	err = f.pool.QueryRow(ctx, `
		SELECT reserved_quantity, sold_quantity
		FROM inventory
		WHERE ticket_category_id = $1
	`, f.categoryID).Scan(&reservedQuantity, &soldQuantity)
	if err != nil {
		t.Fatalf("failed to retrieve inventory: %v", err)
	}

	if reservedQuantity != 3 {
		t.Errorf(
			"expected reserved quantity 3, got %d",
			reservedQuantity,
		)
	}

	if soldQuantity != 0 {
		t.Errorf(
			"expected sold quantity 0, got %d",
			soldQuantity,
		)
	}
}

func TestCreateReservation_DoesNotExpireActiveReservations(t *testing.T) {
	f := setupBookingIntegrationDB(t)
	ctx := context.Background()

	first := f.createReservation(t, 2)
	second := f.createReservation(t, 3)

	var firstStatus string
	var secondStatus string

	err := f.pool.QueryRow(ctx, `
		SELECT status
		FROM reservations
		WHERE id = $1
	`, first.ID).Scan(&firstStatus)
	if err != nil {
		t.Fatalf("failed to retrieve first reservation: %v", err)
	}

	err = f.pool.QueryRow(ctx, `
		SELECT status
		FROM reservations
		WHERE id = $1
	`, second.ID).Scan(&secondStatus)
	if err != nil {
		t.Fatalf("failed to retrieve second reservation: %v", err)
	}

	if firstStatus != "held" || secondStatus != "held" {
		t.Fatalf(
			"expected both reservations to remain held; got %s and %s",
			firstStatus,
			secondStatus,
		)
	}

	var reservedQuantity int32

	err = f.pool.QueryRow(ctx, `
		SELECT reserved_quantity
		FROM inventory
		WHERE ticket_category_id = $1
	`, f.categoryID).Scan(&reservedQuantity)
	if err != nil {
		t.Fatalf("failed to retrieve inventory: %v", err)
	}

	if reservedQuantity != 5 {
		t.Errorf("expected reserved quantity 5, got %d", reservedQuantity)
	}
}

func TestCreateReservation_InsufficientInventory(t *testing.T) {
	f := setupBookingIntegrationDB(t)

	// The test category has a capacity of at most 10.
	_, err := f.service.CreateReservation(
		context.Background(),
		f.customerID,
		f.eventID,
		booking.CreateReservationRequest{
			TicketCategoryID: f.categoryID,
			Quantity:         11,
		},
		uuid.NewString(),
	)

	if !errors.Is(err, booking.ErrInsufficientInventory) {
		t.Fatalf(
			"expected ErrInsufficientInventory, got %v",
			err,
		)
	}
}
