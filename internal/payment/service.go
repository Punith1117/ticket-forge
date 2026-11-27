package payment

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Punith1117/ticket-forge/internal/booking"
	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultInitLeaseDuration = 15 * time.Second

type BookingConfirmer interface {
	ConfirmReservationPayment(
		ctx context.Context,
		tx pgx.Tx,
		reservationID string,
		paymentAttemptID string,
	) (booking.ConfirmationResult, error)
}

type Service struct {
	pool             *pgxpool.Pool
	repository       *Repository
	provider         Provider
	bookingConfirmer BookingConfirmer
}

func NewService(
	pool *pgxpool.Pool,
	repository *Repository,
	provider Provider,
	bookingConfirmer BookingConfirmer,
) (*Service, error) {
	if pool == nil {
		return nil, errors.New("payment: database pool cannot be nil")
	}
	if repository == nil {
		return nil, errors.New("payment: repository cannot be nil")
	}
	if provider == nil {
		return nil, errors.New("payment: provider cannot be nil")
	}

	return &Service{
		pool:             pool,
		repository:       repository,
		provider:         provider,
		bookingConfirmer: bookingConfirmer,
	}, nil
}

func (s *Service) Provider() Provider {
	return s.provider
}

// CreateCheckoutSession validates reservation eligibility, reuses any active pending session,
// or creates/reconciles a new session using a durable initializing lease.
func (s *Service) CreateCheckoutSession(
	ctx context.Context,
	customerIDStr string,
	reservationIDStr string,
) (CheckoutSessionResponse, error) {
	customerUUID, err := uuid.Parse(customerIDStr)
	if err != nil {
		return CheckoutSessionResponse{}, fmt.Errorf("payment: parse customer ID: %w", err)
	}

	reservationUUID, err := uuid.Parse(reservationIDStr)
	if err != nil {
		return CheckoutSessionResponse{}, ErrReservationNotFound
	}

	customerDBID := pgtype.UUID{Bytes: customerUUID, Valid: true}
	reservationDBID := pgtype.UUID{Bytes: reservationUUID, Valid: true}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CheckoutSessionResponse{}, fmt.Errorf("payment: begin checkout transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	// 1. Lock reservation row to serialize concurrent checkout initiations for the same reservation
	reservation, err := s.repository.GetReservationForUpdateTx(ctx, tx, reservationDBID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CheckoutSessionResponse{}, ErrReservationNotFound
		}
		return CheckoutSessionResponse{}, fmt.Errorf("payment: lock reservation: %w", err)
	}

	if reservation.CustomerID != customerDBID {
		return CheckoutSessionResponse{}, ErrReservationForbidden
	}
	if reservation.Status != "held" {
		return CheckoutSessionResponse{}, ErrReservationNotHeld
	}
	if !reservation.ExpiresAt.Valid || !time.Now().UTC().Before(reservation.ExpiresAt.Time) {
		return CheckoutSessionResponse{}, ErrReservationExpired
	}
	if reservation.TotalPriceMinor <= 0 {
		return CheckoutSessionResponse{}, ErrFreeReservationNotSupported
	}

	// 2. Check for an existing initializing or pending attempt
	var (
		attempt        sqlc.PaymentAttempt
		claimToken     pgtype.UUID
		isReconciled   bool
		needProvider   bool
		leaseInterval  = pgtype.Interval{Microseconds: int64(defaultInitLeaseDuration / time.Microsecond), Valid: true}
	)

	existing, err := s.repository.GetActivePaymentAttemptForReservationTx(ctx, tx, reservation.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CheckoutSessionResponse{}, fmt.Errorf("payment: check active attempt: %w", err)
	}

	if err == nil {
		switch existing.Status {
		case "pending":
			if !existing.ExpiresAt.Valid || time.Now().UTC().Before(existing.ExpiresAt.Time) {
				if err := tx.Rollback(ctx); err != nil {
					return CheckoutSessionResponse{}, fmt.Errorf("payment: rollback reuse tx: %w", err)
				}
				return CheckoutSessionResponse{
					PaymentAttemptID:     uuid.UUID(existing.ID.Bytes).String(),
					ReservationID:        reservationUUID.String(),
					Provider:             existing.Provider,
					ProviderSessionID:    existing.ProviderCheckoutSessionID.String,
					CheckoutURL:          existing.CheckoutUrl.String,
					AmountMinor:          existing.AmountMinor,
					Currency:             existing.Currency,
					Status:               existing.Status,
					ReservationExpiresAt: reservation.ExpiresAt.Time,
					Reused:               true,
				}, nil
			}
			// Pending session has expired locally; mark expired before creating a new attempt
			if _, expErr := s.repository.ExpirePaymentAttemptTx(ctx, tx, existing.ID); expErr != nil {
				return CheckoutSessionResponse{}, fmt.Errorf("payment: expire stale pending attempt: %w", expErr)
			}

		case "initializing":
			// Never expire an initializing attempt solely on a timeout; check its lease.
			if existing.InitLeaseExpiresAt.Valid && time.Now().UTC().Before(existing.InitLeaseExpiresAt.Time) {
				return CheckoutSessionResponse{}, ErrCheckoutInProgress
			}
			// Lease expired: reclaim it and reconcile with the provider using the same attempt ID.
			newClaimUUID := uuid.New()
			claimToken = pgtype.UUID{Bytes: newClaimUUID, Valid: true}
			reclaimed, recErr := s.repository.ReclaimPaymentAttemptInitLeaseTx(ctx, tx, sqlc.ReclaimPaymentAttemptInitLeaseParams{
				ID:             existing.ID,
				InitClaimToken: claimToken,
				LeaseDuration:  leaseInterval,
			})
			if recErr != nil {
				if errors.Is(recErr, pgx.ErrNoRows) {
					return CheckoutSessionResponse{}, ErrCheckoutInProgress
				}
				return CheckoutSessionResponse{}, fmt.Errorf("payment: reclaim initialization lease: %w", recErr)
			}
			attempt = reclaimed
			isReconciled = true
			needProvider = true
		}
	}

	// 3. Create a new initializing attempt if none is active
	if !needProvider {
		attemptUUID := uuid.New()
		claimUUID := uuid.New()
		claimToken = pgtype.UUID{Bytes: claimUUID, Valid: true}

		created, createErr := s.repository.CreatePaymentAttemptTx(ctx, tx, sqlc.CreatePaymentAttemptParams{
			ID:             pgtype.UUID{Bytes: attemptUUID, Valid: true},
			ReservationID:  reservation.ID,
			AmountMinor:    reservation.TotalPriceMinor,
			Currency:       reservation.Currency,
			Provider:       s.provider.Name(),
			InitClaimToken: claimToken,
			LeaseDuration:  leaseInterval,
		})
		if createErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(createErr, &pgErr) && pgErr.Code == "23505" {
				return CheckoutSessionResponse{}, ErrCheckoutInProgress
			}
			return CheckoutSessionResponse{}, fmt.Errorf("payment: record initializing attempt: %w", createErr)
		}
		attempt = created
		needProvider = true
	}

	if err := tx.Commit(ctx); err != nil {
		return CheckoutSessionResponse{}, fmt.Errorf("payment: commit initializing attempt: %w", err)
	}

	// 4. Request or reconcile provider checkout session outside DB transaction (idempotent by PaymentAttemptID)
	attemptUUIDStr := uuid.UUID(attempt.ID.Bytes).String()
	sessionReq := CreateCheckoutSessionRequest{
		PaymentAttemptID: attemptUUIDStr,
		ReservationID:    reservationUUID.String(),
		AmountMinor:      attempt.AmountMinor,
		Currency:         attempt.Currency,
		ExpiresAt:        reservation.ExpiresAt.Time,
	}

	session, err := s.provider.CreateCheckoutSession(ctx, sessionReq)
	if err != nil {
		if !isAmbiguousProviderError(err) {
			_, _ = s.repository.FailInitializingPaymentAttempt(ctx, sqlc.FailInitializingPaymentAttemptParams{
				ID:             attempt.ID,
				InitClaimToken: claimToken,
				FailureReason: pgtype.Text{
					String: "provider session creation failed: " + err.Error(),
					Valid:  true,
				},
			})
		}
		return CheckoutSessionResponse{}, fmt.Errorf("%w: %v", ErrProviderSessionFailed, err)
	}

	// 5. Transition initializing -> pending fenced by init_claim_token
	tx2, err := s.pool.Begin(ctx)
	if err != nil {
		return CheckoutSessionResponse{}, fmt.Errorf("payment: begin activation transaction: %w", err)
	}
	defer func() {
		_ = tx2.Rollback(context.Background())
	}()

	expiresAt := session.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = reservation.ExpiresAt.Time
	}

	activated, err := s.repository.ActivatePaymentAttemptSessionTx(ctx, tx2, sqlc.ActivatePaymentAttemptSessionParams{
		ID:                        attempt.ID,
		ProviderCheckoutSessionID: pgtype.Text{String: session.ProviderSessionID, Valid: session.ProviderSessionID != ""},
		ProviderOrderReference:    pgtype.Text{String: session.ProviderOrderReference, Valid: session.ProviderOrderReference != ""},
		CheckoutUrl:               pgtype.Text{String: session.CheckoutURL, Valid: session.CheckoutURL != ""},
		ExpiresAt:                 pgtype.Timestamptz{Time: expiresAt, Valid: true},
		InitClaimToken:            claimToken,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			current, getErr := s.repository.GetPaymentAttemptByID(ctx, attempt.ID)
			if getErr == nil && current.Status == "pending" {
				return CheckoutSessionResponse{
					PaymentAttemptID:     uuid.UUID(current.ID.Bytes).String(),
					ReservationID:        reservationUUID.String(),
					Provider:             current.Provider,
					ProviderSessionID:    current.ProviderCheckoutSessionID.String,
					CheckoutURL:          current.CheckoutUrl.String,
					AmountMinor:          current.AmountMinor,
					Currency:             current.Currency,
					Status:               current.Status,
					ReservationExpiresAt: reservation.ExpiresAt.Time,
					Reused:               true,
				}, nil
			}
			return CheckoutSessionResponse{}, ErrCheckoutInProgress
		}
		return CheckoutSessionResponse{}, fmt.Errorf("payment: activate checkout session: %w", err)
	}

	if err := tx2.Commit(ctx); err != nil {
		return CheckoutSessionResponse{}, fmt.Errorf("payment: commit activated session: %w", err)
	}

	return CheckoutSessionResponse{
		PaymentAttemptID:     uuid.UUID(activated.ID.Bytes).String(),
		ReservationID:        reservationUUID.String(),
		Provider:             activated.Provider,
		ProviderSessionID:    activated.ProviderCheckoutSessionID.String,
		CheckoutURL:          activated.CheckoutUrl.String,
		AmountMinor:          activated.AmountMinor,
		Currency:             activated.Currency,
		Status:               activated.Status,
		ReservationExpiresAt: reservation.ExpiresAt.Time,
		Reused:               isReconciled,
	}, nil
}

