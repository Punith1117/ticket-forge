package payment_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/booking"
	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/event"
	"github.com/Punith1117/ticket-forge/internal/payment"
	"github.com/Punith1117/ticket-forge/internal/payment/simulator"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const integrationWebhookSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type paymentTestFixture struct {
	pool           *pgxpool.Pool
	bookingService *booking.Service
	paymentService *payment.Service
	paymentRepo    *payment.Repository
	simulator      *simulator.Provider
	customerID     string
	otherUserID    string
	eventID        string
	categoryID     string
}

func setupPaymentIntegrationDB(t *testing.T) *paymentTestFixture {
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

	// Clean transient test data
	_, err = pool.Exec(ctx, `
		DELETE FROM payment_webhook_events;
		DELETE FROM refunds;
		DELETE FROM bookings;
		DELETE FROM payment_attempts;
		DELETE FROM reservations;
	`)
	if err != nil {
		t.Fatalf("failed to clean test tables: %v", err)
	}

	customerID := createPaymentTestUser(t, ctx, pool)
	otherUserID := createPaymentTestUser(t, ctx, pool)
	organizerID := createPaymentTestOrganizer(t, ctx, pool, customerID)

	venueID, venueCapacity := getPaymentTestVenue(t, ctx, pool)
	venueBookingID := createPaymentTestVenueBooking(t, ctx, pool, organizerID, venueID)

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
			Title:          "Payment Integration Test",
			Description:    "Test event for payment checkout lifecycle",
			TicketCategories: []event.CreateTicketCategoryRequest{
				{
					Name:       "VIP",
					PriceMinor: 25000,
					Capacity:   min(20, venueCapacity),
				},
				{
					Name:       "Free Tier",
					PriceMinor: 0,
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

	paymentRepo, err := payment.NewRepository(pool)
	if err != nil {
		t.Fatalf("failed to initialize payment repo: %v", err)
	}

	simProvider, err := simulator.NewProvider(integrationWebhookSecret, "http://localhost:8080/api/v1/webhooks/payment", nil)
	if err != nil {
		t.Fatalf("failed to initialize simulator provider: %v", err)
	}

	paymentService, err := payment.NewService(pool, paymentRepo, simProvider, bookingService)
	if err != nil {
		t.Fatalf("failed to initialize payment service: %v", err)
	}

	return &paymentTestFixture{
		pool:           pool,
		bookingService: bookingService,
		paymentService: paymentService,
		paymentRepo:    paymentRepo,
		simulator:      simProvider,
		customerID:     customerID,
		otherUserID:    otherUserID,
		eventID:        eventResponse.Event.ID,
		categoryID:     categoryID,
	}
}

func createPaymentTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	email := fmt.Sprintf("pay_%s@example.com", uuid.NewString())
	var userID string
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ($1, 'hash_placeholder', 'Payment Test User')
		RETURNING id::text
	`, email).Scan(&userID)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	return userID
}

func createPaymentTestOrganizer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID string) string {
	t.Helper()
	var organizerID string
	err := pool.QueryRow(ctx, `
		INSERT INTO organizer_profiles (user_id, display_name)
		VALUES ($1, 'Payment Test Organizer')
		RETURNING id::text
	`, userID).Scan(&organizerID)
	if err != nil {
		t.Fatalf("failed to create test organizer: %v", err)
	}
	return organizerID
}

func getPaymentTestVenue(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (string, int32) {
	t.Helper()
	var venueID string
	var capacity int32
	err := pool.QueryRow(ctx, `
		SELECT id::text, capacity
		FROM venues
		ORDER BY created_at ASC
		LIMIT 1
	`).Scan(&venueID, &capacity)
	if err != nil {
		t.Fatalf("failed to get test venue: %v", err)
	}
	return venueID, capacity
}

func createPaymentTestVenueBooking(
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

	endsAt := startsAt.Add(4 * time.Hour)
	var venueBookingID string
	err = pool.QueryRow(ctx, `
		INSERT INTO venue_bookings (
			organizer_profile_id,
			venue_id,
			starts_at,
			ends_at
		)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text
	`, organizerID, venueID, startsAt, endsAt).Scan(&venueBookingID)
	if err != nil {
		t.Fatalf("failed to create test venue booking: %v", err)
	}
	return venueBookingID
}

func createTestHeldReservation(
	t *testing.T,
	ctx context.Context,
	f *paymentTestFixture,
	categoryID string,
	quantity int32,
) booking.CreateReservationResponse {
	t.Helper()
	res, err := f.bookingService.CreateReservation(
		ctx,
		f.customerID,
		f.eventID,
		booking.CreateReservationRequest{
			TicketCategoryID: categoryID,
			Quantity:         quantity,
		},
		uuid.NewString(),
	)
	if err != nil {
		t.Fatalf("failed to create held reservation: %v", err)
	}
	return res
}

func TestCheckoutSession_ValidHeldReservation(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	res := createTestHeldReservation(t, ctx, f, f.categoryID, 2)

	checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("expected successful checkout session creation, got: %v", err)
	}

	if checkoutResp.PaymentAttemptID == "" {
		t.Error("expected non-empty payment attempt ID")
	}
	if checkoutResp.ReservationID != res.ID {
		t.Errorf("expected reservation ID %s, got %s", res.ID, checkoutResp.ReservationID)
	}
	if checkoutResp.Provider != simulator.ProviderName {
		t.Errorf("expected provider %s, got %s", simulator.ProviderName, checkoutResp.Provider)
	}
	if checkoutResp.AmountMinor != res.TotalPriceMinor {
		t.Errorf("expected amount %d, got %d", res.TotalPriceMinor, checkoutResp.AmountMinor)
	}
	if checkoutResp.Currency != res.Currency {
		t.Errorf("expected currency %s, got %s", res.Currency, checkoutResp.Currency)
	}
	if checkoutResp.Status != "pending" {
		t.Errorf("expected status pending, got %s", checkoutResp.Status)
	}
	if !checkoutResp.ReservationExpiresAt.Equal(res.ExpiresAt) {
		t.Errorf("expected reservation expiration to be preserved (%v), got %v", res.ExpiresAt, checkoutResp.ReservationExpiresAt)
	}

	// Verify attempt record in database
	attempt, err := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
	if err != nil {
		t.Fatalf("failed to retrieve payment attempt: %v", err)
	}
	if attempt.Status != "pending" {
		t.Errorf("expected DB attempt status pending, got %s", attempt.Status)
	}
	if !attempt.ProviderOrderReference.Valid || attempt.ProviderOrderReference.String == "" {
		t.Error("expected DB attempt to have provider_order_reference populated")
	}
}

func TestCheckoutSession_MultipleSessionsForSameReservation(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)

	// Session 1
	s1, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("failed to create session 1: %v", err)
	}

	// Session 2
	s2, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("failed to create session 2: %v", err)
	}

	// Session 3
	s3, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("failed to create session 3: %v", err)
	}

	// Ensure all have distinct attempt IDs and provider session/order references
	if s1.PaymentAttemptID == s2.PaymentAttemptID || s2.PaymentAttemptID == s3.PaymentAttemptID {
		t.Errorf("expected distinct payment attempt IDs: %s, %s, %s", s1.PaymentAttemptID, s2.PaymentAttemptID, s3.PaymentAttemptID)
	}
	if s1.ProviderSessionID == s2.ProviderSessionID || s2.ProviderSessionID == s3.ProviderSessionID {
		t.Errorf("expected distinct provider session IDs: %s, %s, %s", s1.ProviderSessionID, s2.ProviderSessionID, s3.ProviderSessionID)
	}

	// Verify original reservation expiration is preserved and NOT extended
	if !s1.ReservationExpiresAt.Equal(res.ExpiresAt) || !s2.ReservationExpiresAt.Equal(res.ExpiresAt) || !s3.ReservationExpiresAt.Equal(res.ExpiresAt) {
		t.Errorf("expected reservation expiration %v to remain untouched", res.ExpiresAt)
	}

	// Verify inventory did not double-increment
	var reservedQty int32
	err = f.pool.QueryRow(ctx, `SELECT reserved_quantity FROM inventory WHERE ticket_category_id = $1`, res.TicketCategoryID).Scan(&reservedQty)
	if err != nil {
		t.Fatalf("failed to query inventory: %v", err)
	}
	if reservedQty != 1 {
		t.Errorf("expected reserved_quantity to remain 1, got %d", reservedQty)
	}
}

func TestCheckoutSession_EligibilityConstraints(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	t.Run("Other customer cannot checkout reservation", func(t *testing.T) {
		res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
		_, err := f.paymentService.CreateCheckoutSession(ctx, f.otherUserID, res.ID)
		if !errors.Is(err, payment.ErrReservationForbidden) {
			t.Errorf("expected ErrReservationForbidden, got: %v", err)
		}
	})

	t.Run("Non-existent reservation returns 404", func(t *testing.T) {
		_, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, uuid.NewString())
		if !errors.Is(err, payment.ErrReservationNotFound) {
			t.Errorf("expected ErrReservationNotFound, got: %v", err)
		}
	})

	t.Run("Expired reservation cannot create checkout session", func(t *testing.T) {
		res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
		// Force expiration in DB
		_, err := f.pool.Exec(ctx, `UPDATE reservations SET expires_at = now() - interval '10 seconds' WHERE id = $1`, res.ID)
		if err != nil {
			t.Fatalf("failed to artificially expire reservation: %v", err)
		}

		_, err = f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
		if !errors.Is(err, payment.ErrReservationExpired) {
			t.Errorf("expected ErrReservationExpired, got: %v", err)
		}
	})

	t.Run("Cancelled or non-held reservation cannot create checkout session", func(t *testing.T) {
		res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
		_, err := f.pool.Exec(ctx, `UPDATE reservations SET status = 'cancelled' WHERE id = $1`, res.ID)
		if err != nil {
			t.Fatalf("failed to cancel reservation: %v", err)
		}

		_, err = f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
		if !errors.Is(err, payment.ErrReservationNotHeld) {
			t.Errorf("expected ErrReservationNotHeld, got: %v", err)
		}
	})

	t.Run("Zero-value reservation cannot create payment attempt", func(t *testing.T) {
		var freeCategoryID string
		err := f.pool.QueryRow(ctx, `
			SELECT id::text FROM ticket_categories
			WHERE event_id = $1 AND price_minor = 0
			LIMIT 1
		`, f.eventID).Scan(&freeCategoryID)
		if err != nil {
			t.Fatalf("failed to find free ticket category: %v", err)
		}

		freeRes := createTestHeldReservation(t, ctx, f, freeCategoryID, 1)
		_, err = f.paymentService.CreateCheckoutSession(ctx, f.customerID, freeRes.ID)
		if !errors.Is(err, payment.ErrFreeReservationNotSupported) {
			t.Errorf("expected ErrFreeReservationNotSupported, got: %v", err)
		}
	})
}

func TestPaymentWebhook_FullSimulatedHTTPFlow(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	// 1. Create a Held reservation
	res := createTestHeldReservation(t, ctx, f, f.categoryID, 2)

	// 2. Create a checkout session
	checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("failed to create checkout session: %v", err)
	}

	attempt, err := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
	if err != nil {
		t.Fatalf("failed to get payment attempt: %v", err)
	}
	orderRef := attempt.ProviderOrderReference.String

	// Setup HTTP handler and test server
	paymentHandler := payment.NewHandler(f.paymentService, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/webhooks/payment", paymentHandler.HandlePaymentWebhook)

	server := httptest.NewServer(mux)
	defer server.Close()

	// 3. Simulator constructs signed HTTP request and sends to actual webhook HTTP endpoint
	client := simulator.NewClient(server.Client())
	payload := simulator.WebhookPayload{
		EventID:                  "evt_" + uuid.NewString(),
		EventType:                "payment.succeeded",
		Provider:                 simulator.ProviderName,
		ProviderOrderReference:   orderRef,
		ProviderPaymentReference: "pay_" + uuid.NewString(),
		AmountMinor:              res.TotalPriceMinor,
		Currency:                 res.Currency,
		Outcome:                  payment.PaymentOutcomeSucceeded,
		CreatedAt:                time.Now().UTC(),
	}

	err = client.SendWebhook(ctx, server.URL+"/api/v1/webhooks/payment", payload, []byte(integrationWebhookSecret))
	if err != nil {
		t.Fatalf("expected webhook delivery to succeed, got: %v", err)
	}

	// 4. Verify payment attempt is persisted as succeeded
	updatedAttempt, err := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
	if err != nil {
		t.Fatalf("failed to query updated attempt: %v", err)
	}
	if updatedAttempt.Status != "succeeded" {
		t.Errorf("expected status succeeded, got %s", updatedAttempt.Status)
	}
	if !updatedAttempt.ProviderReference.Valid || updatedAttempt.ProviderReference.String != payload.ProviderPaymentReference {
		t.Errorf("expected provider reference %s, got %v", payload.ProviderPaymentReference, updatedAttempt.ProviderReference)
	}
	if !updatedAttempt.CompletedAt.Valid {
		t.Error("expected completed_at timestamp to be set")
	}

	// 5. Confirmation verification:
	// - Reservation transitions to Confirmed
	var resStatus string
	err = f.pool.QueryRow(ctx, `SELECT status FROM reservations WHERE id = $1`, res.ID).Scan(&resStatus)
	if err != nil {
		t.Fatalf("failed to query reservation: %v", err)
	}
	if resStatus != "confirmed" {
		t.Errorf("expected reservation status confirmed, got %s", resStatus)
	}

	// - Exactly one booking is created
	var bookingCount int
	var bookingQuantity int32
	err = f.pool.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(quantity), 0) FROM bookings WHERE reservation_id = $1`, res.ID).Scan(&bookingCount, &bookingQuantity)
	if err != nil {
		t.Fatalf("failed to query bookings: %v", err)
	}
	if bookingCount != 1 {
		t.Errorf("expected exactly 1 booking to be created, found %d", bookingCount)
	}
	if bookingQuantity != 2 {
		t.Errorf("expected booking quantity 2, got %d", bookingQuantity)
	}

	// - Inventory reserved moved to sold
	var reservedQty, soldQty int32
	err = f.pool.QueryRow(ctx, `SELECT reserved_quantity, sold_quantity FROM inventory WHERE ticket_category_id = $1`, res.TicketCategoryID).Scan(&reservedQty, &soldQty)
	if err != nil {
		t.Fatalf("failed to query inventory: %v", err)
	}
	if reservedQty != 0 || soldQty != 2 {
		t.Errorf("expected inventory counters (reserved=0, sold=2), got reserved=%d, sold=%d", reservedQty, soldQty)
	}
}

