package email_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/email"
)

func TestSMTPConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     email.SMTPConfig
		wantErr bool
		errSub  string
	}{
		{
			name: "valid config",
			cfg: email.SMTPConfig{
				Host: "127.0.0.1",
				Port: 1025,
				From: "noreply@ticketforge.local",
			},
			wantErr: false,
		},
		{
			name: "valid config with auth",
			cfg: email.SMTPConfig{
				Host:     "smtp.resend.com",
				Port:     587,
				Username: "resend",
				Password: "re_secret_password",
				From:     "TicketForge <support@ticketforge.com>",
			},
			wantErr: false,
		},
		{
			name: "empty host",
			cfg: email.SMTPConfig{
				Host: "   ",
				Port: 1025,
				From: "noreply@ticketforge.local",
			},
			wantErr: true,
			errSub:  "host cannot be empty",
		},
		{
			name: "invalid port zero",
			cfg: email.SMTPConfig{
				Host: "127.0.0.1",
				Port: 0,
				From: "noreply@ticketforge.local",
			},
			wantErr: true,
			errSub:  "invalid port",
		},
		{
			name: "invalid port too high",
			cfg: email.SMTPConfig{
				Host: "127.0.0.1",
				Port: 70000,
				From: "noreply@ticketforge.local",
			},
			wantErr: true,
			errSub:  "invalid port",
		},
		{
			name: "empty from",
			cfg: email.SMTPConfig{
				Host: "127.0.0.1",
				Port: 1025,
				From: "   ",
			},
			wantErr: true,
			errSub:  "from address cannot be empty",
		},
		{
			name: "from address contains CRLF",
			cfg: email.SMTPConfig{
				Host: "127.0.0.1",
				Port: 1025,
				From: "noreply@ticketforge.local\r\nBcc: evil@example.com",
			},
			wantErr: true,
			errSub:  "from address contains newline characters",
		},
		{
			name: "invalid from address format",
			cfg: email.SMTPConfig{
				Host: "127.0.0.1",
				Port: 1025,
				From: "not-a-valid-email",
			},
			wantErr: true,
			errSub:  "invalid from address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errSub)
				}
				if !errors.Is(err, email.ErrInvalidConfig) {
					t.Errorf("expected error wrapping ErrInvalidConfig, got: %v", err)
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

func TestSMTPConfigFromEnv(t *testing.T) {
	t.Setenv("SMTP_HOST", "mail.example.internal")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("SMTP_USERNAME", "mailer_user")
	t.Setenv("SMTP_PASSWORD", "mailer_pass")
	t.Setenv("SMTP_FROM", "system@example.internal")

	cfg := email.SMTPConfigFromEnv()

	if cfg.Host != "mail.example.internal" {
		t.Errorf("expected host mail.example.internal, got: %s", cfg.Host)
	}
	if cfg.Port != 2525 {
		t.Errorf("expected port 2525, got: %d", cfg.Port)
	}
	if cfg.Username != "mailer_user" {
		t.Errorf("expected username mailer_user, got: %s", cfg.Username)
	}
	if cfg.Password != "mailer_pass" {
		t.Errorf("expected password mailer_pass, got: %s", cfg.Password)
	}
	if cfg.From != "system@example.internal" {
		t.Errorf("expected from system@example.internal, got: %s", cfg.From)
	}
}

// mockSMTPServer sets up a lightweight test SMTP server.
type mockSMTPServer struct {
	listener net.Listener
	port     int
	mu       sync.Mutex
	commands []string
	mailFrom string
	rcptTo   []string
	data     string
}

func startMockSMTPServer(t *testing.T, handler func(tp *textproto.Conn, s *mockSMTPServer) error) *mockSMTPServer {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock smtp server: %v", err)
	}

	addr := l.Addr().(*net.TCPAddr)
	server := &mockSMTPServer{
		listener: l,
		port:     addr.Port,
	}

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				tp := textproto.NewConn(c)
				_ = handler(tp, server)
			}(conn)
		}
	}()

	t.Cleanup(func() {
		_ = l.Close()
	})

	return server
}