func isAmbiguousProviderError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// ProcessWebhook verifies signature and processes an incoming webhook.
func (s *Service) ProcessWebhook(ctx context.Context, payload []byte, headers http.Header) error {
	event, err := s.provider.VerifyWebhook(payload, headers)
	if err != nil {
		return err
	}

	return s.ProcessWebhookEvent(ctx, event)
}

// ProcessWebhookEvent idempotently processes a verified provider webhook event.
func (s *Service) ProcessWebhookEvent(ctx context.Context, event WebhookEvent) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("payment: begin webhook transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	// 1. Inbound webhook deduplication
	eventUUID := uuid.New()
	eventDBID := pgtype.UUID{Bytes: eventUUID, Valid: true}

	webhookRecord, err := s.repository.CreatePaymentWebhookEvent(ctx, tx, sqlc.CreatePaymentWebhookEventParams{
		ID:               eventDBID,
		Provider:         event.Provider,
		ProviderEventID:  event.EventID,
		PaymentAttemptID: pgtype.UUID{Valid: false},
		EventType:        event.EventType,
		Payload:          event.RawPayload,
		ProcessingStatus: "processing",
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("payment: record webhook event: %w", err)
	}

	if !webhookRecord.ID.Valid {
		existing, err := s.repository.GetPaymentWebhookEvent(ctx, tx, event.Provider, event.EventID)
		if err != nil {
			return fmt.Errorf("payment: fetch existing webhook event: %w", err)
		}
		if existing.ProcessingStatus == "processed" || existing.ProcessingStatus == "ignored" {
			return nil
		}
		eventDBID = existing.ID
	} else {
		eventDBID = webhookRecord.ID
	}

	// 2. Un-locked lookup to discover attempt, reservation, and event for canonical lock ordering
	lookupAttempt, err := s.repository.GetPaymentAttemptByProviderOrderTx(
		ctx,
		tx,
		event.Provider,
		event.ProviderOrderReference,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_, _ = s.repository.UpdatePaymentWebhookEventStatus(ctx, tx, sqlc.UpdatePaymentWebhookEventStatusParams{
				ID:               eventDBID,
				PaymentAttemptID: pgtype.UUID{Valid: false},
				ProcessingStatus: "ignored",
			})
			_ = tx.Commit(ctx)
			return ErrPaymentAttemptNotFound
		}
		return fmt.Errorf("payment: find payment attempt: %w", err)
	}

	// 3. Canonical lock order: Event -> Reservation -> PaymentAttempt
	ref, err := s.repository.GetReservationLifecycleReferenceTx(ctx, tx, lookupAttempt.ReservationID)
	if err != nil {
		return fmt.Errorf("payment: get reservation lifecycle reference: %w", err)
	}
	if _, err := s.repository.LockEventForReservationLifecycleTx(ctx, tx, ref.EventID); err != nil {
		return fmt.Errorf("payment: lock event: %w", err)
	}
	if _, err := s.repository.GetReservationForUpdateTx(ctx, tx, lookupAttempt.ReservationID); err != nil {
		return fmt.Errorf("payment: lock reservation: %w", err)
	}
	attempt, err := s.repository.LockPaymentAttemptByIDTx(ctx, tx, lookupAttempt.ID)
	if err != nil {
		return fmt.Errorf("payment: lock payment attempt: %w", err)
	}

	// 4. Validate amount and currency
	if attempt.AmountMinor != event.AmountMinor {
		return ErrAmountMismatch
	}
	if attempt.Currency != event.Currency {
		return ErrCurrencyMismatch
	}

	// 5. State transition
	switch attempt.Status {
	case "succeeded":
		if event.Outcome == PaymentOutcomeSucceeded && s.bookingConfirmer != nil {
			_, bErr := s.repository.GetBookingByReservationIDTx(ctx, tx, attempt.ReservationID)
			_, rErr := s.repository.GetRefundByPaymentAttemptIDTx(ctx, tx, attempt.ID)
			if (bErr != nil || errors.Is(bErr, pgx.ErrNoRows)) && (rErr != nil || errors.Is(rErr, pgx.ErrNoRows)) {
				if err := s.confirmOrRefundTx(ctx, tx, attempt); err != nil {
					return err
				}
			}
		}

	case "failed":
		if event.Outcome == PaymentOutcomeSucceeded {
			return ErrInvalidStateTransition
		}

	case "pending", "expired":
		if event.Outcome == PaymentOutcomeSucceeded {
			attempt, err = s.repository.SucceedPaymentAttemptTx(ctx, tx, sqlc.SucceedPaymentAttemptParams{
				ID: attempt.ID,
				ProviderReference: pgtype.Text{
					String: event.ProviderPaymentReference,
					Valid:  event.ProviderPaymentReference != "",
				},
			})
			if err != nil {
				return fmt.Errorf("payment: record successful attempt: %w", err)
			}

			if s.bookingConfirmer != nil {
				if err := s.confirmOrRefundTx(ctx, tx, attempt); err != nil {
					return err
				}
			}
		} else if event.Outcome == PaymentOutcomeFailed && attempt.Status == "pending" {
			_, err = s.repository.FailPaymentAttemptTx(ctx, tx, sqlc.FailPaymentAttemptParams{
				ID: attempt.ID,
				FailureReason: pgtype.Text{
					String: event.FailureReason,
					Valid:  event.FailureReason != "",
				},
			})
			if err != nil {
				return fmt.Errorf("payment: record failed attempt: %w", err)
			}
		}
	}

	// 6. Mark webhook event as processed
	_, err = s.repository.UpdatePaymentWebhookEventStatus(ctx, tx, sqlc.UpdatePaymentWebhookEventStatusParams{
		ID:               eventDBID,
		PaymentAttemptID: attempt.ID,
		ProcessingStatus: "processed",
	})
	if err != nil {
		return fmt.Errorf("payment: mark webhook processed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("payment: commit webhook transaction: %w", err)
	}

	return nil
}

func (s *Service) confirmOrRefundTx(ctx context.Context, tx pgx.Tx, attempt sqlc.PaymentAttempt) error {
	resIDStr := uuid.UUID(attempt.ReservationID.Bytes).String()
	attemptIDStr := uuid.UUID(attempt.ID.Bytes).String()

	confirmResult, err := s.bookingConfirmer.ConfirmReservationPayment(ctx, tx, resIDStr, attemptIDStr)
	if err != nil {
		return fmt.Errorf("payment: confirm reservation: %w", err)
	}

	if confirmResult.Status == booking.ConfirmationStatusIneligible {
		if _, err := s.CreateRefundTx(ctx, tx, attempt.ID, pgtype.UUID{Valid: false}, attempt.AmountMinor, "auto_ineligible_"+attemptIDStr); err != nil {
			return fmt.Errorf("payment: enqueue refund for ineligible reservation: %w", err)
		}
	}

	return nil
}

// CreateRefundTx locks the parent payment attempt, enforces SUM(active refunds) <= collected amount,
// and inserts a pending refund idempotently when idempotencyKey is non-empty.
func (s *Service) CreateRefundTx(
	ctx context.Context,
	tx pgx.Tx,
	attemptID pgtype.UUID,
	bookingID pgtype.UUID,
	amountMinor int64,
	idempotencyKey string,
) (sqlc.Refund, error) {
	if amountMinor <= 0 {
		return sqlc.Refund{}, ErrOverRefund
	}

	attempt, err := s.repository.LockPaymentAttemptByIDTx(ctx, tx, attemptID)
	if err != nil {
		return sqlc.Refund{}, err
	}
	if attempt.Status != "succeeded" {
		return sqlc.Refund{}, ErrInvalidStateTransition
	}

	activeSum, err := s.repository.SumActiveRefundsForAttemptTx(ctx, tx, attemptID)
	if err != nil {
		return sqlc.Refund{}, err
	}
	if activeSum+amountMinor > attempt.AmountMinor {
		return sqlc.Refund{}, ErrOverRefund
	}

	refundUUID := uuid.New()
	refund, err := s.repository.CreateRefundTx(ctx, tx, sqlc.CreateRefundParams{
		ID:               pgtype.UUID{Bytes: refundUUID, Valid: true},
		PaymentAttemptID: attempt.ID,
		BookingID:        bookingID,
		AmountMinor:      amountMinor,
		Currency:         attempt.Currency,
		IdempotencyKey:   idempotencyKey,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Refund{}, err
	}
	return refund, nil
}

// GetPaymentAttemptByID retrieves a payment attempt by ID.
func (s *Service) GetPaymentAttemptByID(ctx context.Context, idStr string) (sqlc.PaymentAttempt, error) {
	attemptUUID, err := uuid.Parse(idStr)
	if err != nil {
		return sqlc.PaymentAttempt{}, ErrPaymentAttemptNotFound
	}

	attempt, err := s.repository.GetPaymentAttemptByID(ctx, pgtype.UUID{Bytes: attemptUUID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.PaymentAttempt{}, ErrPaymentAttemptNotFound
		}
		return sqlc.PaymentAttempt{}, err
	}

	return attempt, nil
}
