package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/email"
	"github.com/Punith1117/ticket-forge/internal/outbox"
	"github.com/Punith1117/ticket-forge/internal/security"
)

func main() {
	config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("TicketForge worker starting...")

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

	// 2. Email Sender (SMTP)
	smtpCfg := email.SMTPConfigFromEnv()
	sender, err := email.NewSMTPSender(smtpCfg)
	if err != nil {
		logger.Error("Failed to initialize email sender", "error", err)
		os.Exit(1)
	}
	logger.Info("Email sender initialized", "host", smtpCfg.Host, "port", smtpCfg.Port)

	// 3. Token Encrypter (KeyRing)
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

	// 4. Outbox Repository & Worker
	repo := outbox.NewPGRepository(pool)
	workerCfg := outbox.WorkerConfigFromEnv()
	if err := workerCfg.Validate(); err != nil {
		logger.Error("Invalid worker configuration", "error", err)
		os.Exit(1)
	}

	worker, err := outbox.NewWorker(repo, sender, encrypter, workerCfg, logger)
	if err != nil {
		logger.Error("Failed to initialize outbox worker", "error", err)
		os.Exit(1)
	}

	// 5. Graceful Shutdown on OS Signal
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := worker.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("Worker run error", "error", err)
		os.Exit(1)
	}

	logger.Info("TicketForge worker stopped successfully")
}
