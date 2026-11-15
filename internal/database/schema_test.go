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
		DELETE FROM sessions;
		DELETE FROM password_reset_tokens;
		DELETE FROM idempotency_records;
		DELETE FROM users;
		DELETE FROM registration_verification_challenges;
		DELETE FROM outbox_events;
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

func TestAuthenticationEmailNormalizationAndUniqueness(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// 1. Insert valid normalized email
	_, err := pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ('user_norm@ticketforge.dev', 'hash_sample', 'Normalized User')
	`)
	if err != nil {
		t.Fatalf("failed to insert initial normalized user: %v", err)
	}

	// 2. Reject duplicate normalized email (exact match)
	_, err = pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ('user_norm@ticketforge.dev', 'hash_sample_2', 'Duplicate User')
	`)
	if err == nil {
		t.Fatal("expected duplicate normalized email to be rejected, but succeeded")
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23505" { // unique_violation
		t.Fatalf("expected unique_violation (23505), got: %v", err)
	}

	// 3. Reject un-normalized email via raw SQL violating chk_users_email_normalized
	_, err = pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ('  UPPER_USER@ticketforge.dev  ', 'hash_sample_3', 'Un-normalized User')
	`)
	if err == nil {
		t.Fatal("expected un-normalized email to violate check constraint, but succeeded")
	}
	pgErr, ok = err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23514" { // check_violation
		t.Fatalf("expected check_violation (23514), got: %v", err)
	}
}

func TestTokenHashConstraints(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	userID, _ := createTestUserAndOrganizer(t, ctx, pool, "token_hash_test@ticketforge.dev")

	validHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	invalidUpperHash := "E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855"
	invalidShortHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85"
	invalidNonHexHash := "g3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85z"

	// 1. registration_verification_challenges
	// Uppercase hex rejected
	_, err := pool.Exec(ctx, `
		INSERT INTO registration_verification_challenges (email, token_hash, expires_at)
		VALUES ('challenge@ticketforge.dev', $1, now() + interval '24 hours')
	`, invalidUpperHash)
	if err == nil {
		t.Fatal("expected uppercase token hash to be rejected in registration challenge")
	}

	// Short hash rejected
	_, err = pool.Exec(ctx, `
		INSERT INTO registration_verification_challenges (email, token_hash, expires_at)
		VALUES ('challenge@ticketforge.dev', $1, now() + interval '24 hours')
	`, invalidShortHash)
	if err == nil {
		t.Fatal("expected short token hash to be rejected in registration challenge")
	}

	// Valid hash accepted
	_, err = pool.Exec(ctx, `
		INSERT INTO registration_verification_challenges (email, token_hash, expires_at)
		VALUES ('challenge@ticketforge.dev', $1, now() + interval '24 hours')
	`, validHash)
	if err != nil {
		t.Fatalf("expected valid token hash to be accepted in registration challenge: %v", err)
	}

	// 2. password_reset_tokens
	_, err = pool.Exec(ctx, `
		INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, now() + interval '20 minutes')
	`, userID, invalidNonHexHash)
	if err == nil {
		t.Fatal("expected non-hex token hash to be rejected in password reset token")
	}

	// 3. sessions
	_, err = pool.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, idle_expires_at, absolute_expires_at)
		VALUES ($1, $2, now() + interval '30 minutes', now() + interval '7 days')
	`, userID, invalidUpperHash)
	if err == nil {
		t.Fatal("expected uppercase token hash to be rejected in sessions")
	}
}

func TestRegistrationChallengesWithoutUsers(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	email := "unregistered_lead@ticketforge.dev"
	tokenHash1 := "1111111111111111111111111111111111111111111111111111111111111111"
	tokenHash2 := "2222222222222222222222222222222222222222222222222222222222222222"

	// 1. Challenge can exist without user account
	_, err := pool.Exec(ctx, `
		INSERT INTO registration_verification_challenges (email, token_hash, expires_at)
		VALUES ($1, $2, now() + interval '24 hours')
	`, email, tokenHash1)
	if err != nil {
		t.Fatalf("failed to insert challenge without user account: %v", err)
	}

	// 2. Multiple outstanding challenges for the same email are permitted
	_, err = pool.Exec(ctx, `
		INSERT INTO registration_verification_challenges (email, token_hash, expires_at)
		VALUES ($1, $2, now() + interval '24 hours')
	`, email, tokenHash2)
	if err != nil {
		t.Fatalf("expected multiple challenges for same email to succeed, got: %v", err)
	}

	// 3. Invalid consumption timestamp (consumed_at < created_at) is rejected
	_, err = pool.Exec(ctx, `
		INSERT INTO registration_verification_challenges (email, token_hash, expires_at, created_at, consumed_at)
		VALUES ('pre_used@ticketforge.dev', '3333333333333333333333333333333333333333333333333333333333333333',
		        now() + interval '24 hours', now(), now() - interval '1 hour')
	`)
	if err == nil {
		t.Fatal("expected consumption timestamp before creation to be rejected")
	}
}

