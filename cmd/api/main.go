package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/security"
)

func main() {
	config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("TicketForge API starting...")

	// 1. Database Connection Pool
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

	// 2. Token Encrypter (KeyRing)
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

	// 3. Auth Service & Handlers
	authService, err := auth.NewService(pool, encrypter)
	if err != nil {
		logger.Error("Failed to initialize auth service", "error", err)
		os.Exit(1)
	}

	authHandler := auth.NewHandler(authService, logger)

	mux := http.NewServeMux()
	authHandler.RegisterRoutes(mux)

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
