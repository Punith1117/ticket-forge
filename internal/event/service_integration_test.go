package event_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/event"
	"github.com/Punith1117/ticket-forge/internal/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupIntegrationDB(t *testing.T) (
	*pgxpool.Pool,
	*event.Service,
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

	service, err := event.NewService(pool)
	if err != nil {
		t.Fatalf("failed to initialize event service: %v", err)
	}

	return pool, service
}

func createTestUserAndOrganizer(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (userID string, organizerID string) {
	t.Helper()

	email := fmt.Sprintf("event_%s@example.com", uuid.NewString())

	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ($1, 'hash_placeholder', 'Event Test User')
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

func createTestEvent(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	service *event.Service,
	userID string,
	organizerID string,
) string {
	t.Helper()

	venueID, _ := getActiveVenue(t, ctx, pool)
	bookingID := createTestVenueBooking(
		t,
		ctx,
		pool,
		organizerID,
		venueID,
	)

	response, err := service.Create(
		ctx,
		userID,
		organizerID,
		uuid.NewString(),
		newCreateEventRequest(bookingID),
	)
	if err != nil {
		t.Fatalf("failed to create test event: %v", err)
	}

	return response.Event.ID
}

func getActiveVenue(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (venueID string, capacity int32) {
	t.Helper()

	err := pool.QueryRow(ctx, `
		SELECT id::text, capacity
		FROM venues
		WHERE is_active = true
		ORDER BY id
		LIMIT 1
	`).Scan(&venueID, &capacity)
	if err != nil {
		t.Fatalf("failed to retrieve active test venue: %v", err)
	}

	return venueID, capacity
}

func createTestVenueBooking(
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
		t.Fatalf("failed to calculate test booking schedule: %v", err)
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
		t.Fatalf("failed to create test venue booking: %v", err)
	}

	return bookingID
}

func newCreateEventRequest(bookingID string) event.CreateEventRequest {
	return event.CreateEventRequest{
		VenueBookingID: bookingID,
		Title:          "Go Conference",
		Description:    "A conference for Go developers",
		TicketCategories: []event.CreateTicketCategoryRequest{
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
	}
}

func TestCreate_Success(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID, venueCapacity := getActiveVenue(t, ctx, pool)
	bookingID := createTestVenueBooking(t, ctx, pool, organizerID, venueID)

	req := newCreateEventRequest(bookingID)

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

	if response.Event.ID == "" {
		t.Fatal("expected an event ID")
	}

	if response.Event.OrganizerProfileID != organizerID {
		t.Errorf(
			"expected organizer ID %s, got %s",
			organizerID,
			response.Event.OrganizerProfileID,
		)
	}

	if response.Event.VenueBookingID != bookingID {
		t.Errorf(
			"expected booking ID %s, got %s",
			bookingID,
			response.Event.VenueBookingID,
		)
	}

	if response.Event.Status != "active" {
		t.Errorf("expected active status, got %s", response.Event.Status)
	}

	if response.Event.BookingStatus != "open" {
		t.Errorf(
			"expected open booking status, got %s",
			response.Event.BookingStatus,
		)
	}

	if len(response.TicketCategories) != 2 {
		t.Fatalf(
			"expected 2 ticket categories, got %d",
			len(response.TicketCategories),
		)
	}

	var totalCapacity int64

	for _, category := range response.TicketCategories {
		totalCapacity += int64(category.Capacity)

		if category.EventID != response.Event.ID {
			t.Errorf(
				"expected category event ID %s, got %s",
				response.Event.ID,
				category.EventID,
			)
		}

		if category.Currency != "INR" {
			t.Errorf("expected INR currency, got %s", category.Currency)
		}
	}

	if totalCapacity > int64(venueCapacity) {
		t.Errorf(
			"ticket capacity %d exceeds venue capacity %d",
			totalCapacity,
			venueCapacity,
		)
	}

	var eventCount int
	var categoryCount int

	err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM events
		WHERE id = $1
	`, response.Event.ID).Scan(&eventCount)
	if err != nil {
		t.Fatalf("failed to count events: %v", err)
	}

	err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM ticket_categories
		WHERE event_id = $1
	`, response.Event.ID).Scan(&categoryCount)
	if err != nil {
		t.Fatalf("failed to count ticket categories: %v", err)
	}

	if eventCount != 1 || categoryCount != 2 {
		t.Fatalf(
			"expected 1 event and 2 categories, got %d events and %d categories",
			eventCount,
			categoryCount,
		)
	}
}

func TestCreate_IdempotencyReplay(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID, _ := getActiveVenue(t, ctx, pool)
	bookingID := createTestVenueBooking(t, ctx, pool, organizerID, venueID)

	req := newCreateEventRequest(bookingID)
	key := uuid.NewString()

	first, err := service.Create(ctx, userID, organizerID, key, req)
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	second, err := service.Create(ctx, userID, organizerID, key, req)
	if err != nil {
		t.Fatalf("second Create failed: %v", err)
	}

	if first.Event.ID != second.Event.ID {
		t.Fatalf(
			"expected replayed event ID %s, got %s",
			first.Event.ID,
			second.Event.ID,
		)
	}

	if len(first.TicketCategories) != len(second.TicketCategories) {
		t.Fatalf(
			"expected %d replayed categories, got %d",
			len(first.TicketCategories),
			len(second.TicketCategories),
		)
	}

	var eventCount int
	var categoryCount int

	err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM events
		WHERE venue_booking_id = $1
	`, bookingID).Scan(&eventCount)
	if err != nil {
		t.Fatalf("failed to count events: %v", err)
	}

	err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM ticket_categories
		WHERE event_id = $1
	`, first.Event.ID).Scan(&categoryCount)
	if err != nil {
		t.Fatalf("failed to count categories: %v", err)
	}

	if eventCount != 1 || categoryCount != 2 {
		t.Fatalf(
			"expected 1 event and 2 categories, got %d events and %d categories",
			eventCount,
			categoryCount,
		)
	}
}

func TestCreate_IdempotencyKeyReuse(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID, _ := getActiveVenue(t, ctx, pool)
	bookingID := createTestVenueBooking(t, ctx, pool, organizerID, venueID)

	req := newCreateEventRequest(bookingID)
	key := uuid.NewString()

	_, err := service.Create(ctx, userID, organizerID, key, req)
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	req.Title = "Different Go Conference"

	_, err = service.Create(ctx, userID, organizerID, key, req)

	if !errors.Is(err, idempotency.ErrKeyReused) {
		t.Fatalf("expected ErrKeyReused, got %v", err)
	}
}

func TestCreate_RejectsTicketCapacityExceedingVenueCapacity(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID, venueCapacity := getActiveVenue(t, ctx, pool)
	bookingID := createTestVenueBooking(t, ctx, pool, organizerID, venueID)

	req := newCreateEventRequest(bookingID)
	req.TicketCategories = []event.CreateTicketCategoryRequest{
		{
			Name:       "Prime",
			PriceMinor: 50000,
			Capacity:   venueCapacity + 1,
		},
	}

	_, err := service.Create(
		ctx,
		userID,
		organizerID,
		uuid.NewString(),
		req,
	)

	if !errors.Is(err, event.ErrTicketCapacityExceeded) {
		t.Fatalf("expected ErrTicketCapacityExceeded, got %v", err)
	}

	var count int

	err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM events
		WHERE venue_booking_id = $1
	`, bookingID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count events: %v", err)
	}

	if count != 0 {
		t.Fatalf("expected no event to be created, got %d", count)
	}
}

func TestCreate_RejectsCancelledVenueBooking(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID, _ := getActiveVenue(t, ctx, pool)
	bookingID := createTestVenueBooking(t, ctx, pool, organizerID, venueID)

	_, err := pool.Exec(ctx, `
		UPDATE venue_bookings
		SET status = 'cancelled'
		WHERE id = $1
	`, bookingID)
	if err != nil {
		t.Fatalf("failed to cancel test booking: %v", err)
	}

	_, err = service.Create(
		ctx,
		userID,
		organizerID,
		uuid.NewString(),
		newCreateEventRequest(bookingID),
	)

	if !errors.Is(err, event.ErrVenueBookingUnavailable) {
		t.Fatalf("expected ErrVenueBookingUnavailable, got %v", err)
	}
}

func TestCreate_RejectsBookingOwnedByAnotherOrganizer(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	_, bookingOwnerID := createTestUserAndOrganizer(t, ctx, pool)
	otherUserID, otherOrganizerID := createTestUserAndOrganizer(t, ctx, pool)

	venueID, _ := getActiveVenue(t, ctx, pool)
	bookingID := createTestVenueBooking(t, ctx, pool, bookingOwnerID, venueID)

	_, err := service.Create(
		ctx,
		otherUserID,
		otherOrganizerID,
		uuid.NewString(),
		newCreateEventRequest(bookingID),
	)

	if !errors.Is(err, event.ErrVenueBookingNotFound) {
		t.Fatalf("expected ErrVenueBookingNotFound, got %v", err)
	}
}

func TestCreate_AllowsBookingWhenVenueIsDeactivated(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID, _ := getActiveVenue(t, ctx, pool)
	bookingID := createTestVenueBooking(t, ctx, pool, organizerID, venueID)

	// Restore the venue after this test so other tests can use it.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			UPDATE venues
			SET is_active = true
			WHERE id = $1
		`, venueID)
	})

	_, err := pool.Exec(ctx, `
		UPDATE venues
		SET is_active = false
		WHERE id = $1
	`, venueID)
	if err != nil {
		t.Fatalf("failed to deactivate venue: %v", err)
	}

	_, err = service.Create(
		ctx,
		userID,
		organizerID,
		uuid.NewString(),
		newCreateEventRequest(bookingID),
	)
	if err != nil {
		t.Fatalf("expected event creation to work for existing booking: %v", err)
	}
}