func standardMockSMTPHandler(tp *textproto.Conn, s *mockSMTPServer) error {
	if err := tp.Writer.PrintfLine("220 mock-smtp TicketForge Service ready"); err != nil {
		return err
	}

	for {
		line, err := tp.ReadLine()
		if err != nil {
			return err
		}

		s.mu.Lock()
		s.commands = append(s.commands, line)
		s.mu.Unlock()

		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
			_ = tp.Writer.PrintfLine("250-mock-smtp\r\n250 HELP")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			s.mu.Lock()
			s.mailFrom = strings.Trim(line[10:], "<> ")
			s.mu.Unlock()
			_ = tp.Writer.PrintfLine("250 2.1.0 Ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			s.mu.Lock()
			s.rcptTo = append(s.rcptTo, strings.Trim(line[8:], "<> "))
			s.mu.Unlock()
			_ = tp.Writer.PrintfLine("250 2.1.5 Ok")
		case upper == "DATA":
			_ = tp.Writer.PrintfLine("354 End data with <CR><LF>.<CR><LF>")
			body, err := tp.ReadDotBytes()
			if err != nil {
				return err
			}
			s.mu.Lock()
			s.data = string(body)
			s.mu.Unlock()
			_ = tp.Writer.PrintfLine("250 2.0.0 Ok: queued")
		case upper == "QUIT":
			_ = tp.Writer.PrintfLine("221 2.0.0 Bye")
			return nil
		default:
			_ = tp.Writer.PrintfLine("502 5.5.1 Command not recognized")
		}
	}
}

func TestSMTPSender_Send_MockServer(t *testing.T) {
	server := startMockSMTPServer(t, standardMockSMTPHandler)

	cfg := email.SMTPConfig{
		Host: "127.0.0.1",
		Port: server.port,
		From: "noreply@ticketforge.local",
	}

	sender, err := email.NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("failed to create SMTPSender: %v", err)
	}

	msg := email.Message{
		From:     "Alerts <alerts@ticketforge.local>",
		To:       []string{"alice@example.com", "Bob <bob@example.com>"},
		Subject:  "Server Alert",
		TextBody: "Everything is nominal.",
		HTMLBody: "<p>Everything is nominal.</p>",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sender.Send(ctx, msg); err != nil {
		t.Fatalf("sender.Send failed: %v", err)
	}

	server.mu.Lock()
	defer server.mu.Unlock()

	if server.mailFrom != "alerts@ticketforge.local" {
		t.Errorf("expected mailFrom alerts@ticketforge.local, got: %s", server.mailFrom)
	}
	if len(server.rcptTo) != 2 {
		t.Fatalf("expected 2 recipients, got %d: %v", len(server.rcptTo), server.rcptTo)
	}
	if server.rcptTo[0] != "alice@example.com" || server.rcptTo[1] != "bob@example.com" {
		t.Errorf("unexpected recipients: %v", server.rcptTo)
	}
	if !strings.Contains(server.data, "Subject: Server Alert") {
		t.Errorf("expected data to contain subject, got: %s", server.data)
	}
	if !strings.Contains(server.data, "Everything is nominal.") {
		t.Errorf("expected data to contain text body, got: %s", server.data)
	}
}

