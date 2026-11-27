package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/booking"
	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/Punith1117/ticket-forge/internal/event"
	"github.com/Punith1117/ticket-forge/internal/outbox"
	"github.com/Punith1117/ticket-forge/internal/payment"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type dummyProvider struct{}

func (d *dummyProvider) Name() string { return "simulated" }
func (d *dummyProvider) CreateCheckoutSession(ctx context.Context, req payment.CreateCheckoutSessionRequest) (payment.CheckoutSession, error) {
	return payment.CheckoutSession{
		ProviderSessionID: "sess_" + req.PaymentAttemptID,
		CheckoutURL:       "http://localhost/checkout",
		ExpiresAt:         req.ExpiresAt,
	}, nil
}
func (d *dummyProvider) VerifyWebhook(payload []byte, headers http.Header) (payment.WebhookEvent, error) {
	return payment.WebhookEvent{}, nil
}
func (d *dummyProvider) Refund(ctx context.Context, req payment.RefundRequest) (payment.RefundResult, error) {
	return payment.RefundResult{
		ProviderRefundReference: "ref_" + req.RefundID,
	}, nil
}

func TestCancelEvent_Integration(t *testing.T) {
	ctx := context.Background()
	pool, service := setupIntegrationDB(t)
	_, _ = pool.Exec(ctx, "DELETE FROM outbox_events WHERE event_type = $1", event.EventTypeEventCancellationRequested)

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	eventID := createTestEvent(t, ctx, pool, service, userID, organizerID)

	idempotencyKey := uuid.NewString()
	reason := "Headliner fell ill"

	// 1. Cancel the active event
	cancelResp, err := service.Cancel(ctx, userID, organizerID, eventID, idempotencyKey, event.CancelEventRequest{
		Reason: reason,
	})
	if err != nil {
		t.Fatalf("expected successful cancellation, got: %v", err)
	}

	if cancelResp.Event.Status != "cancelled" {
		t.Errorf("expected status 'cancelled', got %s", cancelResp.Event.Status)
	}
	if cancelResp.Event.BookingStatus != "paused" {
		t.Errorf("expected booking_status 'paused', got %s", cancelResp.Event.BookingStatus)
	}

	// 2. Verify outbox event was created
	var (
		outboxCount int
		outboxPayload []byte
	)
	eventUUID, _ := uuid.Parse(eventID)
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(MAX(payload::text), '')::bytea
		FROM outbox_events
		WHERE aggregate_type = 'event'
		  AND aggregate_id = $1
		  AND event_type = $2
	`, eventUUID, event.EventTypeEventCancellationRequested).Scan(&outboxCount, &outboxPayload)
	if err != nil {
		t.Fatalf("failed to query outbox: %v", err)
	}
	if outboxCount != 1 {
		t.Errorf("expected 1 outbox event, got %d", outboxCount)
	}

	var parsedPayload event.EventCancellationPayload
	if err := json.Unmarshal(outboxPayload, &parsedPayload); err != nil {
		t.Fatalf("failed to unmarshal outbox payload: %v", err)
	}
	if parsedPayload.Reason != reason {
		t.Errorf("expected payload reason %q, got %q", reason, parsedPayload.Reason)
	}

	// 3. Idempotent replay with same key returns identical response
	replayResp, err := service.Cancel(ctx, userID, organizerID, eventID, idempotencyKey, event.CancelEventRequest{
		Reason: reason,
	})
	if err != nil {
		t.Fatalf("expected replay success, got: %v", err)
	}
	if replayResp.Event.ID != cancelResp.Event.ID {
		t.Errorf("expected replayed event ID %s, got %s", cancelResp.Event.ID, replayResp.Event.ID)
	}

	// 4. Calling cancel with a different idempotency key on already-cancelled event returns ErrEventAlreadyCancelled
	newKey := uuid.NewString()
	_, err = service.Cancel(ctx, userID, organizerID, eventID, newKey, event.CancelEventRequest{
		Reason: "another reason",
	})
	if !errors.Is(err, event.ErrEventAlreadyCancelled) {
		t.Errorf("expected ErrEventAlreadyCancelled, got: %v", err)
	}

	// 5. Calling cancel with invalid organizer returns ErrEventNotFound
	otherUserID, otherOrgID := createTestUserAndOrganizer(t, ctx, pool)
	_, err = service.Cancel(ctx, otherUserID, otherOrgID, eventID, uuid.NewString(), event.CancelEventRequest{})
	if !errors.Is(err, event.ErrEventNotFound) {
		t.Errorf("expected ErrEventNotFound for non-owning organizer, got: %v", err)
	}
}

func TestEventCancellation_ImmediateGuardsAndWorkerBatching(t *testing.T) {
	ctx := context.Background()
	pool, eventService := setupIntegrationDB(t)
	_, _ = pool.Exec(ctx, "DELETE FROM outbox_events WHERE event_type = $1", event.EventTypeEventCancellationRequested)

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	eventID := createTestEvent(t, ctx, pool, eventService, userID, organizerID)

	// Fetch created ticket category
	var categoryID string
	err := pool.QueryRow(ctx, `
		SELECT id::text FROM ticket_categories WHERE event_id = $1 LIMIT 1
	`, eventID).Scan(&categoryID)
	if err != nil {
		t.Fatalf("failed to get ticket category: %v", err)
	}

	bookingService, err := booking.NewService(pool)
	if err != nil {
		t.Fatalf("failed to initialize booking service: %v", err)
	}

	payRepo, err := payment.NewRepository(pool)
	if err != nil {
		t.Fatalf("failed to initialize payment repository: %v", err)
	}
	provider := &dummyProvider{}
	payService, err := payment.NewService(pool, payRepo, provider, bookingService)
	if err != nil {
		t.Fatalf("failed to initialize payment service: %v", err)
	}

	// Create customer 1: will hold a reservation
	cust1ID, _ := createTestUserAndOrganizer(t, ctx, pool)
	res1Resp, err := bookingService.CreateReservation(ctx, cust1ID, eventID, booking.CreateReservationRequest{
		TicketCategoryID: categoryID,
		Quantity:         2,
	}, uuid.NewString())
	if err != nil {
		t.Fatalf("failed to create reservation 1: %v", err)
	}

	// Create customer 2: will have a confirmed booking
	cust2ID, _ := createTestUserAndOrganizer(t, ctx, pool)
	res2Resp, err := bookingService.CreateReservation(ctx, cust2ID, eventID, booking.CreateReservationRequest{
		TicketCategoryID: categoryID,
		Quantity:         3,
	}, uuid.NewString())
	if err != nil {
		t.Fatalf("failed to create reservation 2: %v", err)
	}

	// Create checkout session for customer 2
	checkoutResp, err := payService.CreateCheckoutSession(ctx, cust2ID, res2Resp.ID)
	if err != nil {
		t.Fatalf("failed to create checkout session: %v", err)
	}

	// Manually succeed attempt and confirm booking for customer 2
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	paUUID, _ := uuid.Parse(checkoutResp.PaymentAttemptID)
	_, err = payRepo.SucceedPaymentAttemptTx(ctx, tx, sqlc.SucceedPaymentAttemptParams{
		ID:                pgtype.UUID{Bytes: paUUID, Valid: true},
		ProviderReference: pgtype.Text{String: "ch_test_" + uuid.NewString(), Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to succeed payment attempt: %v", err)
	}
	confirmResult, err := bookingService.ConfirmReservationPayment(ctx, tx, res2Resp.ID, checkoutResp.PaymentAttemptID)
	if err != nil {
		t.Fatalf("failed to confirm booking: %v", err)
	}
	if confirmResult.Status != booking.ConfirmationStatusConfirmed {
		t.Fatalf("expected booking confirmation confirmed, got %s", confirmResult.Status)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("failed to commit booking confirmation: %v", err)
	}

	// Now Cancel the Event (Transaction 1)
	reason := "Unforeseen circumstance"
	_, err = eventService.Cancel(ctx, userID, organizerID, eventID, uuid.NewString(), event.CancelEventRequest{
		Reason: reason,
	})
	if err != nil {
		t.Fatalf("failed to cancel event: %v", err)
	}

	// === VERIFY IMMEDIATE INVARIANTS ===

	// Invariant 1: New reservations must be rejected immediately
	cust3ID, _ := createTestUserAndOrganizer(t, ctx, pool)
	_, err = bookingService.CreateReservation(ctx, cust3ID, eventID, booking.CreateReservationRequest{
		TicketCategoryID: categoryID,
		Quantity:         1,
	}, uuid.NewString())
	if !errors.Is(err, booking.ErrEventNotBookable) {
		t.Errorf("expected ErrEventNotBookable on cancelled event, got: %v", err)
	}

	// Invariant 2: New checkout session initiation on cancelled event must be rejected immediately
	_, err = payService.CreateCheckoutSession(ctx, cust1ID, res1Resp.ID)
	if !errors.Is(err, payment.ErrEventCancelled) {
		t.Errorf("expected ErrEventCancelled on checkout for cancelled event, got: %v", err)
	}

	// Invariant 3: Booking confirmation for late payment on cancelled event must fail and return Ineligible
	txLate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("failed to begin txLate: %v", err)
	}
	defer func() { _ = txLate.Rollback(ctx) }()

	// Create dummy payment attempt for res1
	dummyAttemptID := uuid.New()
	_, err = txLate.Exec(ctx, `
		INSERT INTO payment_attempts (id, reservation_id, amount_minor, currency, status, provider)
		VALUES ($1, $2, 2000, 'INR', 'succeeded', 'simulated')
	`, dummyAttemptID, res1Resp.ID)
	if err != nil {
		t.Fatalf("failed to insert dummy payment attempt: %v", err)
	}
	lateConfirmResult, err := bookingService.ConfirmReservationPayment(ctx, txLate, res1Resp.ID, dummyAttemptID.String())
	if err != nil {
		t.Fatalf("unexpected error from ConfirmReservationPayment: %v", err)
	}
	if lateConfirmResult.Status != booking.ConfirmationStatusIneligible {
		t.Errorf("expected ConfirmationStatusIneligible for cancelled event, got: %s", lateConfirmResult.Status)
	}
	_ = txLate.Rollback(ctx)

	// === VERIFY DOWNSTREAM WORKER BATCHING ===

	outboxRepo := outbox.NewPGRepository(pool)
	cancellationWorker, err := event.NewCancellationWorker(pool, outboxRepo, event.CancellationWorkerConfig{
		BatchSize:     10,
		LeaseDuration: time.Minute,
	}, nil)
	if err != nil {
		t.Fatalf("failed to initialize cancellation worker: %v", err)
	}

	// Run worker batch processing
	processed, err := cancellationWorker.ProcessNextBatch(ctx)
	if err != nil {
		t.Fatalf("worker failed to process batch: %v", err)
	}
	if processed != 1 {
		t.Errorf("expected worker to process 1 cancellation outbox event, got %d", processed)
	}

	// Check that held reservation 1 was invalidated
	var (
		res1Status string
		res1Reason string
	)
	err = pool.QueryRow(ctx, `
		SELECT status, COALESCE(cancellation_reason, '') FROM reservations WHERE id = $1
	`, res1Resp.ID).Scan(&res1Status, &res1Reason)
	if err != nil {
		t.Fatalf("failed to query reservation 1: %v", err)
	}
	if res1Status != "cancelled" {
		t.Errorf("expected reservation 1 status 'cancelled', got %s", res1Status)
	}
	if res1Reason != reason {
		t.Errorf("expected reservation 1 cancellation_reason %q, got %q", reason, res1Reason)
	}

	// Check that reserved inventory was released
	var reservedQty int
	err = pool.QueryRow(ctx, `
		SELECT reserved_quantity FROM inventory WHERE ticket_category_id = $1
	`, categoryID).Scan(&reservedQty)
	if err != nil {
		t.Fatalf("failed to query inventory: %v", err)
	}
	if reservedQty != 0 {
		t.Errorf("expected reserved_quantity to be 0 after held cancellation, got %d", reservedQty)
	}

	// Check that confirmed booking 2 was marked cancelled
	var booking2Status string
	err = pool.QueryRow(ctx, `
		SELECT status FROM bookings WHERE reservation_id = $1
	`, res2Resp.ID).Scan(&booking2Status)
	if err != nil {
		t.Fatalf("failed to query booking 2: %v", err)
	}
	if booking2Status != "cancelled" {
		t.Errorf("expected booking 2 status 'cancelled', got %s", booking2Status)
	}

	// Check that a refund record was created for booking 2
	var (
		refundStatus string
		refundAmount int64
	)
	err = pool.QueryRow(ctx, `
		SELECT status, amount_minor FROM refunds WHERE booking_id = $1
	`, confirmResult.BookingID).Scan(&refundStatus, &refundAmount)
	if err != nil {
		t.Fatalf("failed to query refund: %v", err)
	}
	if refundStatus != "pending" {
		t.Errorf("expected refund status 'pending', got %s", refundStatus)
	}
	if refundAmount <= 0 {
		t.Errorf("expected positive refund amount, got %d", refundAmount)
	}

	// Verify cancellation summary endpoint
	summary, err := eventService.GetCancellationSummary(ctx, organizerID, eventID)
	if err != nil {
		t.Fatalf("failed to get cancellation summary: %v", err)
	}
	if summary.Status != "cancelled" {
		t.Errorf("expected summary status 'cancelled', got %s", summary.Status)
	}
	if summary.TotalBookings != 1 {
		t.Errorf("expected 1 total booking in summary, got %d", summary.TotalBookings)
	}
	if summary.CancelledBookings != 1 {
		t.Errorf("expected 1 cancelled booking in summary, got %d", summary.CancelledBookings)
	}
	if summary.CancelledReservations < 1 {
		t.Errorf("expected at least 1 cancelled reservation in summary, got %d", summary.CancelledReservations)
	}
	if summary.RefundsPending != 1 {
		t.Errorf("expected 1 pending refund in summary, got %d", summary.RefundsPending)
	}
}

func TestEventCancellationSummary_FinancialReconciliation(t *testing.T) {
	ctx := context.Background()
	pool, eventService := setupIntegrationDB(t)
	_, _ = pool.Exec(ctx, "DELETE FROM outbox_events WHERE event_type = $1", event.EventTypeEventCancellationRequested)

	userID, organizerID := createTestUserAndOrganizer(t, ctx, pool)
	eventID := createTestEvent(t, ctx, pool, eventService, userID, organizerID)

	var categoryID string
	err := pool.QueryRow(ctx, `
		SELECT id::text FROM ticket_categories WHERE event_id = $1 LIMIT 1
	`, eventID).Scan(&categoryID)
	if err != nil {
		t.Fatalf("failed to get ticket category: %v", err)
	}

	bookingService, err := booking.NewService(pool)
	if err != nil {
		t.Fatalf("failed to initialize booking service: %v", err)
	}
	payRepo, err := payment.NewRepository(pool)
	if err != nil {
		t.Fatalf("failed to initialize payment repository: %v", err)
	}
	provider := &dummyProvider{}
	payService, err := payment.NewService(pool, payRepo, provider, bookingService)
	if err != nil {
		t.Fatalf("failed to initialize payment service: %v", err)
	}

	// 1. Customer A: Confirmed booking (unit price = 1000, qty = 2 -> total = 2000)
	custAID, _ := createTestUserAndOrganizer(t, ctx, pool)
	resAResp, err := bookingService.CreateReservation(ctx, custAID, eventID, booking.CreateReservationRequest{
		TicketCategoryID: categoryID,
		Quantity:         2,
	}, uuid.NewString())
	if err != nil {
		t.Fatalf("failed to create reservation A: %v", err)
	}
	checkoutAResp, err := payService.CreateCheckoutSession(ctx, custAID, resAResp.ID)
	if err != nil {
		t.Fatalf("failed to create checkout session A: %v", err)
	}
	txA, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("failed to begin txA: %v", err)
	}
	defer func() { _ = txA.Rollback(context.Background()) }()
	paUUIDA, _ := uuid.Parse(checkoutAResp.PaymentAttemptID)
	_, err = payRepo.SucceedPaymentAttemptTx(ctx, txA, sqlc.SucceedPaymentAttemptParams{
		ID:                pgtype.UUID{Bytes: paUUIDA, Valid: true},
		ProviderReference: pgtype.Text{String: "ch_test_" + uuid.NewString(), Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to succeed attempt A: %v", err)
	}
	confirmResultA, err := bookingService.ConfirmReservationPayment(ctx, txA, resAResp.ID, checkoutAResp.PaymentAttemptID)
	if err != nil {
		t.Fatalf("failed to confirm booking A: %v", err)
	}
	if err := txA.Commit(ctx); err != nil {
		t.Fatalf("failed to commit booking A: %v", err)
	}

	// 2. Customer B: Held reservation with multiple payment attempts (e.g. failed attempt + pending attempt)
	custBID, _ := createTestUserAndOrganizer(t, ctx, pool)
	resBResp, err := bookingService.CreateReservation(ctx, custBID, eventID, booking.CreateReservationRequest{
		TicketCategoryID: categoryID,
		Quantity:         1,
	}, uuid.NewString())
	if err != nil {
		t.Fatalf("failed to create reservation B: %v", err)
	}
	attemptB1 := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO payment_attempts (id, reservation_id, amount_minor, currency, status, provider)
		VALUES ($1, $2, 1000, 'INR', 'failed', 'simulated')
	`, attemptB1, resBResp.ID)
	if err != nil {
		t.Fatalf("failed to insert attempt B1: %v", err)
	}
	attemptB2 := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO payment_attempts (id, reservation_id, amount_minor, currency, status, provider)
		VALUES ($1, $2, 1000, 'INR', 'pending', 'simulated')
	`, attemptB2, resBResp.ID)
	if err != nil {
		t.Fatalf("failed to insert attempt B2: %v", err)
	}

	// 3. Customer C: Will have a late payment with NO booking (automatic refund)
	custCID, _ := createTestUserAndOrganizer(t, ctx, pool)
	resCResp, err := bookingService.CreateReservation(ctx, custCID, eventID, booking.CreateReservationRequest{
		TicketCategoryID: categoryID,
		Quantity:         3,
	}, uuid.NewString())
	if err != nil {
		t.Fatalf("failed to create reservation C: %v", err)
	}
	attemptC := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO payment_attempts (id, reservation_id, amount_minor, currency, status, provider)
		VALUES ($1, $2, 3000, 'INR', 'succeeded', 'simulated')
	`, attemptC, resCResp.ID)
	if err != nil {
		t.Fatalf("failed to insert attempt C: %v", err)
	}

	// 4. Customer D: Will have a failed refund
	custDID, _ := createTestUserAndOrganizer(t, ctx, pool)
	resDResp, err := bookingService.CreateReservation(ctx, custDID, eventID, booking.CreateReservationRequest{
		TicketCategoryID: categoryID,
		Quantity:         1,
	}, uuid.NewString())
	if err != nil {
		t.Fatalf("failed to create reservation D: %v", err)
	}
	attemptD := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO payment_attempts (id, reservation_id, amount_minor, currency, status, provider)
		VALUES ($1, $2, 1000, 'INR', 'succeeded', 'simulated')
	`, attemptD, resDResp.ID)
	if err != nil {
		t.Fatalf("failed to insert attempt D: %v", err)
	}

	// Now Cancel the Event (Transaction 1)
	_, err = eventService.Cancel(ctx, userID, organizerID, eventID, uuid.NewString(), event.CancelEventRequest{
		Reason: "Organizer cancellation",
	})
	if err != nil {
		t.Fatalf("failed to cancel event: %v", err)
	}

	// Run cancellation worker batch processing
	outboxRepo := outbox.NewPGRepository(pool)
	cancellationWorker, err := event.NewCancellationWorker(pool, outboxRepo, event.CancellationWorkerConfig{
		BatchSize:     10,
		LeaseDuration: time.Minute,
	}, nil)
	if err != nil {
		t.Fatalf("failed to initialize cancellation worker: %v", err)
	}
	processed, err := cancellationWorker.ProcessNextBatch(ctx)
	if err != nil {
		t.Fatalf("worker failed: %v", err)
	}
	if processed != 1 {
		t.Errorf("expected 1 event processed, got %d", processed)
	}

	// Now simulate late payment webhook for Customer C (arrives after event cancellation)
	txC, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("failed to begin txC: %v", err)
	}
	defer func() { _ = txC.Rollback(context.Background()) }()
	confirmResultC, err := bookingService.ConfirmReservationPayment(ctx, txC, resCResp.ID, attemptC.String())
	if err != nil {
		t.Fatalf("unexpected error from ConfirmReservationPayment: %v", err)
	}
	if confirmResultC.Status != booking.ConfirmationStatusIneligible {
		t.Fatalf("expected Ineligible, got %s", confirmResultC.Status)
	}
	// Create unbooked automatic refund for attempt C (amount = 3000)
	_, err = payService.CreateRefundTx(ctx, txC, pgtype.UUID{Bytes: attemptC, Valid: true}, pgtype.UUID{Valid: false}, 3000, "auto_ineligible_"+attemptC.String())
	if err != nil {
		t.Fatalf("failed to create auto refund C: %v", err)
	}
	if err := txC.Commit(ctx); err != nil {
		t.Fatalf("failed to commit txC: %v", err)
	}

	// For Customer D, insert a failed refund on attempt D
	refundD := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO refunds (id, payment_attempt_id, amount_minor, currency, status, failure_reason)
		VALUES ($1, $2, 1000, 'INR', 'failed', 'bank rejected')
	`, refundD, attemptD)
	if err != nil {
		t.Fatalf("failed to insert failed refund D: %v", err)
	}

	// Check Summary State 1:
	// Booking A was cancelled, has pending refund (amount 2000)
	// Attempt C has pending auto-refund (amount 3000, NO booking)
	// Attempt D has failed refund (amount 1000, NO booking)
	// Total bookings: exactly 1 (Booking A)
	// Cancelled bookings: exactly 1 (Booking A)
	// Refunds pending: 2 (Refund A + Refund C)
	// Refunds failed: 1 (Refund D)
	// Refunds processing: 0
	// Refunds succeeded: 0
	// Total refunded: 0
	summary1, err := eventService.GetCancellationSummary(ctx, organizerID, eventID)
	if err != nil {
		t.Fatalf("failed to get summary 1: %v", err)
	}
	if summary1.TotalBookings != 1 {
		t.Errorf("summary1: expected 1 total booking, got %d", summary1.TotalBookings)
	}
	if summary1.CancelledBookings != 1 {
		t.Errorf("summary1: expected 1 cancelled booking, got %d", summary1.CancelledBookings)
	}
	if summary1.RefundsPending != 2 {
		t.Errorf("summary1: expected 2 pending refunds (1 booked + 1 unbooked), got %d", summary1.RefundsPending)
	}
	if summary1.RefundsFailed != 1 {
		t.Errorf("summary1: expected 1 failed refund, got %d", summary1.RefundsFailed)
	}
	if summary1.RefundsProcessing != 0 {
		t.Errorf("summary1: expected 0 processing refunds, got %d", summary1.RefundsProcessing)
	}
	if summary1.RefundsSucceeded != 0 {
		t.Errorf("summary1: expected 0 succeeded refunds, got %d", summary1.RefundsSucceeded)
	}
	if summary1.TotalRefundedAmountMinor != 0 {
		t.Errorf("summary1: expected 0 total refunded, got %d", summary1.TotalRefundedAmountMinor)
	}

	// State 2: Progress Refund A to 'succeeded' and Refund C to 'processing'
	_, err = pool.Exec(ctx, `
		UPDATE refunds SET status = 'succeeded', completed_at = clock_timestamp() WHERE booking_id = $1
	`, confirmResultA.BookingID)
	if err != nil {
		t.Fatalf("failed to update refund A to succeeded: %v", err)
	}
	_, err = pool.Exec(ctx, `
		UPDATE refunds SET status = 'processing', claim_token = uuidv7(), claim_expires_at = clock_timestamp() + interval '2 minutes'
		WHERE payment_attempt_id = $1
	`, attemptC)
	if err != nil {
		t.Fatalf("failed to update refund C to processing: %v", err)
	}

	var bookingAPrice int64
	err = pool.QueryRow(ctx, `SELECT total_price_minor FROM bookings WHERE id = $1`, confirmResultA.BookingID).Scan(&bookingAPrice)
	if err != nil {
		t.Fatalf("failed to query booking A price: %v", err)
	}

	summary2, err := eventService.GetCancellationSummary(ctx, organizerID, eventID)
	if err != nil {
		t.Fatalf("failed to get summary 2: %v", err)
	}
	if summary2.RefundsSucceeded != 1 {
		t.Errorf("summary2: expected 1 succeeded refund, got %d", summary2.RefundsSucceeded)
	}
	if summary2.RefundsProcessing != 1 {
		t.Errorf("summary2: expected 1 processing refund, got %d", summary2.RefundsProcessing)
	}
	if summary2.RefundsPending != 0 {
		t.Errorf("summary2: expected 0 pending refunds, got %d", summary2.RefundsPending)
	}
	if summary2.TotalRefundedAmountMinor != bookingAPrice {
		t.Errorf("summary2: expected %d refunded minor, got %d", bookingAPrice, summary2.TotalRefundedAmountMinor)
	}

	// State 3: Complete Refund C (unbooked refund) to 'succeeded'
	_, err = pool.Exec(ctx, `
		UPDATE refunds SET status = 'succeeded', completed_at = clock_timestamp() WHERE payment_attempt_id = $1
	`, attemptC)
	if err != nil {
		t.Fatalf("failed to update refund C to succeeded: %v", err)
	}

	summary3, err := eventService.GetCancellationSummary(ctx, organizerID, eventID)
	if err != nil {
		t.Fatalf("failed to get summary 3: %v", err)
	}
	if summary3.RefundsSucceeded != 2 {
		t.Errorf("summary3: expected 2 succeeded refunds (1 booked + 1 unbooked), got %d", summary3.RefundsSucceeded)
	}
	expectedTotal := bookingAPrice + 3000
	if summary3.TotalRefundedAmountMinor != expectedTotal {
		t.Errorf("summary3: expected %d total refunded, got %d", expectedTotal, summary3.TotalRefundedAmountMinor)
	}
	if summary3.TotalBookings != 1 || summary3.CancelledBookings != 1 {
		t.Errorf("summary3: bookings counts changed unexpectedly: total=%d, cancelled=%d", summary3.TotalBookings, summary3.CancelledBookings)
	}

	// State 4: Test unauthorized access (different organizer)
	_, otherOrgID := createTestUserAndOrganizer(t, ctx, pool)
	_, err = eventService.GetCancellationSummary(ctx, otherOrgID, eventID)
	if !errors.Is(err, event.ErrEventNotFound) {
		t.Errorf("expected ErrEventNotFound for non-owning organizer, got: %v", err)
	}
}
