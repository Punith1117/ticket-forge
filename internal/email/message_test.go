package email_test

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/email"
)

func TestMessageValidate(t *testing.T) {
	tests := []struct {
		name    string
		msg     email.Message
		wantErr bool
		errSub  string
	}{
		{
			name: "valid text only",
			msg: email.Message{
				To:       []string{"user@example.com"},
				Subject:  "Welcome to TicketForge",
				TextBody: "Hello and welcome!",
			},
			wantErr: false,
		},
		{
			name: "valid html only",
			msg: email.Message{
				To:       []string{"user@example.com"},
				Subject:  "Welcome to TicketForge",
				HTMLBody: "<p>Hello and welcome!</p>",
			},
			wantErr: false,
		},
		{
			name: "valid multipart",
			msg: email.Message{
				From:     "support@ticketforge.local",
				To:       []string{"user1@example.com", "user2@example.com"},
				Subject:  "Verify your email",
				TextBody: "Please verify your email",
				HTMLBody: "<p>Please verify your email</p>",
			},
			wantErr: false,
		},
		{
			name: "missing recipients",
			msg: email.Message{
				Subject:  "Subject",
				TextBody: "Body",
			},
			wantErr: true,
			errSub:  "at least one recipient is required",
		},
		{
			name: "empty recipient in slice",
			msg: email.Message{
				To:       []string{"   "},
				Subject:  "Subject",
				TextBody: "Body",
			},
			wantErr: true,
			errSub:  "recipient address cannot be empty",
		},
		{
			name: "invalid recipient address format",
			msg: email.Message{
				To:       []string{"not-an-email"},
				Subject:  "Subject",
				TextBody: "Body",
			},
			wantErr: true,
			errSub:  "invalid recipient address",
		},
		{
			name: "recipient with CRLF header injection",
			msg: email.Message{
				To:       []string{"victim@example.com\r\nBcc: evil@example.com"},
				Subject:  "Subject",
				TextBody: "Body",
			},
			wantErr: true,
			errSub:  "recipient address contains newline characters",
		},
		{
			name: "recipient with LF header injection",
			msg: email.Message{
				To:       []string{"victim@example.com\nBcc: evil@example.com"},
				Subject:  "Subject",
				TextBody: "Body",
			},
			wantErr: true,
			errSub:  "recipient address contains newline characters",
		},
		{
			name: "empty subject",
			msg: email.Message{
				To:       []string{"user@example.com"},
				Subject:  "   ",
				TextBody: "Body",
			},
			wantErr: true,
			errSub:  "subject cannot be empty",
		},
		{
			name: "subject with CRLF injection",
			msg: email.Message{
				To:       []string{"user@example.com"},
				Subject:  "Hello\r\nInjected-Header: evil",
				TextBody: "Body",
			},
			wantErr: true,
			errSub:  "subject contains newline characters",
		},
		{
			name: "custom from with CRLF injection",
			msg: email.Message{
				From:     "admin@ticketforge.local\r\nInjected: evil",
				To:       []string{"user@example.com"},
				Subject:  "Subject",
				TextBody: "Body",
			},
			wantErr: true,
			errSub:  "from address contains newline characters",
		},
		{
			name: "custom from with invalid address",
			msg: email.Message{
				From:     "invalid-from-email",
				To:       []string{"user@example.com"},
				Subject:  "Subject",
				TextBody: "Body",
			},
			wantErr: true,
			errSub:  "invalid from address",
		},
		{
			name: "missing both text and html body",
			msg: email.Message{
				To:      []string{"user@example.com"},
				Subject: "Subject",
			},
			wantErr: true,
			errSub:  "at least one of plain text body or html body must be provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errSub)
				}
				if !errors.Is(err, email.ErrInvalidMessage) {
					t.Errorf("expected error wrapping ErrInvalidMessage, got: %v", err)
				}
				if !strings.Contains(err.Error(), tt.errSub) {
					t.Errorf("expected error to contain %q, got: %v", tt.errSub, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBuildMIME_PlainText(t *testing.T) {
	msg := email.Message{
		From:     "TicketForge Support <support@ticketforge.local>",
		To:       []string{"Customer <customer@example.com>"},
		Subject:  "Account Notification",
		TextBody: "Hello,\n\nYour account has been updated.\n\nBest regards,\nTicketForge Team",
	}

	raw, err := msg.BuildMIME("fallback@ticketforge.local")
	if err != nil {
		t.Fatalf("BuildMIME failed: %v", err)
	}

	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parsing generated MIME: %v", err)
	}

	if parsed.Header.Get("Subject") != "Account Notification" {
		t.Errorf("unexpected Subject: %s", parsed.Header.Get("Subject"))
	}
	fromAddr, err := mail.ParseAddress(parsed.Header.Get("From"))
	if err != nil {
		t.Fatalf("parsing From header: %v", err)
	}
	if fromAddr.Name != "TicketForge Support" || fromAddr.Address != "support@ticketforge.local" {
		t.Errorf("unexpected From: name=%q, addr=%q", fromAddr.Name, fromAddr.Address)
	}

	toAddr, err := mail.ParseAddress(parsed.Header.Get("To"))
	if err != nil {
		t.Fatalf("parsing To header: %v", err)
	}
	if toAddr.Name != "Customer" || toAddr.Address != "customer@example.com" {
		t.Errorf("unexpected To: name=%q, addr=%q", toAddr.Name, toAddr.Address)
	}
	if parsed.Header.Get("MIME-Version") != "1.0" {
		t.Errorf("unexpected MIME-Version: %s", parsed.Header.Get("MIME-Version"))
	}
	if !strings.HasPrefix(parsed.Header.Get("Content-Type"), "text/plain; charset=UTF-8") {
		t.Errorf("unexpected Content-Type: %s", parsed.Header.Get("Content-Type"))
	}
	if parsed.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Errorf("unexpected Content-Transfer-Encoding: %s", parsed.Header.Get("Content-Transfer-Encoding"))
	}
	if parsed.Header.Get("Date") == "" {
		t.Error("missing Date header")
	}
	if parsed.Header.Get("Message-ID") == "" {
		t.Error("missing Message-ID header")
	}

	body, err := io.ReadAll(parsed.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if !strings.Contains(string(body), "Your account has been updated.") {
		t.Errorf("expected body to contain text, got: %s", string(body))
	}
}

func TestBuildMIME_HTML(t *testing.T) {
	msg := email.Message{
		To:       []string{"user@example.com"},
		Subject:  "HTML Notification",
		HTMLBody: "<h1>Hello</h1><p>Your booking is confirmed.</p>",
	}

	raw, err := msg.BuildMIME("noreply@ticketforge.local")
	if err != nil {
		t.Fatalf("BuildMIME failed: %v", err)
	}

	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parsing generated MIME: %v", err)
	}

	if !strings.HasPrefix(parsed.Header.Get("Content-Type"), "text/html; charset=UTF-8") {
		t.Errorf("unexpected Content-Type: %s", parsed.Header.Get("Content-Type"))
	}

	body, err := io.ReadAll(parsed.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if !strings.Contains(string(body), "<h1>Hello</h1>") {
		t.Errorf("expected body to contain HTML, got: %s", string(body))
	}
}

func TestBuildMIME_MultipartAlternative(t *testing.T) {
	msg := email.Message{
		To:       []string{"user@example.com"},
		Subject:  "Multipart Test",
		TextBody: "Plain text version of message.",
		HTMLBody: "<p>HTML version of message.</p>",
	}

	raw, err := msg.BuildMIME("noreply@ticketforge.local")
	if err != nil {
		t.Fatalf("BuildMIME failed: %v", err)
	}

	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parsing generated MIME: %v", err)
	}

	ct := parsed.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/alternative;") {
		t.Fatalf("expected multipart/alternative, got: %s", ct)
	}

	// Extract boundary parameter
	parts := strings.Split(ct, "boundary=")
	if len(parts) != 2 {
		t.Fatalf("failed to extract boundary from Content-Type: %s", ct)
	}
	boundary := strings.Trim(parts[1], "\"")

	mr := multipart.NewReader(parsed.Body, boundary)

	// Part 1: Text
	p1, err := mr.NextPart()
	if err != nil {
		t.Fatalf("reading part 1: %v", err)
	}
	if !strings.HasPrefix(p1.Header.Get("Content-Type"), "text/plain; charset=UTF-8") {
		t.Errorf("part 1 unexpected Content-Type: %s", p1.Header.Get("Content-Type"))
	}
	b1, err := io.ReadAll(p1)
	if err != nil {
		t.Fatalf("reading part 1 content: %v", err)
	}
	if !strings.Contains(string(b1), "Plain text version") {
		t.Errorf("part 1 content mismatch: %s", string(b1))
	}

	// Part 2: HTML
	p2, err := mr.NextPart()
	if err != nil {
		t.Fatalf("reading part 2: %v", err)
	}
	if !strings.HasPrefix(p2.Header.Get("Content-Type"), "text/html; charset=UTF-8") {
		t.Errorf("part 2 unexpected Content-Type: %s", p2.Header.Get("Content-Type"))
	}
	b2, err := io.ReadAll(p2)
	if err != nil {
		t.Fatalf("reading part 2 content: %v", err)
	}
	if !strings.Contains(string(b2), "<p>HTML version") {
		t.Errorf("part 2 content mismatch: %s", string(b2))
	}

	// End of multipart
	_, err = mr.NextPart()
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected EOF after 2 parts, got: %v", err)
	}
}

func TestBuildMIME_SubjectEncoding(t *testing.T) {
	unicodeSubject := "TicketForge — Confirm Your Email: 🎟️"
	msg := email.Message{
		To:       []string{"user@example.com"},
		Subject:  unicodeSubject,
		TextBody: "Welcome!",
	}

	raw, err := msg.BuildMIME("noreply@ticketforge.local")
	if err != nil {
		t.Fatalf("BuildMIME failed: %v", err)
	}

	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parsing generated MIME: %v", err)
	}

	encodedSubject := parsed.Header.Get("Subject")
	if !strings.HasPrefix(encodedSubject, "=?utf-8?q?") {
		t.Errorf("expected RFC 2047 Q-encoded subject, got: %s", encodedSubject)
	}

	dec := new(mime.WordDecoder)
	decoded, err := dec.DecodeHeader(encodedSubject)
	if err != nil {
		t.Fatalf("decoding subject: %v", err)
	}
	if decoded != unicodeSubject {
		t.Errorf("expected decoded subject %q, got %q", unicodeSubject, decoded)
	}
}
