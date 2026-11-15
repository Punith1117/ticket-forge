package email

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

var (
	// ErrInvalidMessage indicates that an email message failed validation.
	ErrInvalidMessage = errors.New("email: invalid message")
)

// Message represents an email message to be dispatched.
type Message struct {
	From     string   // Optional; if empty, sender default From address is used.
	To       []string // One or more recipient email addresses.
	Subject  string   // Subject line of the email.
	TextBody string   // Plain-text content.
	HTMLBody string   // HTML content.
}

// Validate checks whether the message contains all required fields and valid email addresses,
// and ensures no header injection characters are present.
func (m Message) Validate() error {
	if len(m.To) == 0 {
		return fmt.Errorf("%w: at least one recipient is required", ErrInvalidMessage)
	}

	for _, to := range m.To {
		trimmed := strings.TrimSpace(to)
		if trimmed == "" {
			return fmt.Errorf("%w: recipient address cannot be empty", ErrInvalidMessage)
		}
		if strings.ContainsAny(trimmed, "\r\n") {
			return fmt.Errorf("%w: recipient address contains newline characters: %q", ErrInvalidMessage, trimmed)
		}
		if _, err := mail.ParseAddress(trimmed); err != nil {
			return fmt.Errorf("%w: invalid recipient address %q: %v", ErrInvalidMessage, trimmed, err)
		}
	}

	trimmedSubject := strings.TrimSpace(m.Subject)
	if trimmedSubject == "" {
		return fmt.Errorf("%w: subject cannot be empty", ErrInvalidMessage)
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("%w: subject contains newline characters", ErrInvalidMessage)
	}

	if m.From != "" {
		trimmedFrom := strings.TrimSpace(m.From)
		if strings.ContainsAny(trimmedFrom, "\r\n") {
			return fmt.Errorf("%w: from address contains newline characters", ErrInvalidMessage)
		}
		if _, err := mail.ParseAddress(trimmedFrom); err != nil {
			return fmt.Errorf("%w: invalid from address %q: %v", ErrInvalidMessage, trimmedFrom, err)
		}
	}

	if strings.TrimSpace(m.TextBody) == "" && strings.TrimSpace(m.HTMLBody) == "" {
		return fmt.Errorf("%w: at least one of plain text body or html body must be provided", ErrInvalidMessage)
	}

	return nil
}

// BuildMIME constructs an RFC 5322 compliant MIME message payload.
func (m Message) BuildMIME(defaultFrom string) ([]byte, error) {
	effectiveFrom := strings.TrimSpace(m.From)
	if effectiveFrom == "" {
		effectiveFrom = strings.TrimSpace(defaultFrom)
	}
	if effectiveFrom == "" {
		return nil, fmt.Errorf("%w: sender from address cannot be empty", ErrInvalidMessage)
	}

	parsedFrom, err := mail.ParseAddress(effectiveFrom)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid from address %q: %v", ErrInvalidMessage, effectiveFrom, err)
	}

	var toHeaders []string
	for _, to := range m.To {
		parsedTo, err := mail.ParseAddress(strings.TrimSpace(to))
		if err != nil {
			return nil, fmt.Errorf("%w: invalid recipient address %q: %v", ErrInvalidMessage, to, err)
		}
		if parsedTo.Name != "" {
			toHeaders = append(toHeaders, parsedTo.String())
		} else {
			toHeaders = append(toHeaders, parsedTo.Address)
		}
	}

	fromHeader := parsedFrom.Address
	if parsedFrom.Name != "" {
		fromHeader = parsedFrom.String()
	}

	fromDomain := "ticketforge.local"
	if parts := strings.Split(parsedFrom.Address, "@"); len(parts) == 2 && parts[1] != "" {
		fromDomain = parts[1]
	}

	var buf bytes.Buffer

	// Standard RFC 5322 headers
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "From: %s\r\n", fromHeader)
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(toHeaders, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", encodeSubject(m.Subject))
	fmt.Fprintf(&buf, "Message-ID: <%s>\r\n", generateMessageID(fromDomain))
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")

	hasText := strings.TrimSpace(m.TextBody) != ""
	hasHTML := strings.TrimSpace(m.HTMLBody) != ""

	switch {
	case hasText && hasHTML:
		boundary := fmt.Sprintf("=_tf_%s", randomHex(16))
		fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)

		// Plain-text part
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: text/plain; charset=UTF-8\r\n")
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		if err := writeQuotedPrintable(&buf, m.TextBody); err != nil {
			return nil, err
		}
		fmt.Fprintf(&buf, "\r\n")

		// HTML part
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: text/html; charset=UTF-8\r\n")
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		if err := writeQuotedPrintable(&buf, m.HTMLBody); err != nil {
			return nil, err
		}
		fmt.Fprintf(&buf, "\r\n")

		// End boundary
		fmt.Fprintf(&buf, "--%s--\r\n", boundary)

	case hasHTML:
		fmt.Fprintf(&buf, "Content-Type: text/html; charset=UTF-8\r\n")
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		if err := writeQuotedPrintable(&buf, m.HTMLBody); err != nil {
			return nil, err
		}
		fmt.Fprintf(&buf, "\r\n")

	default: // hasText
		fmt.Fprintf(&buf, "Content-Type: text/plain; charset=UTF-8\r\n")
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		if err := writeQuotedPrintable(&buf, m.TextBody); err != nil {
			return nil, err
		}
		fmt.Fprintf(&buf, "\r\n")
	}

	return buf.Bytes(), nil
}

func encodeSubject(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < 32 || s[i] > 126 {
			return mime.QEncoding.Encode("utf-8", s)
		}
	}
	return s
}

func writeQuotedPrintable(w io.Writer, content string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(content)); err != nil {
		return err
	}
	return qp.Close()
}

func generateMessageID(domain string) string {
	return fmt.Sprintf("%d.%s@%s", time.Now().UnixNano(), randomHex(8), domain)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