func TestSMTPSender_Send_ContextCancellation(t *testing.T) {
	// Mock server that stalls on connection
	server := startMockSMTPServer(t, func(tp *textproto.Conn, s *mockSMTPServer) error {
		time.Sleep(2 * time.Second)
		return nil
	})

	cfg := email.SMTPConfig{
		Host: "127.0.0.1",
		Port: server.port,
		From: "noreply@ticketforge.local",
	}

	sender, err := email.NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("failed to create SMTPSender: %v", err)
	}

	msg := email.Message{
		To:       []string{"user@example.com"},
		Subject:  "Timeout Test",
		TextBody: "This should time out.",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = sender.Send(ctx, msg)
	if err == nil {
		t.Fatal("expected Send to fail due to context timeout, but succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("expected context deadline exceeded error, got: %v", err)
	}
}

func TestSMTPSender_Send_ServerError(t *testing.T) {
	// Mock server that rejects recipient
	server := startMockSMTPServer(t, func(tp *textproto.Conn, s *mockSMTPServer) error {
		_ = tp.Writer.PrintfLine("220 mock-smtp Error Test")
		for {
			line, err := tp.ReadLine()
			if err != nil {
				return err
			}
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
				_ = tp.Writer.PrintfLine("250 Ok")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				_ = tp.Writer.PrintfLine("250 Ok")
			case strings.HasPrefix(upper, "RCPT TO:"):
				_ = tp.Writer.PrintfLine("550 5.1.1 User mailbox not found")
			case upper == "QUIT":
				_ = tp.Writer.PrintfLine("221 Bye")
				return nil
			default:
				_ = tp.Writer.PrintfLine("500 Syntax error")
			}
		}
	})

	cfg := email.SMTPConfig{
		Host: "127.0.0.1",
		Port: server.port,
		From: "noreply@ticketforge.local",
	}

	sender, err := email.NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("failed to create SMTPSender: %v", err)
	}

	msg := email.Message{
		To:       []string{"nonexistent@example.com"},
		Subject:  "Error Test",
		TextBody: "Hello",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err = sender.Send(ctx, msg)
	if err == nil {
		t.Fatal("expected Send to fail due to 550 recipient error, but succeeded")
	}
	if !strings.Contains(err.Error(), "550") {
		t.Errorf("expected error message to contain 550, got: %v", err)
	}
}

// TestMailpitIntegration is an opt-in integration test that sends a real email to
// Mailpit running at 127.0.0.1:1025, and verifies its receipt via Mailpit's REST API at 127.0.0.1:8025.
func TestMailpitIntegration(t *testing.T) {
	if os.Getenv("TEST_MAILPIT_INTEGRATION") != "1" && os.Getenv("TEST_MAILPIT_INTEGRATION") != "true" {
		t.Skip("skipping Mailpit integration test: set TEST_MAILPIT_INTEGRATION=1 to run")
	}

	config.Load("../../.env")
	cfg := email.SMTPConfigFromEnv()

	sender, err := email.NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("failed to create SMTPSender with env config: %v", err)
	}

	uniqueTimestamp := time.Now().Format("2006-01-02 15:04:05.000")
	testSubject := fmt.Sprintf("TicketForge Mailpit Integration Test — %s", uniqueTimestamp)
	testRecipient := "integration-tester@ticketforge.local"

	msg := email.Message{
		From:     "TicketForge <noreply@ticketforge.local>",
		To:       []string{testRecipient},
		Subject:  testSubject,
		TextBody: fmt.Sprintf("Hello!\n\nThis is a verification email from TicketForge integration test dispatched at %s.\n\nAll systems operational.", uniqueTimestamp),
		HTMLBody: fmt.Sprintf("<h2>TicketForge Mailpit Integration Test</h2><p>Dispatched at <strong>%s</strong>.</p><p>Status: <em>Operational</em></p>", uniqueTimestamp),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sender.Send(ctx, msg); err != nil {
		t.Fatalf("failed to send message to Mailpit: %v", err)
	}

	t.Logf("Successfully dispatched test email to Mailpit SMTP (%s:%d)", cfg.Host, cfg.Port)

	// Verify message in Mailpit via its REST API (http://127.0.0.1:8025/api/v1/messages)
	mailpitHTTP := fmt.Sprintf("http://%s:8025/api/v1/messages", cfg.Host)
	resp, err := http.Get(mailpitHTTP)
	if err != nil {
		t.Logf("Note: could not query Mailpit HTTP API at %s: %v (inspect manually at http://localhost:8025)", mailpitHTTP, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Logf("Mailpit HTTP API returned status %d", resp.StatusCode)
		return
	}

	var result struct {
		Messages []struct {
			ID      string `json:"ID"`
			Subject string `json:"Subject"`
			To      []struct {
				Address string `json:"Address"`
			} `json:"To"`
		} `json:"messages"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Logf("failed to decode Mailpit messages JSON: %v", err)
		return
	}

	found := false
	for _, m := range result.Messages {
		if m.Subject == testSubject {
			found = true
			t.Logf("Confirmed email in Mailpit! Message ID: %s, Subject: %q", m.ID, m.Subject)
			break
		}
	}

	if !found {
		t.Errorf("email with subject %q was sent to Mailpit SMTP, but was not found in Mailpit API messages", testSubject)
	}
}
