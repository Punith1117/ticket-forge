package email_test

import (
	"strings"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/email"
)

func TestRegistrationVerificationEmail(t *testing.T) {
	from := "noreply@ticketforge.local"
	recipient := "newuser@example.com"
	verifyURL := "https://ticketforge.example.com/verify-email?token=xyz123"

	msg := email.RegistrationVerificationEmail(from, recipient, verifyURL)

	if err := msg.Validate(); err != nil {
		t.Fatalf("RegistrationVerificationEmail produced invalid message: %v", err)
	}

	if msg.Subject != "Verify your email address for TicketForge" {
		t.Errorf("unexpected subject: %s", msg.Subject)
	}
	if len(msg.To) != 1 || msg.To[0] != recipient {
		t.Errorf("unexpected recipient: %v", msg.To)
	}
	if msg.From != from {
		t.Errorf("unexpected from: %s", msg.From)
	}
	if !strings.Contains(msg.TextBody, verifyURL) {
		t.Errorf("text body does not contain verification URL: %s", msg.TextBody)
	}
	if !strings.Contains(msg.HTMLBody, verifyURL) {
		t.Errorf("html body does not contain verification URL: %s", msg.HTMLBody)
	}
}

func TestPasswordResetEmail(t *testing.T) {
	from := "noreply@ticketforge.local"
	recipient := "resetuser@example.com"
	resetURL := "https://ticketforge.example.com/reset-password?token=abc456"

	msg := email.PasswordResetEmail(from, recipient, resetURL)

	if err := msg.Validate(); err != nil {
		t.Fatalf("PasswordResetEmail produced invalid message: %v", err)
	}

	if msg.Subject != "Reset your TicketForge password" {
		t.Errorf("unexpected subject: %s", msg.Subject)
	}
	if len(msg.To) != 1 || msg.To[0] != recipient {
		t.Errorf("unexpected recipient: %v", msg.To)
	}
	if msg.From != from {
		t.Errorf("unexpected from: %s", msg.From)
	}
	if !strings.Contains(msg.TextBody, resetURL) {
		t.Errorf("text body does not contain reset URL: %s", msg.TextBody)
	}
	if !strings.Contains(msg.HTMLBody, resetURL) {
		t.Errorf("html body does not contain reset URL: %s", msg.HTMLBody)
	}
}