func TestPasswordResetTokenRequiresUser(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	fakeUserID := "018f0000-0000-7000-8000-000000000999"
	validHash := "4444444444444444444444444444444444444444444444444444444444444444"

	// Password reset tokens must fail for non-existent users
	_, err := pool.Exec(ctx, `
		INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, now() + interval '20 minutes')
	`, fakeUserID, validHash)
	if err == nil {
		t.Fatal("expected foreign key violation when creating password reset token for nonexistent user")
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23503" { // foreign_key_violation
		t.Fatalf("expected foreign_key_violation (23503), got: %v", err)
	}
}

func TestSessionConstraintsIntegrity(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	userID, _ := createTestUserAndOrganizer(t, ctx, pool, "session_test@ticketforge.dev")
	validHash := "5555555555555555555555555555555555555555555555555555555555555555"

	// 1. Inverted expiry (absolute < idle) must be rejected
	_, err := pool.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, idle_expires_at, absolute_expires_at)
		VALUES ($1, $2, now() + interval '10 days', now() + interval '5 days')
	`, userID, validHash)
	if err == nil {
		t.Fatal("expected inverted session expiration to be rejected by check constraint")
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23514" {
		t.Fatalf("expected check_violation (23514), got: %v", err)
	}

	// 2. Valid session succeeds
	_, err = pool.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, idle_expires_at, absolute_expires_at)
		VALUES ($1, $2, now() + interval '30 minutes', now() + interval '7 days')
	`, userID, validHash)
	if err != nil {
		t.Fatalf("expected valid session to be inserted successfully: %v", err)
	}
}

func TestObsoleteEmailVerificationStateDisallowed(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// 1. email_verification_tokens table must no longer exist
	_, err := pool.Exec(ctx, `SELECT 1 FROM email_verification_tokens LIMIT 1`)
	if err == nil {
		t.Fatal("expected email_verification_tokens table to be dropped, but table exists")
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "42P01" { // undefined_table
		t.Fatalf("expected undefined_table (42P01), got: %v", err)
	}

	// 2. email_verified boolean column must no longer exist on users
	_, err = pool.Exec(ctx, `SELECT email_verified FROM users LIMIT 1`)
	if err == nil {
		t.Fatal("expected email_verified column to be dropped from users, but column exists")
	}
	pgErr, ok = err.(*pgconn.PgError)
	if !ok || pgErr.Code != "42703" { // undefined_column
		t.Fatalf("expected undefined_column (42703), got: %v", err)
	}

	// 3. email_verified_at cannot be null on newly created users
	_, err = pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, display_name, email_verified_at)
		VALUES ('unverified@ticketforge.dev', 'hash', 'Unverified User', NULL)
	`)
	if err == nil {
		t.Fatal("expected NULL email_verified_at to be rejected by NOT NULL constraint")
	}
	pgErr, ok = err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23502" { // not_null_violation
		t.Fatalf("expected not_null_violation (23502), got: %v", err)
	}
}

func TestOutboxStatusAndAttemptsConstraints(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// 1. Invalid status rejected
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload, status)
		VALUES ('user', uuidv7(), 'user.created', '{}'::jsonb, 'queued')
	`)
	if err == nil {
		t.Fatal("expected invalid outbox status 'queued' to be rejected")
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23514" {
		t.Fatalf("expected check_violation (23514), got: %v", err)
	}

	// 2. Negative attempts rejected
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload, attempts)
		VALUES ('user', uuidv7(), 'user.created', '{}'::jsonb, -1)
	`)
	if err == nil {
		t.Fatal("expected negative outbox attempts to be rejected")
	}
	pgErr, ok = err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23514" {
		t.Fatalf("expected check_violation (23514), got: %v", err)
	}

	// 3. Valid default insertion has status 'pending' and non-null next_attempt_at
	var status string
	var nextAttempt time.Time
	err = pool.QueryRow(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		VALUES ('user', uuidv7(), 'user.created', '{"sample": true}'::jsonb)
		RETURNING status, next_attempt_at
	`).Scan(&status, &nextAttempt)
	if err != nil {
		t.Fatalf("failed to insert default outbox event: %v", err)
	}
	if status != "pending" {
		t.Errorf("expected default status 'pending', got: %s", status)
	}
	if nextAttempt.IsZero() {
		t.Error("expected default next_attempt_at to be populated, got zero time")
	}
}

