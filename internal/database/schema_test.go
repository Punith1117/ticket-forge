package database_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	config.Load("../../.env")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := database.ConfigFromEnv()

	testDB := os.Getenv("TEST_POSTGRES_DB")
	if testDB == "" {
		t.Fatal("TEST_POSTGRES_DB must be set")
	}
	cfg.Database = testDB

	pool, err := database.NewPool(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect to test db: %v", err)
	}

	// Clean transient test data
	_, err = pool.Exec(ctx, `
		DELETE FROM refunds;
		DELETE FROM bookings;
		DELETE FROM payment_attempts;
		DELETE FROM reservations;
		DELETE FROM inventory;
		DELETE FROM ticket_categories;
		DELETE FROM events;
		DELETE FROM organizer_profiles;
		DELETE FROM users;
	`)
	if err != nil {
		pool.Close()
		t.Fatalf("failed to clean test database: %v", err)
	}

	return pool
}

func loadSeeds(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	seedPath := filepath.Join("..", "..", "db", "seeds", "001_venues.sql")
	seedSQL, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("failed to read seed file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, string(seedSQL)); err != nil {
		t.Fatalf("failed to apply seed data: %v", err)
	}
}

func createTestUserAndOrganizer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) (userID string, organizerID string) {
	t.Helper()
	uniqueEmail := fmt.Sprintf("%d_%s", time.Now().UnixNano(), email)
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ($1, 'hash_placeholder', 'Organizer User')
		RETURNING id::text
	`, uniqueEmail).Scan(&userID)
	if err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO organizer_profiles (user_id, display_name)
		VALUES ($1, 'Test Org')
		RETURNING id::text
	`, userID).Scan(&organizerID)
	if err != nil {
		t.Fatalf("failed to insert test organizer: %v", err)
	}

	return userID, organizerID
}

func TestSeedsApplication(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	loadSeeds(t, pool)

	ctx := context.Background()
	var count int
	err := pool.QueryRow(ctx, "SELECT count(*) FROM venues WHERE is_active = true").Scan(&count)
	if err != nil {
		t.Fatalf("querying venues failed: %v", err)
	}

	if count < 6 {
		t.Errorf("expected at least 6 seeded venues, got %d", count)
	}
}

func TestVenueScheduleOverlapConstraint(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	loadSeeds(t, pool)

	ctx := context.Background()
	_, orgID := createTestUserAndOrganizer(t, ctx, pool, "org_overlap@ticketforge.dev")

	venueID := "018f0000-0000-7000-8000-000000000001" // Kanteerava Stadium
	baseTime := time.Date(2027, 1, 15, 10, 0, 0, 0, time.UTC)
	salesStart := baseTime.Add(-10 * 24 * time.Hour)
	salesEnd := baseTime.Add(-1 * time.Hour)

	// 1. First event: 10:00 to 12:00
	event1Starts := baseTime
	event1Ends := baseTime.Add(2 * time.Hour)
	_, err := pool.Exec(ctx, `
		INSERT INTO events (organizer_profile_id, venue_id, title, status, starts_at, ends_at, sales_start_at, sales_end_at)
		VALUES ($1, $2, 'Event 1', 'active', $3, $4, $5, $6)
	`, orgID, venueID, event1Starts, event1Ends, salesStart, salesEnd)
	if err != nil {
		t.Fatalf("failed to insert event 1: %v", err)
	}

	// 2. Overlapping active event: 11:00 to 13:00 -> MUST FAIL
	event2Starts := baseTime.Add(1 * time.Hour)
	event2Ends := baseTime.Add(3 * time.Hour)
	_, err = pool.Exec(ctx, `
		INSERT INTO events (organizer_profile_id, venue_id, title, status, starts_at, ends_at, sales_start_at, sales_end_at)
		VALUES ($1, $2, 'Event 2 Overlapping', 'active', $3, $4, $5, $6)
	`, orgID, venueID, event2Starts, event2Ends, salesStart, salesEnd)
	if err == nil {
		t.Fatal("expected exclusion constraint violation for overlapping active event, but insertion succeeded")
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23P01" { // 23P01 is exclusion_violation
		t.Fatalf("expected exclusion_violation (23P01), got: %v", err)
	}

	// 3. Adjacent half-open event: 12:00 to 14:00 [10:00, 12:00) and [12:00, 14:00) -> MUST SUCCEED
	event3Starts := event1Ends
	event3Ends := baseTime.Add(4 * time.Hour)
	_, err = pool.Exec(ctx, `
		INSERT INTO events (organizer_profile_id, venue_id, title, status, starts_at, ends_at, sales_start_at, sales_end_at)
		VALUES ($1, $2, 'Event 3 Adjacent', 'active', $3, $4, $5, $6)
	`, orgID, venueID, event3Starts, event3Ends, salesStart, salesEnd)
	if err != nil {
		t.Fatalf("adjacent event should not overlap with [) bounds: %v", err)
	}

	// 4. Overlapping cancelled event: 10:30 to 11:30 with status = 'cancelled' -> MUST SUCCEED
	_, err = pool.Exec(ctx, `
		INSERT INTO events (organizer_profile_id, venue_id, title, status, starts_at, ends_at, sales_start_at, sales_end_at)
		VALUES ($1, $2, 'Event 4 Cancelled Overlap', 'cancelled', $3, $4, $5, $6)
	`, orgID, venueID, baseTime.Add(30*time.Minute), baseTime.Add(90*time.Minute), salesStart, salesEnd)
	if err != nil {
		t.Fatalf("cancelled event must not trigger schedule exclusion: %v", err)
	}
}