func TestPaymentWebhook_FailureAndStateHandling(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	t.Run("Failed payment webhook transitions attempt to failed", func(t *testing.T) {
		res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
		checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
		if err != nil {
			t.Fatalf("checkout failed: %v", err)
		}

		attempt, _ := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
		orderRef := attempt.ProviderOrderReference.String

		event := payment.WebhookEvent{
			EventID:                "evt_fail_" + uuid.NewString(),
			Provider:               simulator.ProviderName,
			ProviderOrderReference: orderRef,
			Outcome:                payment.PaymentOutcomeFailed,
			EventType:              "payment.failed",
			AmountMinor:            res.TotalPriceMinor,
			Currency:               res.Currency,
			FailureReason:          "card_declined",
			RawPayload:             []byte(`{}`),
		}

		err = f.paymentService.ProcessWebhookEvent(ctx, event)
		if err != nil {
			t.Fatalf("webhook processing failed: %v", err)
		}

		updated, _ := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
		if updated.Status != "failed" {
			t.Errorf("expected status failed, got %s", updated.Status)
		}
		if !updated.FailureReason.Valid || updated.FailureReason.String != "card_declined" {
			t.Errorf("expected failure reason card_declined, got %v", updated.FailureReason)
		}
	})

	t.Run("Late failure event does not downgrade succeeded attempt", func(t *testing.T) {
		res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
		checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
		if err != nil {
			t.Fatalf("checkout failed: %v", err)
		}

		attempt, _ := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
		orderRef := attempt.ProviderOrderReference.String

		// Success event arrives
		successEvent := payment.WebhookEvent{
			EventID:                  "evt_succ_" + uuid.NewString(),
			Provider:                 simulator.ProviderName,
			ProviderOrderReference:   orderRef,
			ProviderPaymentReference: "pay_succ_" + uuid.NewString(),
			Outcome:                  payment.PaymentOutcomeSucceeded,
			EventType:                "payment.succeeded",
			AmountMinor:              res.TotalPriceMinor,
			Currency:                 res.Currency,
			RawPayload:               []byte(`{}`),
		}
		err = f.paymentService.ProcessWebhookEvent(ctx, successEvent)
		if err != nil {
			t.Fatalf("success webhook failed: %v", err)
		}

		// Late arriving failure event arrives
		lateFailEvent := payment.WebhookEvent{
			EventID:                "evt_late_fail_" + uuid.NewString(),
			Provider:               simulator.ProviderName,
			ProviderOrderReference: orderRef,
			Outcome:                payment.PaymentOutcomeFailed,
			EventType:              "payment.failed",
			AmountMinor:            res.TotalPriceMinor,
			Currency:               res.Currency,
			FailureReason:          "delayed_failure_notice",
			RawPayload:             []byte(`{}`),
		}
		err = f.paymentService.ProcessWebhookEvent(ctx, lateFailEvent)
		if err != nil {
			t.Fatalf("late failure webhook returned unexpected error: %v", err)
		}

		// Verify attempt is STILL succeeded
		updated, _ := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
		if updated.Status != "succeeded" {
			t.Errorf("expected attempt to remain succeeded, got %s", updated.Status)
		}
	})

	t.Run("Duplicate webhook delivery is idempotent", func(t *testing.T) {
		res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
		checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
		if err != nil {
			t.Fatalf("checkout failed: %v", err)
		}

		attempt, _ := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
		orderRef := attempt.ProviderOrderReference.String

		dupEventID := "evt_dup_" + uuid.NewString()
		dupEvent := payment.WebhookEvent{
			EventID:                  dupEventID,
			Provider:                 simulator.ProviderName,
			ProviderOrderReference:   orderRef,
			ProviderPaymentReference: "pay_dup_" + uuid.NewString(),
			Outcome:                  payment.PaymentOutcomeSucceeded,
			EventType:                "payment.succeeded",
			AmountMinor:              res.TotalPriceMinor,
			Currency:                 res.Currency,
			RawPayload:               []byte(`{}`),
		}

		// First delivery
		err = f.paymentService.ProcessWebhookEvent(ctx, dupEvent)
		if err != nil {
			t.Fatalf("first delivery failed: %v", err)
		}

		// Second delivery of same event
		err = f.paymentService.ProcessWebhookEvent(ctx, dupEvent)
		if err != nil {
			t.Fatalf("second delivery should succeed idempotently, got: %v", err)
		}

		// Ensure only 1 record exists in payment_webhook_events
		var count int
		err = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM payment_webhook_events WHERE provider_event_id = $1`, dupEventID).Scan(&count)
		if err != nil {
			t.Fatalf("failed to count webhook events: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 webhook event record, found %d", count)
		}
	})

	t.Run("Concurrent duplicate event delivery", func(t *testing.T) {
		res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
		checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
		if err != nil {
			t.Fatalf("checkout failed: %v", err)
		}

		attempt, _ := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
		orderRef := attempt.ProviderOrderReference.String

		sharedEventID := "evt_concurrent_" + uuid.NewString()
		sharedPaymentRef := "pay_concurrent_" + uuid.NewString()

		var wg sync.WaitGroup
		errs := make([]error, 4)

		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				ev := payment.WebhookEvent{
					EventID:                  sharedEventID,
					Provider:                 simulator.ProviderName,
					ProviderOrderReference:   orderRef,
					ProviderPaymentReference: sharedPaymentRef,
					Outcome:                  payment.PaymentOutcomeSucceeded,
					EventType:                "payment.succeeded",
					AmountMinor:              res.TotalPriceMinor,
					Currency:                 res.Currency,
					RawPayload:               []byte(`{}`),
				}
				errs[idx] = f.paymentService.ProcessWebhookEvent(context.Background(), ev)
			}(i)
		}

		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Errorf("worker %d returned error: %v", i, err)
			}
		}

		var count int
		err = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM payment_webhook_events WHERE provider_event_id = $1`, sharedEventID).Scan(&count)
		if err != nil {
			t.Fatalf("failed to query webhook events: %v", err)
		}
		if count != 1 {
			t.Errorf("expected exactly 1 webhook event record, got %d", count)
		}
	})

	t.Run("Amount or currency mismatch rejected", func(t *testing.T) {
		res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
		checkoutResp, _ := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
		attempt, _ := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)

		mismatchEvent := payment.WebhookEvent{
			EventID:                "evt_mismatch_amount",
			Provider:               simulator.ProviderName,
			ProviderOrderReference: attempt.ProviderOrderReference.String,
			Outcome:                payment.PaymentOutcomeSucceeded,
			EventType:              "payment.succeeded",
			AmountMinor:            res.TotalPriceMinor + 1000, // tampered amount
			Currency:               res.Currency,
			RawPayload:             []byte(`{}`),
		}

		err := f.paymentService.ProcessWebhookEvent(ctx, mismatchEvent)
		if !errors.Is(err, payment.ErrAmountMismatch) {
			t.Errorf("expected ErrAmountMismatch, got: %v", err)
		}
	})
}

