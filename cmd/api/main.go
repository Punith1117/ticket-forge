package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/booking"
	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/Punith1117/ticket-forge/internal/event"
	"github.com/Punith1117/ticket-forge/internal/organizer"
	"github.com/Punith1117/ticket-forge/internal/payment"
	"github.com/Punith1117/ticket-forge/internal/payment/simulator"
	"github.com/Punith1117/ticket-forge/internal/ratelimit"
	"github.com/Punith1117/ticket-forge/internal/security"
	"github.com/Punith1117/ticket-forge/internal/venuebooking"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("TicketForge API starting...")

	// Database Connection Pool
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dbCancel()

	dbCfg := database.ConfigFromEnv()
	pool, err := database.NewPool(dbCtx, dbCfg)
	if err != nil {
		logger.Error("Failed to initialize database connection pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	logger.Info("Database connection pool established")

	// Token Encrypter (KeyRing)
	encCfg := security.EncryptionConfigFromEnv()
	keyRing, err := encCfg.BuildKeyRing()
	if err != nil {
		logger.Error("Failed to build encryption key ring", "error", err)
		os.Exit(1)
	}
	encrypter, err := security.NewTokenEncrypter(keyRing)
	if err != nil {
		logger.Error("Failed to initialize token encrypter", "error", err)
		os.Exit(1)
	}
	logger.Info("Token encrypter initialized", "active_version", encrypter.ActiveKeyVersion())

	// Auth Service & Handlers
	authService, err := auth.NewService(pool, encrypter)
	if err != nil {
		logger.Error("Failed to initialize auth service", "error", err)
		os.Exit(1)
	}

	// Rate Limiter
	rlCfg, err := ratelimit.ConfigFromEnv()
	if err != nil {
		logger.Error("Failed to load rate limiter config", "error", err)
		os.Exit(1)
	}
	limiter, err := ratelimit.NewPostgresLimiter(pool)
	if err != nil {
		logger.Error("Failed to initialize rate limiter", "error", err)
		os.Exit(1)
	}
	keyDeriver, err := ratelimit.NewKeyDeriver(rlCfg.HMACSecret)
	if err != nil {
		logger.Error("Failed to initialize rate limit key deriver", "error", err)
		os.Exit(1)
	}
	logger.Info("Rate limiter initialized")

	// Trusted proxy configuration for client IP extraction.
	// TRUSTED_PROXIES is a comma-separated list of trusted proxy IPs or CIDRs (e.g. "10.0.0.0/8,172.16.0.1").
	// When absent or empty, only RemoteAddr is used; forwarded headers are ignored entirely.
	var ipExtractor *auth.IPExtractor
	if rawProxies := os.Getenv("TRUSTED_PROXIES"); rawProxies != "" {
		var entries []string
		for _, e := range strings.Split(rawProxies, ",") {
			if s := strings.TrimSpace(e); s != "" {
				entries = append(entries, s)
			}
		}
		extractor, extErr := auth.NewIPExtractor(entries)
		if extErr != nil {
			logger.Error("Invalid TRUSTED_PROXIES configuration", "error", extErr)
			os.Exit(1)
		}
		ipExtractor = extractor
		logger.Info("Trusted proxy configuration loaded", "count", len(entries))
	} else {
		var extErr error
		ipExtractor, extErr = auth.NewIPExtractor(nil)
		if extErr != nil {
			logger.Error("Failed to initialize IP extractor", "error", extErr)
			os.Exit(1)
		}
		logger.Info("No trusted proxies configured; using RemoteAddr for client IP")
	}

	authHandler := auth.NewHandler(
		authService,
		logger,
		os.Getenv("SECURE_COOKIES") == "true",
		auth.WithRateLimiter(limiter, keyDeriver),
		auth.WithIPExtractor(ipExtractor),
	)

	// Organizer Service & Handler
	organizerService, err := organizer.NewService(pool)
	if err != nil {
		logger.Error("Failed to initialize organizer service", "error", err)
		os.Exit(1)
	}

	organizerHandler := organizer.NewHandler(organizerService, logger)

	// Venue Booking Service & Handler
	venueBookingService, err := venuebooking.NewService(pool)
	if err != nil {
		logger.Error("Failed to initialize venue booking service", "error", err)
		os.Exit(1)
	}

	venueBookingHandler := venuebooking.NewHandler(
		venueBookingService,
		organizerService,
		logger,
	)

	eventService, err := event.NewService(pool)
	if err != nil {
		logger.Error("Failed to initialize event service", "error", err)
		os.Exit(1)
	}

	eventHandler := event.NewHandler(
		eventService,
		organizerService,
		logger,
	)

	// Booking Service & Handler
	bookingService, err := booking.NewService(pool)
	if err != nil {
		logger.Error("Failed to initialize booking service", "error", err)
		os.Exit(1)
	}

	bookingHandler := booking.NewHandler(bookingService, logger)

	// Payment Service & Handler
	payCfg, err := payment.ConfigFromEnv()
	if err != nil {
		logger.Error("Failed to load payment configuration", "error", err)
		os.Exit(1)
	}

	simProvider, err := simulator.NewProvider(payCfg.WebhookSecret, payCfg.WebhookURL, nil)
	if err != nil {
		logger.Error("Failed to initialize simulated payment provider", "error", err)
		os.Exit(1)
	}

	paymentRepo, err := payment.NewRepository(pool)
	if err != nil {
		logger.Error("Failed to initialize payment repository", "error", err)
		os.Exit(1)
	}

	paymentService, err := payment.NewService(pool, paymentRepo, simProvider, bookingService)
	if err != nil {
		logger.Error("Failed to initialize payment service", "error", err)
		os.Exit(1)
	}

	paymentHandler := payment.NewHandler(paymentService, logger)

	mux := http.NewServeMux()

	authHandler.RegisterRoutes(mux)
	organizerHandler.RegisterRoutes(mux, authHandler)
	venueBookingHandler.RegisterRoutes(mux, authHandler)
	eventHandler.RegisterRoutes(mux, authHandler)
	bookingHandler.RegisterRoutes(mux, authHandler)
	paymentHandler.RegisterRoutes(mux, authHandler)

	// Simulated hosted checkout routes (strictly disabled in production)
	isDevMode := os.Getenv("ENV") != "production"
	if isDevMode && payCfg.Provider == "simulated" {
		simProvider.SetSessionLookup(&sessionLookupAdapter{pool: pool, provider: simProvider.Name()})
		simProvider.RegisterRoutes(mux)
		logger.Info("Simulated payment hosted checkout enabled", "provider", payCfg.Provider)
	}

	// Health check endpoint
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 4. Graceful Shutdown on OS Signal
	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("API server listening", "port", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-shutdownCtx.Done()
	logger.Info("API server shutting down gracefully...")

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(stopCtx); err != nil {
		logger.Error("Server forced shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("TicketForge API server stopped successfully")
}

type sessionLookupAdapter struct {
	pool     *pgxpool.Pool
	provider string
}

func (a *sessionLookupAdapter) GetSessionBySessionID(ctx context.Context, sessionID string) (simulator.PersistedSession, error) {
	attempt, err := sqlc.New(a.pool).GetPaymentAttemptByProviderSession(ctx, sqlc.GetPaymentAttemptByProviderSessionParams{
		Provider:                   a.provider,
		ProviderCheckoutSessionID: pgtype.Text{String: sessionID, Valid: true},
	})
	if err != nil {
		return simulator.PersistedSession{}, err
	}
	return toPersistedSession(attempt), nil
}

func (a *sessionLookupAdapter) GetSessionByAttemptID(ctx context.Context, attemptID string) (simulator.PersistedSession, error) {
	u, err := uuid.Parse(attemptID)
	if err != nil {
		return simulator.PersistedSession{}, err
	}
	attempt, err := sqlc.New(a.pool).GetPaymentAttemptByID(ctx, pgtype.UUID{Bytes: u, Valid: true})
	if err != nil {
		return simulator.PersistedSession{}, err
	}
	return toPersistedSession(attempt), nil
}

func toPersistedSession(attempt sqlc.PaymentAttempt) simulator.PersistedSession {
	return simulator.PersistedSession{
		PaymentAttemptID: uuid.UUID(attempt.ID.Bytes).String(),
		SessionID:        attempt.ProviderCheckoutSessionID.String,
		OrderRef:         attempt.ProviderOrderReference.String,
		ReservationID:    uuid.UUID(attempt.ReservationID.Bytes).String(),
		AmountMinor:      attempt.AmountMinor,
		Currency:         attempt.Currency,
		Status:           attempt.Status,
		ExpiresAt:        attempt.ExpiresAt.Time,
	}
}
