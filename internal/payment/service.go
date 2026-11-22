package payment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool       *pgxpool.Pool
	repository *Repository
	provider   Provider
}

func NewService(pool *pgxpool.Pool, repository *Repository, provider Provider) (*Service, error) {
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
		pool:       pool,
		repository: repository,
		provider:   provider,
	}, nil
}

func (s *Service) Provider() Provider {
	return s.provider
}

// CreateCheckoutSession validates reservation eligibility and creates a pending payment attempt and provider session.
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

	// 1. Fetch reservation for customer
	reservation, err := s.repository.GetReservationByIDForCustomer(ctx, reservationDBID, customerDBID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Check if reservation exists at all to differentiate 404 from 403
			rawRes, checkErr := sqlc.New(s.pool).GetReservationByID(ctx, reservationDBID)
			if checkErr == nil && rawRes.ID.Valid {
				return CheckoutSessionResponse{}, ErrReservationForbidden
			}
			return CheckoutSessionResponse{}, ErrReservationNotFound
		}
		return CheckoutSessionResponse{}, fmt.Errorf("payment: query reservation: %w", err)
	}

	// 2. Validate status
	if reservation.Status != "held" {
		return CheckoutSessionResponse{}, ErrReservationNotHeld
	}

	// 3. Validate expiration (database clock reference)
	if !reservation.ExpiresAt.Valid || !time.Now().UTC().Before(reservation.ExpiresAt.Time) {
		return CheckoutSessionResponse{}, ErrReservationExpired
	}

	// 4. Validate payable amount (free reservations not supported in payment checkout)
	if reservation.TotalPriceMinor <= 0 {
		return CheckoutSessionResponse{}, ErrFreeReservationNotSupported
	}

	// 5. Create pending payment attempt in database before contacting provider
	attemptUUID := uuid.New()
	attemptDBID := pgtype.UUID{Bytes: attemptUUID, Valid: true}

	attempt, err := s.repository.CreatePaymentAttempt(ctx, sqlc.CreatePaymentAttemptParams{
		ID:                     attemptDBID,
		ReservationID:          reservation.ID,
		AmountMinor:            reservation.TotalPriceMinor,
		Currency:               reservation.Currency,
		Provider:               s.provider.Name(),
		ProviderOrderReference: pgtype.Text{Valid: false},
	})
	if err != nil {
		return CheckoutSessionResponse{}, fmt.Errorf("payment: record pending attempt: %w", err)
	}

	// 6. Request provider checkout session outside database transaction
	sessionReq := CreateCheckoutSessionRequest{
		PaymentAttemptID: attemptUUID.String(),
		ReservationID:    reservationUUID.String(),
		AmountMinor:      reservation.TotalPriceMinor,
		Currency:         reservation.Currency,
		ExpiresAt:        reservation.ExpiresAt.Time,
	}

	session, err := s.provider.CreateCheckoutSession(ctx, sessionReq)
	if err != nil {
		// Record failure on the payment attempt
		_, _ = s.repository.FailPaymentAttempt(ctx, sqlc.FailPaymentAttemptParams{
			ID: attempt.ID,
			FailureReason: pgtype.Text{
				String: "provider session creation failed: " + err.Error(),
				Valid:  true,
			},
		})
		return CheckoutSessionResponse{}, fmt.Errorf("%w: %v", ErrProviderSessionFailed, err)
	}

	// 7. Update attempt with returned provider order reference
	updatedAttempt, err := s.repository.UpdatePaymentAttemptProviderOrder(ctx, sqlc.UpdatePaymentAttemptProviderOrderParams{
		ID: attempt.ID,
		ProviderOrderReference: pgtype.Text{
			String: session.ProviderOrderReference,
			Valid:  true,
		},
	})
	if err != nil {
		return CheckoutSessionResponse{}, fmt.Errorf("payment: persist provider order reference: %w", err)
	}

	return CheckoutSessionResponse{
		PaymentAttemptID:     uuid.UUID(updatedAttempt.ID.Bytes).String(),
		ReservationID:        reservationUUID.String(),
		Provider:             updatedAttempt.Provider,
		ProviderSessionID:    session.ProviderSessionID,
		CheckoutURL:          session.CheckoutURL,
		AmountMinor:          updatedAttempt.AmountMinor,
		Currency:             updatedAttempt.Currency,
		Status:               updatedAttempt.Status,
		ReservationExpiresAt: reservation.ExpiresAt.Time,
	}, nil
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

	// 1. Idempotency record insertion
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
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("payment: record webhook event: %w", err)
		}
	}

	// If ON CONFLICT DO NOTHING returned no rows, event already exists
	if !webhookRecord.ID.Valid {
		existing, err := s.repository.GetPaymentWebhookEvent(ctx, tx, event.Provider, event.EventID)
		if err != nil {
			return fmt.Errorf("payment: fetch existing webhook event: %w", err)
		}
		if existing.ProcessingStatus == "processed" {
			// Idempotent duplicate: already processed
			return nil
		}
		eventDBID = existing.ID
	} else {
		eventDBID = webhookRecord.ID
	}

	// 2. Identify corresponding payment attempt
	attempt, err := s.repository.GetPaymentAttemptByProviderOrderTx(
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

	// 3. Validate amount and currency
	if attempt.AmountMinor != event.AmountMinor {
		return ErrAmountMismatch
	}
	if attempt.Currency != event.Currency {
		return ErrCurrencyMismatch
	}

	// 4. State transition
	switch attempt.Status {
	case "succeeded":
		// Out-of-order late failure event must not downgrade an already succeeded payment attempt
		// Success duplicate is idempotent no-op.

	case "failed":
		if event.Outcome == PaymentOutcomeSucceeded {
			return ErrInvalidStateTransition
		}

	case "pending":
		if event.Outcome == PaymentOutcomeSucceeded {
			_, err = s.repository.SucceedPaymentAttemptTx(ctx, tx, sqlc.SucceedPaymentAttemptParams{
				ID: attempt.ID,
				ProviderReference: pgtype.Text{
					String: event.ProviderPaymentReference,
					Valid:  event.ProviderPaymentReference != "",
				},
			})
			if err != nil {
				return fmt.Errorf("payment: record successful attempt: %w", err)
			}
		} else if event.Outcome == PaymentOutcomeFailed {
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

	// 5. Mark webhook event as processed
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
