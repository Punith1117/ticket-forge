package payment

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	Provider      string
	WebhookSecret string
	WebhookURL    string
}

func ConfigFromEnv() (Config, error) {
	provider := strings.TrimSpace(os.Getenv("PAYMENT_PROVIDER"))
	if provider == "" {
		provider = "simulated"
	}

	secret := strings.TrimSpace(os.Getenv("PAYMENT_WEBHOOK_SECRET"))
	if secret == "" {
		return Config{}, errors.New("payment: PAYMENT_WEBHOOK_SECRET environment variable is required")
	}

	webhookURL := strings.TrimSpace(os.Getenv("PAYMENT_WEBHOOK_URL"))
	if webhookURL == "" {
		webhookURL = "http://localhost:8080/api/v1/webhooks/payment"
	}

	return Config{
		Provider:      provider,
		WebhookSecret: secret,
		WebhookURL:    webhookURL,
	}, nil
}

func (c Config) Validate() error {
	if c.Provider == "" {
		return errors.New("payment: provider is required")
	}
	if c.WebhookSecret == "" {
		return errors.New("payment: webhook secret is required")
	}
	if c.WebhookURL == "" {
		return errors.New("payment: webhook url is required")
	}
	return nil
}