func TestCreate_RejectsSecondActiveEventForBooking(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID, _ := getActiveVenue(t, ctx, pool)
	bookingID := createTestVenueBooking(t, ctx, pool, organizerID, venueID)

	req := newCreateEventRequest(bookingID)

	_, err := service.Create(
		ctx,
		userID,
		organizerID,
		uuid.NewString(),
		req,
	)
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	_, err = service.Create(
		ctx,
		userID,
		organizerID,
		uuid.NewString(),
		req,
	)

	if !errors.Is(err, event.ErrActiveEventAlreadyExists) {
		t.Fatalf("expected ErrActiveEventAlreadyExists, got %v", err)
	}

	var count int

	err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM events
		WHERE venue_booking_id = $1
		  AND status = 'active'
	`, bookingID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count active events: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected exactly 1 active event, got %d", count)
	}
}

func TestCreate_ConcurrentCreationForSameBooking(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	venueID, _ := getActiveVenue(t, ctx, pool)
	bookingID := createTestVenueBooking(t, ctx, pool, organizerID, venueID)

	const attempts = 2

	var wg sync.WaitGroup
	errs := make(chan error, attempts)

	start := make(chan struct{})

	for i := 0; i < attempts; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			<-start

			_, err := service.Create(
				ctx,
				userID,
				organizerID,
				uuid.NewString(),
				newCreateEventRequest(bookingID),
			)

			errs <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	var successCount int
	var conflictCount int

	for err := range errs {
		switch {
		case err == nil:
			successCount++

		case errors.Is(err, event.ErrActiveEventAlreadyExists):
			conflictCount++

		default:
			t.Fatalf("unexpected concurrent creation error: %v", err)
		}
	}

	if successCount != 1 {
		t.Errorf("expected 1 successful creation, got %d", successCount)
	}

	if conflictCount != 1 {
		t.Errorf("expected 1 active-event conflict, got %d", conflictCount)
	}

	var count int

	err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM events
		WHERE venue_booking_id = $1
		  AND status = 'active'
	`, bookingID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count active events: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected exactly 1 active event in database, got %d", count)
	}
}