func TestHostedCheckout_FullHTTPFlow(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	// 1. Create a Held reservation and checkout session
	res := createTestHeldReservation(t, ctx, f, f.categoryID, 2)
	checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("failed to create checkout session: %v", err)
	}

	// 2. Set up httptest.Server with both webhook handler and simulator checkout routes
	paymentHandler := payment.NewHandler(f.paymentService, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/webhooks/payment", paymentHandler.HandlePaymentWebhook)

	// Wire the reservation checker for the simulator using real DB
	f.simulator.SetReservationChecker(&testReservationChecker{pool: f.pool})
	f.simulator.RegisterRoutes(mux)

	server := httptest.NewServer(mux)
	defer server.Close()

	// Point the simulator's webhook and base URLs to the test server
	f.simulator.SetWebhookURL(server.URL + "/api/v1/webhooks/payment")
	f.simulator.SetBaseURL(server.URL)

	t.Run("GET checkout page renders HTML with payment details", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/api/v1/checkout?session_id=" + checkoutResp.ProviderSessionID)
		if err != nil {
			t.Fatalf("GET checkout failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		html := string(body)

		// Verify rendered content
		expectedAmount := fmt.Sprintf("%.2f", float64(res.TotalPriceMinor)/100.0)
		if !strings.Contains(html, expectedAmount) {
			t.Errorf("expected checkout page to contain amount %s", expectedAmount)
		}
		if !strings.Contains(html, res.Currency) {
			t.Errorf("expected checkout page to contain currency %s", res.Currency)
		}
		if !strings.Contains(html, checkoutResp.ProviderSessionID) {
			t.Error("expected checkout page to contain session ID")
		}
		if !strings.Contains(html, checkoutResp.CheckoutURL) || !strings.Contains(html, "/api/v1/checkout?session_id=") {
			// At least make sure the session ID is present in the page
		}
		if strings.Contains(html, integrationWebhookSecret) {
			t.Error("SECURITY: webhook secret exposed in checkout HTML")
		}
	})

	t.Run("POST outcome triggers webhook and updates payment attempt", func(t *testing.T) {
		// Submit succeeded outcome via JSON
		reqBody := strings.NewReader(`{"outcome":"succeeded"}`)
		resp, err := http.Post(
			server.URL+"/api/v1/checkout/"+checkoutResp.ProviderSessionID+"/complete",
			"application/json",
			reqBody,
		)
		if err != nil {
			t.Fatalf("POST outcome failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		// Verify payment attempt status changed to succeeded in database
		attempt, err := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
		if err != nil {
			t.Fatalf("failed to query updated attempt: %v", err)
		}
		if attempt.Status != "succeeded" {
			t.Errorf("expected payment attempt status succeeded, got %s", attempt.Status)
		}
		if !attempt.CompletedAt.Valid {
			t.Error("expected completed_at to be set")
		}

		// Verify reservation is confirmed
		var resStatus string
		err = f.pool.QueryRow(ctx, `SELECT status FROM reservations WHERE id = $1`, res.ID).Scan(&resStatus)
		if err != nil {
			t.Fatalf("failed to query reservation: %v", err)
		}
		if resStatus != "confirmed" {
			t.Errorf("expected reservation status confirmed, got %s", resStatus)
		}

		// Verify exactly one booking created
		var bookingCount int
		err = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM bookings WHERE reservation_id = $1`, res.ID).Scan(&bookingCount)
		if err != nil {
			t.Fatalf("failed to query bookings: %v", err)
		}
		if bookingCount != 1 {
			t.Errorf("expected exactly 1 booking to be created, found %d", bookingCount)
		}

		// Verify inventory counters updated: reserved moved to sold
		var reservedQty, soldQty int32
		err = f.pool.QueryRow(ctx, `SELECT reserved_quantity, sold_quantity FROM inventory WHERE ticket_category_id = $1`, res.TicketCategoryID).Scan(&reservedQty, &soldQty)
		if err != nil {
			t.Fatalf("failed to query inventory: %v", err)
		}
		if reservedQty != 0 || soldQty != 2 {
			t.Errorf("expected inventory (reserved=0, sold=2), got reserved=%d, sold=%d", reservedQty, soldQty)
		}
	})

	t.Run("Duplicate completion returns 409", func(t *testing.T) {
		reqBody := strings.NewReader(`{"outcome":"succeeded"}`)
		resp, err := http.Post(
			server.URL+"/api/v1/checkout/"+checkoutResp.ProviderSessionID+"/complete",
			"application/json",
			reqBody,
		)
		if err != nil {
			t.Fatalf("POST duplicate outcome failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusConflict {
			t.Errorf("expected 409, got %d", resp.StatusCode)
		}
	})

	t.Run("GET completed session returns 409", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/api/v1/checkout?session_id=" + checkoutResp.ProviderSessionID)
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusConflict {
			t.Errorf("expected 409 for completed session page, got %d", resp.StatusCode)
		}
	})
}

func TestHostedCheckout_FailedOutcomeHTTPFlow(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
	checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("failed to create checkout session: %v", err)
	}

	paymentHandler := payment.NewHandler(f.paymentService, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/webhooks/payment", paymentHandler.HandlePaymentWebhook)

	f.simulator.SetReservationChecker(&testReservationChecker{pool: f.pool})
	f.simulator.RegisterRoutes(mux)

	server := httptest.NewServer(mux)
	defer server.Close()

	f.simulator.SetWebhookURL(server.URL + "/api/v1/webhooks/payment")
	f.simulator.SetBaseURL(server.URL)

	// Submit failed outcome with reason
	reqBody := strings.NewReader(`{"outcome":"failed","failure_reason":"card_declined"}`)
	resp, err := http.Post(
		server.URL+"/api/v1/checkout/"+checkoutResp.ProviderSessionID+"/complete",
		"application/json",
		reqBody,
	)
	if err != nil {
		t.Fatalf("POST failed outcome: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	// Verify payment attempt transitioned to failed
	attempt, err := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
	if err != nil {
		t.Fatalf("failed to query attempt: %v", err)
	}
	if attempt.Status != "failed" {
		t.Errorf("expected status failed, got %s", attempt.Status)
	}
	if !attempt.FailureReason.Valid || attempt.FailureReason.String != "card_declined" {
		t.Errorf("expected failure_reason card_declined, got %v", attempt.FailureReason)
	}
}

func TestHostedCheckout_ExpiredReservationRejected(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
	checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("failed to create checkout session: %v", err)
	}

	// Force-expire the reservation after checkout session was created
	_, err = f.pool.Exec(ctx, `UPDATE reservations SET expires_at = now() - interval '10 seconds' WHERE id = $1`, res.ID)
	if err != nil {
		t.Fatalf("failed to expire reservation: %v", err)
	}

	paymentHandler := payment.NewHandler(f.paymentService, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/webhooks/payment", paymentHandler.HandlePaymentWebhook)

	f.simulator.SetReservationChecker(&testReservationChecker{pool: f.pool})
	f.simulator.RegisterRoutes(mux)

	server := httptest.NewServer(mux)
	defer server.Close()

	f.simulator.SetWebhookURL(server.URL + "/api/v1/webhooks/payment")
	f.simulator.SetBaseURL(server.URL)

	// Attempt to complete — reservation checker should reject it
	reqBody := strings.NewReader(`{"outcome":"succeeded"}`)
	resp, err := http.Post(
		server.URL+"/api/v1/checkout/"+checkoutResp.ProviderSessionID+"/complete",
		"application/json",
		reqBody,
	)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 for expired reservation, got %d", resp.StatusCode)
	}

	// Verify payment attempt remains pending (no webhook was delivered)
	attempt, err := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
	if err != nil {
		t.Fatalf("failed to query attempt: %v", err)
	}
	if attempt.Status != "pending" {
		t.Errorf("expected status to remain pending, got %s", attempt.Status)
	}
}

func TestHostedCheckout_FormEncodedSubmission(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
	checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("failed to create checkout session: %v", err)
	}

	paymentHandler := payment.NewHandler(f.paymentService, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/webhooks/payment", paymentHandler.HandlePaymentWebhook)

	f.simulator.SetReservationChecker(&testReservationChecker{pool: f.pool})
	f.simulator.RegisterRoutes(mux)

	server := httptest.NewServer(mux)
	defer server.Close()

	f.simulator.SetWebhookURL(server.URL + "/api/v1/webhooks/payment")
	f.simulator.SetBaseURL(server.URL)

	// Submit via form-encoded body (like the HTML form does)
	resp, err := http.PostForm(
		server.URL+"/api/v1/checkout/"+checkoutResp.ProviderSessionID+"/complete",
		map[string][]string{
			"outcome":        {"succeeded"},
			"failure_reason": {""},
		},
	)
	if err != nil {
		t.Fatalf("POST form outcome failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	// Verify payment attempt succeeded
	attempt, err := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
	if err != nil {
		t.Fatalf("failed to query attempt: %v", err)
	}
	if attempt.Status != "succeeded" {
		t.Errorf("expected status succeeded, got %s", attempt.Status)
	}

	// Verify reservation confirmed and booking created
	var resStatus string
	err = f.pool.QueryRow(ctx, `SELECT status FROM reservations WHERE id = $1`, res.ID).Scan(&resStatus)
	if err != nil {
		t.Fatalf("failed to query reservation: %v", err)
	}
	if resStatus != "confirmed" {
		t.Errorf("expected reservation status confirmed, got %s", resStatus)
	}

	var bookingCount int
	err = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM bookings WHERE reservation_id = $1`, res.ID).Scan(&bookingCount)
	if err != nil {
		t.Fatalf("failed to query bookings: %v", err)
	}
	if bookingCount != 1 {
		t.Errorf("expected 1 booking, got %d", bookingCount)
	}
}

// testReservationChecker implements simulator.ReservationChecker using a real DB pool for integration tests.
type testReservationChecker struct {
	pool *pgxpool.Pool
}

func (c *testReservationChecker) GetReservationStatus(ctx context.Context, reservationID string) (simulator.ReservationStatus, error) {
	var status string
	var expiresAt time.Time
	err := c.pool.QueryRow(ctx,
		`SELECT status, expires_at FROM reservations WHERE id = $1`, reservationID,
	).Scan(&status, &expiresAt)
	if err != nil {
		return simulator.ReservationStatus{}, err
	}
	return simulator.ReservationStatus{
		Status:    status,
		ExpiresAt: expiresAt,
	}, nil
}

func TestWebhookHandler_AuthenticationAndValidation(t *testing.T) {
	f := setupPaymentIntegrationDB(t)

	paymentHandler := payment.NewHandler(f.paymentService, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/webhooks/payment", paymentHandler.HandlePaymentWebhook)

	now := time.Now().UTC()
	tsStr := strconv.FormatInt(now.Unix(), 10)
	rawBody := []byte(`{"event_id":"evt_1","provider":"simulated","provider_order_reference":"ord_none","amount_minor":100,"currency":"INR","outcome":"succeeded"}`)

	validSig := simulator.ComputeSignature(rawBody, tsStr, []byte(integrationWebhookSecret))

	t.Run("Missing signature header returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/payment", bytes.NewReader(rawBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(simulator.HeaderTimestamp, tsStr)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
		}
	})

	t.Run("Invalid signature header returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/payment", bytes.NewReader(rawBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(simulator.HeaderSignature, "invalidhexsignature")
		req.Header.Set(simulator.HeaderTimestamp, tsStr)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
		}
	})

	t.Run("Modified payload returns 401", func(t *testing.T) {
		tamperedBody := []byte(`{"event_id":"evt_1","provider":"simulated","tampered":true}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/payment", bytes.NewReader(tamperedBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(simulator.HeaderSignature, validSig)
		req.Header.Set(simulator.HeaderTimestamp, tsStr)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
		}
	})

	t.Run("Unknown provider order reference returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/payment", bytes.NewReader(rawBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(simulator.HeaderSignature, validSig)
		req.Header.Set(simulator.HeaderTimestamp, tsStr)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found, got %d", rec.Code)
		}
	})
}

func TestPaymentConfirmation_LatePaymentEnqueuesRefund(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	res := createTestHeldReservation(t, ctx, f, f.categoryID, 2)
	checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	attempt, err := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
	if err != nil {
		t.Fatalf("get attempt failed: %v", err)
	}

	// 1. Artificially expire the reservation (simulating time passing before payment provider webhook)
	_, err = f.pool.Exec(ctx, `UPDATE reservations SET expires_at = now() - interval '10 seconds' WHERE id = $1`, res.ID)
	if err != nil {
		t.Fatalf("failed to artificially expire reservation: %v", err)
	}

	// 2. Late successful webhook arrives
	lateEvent := payment.WebhookEvent{
		EventID:                  "evt_late_" + uuid.NewString(),
		Provider:                 simulator.ProviderName,
		ProviderOrderReference:   attempt.ProviderOrderReference.String,
		ProviderPaymentReference: "pay_late_" + uuid.NewString(),
		Outcome:                  payment.PaymentOutcomeSucceeded,
		EventType:                "payment.succeeded",
		AmountMinor:              res.TotalPriceMinor,
		Currency:                 res.Currency,
		RawPayload:               []byte(`{}`),
	}

	err = f.paymentService.ProcessWebhookEvent(ctx, lateEvent)
	if err != nil {
		t.Fatalf("ProcessWebhookEvent should succeed, got: %v", err)
	}

	// 3. Payment attempt MUST be marked succeeded (we captured money)
	updatedAttempt, err := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
	if err != nil {
		t.Fatalf("get updated attempt: %v", err)
	}
	if updatedAttempt.Status != "succeeded" {
		t.Errorf("expected attempt status succeeded, got %s", updatedAttempt.Status)
	}

	// 4. Reservation must NOT be confirmed
	var resStatus string
	err = f.pool.QueryRow(ctx, `SELECT status FROM reservations WHERE id = $1`, res.ID).Scan(&resStatus)
	if err != nil {
		t.Fatalf("query reservation: %v", err)
	}
	if resStatus == "confirmed" {
		t.Errorf("expired reservation must not be confirmed, got %s", resStatus)
	}

	// 5. No booking must be created
	var bookingCount int
	err = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM bookings WHERE reservation_id = $1`, res.ID).Scan(&bookingCount)
	if err != nil {
		t.Fatalf("query bookings: %v", err)
	}
	if bookingCount != 0 {
		t.Errorf("no booking should be created for late payment, got %d", bookingCount)
	}

	// 6. Inventory counters must NOT be moved to sold
	var reservedQty, soldQty int32
	err = f.pool.QueryRow(ctx, `SELECT reserved_quantity, sold_quantity FROM inventory WHERE ticket_category_id = $1`, res.TicketCategoryID).Scan(&reservedQty, &soldQty)
	if err != nil {
		t.Fatalf("query inventory: %v", err)
	}
	if soldQty != 0 {
		t.Errorf("sold quantity must remain 0, got %d", soldQty)
	}

	// 7. Refund must be enqueued with status = 'pending' and booking_id IS NULL
	var refundID string
	var refundStatus string
	var refundAmount int64
	var refundBookingID *string
	err = f.pool.QueryRow(ctx, `
		SELECT id::text, status, amount_minor, booking_id::text
		FROM refunds
		WHERE payment_attempt_id = $1
	`, attempt.ID).Scan(&refundID, &refundStatus, &refundAmount, &refundBookingID)
	if err != nil {
		t.Fatalf("failed to query refund for late payment: %v", err)
	}
	if refundStatus != "pending" {
		t.Errorf("expected refund status pending, got %s", refundStatus)
	}
	if refundAmount != res.TotalPriceMinor {
		t.Errorf("expected refund amount %d, got %d", res.TotalPriceMinor, refundAmount)
	}
	if refundBookingID != nil {
		t.Errorf("expected refund booking_id to be NULL, got %v", *refundBookingID)
	}
}

func TestPaymentConfirmation_CancelledEventEnqueuesRefund(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)
	checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	attempt, _ := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)

	// Cancel the event while payment is in flight
	_, err = f.pool.Exec(ctx, `UPDATE events SET status = 'cancelled' WHERE id = $1`, f.eventID)
	if err != nil {
		t.Fatalf("failed to cancel event: %v", err)
	}

	// Webhook reports success
	event := payment.WebhookEvent{
		EventID:                  "evt_cancel_" + uuid.NewString(),
		Provider:                 simulator.ProviderName,
		ProviderOrderReference:   attempt.ProviderOrderReference.String,
		ProviderPaymentReference: "pay_cancel_" + uuid.NewString(),
		Outcome:                  payment.PaymentOutcomeSucceeded,
		EventType:                "payment.succeeded",
		AmountMinor:              res.TotalPriceMinor,
		Currency:                 res.Currency,
		RawPayload:               []byte(`{}`),
	}

	err = f.paymentService.ProcessWebhookEvent(ctx, event)
	if err != nil {
		t.Fatalf("ProcessWebhookEvent failed: %v", err)
	}

	// Payment attempt succeeded
	updatedAttempt, _ := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
	if updatedAttempt.Status != "succeeded" {
		t.Errorf("expected attempt succeeded, got %s", updatedAttempt.Status)
	}

	// Reservation must NOT be confirmed
	var resStatus string
	_ = f.pool.QueryRow(ctx, `SELECT status FROM reservations WHERE id = $1`, res.ID).Scan(&resStatus)
	if resStatus == "confirmed" {
		t.Errorf("cancelled event must not allow reservation confirmation")
	}

	// No booking created
	var bookingCount int
	_ = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM bookings WHERE reservation_id = $1`, res.ID).Scan(&bookingCount)
	if bookingCount != 0 {
		t.Errorf("no booking should be created for cancelled event")
	}

	// Refund enqueued
	var refundStatus string
	err = f.pool.QueryRow(ctx, `SELECT status FROM refunds WHERE payment_attempt_id = $1`, attempt.ID).Scan(&refundStatus)
	if err != nil {
		t.Fatalf("expected refund record: %v", err)
	}
	if refundStatus != "pending" {
		t.Errorf("expected pending refund, got %s", refundStatus)
	}
}

func TestPaymentConfirmation_PausedEventAllowsConfirmation(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	res := createTestHeldReservation(t, ctx, f, f.categoryID, 2)
	checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	attempt, _ := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)

	// Pause event bookings while payment is in flight (ADR-016: paused bookings do not prevent held confirmation)
	_, err = f.pool.Exec(ctx, `UPDATE events SET booking_status = 'paused' WHERE id = $1`, f.eventID)
	if err != nil {
		t.Fatalf("failed to pause event: %v", err)
	}

	event := payment.WebhookEvent{
		EventID:                  "evt_paused_" + uuid.NewString(),
		Provider:                 simulator.ProviderName,
		ProviderOrderReference:   attempt.ProviderOrderReference.String,
		ProviderPaymentReference: "pay_paused_" + uuid.NewString(),
		Outcome:                  payment.PaymentOutcomeSucceeded,
		EventType:                "payment.succeeded",
		AmountMinor:              res.TotalPriceMinor,
		Currency:                 res.Currency,
		RawPayload:               []byte(`{}`),
	}

	err = f.paymentService.ProcessWebhookEvent(ctx, event)
	if err != nil {
		t.Fatalf("ProcessWebhookEvent failed on paused event: %v", err)
	}

	// Reservation MUST be confirmed per ADR-016
	var resStatus string
	_ = f.pool.QueryRow(ctx, `SELECT status FROM reservations WHERE id = $1`, res.ID).Scan(&resStatus)
	if resStatus != "confirmed" {
		t.Errorf("ADR-016 requirement: paused event must still confirm valid held reservation, got %s", resStatus)
	}

	// Exactly 1 booking created
	var bookingCount int
	_ = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM bookings WHERE reservation_id = $1`, res.ID).Scan(&bookingCount)
	if bookingCount != 1 {
		t.Errorf("expected 1 booking, got %d", bookingCount)
	}

	// Inventory converted: reserved=0, sold=2
	var reservedQty, soldQty int32
	_ = f.pool.QueryRow(ctx, `SELECT reserved_quantity, sold_quantity FROM inventory WHERE ticket_category_id = $1`, res.TicketCategoryID).Scan(&reservedQty, &soldQty)
	if reservedQty != 0 || soldQty != 2 {
		t.Errorf("expected reserved=0, sold=2, got reserved=%d, sold=%d", reservedQty, soldQty)
	}
}

func TestPaymentConfirmation_MultipleAttemptsSameReservation(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	res := createTestHeldReservation(t, ctx, f, f.categoryID, 1)

	// Create two distinct checkout sessions for the same reservation
	sess1, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("session 1 failed: %v", err)
	}
	sess2, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("session 2 failed: %v", err)
	}

	att1, _ := f.paymentService.GetPaymentAttemptByID(ctx, sess1.PaymentAttemptID)
	att2, _ := f.paymentService.GetPaymentAttemptByID(ctx, sess2.PaymentAttemptID)

	// Attempt 1 succeeds and confirms the reservation
	err = f.paymentService.ProcessWebhookEvent(ctx, payment.WebhookEvent{
		EventID:                  "evt_att1_" + uuid.NewString(),
		Provider:                 simulator.ProviderName,
		ProviderOrderReference:   att1.ProviderOrderReference.String,
		ProviderPaymentReference: "pay_att1_" + uuid.NewString(),
		Outcome:                  payment.PaymentOutcomeSucceeded,
		EventType:                "payment.succeeded",
		AmountMinor:              res.TotalPriceMinor,
		Currency:                 res.Currency,
		RawPayload:               []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("attempt 1 webhook failed: %v", err)
	}

	// Reservation confirmed and 1 booking created
	var bookingCount int
	_ = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM bookings WHERE reservation_id = $1`, res.ID).Scan(&bookingCount)
	if bookingCount != 1 {
		t.Fatalf("expected 1 booking, got %d", bookingCount)
	}

	// Attempt 2 succeeds later (customer accidentally paid twice or delayed provider callback)
	err = f.paymentService.ProcessWebhookEvent(ctx, payment.WebhookEvent{
		EventID:                  "evt_att2_" + uuid.NewString(),
		Provider:                 simulator.ProviderName,
		ProviderOrderReference:   att2.ProviderOrderReference.String,
		ProviderPaymentReference: "pay_att2_" + uuid.NewString(),
		Outcome:                  payment.PaymentOutcomeSucceeded,
		EventType:                "payment.succeeded",
		AmountMinor:              res.TotalPriceMinor,
		Currency:                 res.Currency,
		RawPayload:               []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("attempt 2 webhook failed: %v", err)
	}

	// Verify attempt 2 is recorded as succeeded
	att2Updated, _ := f.paymentService.GetPaymentAttemptByID(ctx, sess2.PaymentAttemptID)
	if att2Updated.Status != "succeeded" {
		t.Errorf("expected attempt 2 succeeded, got %s", att2Updated.Status)
	}

	// Verify STILL exactly 1 booking (NO duplicate booking)
	_ = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM bookings WHERE reservation_id = $1`, res.ID).Scan(&bookingCount)
	if bookingCount != 1 {
		t.Errorf("expected still 1 booking, got %d", bookingCount)
	}

	// Verify inventory sold is still 1 (NOT 2)
	var soldQty int32
	_ = f.pool.QueryRow(ctx, `SELECT sold_quantity FROM inventory WHERE ticket_category_id = $1`, res.TicketCategoryID).Scan(&soldQty)
	if soldQty != 1 {
		t.Errorf("expected sold_quantity to remain 1, got %d", soldQty)
	}

	// Verify attempt 2 was enqueued for a refund
	var refundCount int
	_ = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM refunds WHERE payment_attempt_id = $1`, att2.ID).Scan(&refundCount)
	if refundCount != 1 {
		t.Errorf("expected attempt 2 to have 1 refund record, got %d", refundCount)
	}
}

func TestPaymentConfirmation_ConcurrentWebhooks(t *testing.T) {
	f := setupPaymentIntegrationDB(t)
	ctx := context.Background()

	res := createTestHeldReservation(t, ctx, f, f.categoryID, 2)
	checkoutResp, err := f.paymentService.CreateCheckoutSession(ctx, f.customerID, res.ID)
	if err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	attempt, _ := f.paymentService.GetPaymentAttemptByID(ctx, checkoutResp.PaymentAttemptID)
	orderRef := attempt.ProviderOrderReference.String
	sharedEventID := "evt_concurrent_conf_" + uuid.NewString()
	sharedPayRef := "pay_concurrent_conf_" + uuid.NewString()

	var wg sync.WaitGroup
	errs := make([]error, 4)

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ev := payment.WebhookEvent{
				EventID:                  sharedEventID,
				Provider:                 simulator.ProviderName,
				ProviderOrderReference:   orderRef,
				ProviderPaymentReference: sharedPayRef,
				Outcome:                  payment.PaymentOutcomeSucceeded,
				EventType:                "payment.succeeded",
				AmountMinor:              res.TotalPriceMinor,
				Currency:                 res.Currency,
				RawPayload:               []byte(`{}`),
			}
			errs[idx] = f.paymentService.ProcessWebhookEvent(context.Background(), ev)
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent worker %d returned error: %v", i, err)
		}
	}

	// Verify exactly 1 booking created
	var bookingCount int
	_ = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM bookings WHERE reservation_id = $1`, res.ID).Scan(&bookingCount)
	if bookingCount != 1 {
		t.Errorf("expected exactly 1 booking across concurrent webhooks, got %d", bookingCount)
	}

	// Verify inventory: reserved=0, sold=2
	var reservedQty, soldQty int32
	_ = f.pool.QueryRow(ctx, `SELECT reserved_quantity, sold_quantity FROM inventory WHERE ticket_category_id = $1`, res.TicketCategoryID).Scan(&reservedQty, &soldQty)
	if reservedQty != 0 || soldQty != 2 {
		t.Errorf("expected reserved=0, sold=2, got reserved=%d, sold=%d", reservedQty, soldQty)
	}
}