func TestOutboxClaimAndLeaseConstraints(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// 1. Processing without claim_token must be rejected
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload, status, claim_expires_at)
		VALUES ('user', uuidv7(), 'user.created', '{}'::jsonb, 'processing', now() + interval '2 minutes')
	`)
	if err == nil {
		t.Fatal("expected status 'processing' without claim_token to be rejected")
	}

	// 2. Processing without claim_expires_at must be rejected
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload, status, claim_token)
		VALUES ('user', uuidv7(), 'user.created', '{}'::jsonb, 'processing', uuidv7())
	`)
	if err == nil {
		t.Fatal("expected status 'processing' without claim_expires_at to be rejected")
	}

	// 3. Processing with both claim_token and claim_expires_at succeeds
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload, status, claim_token, claim_expires_at, claimed_at)
		VALUES ('user', uuidv7(), 'user.created', '{}'::jsonb, 'processing', uuidv7(), now() + interval '2 minutes', now())
	`)
	if err != nil {
		t.Fatalf("expected valid processing claim to succeed: %v", err)
	}
}

func TestOutboxPublishedConstraints(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// 1. Published without published_at rejected
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload, status, published_at)
		VALUES ('user', uuidv7(), 'user.created', '{}'::jsonb, 'published', NULL)
	`)
	if err == nil {
		t.Fatal("expected status 'published' without published_at to be rejected")
	}

	// 2. Published with published_at succeeds
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload, status, published_at, processed_at)
		VALUES ('user', uuidv7(), 'user.created', '{}'::jsonb, 'published', now(), now())
	`)
	if err != nil {
		t.Fatalf("expected valid published event to succeed: %v", err)
	}
}

func TestOutboxPayloadScrubbingAndDomainEvents(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// 1. Insert email event with encrypted token payload
	var eventID string
	err := pool.QueryRow(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		VALUES ('registration_challenge', uuidv7(), 'auth.registration_verification_requested',
		        '{"recipient": "customer@ticketforge.dev", "encrypted_token": {"ciphertext": "c2VjcmV0", "nonce": "bm9uY2U=", "key_version": 1}}'::jsonb)
		RETURNING id::text
	`).Scan(&eventID)
	if err != nil {
		t.Fatalf("failed to insert outbox event with encrypted token: %v", err)
	}

	// 2. Simulate worker completing delivery and scrubbing encrypted token material
	_, err = pool.Exec(ctx, `
		UPDATE outbox_events
		SET status = 'published',
		    published_at = now(),
		    processed_at = now(),
		    provider_message_id = 're_msg_123456789',
		    payload = payload - 'encrypted_token'
		WHERE id = $1::uuid
	`, eventID)
	if err != nil {
		t.Fatalf("failed to update outbox event and scrub payload: %v", err)
	}

	// 3. Verify encrypted_token is deleted while non-sensitive recipient is preserved
	var hasEncryptedToken bool
	var recipient string
	err = pool.QueryRow(ctx, `
		SELECT payload ? 'encrypted_token', payload->>'recipient'
		FROM outbox_events
		WHERE id = $1::uuid
	`, eventID).Scan(&hasEncryptedToken, &recipient)
	if err != nil {
		t.Fatalf("failed to query scrubbed outbox payload: %v", err)
	}
	if hasEncryptedToken {
		t.Error("expected encrypted_token to be stripped from payload after publication")
	}
	if recipient != "customer@ticketforge.dev" {
		t.Errorf("expected recipient 'customer@ticketforge.dev', got '%s'", recipient)
	}

	// 4. Verify non-email domain events are supported
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		VALUES ('booking', uuidv7(), 'booking.confirmed',
		        '{"booking_id": "018f0000-0000-7000-8000-000000000001", "total_minor_units": 250000}'::jsonb)
	`)
	if err != nil {
		t.Fatalf("expected non-email domain event to succeed: %v", err)
	}
}