func TestUpdateBookingStatus_PauseEvent(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)

	eventID := createTestEvent(
		t,
		ctx,
		pool,
		service,
		userID,
		organizerID,
	)

	response, err := service.UpdateBookingStatus(
		ctx,
		organizerID,
		eventID,
		event.UpdateBookingStatusRequest{
			BookingStatus: "paused",
		},
	)
	if err != nil {
		t.Fatalf("UpdateBookingStatus failed: %v", err)
	}

	if response.Event.BookingStatus != "paused" {
		t.Errorf(
			"expected booking status paused, got %s",
			response.Event.BookingStatus,
		)
	}

	if response.Event.Status != "active" {
		t.Errorf(
			"expected event status active, got %s",
			response.Event.Status,
		)
	}

	var bookingStatus string
	err = pool.QueryRow(ctx, `
		SELECT booking_status
		FROM events
		WHERE id = $1
	`, eventID).Scan(&bookingStatus)
	if err != nil {
		t.Fatalf("failed to retrieve event booking status: %v", err)
	}

	if bookingStatus != "paused" {
		t.Errorf("expected database status paused, got %s", bookingStatus)
	}
}

func TestUpdateBookingStatus_ReopenEvent(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)

	eventID := createTestEvent(
		t,
		ctx,
		pool,
		service,
		userID,
		organizerID,
	)

	_, err := service.UpdateBookingStatus(
		ctx,
		organizerID,
		eventID,
		event.UpdateBookingStatusRequest{
			BookingStatus: "paused",
		},
	)
	if err != nil {
		t.Fatalf("failed to pause event: %v", err)
	}

	response, err := service.UpdateBookingStatus(
		ctx,
		organizerID,
		eventID,
		event.UpdateBookingStatusRequest{
			BookingStatus: "open",
		},
	)
	if err != nil {
		t.Fatalf("failed to reopen event: %v", err)
	}

	if response.Event.BookingStatus != "open" {
		t.Errorf(
			"expected booking status open, got %s",
			response.Event.BookingStatus,
		)
	}
}