func TestInvalidStateConstraints(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	_, orgID := createTestUserAndOrganizer(t, ctx, pool, "org_state@ticketforge.dev")
	venueID := "018f0000-0000-7000-8000-000000000002"
	now := time.Now().UTC().Add(48 * time.Hour)

	// Inserting invalid status 'finished' must fail CHECK constraint
	_, err := pool.Exec(ctx, `
		INSERT INTO events (organizer_profile_id, venue_id, title, status, starts_at, ends_at, sales_start_at, sales_end_at)
		VALUES ($1, $2, 'Invalid State Event', 'finished', $3, $4, $5, $6)
	`, orgID, venueID, now, now.Add(2*time.Hour), now.Add(-24*time.Hour), now.Add(-1*time.Hour))
	if err == nil {
		t.Fatal("expected check constraint violation for invalid event status, but succeeded")
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23514" { // 23514 is check_violation
		t.Fatalf("expected check_violation (23514), got: %v", err)
	}
}

func TestInvalidInventoryQuantities(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	_, orgID := createTestUserAndOrganizer(t, ctx, pool, "org_inv@ticketforge.dev")
	venueID := "018f0000-0000-7000-8000-000000000003"
	now := time.Now().UTC().Add(48 * time.Hour)

	var eventID string
	err := pool.QueryRow(ctx, `
		INSERT INTO events (organizer_profile_id, venue_id, title, status, starts_at, ends_at, sales_start_at, sales_end_at)
		VALUES ($1, $2, 'Inv Test Event', 'active', $3, $4, $5, $6)
		RETURNING id::text
	`, orgID, venueID, now, now.Add(2*time.Hour), now.Add(-24*time.Hour), now.Add(-1*time.Hour)).Scan(&eventID)
	if err != nil {
		t.Fatalf("failed to insert event: %v", err)
	}

	var catID string
	err = pool.QueryRow(ctx, `
		INSERT INTO ticket_categories (event_id, name, price_minor, currency, capacity)
		VALUES ($1, 'Standard', 10000, 'INR', 500)
		RETURNING id::text
	`, eventID).Scan(&catID)
	if err != nil {
		t.Fatalf("failed to insert ticket category: %v", err)
	}

	// Negative reserved quantity must fail check constraint
	_, err = pool.Exec(ctx, `
		INSERT INTO inventory (ticket_category_id, reserved_quantity, sold_quantity)
		VALUES ($1, -5, 0)
	`, catID)
	if err == nil {
		t.Fatal("expected check constraint violation for negative inventory, but succeeded")
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23514" {
		t.Fatalf("expected check_violation (23514), got: %v", err)
	}
}

