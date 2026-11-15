package email

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalidConfig indicates that the SMTP configuration is invalid.
	ErrInvalidConfig = errors.New("email: invalid smtp configuration")
)

// SMTPConfig holds configuration for the SMTP email transport.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// SMTPConfigFromEnv reads SMTP configuration from environment variables,
// falling back to local Mailpit defaults.
func SMTPConfigFromEnv() SMTPConfig {
	port := 1025
	if p := os.Getenv("SMTP_PORT"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			port = parsed
		}
	}

	return SMTPConfig{
		Host:     getEnv("SMTP_HOST", "127.0.0.1"),
		Port:     port,
		Username: os.Getenv("SMTP_USERNAME"),
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     getEnv("SMTP_FROM", "noreply@ticketforge.local"),
	}
}

// Validate ensures the SMTP configuration has a valid host, port, and sender address.
func (c SMTPConfig) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("%w: host cannot be empty", ErrInvalidConfig)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("%w: invalid port %d, must be between 1 and 65535", ErrInvalidConfig, c.Port)
	}
	if strings.TrimSpace(c.From) == "" {
		return fmt.Errorf("%w: from address cannot be empty", ErrInvalidConfig)
	}
	if strings.ContainsAny(c.From, "\r\n") {
		return fmt.Errorf("%w: from address contains newline characters", ErrInvalidConfig)
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return fmt.Errorf("%w: invalid from address %q: %v", ErrInvalidConfig, c.From, err)
	}
	return nil
}

// SMTPSender implements the Sender interface using SMTP.
type SMTPSender struct {
	cfg SMTPConfig
}

// NewSMTPSender constructs and validates a new SMTPSender.
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &SMTPSender{cfg: cfg}, nil
}

// Send dispatches an email message using SMTP, respecting context cancellation and deadlines.
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}

	effectiveFrom := strings.TrimSpace(msg.From)
	if effectiveFrom == "" {
		effectiveFrom = s.cfg.From
	}

	parsedFrom, err := mail.ParseAddress(effectiveFrom)
	if err != nil {
		return fmt.Errorf("parsing sender address: %w", err)
	}

	var recipients []string
	for _, to := range msg.To {
		parsedTo, err := mail.ParseAddress(strings.TrimSpace(to))
		if err != nil {
			return fmt.Errorf("parsing recipient address: %w", err)
		}
		recipients = append(recipients, parsedTo.Address)
	}

	rawMIME, err := msg.BuildMIME(s.cfg.From)
	if err != nil {
		return fmt.Errorf("building mime payload: %w", err)
	}

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))

	dialer := net.Dialer{
		Timeout: 10 * time.Second,
	}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("connecting to smtp server %s: %w", addr, err)
	}

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("smtp handshake with %s: %w", addr, err)
	}
	defer client.Close()

	// Optional authentication if username is provided
	if s.cfg.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("smtp authentication on %s failed: %w", addr, err)
		}
	}

	if err := client.Mail(parsedFrom.Address); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("smtp MAIL FROM %s: %w", parsedFrom.Address, err)
	}

	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("smtp RCPT TO %s: %w", rcpt, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("smtp DATA command on %s: %w", addr, err)
	}

	if _, err := w.Write(rawMIME); err != nil {
		_ = w.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("writing email body: %w", err)
	}

	if err := w.Close(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("finalizing smtp data stream: %w", err)
	}

	_ = client.Quit()
	return nil
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