func TestUpdateBookingStatus_RejectsAnotherOrganizer(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	_, anotherOrganizerID := createTestUserAndOrganizer(t, ctx, pool)

	eventID := createTestEvent(
		t,
		ctx,
		pool,
		service,
		userID,
		organizerID,
	)

	_, err := service.UpdateBookingStatus(
		ctx,
		anotherOrganizerID,
		eventID,
		event.UpdateBookingStatusRequest{
			BookingStatus: "paused",
		},
	)

	if !errors.Is(err, event.ErrEventNotFound) {
		t.Fatalf("expected ErrEventNotFound, got %v", err)
	}

	var bookingStatus string
	err = pool.QueryRow(ctx, `
		SELECT booking_status
		FROM events
		WHERE id = $1
	`, eventID).Scan(&bookingStatus)
	if err != nil {
		t.Fatalf("failed to retrieve event: %v", err)
	}

	if bookingStatus != "open" {
		t.Errorf(
			"expected booking status to remain open, got %s",
			bookingStatus,
		)
	}
}

func TestUpdateBookingStatus_RejectsCancelledEvent(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)

	eventID := createTestEvent(
		t,
		ctx,
		pool,
		service,
		userID,
		organizerID,
	)

	_, err := pool.Exec(ctx, `
		UPDATE events
		SET status = 'cancelled'
		WHERE id = $1
	`, eventID)
	if err != nil {
		t.Fatalf("failed to cancel test event: %v", err)
	}

	_, err = service.UpdateBookingStatus(
		ctx,
		organizerID,
		eventID,
		event.UpdateBookingStatusRequest{
			BookingStatus: "open",
		},
	)

	if !errors.Is(err, event.ErrEventNotActive) {
		t.Fatalf("expected ErrEventNotActive, got %v", err)
	}
}

func TestUpdateBookingStatus_RejectsInvalidStatus(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	_, organizerID := createTestUserAndOrganizer(t, ctx, pool)

	_, err := service.UpdateBookingStatus(
		ctx,
		organizerID,
		uuid.NewString(),
		event.UpdateBookingStatusRequest{
			BookingStatus: "closed",
		},
	)

	if !errors.Is(err, event.ErrInvalidBookingStatus) {
		t.Fatalf("expected ErrInvalidBookingStatus, got %v", err)
	}
}

func TestUpdateBookingStatus_SameStatusSucceeds(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)

	eventID := createTestEvent(
		t,
		ctx,
		pool,
		service,
		userID,
		organizerID,
	)

	response, err := service.UpdateBookingStatus(
		ctx,
		organizerID,
		eventID,
		event.UpdateBookingStatusRequest{
			BookingStatus: "open",
		},
	)
	if err != nil {
		t.Fatalf("updating to the existing status failed: %v", err)
	}

	if response.Event.BookingStatus != "open" {
		t.Errorf(
			"expected booking status open, got %s",
			response.Event.BookingStatus,
		)
	}
}
