package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/email"
	"github.com/Punith1117/ticket-forge/internal/outbox"
	"github.com/Punith1117/ticket-forge/internal/payment"
	"github.com/Punith1117/ticket-forge/internal/payment/simulator"
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

	outboxWorker, err := outbox.NewWorker(repo, sender, encrypter, workerCfg, logger)
	if err != nil {
		logger.Error("Failed to initialize outbox worker", "error", err)
		os.Exit(1)
	}

	// 5. Payment Refund Worker
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
	refundWorker, err := payment.NewRefundWorker(paymentRepo, simProvider, payment.DefaultRefundWorkerConfig(), logger)
	if err != nil {
		logger.Error("Failed to initialize refund worker", "error", err)
		os.Exit(1)
	}

	// 6. Graceful Shutdown on OS Signal
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if err := outboxWorker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("Outbox worker run error", "error", err)
		}
	}()

	go func() {
		defer wg.Done()
		if err := refundWorker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("Refund worker run error", "error", err)
		}
	}()

	wg.Wait()
	logger.Info("TicketForge worker stopped successfully")
}