func TestUniqueReservationToBookingRelationship(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	custID, orgID := createTestUserAndOrganizer(t, ctx, pool, "booking_test@ticketforge.dev")
	venueID := "018f0000-0000-7000-8000-000000000004"
	now := time.Now().UTC().Add(48 * time.Hour)

	var eventID, catID string
	err := pool.QueryRow(ctx, `
		INSERT INTO events (organizer_profile_id, venue_id, title, status, starts_at, ends_at, sales_start_at, sales_end_at)
		VALUES ($1, $2, 'Booking Unique Test Event', 'active', $3, $4, $5, $6)
		RETURNING id::text
	`, orgID, venueID, now, now.Add(2*time.Hour), now.Add(-24*time.Hour), now.Add(-1*time.Hour)).Scan(&eventID)
	if err != nil {
		t.Fatalf("failed to insert event: %v", err)
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO ticket_categories (event_id, name, price_minor, currency, capacity)
		VALUES ($1, 'General', 5000, 'INR', 100)
		RETURNING id::text
	`, eventID).Scan(&catID)
	if err != nil {
		t.Fatalf("failed to insert ticket category: %v", err)
	}

	var resID string
	err = pool.QueryRow(ctx, `
		INSERT INTO reservations (customer_id, ticket_category_id, quantity, unit_price_minor, total_price_minor, currency, status, expires_at)
		VALUES ($1, $2, 2, 5000, 10000, 'INR', 'confirmed', now() + interval '5 minutes')
		RETURNING id::text
	`, custID, catID).Scan(&resID)
	if err != nil {
		t.Fatalf("failed to insert reservation: %v", err)
	}

	// First booking succeeds
	var bookingID string
	err = pool.QueryRow(ctx, `
		INSERT INTO bookings (reservation_id, customer_id, quantity, unit_price_minor, total_price_minor, currency, status)
		VALUES ($1, $2, 2, 5000, 10000, 'INR', 'confirmed')
		RETURNING id::text
	`, resID, custID).Scan(&bookingID)
	if err != nil {
		t.Fatalf("failed to insert first booking: %v", err)
	}

	// Second booking for same reservation MUST FAIL (unique constraint)
	_, err = pool.Exec(ctx, `
		INSERT INTO bookings (reservation_id, customer_id, quantity, unit_price_minor, total_price_minor, currency, status)
		VALUES ($1, $2, 2, 5000, 10000, 'INR', 'confirmed')
	`, resID, custID)
	if err == nil {
		t.Fatal("expected unique violation for second booking on same reservation, but succeeded")
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23505" { // 23505 is unique_violation
		t.Fatalf("expected unique_violation (23505), got: %v", err)
	}

	// First refund succeeds
	_, err = pool.Exec(ctx, `
		INSERT INTO refunds (booking_id, amount_minor, currency, status)
		VALUES ($1, 10000, 'INR', 'pending')
	`, bookingID)
	if err != nil {
		t.Fatalf("failed to insert first refund: %v", err)
	}

	// Second refund for same booking MUST FAIL (unique constraint)
	_, err = pool.Exec(ctx, `
		INSERT INTO refunds (booking_id, amount_minor, currency, status)
		VALUES ($1, 10000, 'INR', 'pending')
	`, bookingID)
	if err == nil {
		t.Fatal("expected unique violation for second refund on same booking, but succeeded")
	}
	pgErr, ok = err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23505" {
		t.Fatalf("expected unique_violation (23505), got: %v", err)
	}
}

func TestForeignKeyRestrictions(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	_, orgID := createTestUserAndOrganizer(t, ctx, pool, "org_fk@ticketforge.dev")
	nonExistentVenue := "00000000-0000-0000-0000-000000000099"
	now := time.Now().UTC().Add(48 * time.Hour)

	// Inserting event referencing non-existent venue fails FK check
	_, err := pool.Exec(ctx, `
		INSERT INTO events (organizer_profile_id, venue_id, title, status, starts_at, ends_at, sales_start_at, sales_end_at)
		VALUES ($1, $2, 'FK Failure Event', 'active', $3, $4, $5, $6)
	`, orgID, nonExistentVenue, now, now.Add(2*time.Hour), now.Add(-24*time.Hour), now.Add(-1*time.Hour))
	if err == nil {
		t.Fatal("expected foreign key violation for non-existent venue, but succeeded")
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23503" { // 23503 is foreign_key_violation
		t.Fatalf("expected foreign_key_violation (23503), got: %v", err)
	}

	// Deleting a venue that has events must fail due to ON DELETE RESTRICT
	realVenueID := "018f0000-0000-7000-8000-000000000005"
	_, err = pool.Exec(ctx, `
		INSERT INTO events (organizer_profile_id, venue_id, title, status, starts_at, ends_at, sales_start_at, sales_end_at)
		VALUES ($1, $2, 'Restrict Event', 'active', $3, $4, $5, $6)
	`, orgID, realVenueID, now, now.Add(2*time.Hour), now.Add(-24*time.Hour), now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("failed to insert event on real venue: %v", err)
	}

	_, err = pool.Exec(ctx, "DELETE FROM venues WHERE id = $1", realVenueID)
	if err == nil {
		t.Fatal("expected foreign key restrict violation when deleting venue with events, but succeeded")
	}
	pgErr, ok = err.(*pgconn.PgError)
	if !ok || (pgErr.Code != "23001" && pgErr.Code != "23503") {
		t.Fatalf("expected restrict_violation (23001) or foreign_key_violation (23503), got: %v", err)
	}
}

func TestSQLCQueries(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	loadSeeds(t, pool)

	ctx := context.Background()
	queries := sqlc.New(pool)

	// 1. Test GetVenueBySlug
	venue, err := queries.GetVenueBySlug(ctx, "kanteerava-indoor-stadium")
	if err != nil {
		t.Fatalf("failed to get venue by slug via sqlc: %v", err)
	}
	if venue.Name != "Kanteerava Indoor Stadium" {
		t.Errorf("expected Kanteerava Indoor Stadium, got %s", venue.Name)
	}

	// 2. Test ListActiveVenues
	venues, err := queries.ListActiveVenues(ctx)
	if err != nil {
		t.Fatalf("failed to list active venues via sqlc: %v", err)
	}
	if len(venues) < 6 {
		t.Errorf("expected at least 6 venues, got %d", len(venues))
	}

	// 3. Test CreateUser and GetUserByEmail
	createdUser, err := queries.CreateUser(ctx, sqlc.CreateUserParams{
		Email:        "sqlc_user@ticketforge.dev",
		PasswordHash: "secure_argon2id_hash_sample",
		DisplayName:  "SQLC User",
	})
	if err != nil {
		t.Fatalf("failed to create user via sqlc: %v", err)
	}

	fetchedUser, err := queries.GetUserByEmail(ctx, "  SQLC_USER@ticketforge.dev  ")
	if err != nil {
		t.Fatalf("failed to fetch user by email via sqlc: %v", err)
	}
	if fetchedUser.ID != createdUser.ID {
		t.Errorf("expected user ID %v, got %v", createdUser.ID, fetchedUser.ID)
	}
}
