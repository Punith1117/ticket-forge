package simulator

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

const (
	HeaderSignature = "X-TicketForge-Signature"
	HeaderTimestamp = "X-TicketForge-Timestamp"

	DefaultTolerance = 5 * time.Minute
)

var (
	ErrMissingSignature = errors.New("simulator: missing signature header")
	ErrMissingTimestamp = errors.New("simulator: missing timestamp header")
	ErrInvalidTimestamp = errors.New("simulator: invalid timestamp format")
	ErrStaleTimestamp   = errors.New("simulator: webhook timestamp outside acceptable tolerance")
	ErrInvalidSignature = errors.New("simulator: signature mismatch")
)

// ComputeSignature calculates the HMAC-SHA256 hex signature over "<timestamp>.<payload>".
func ComputeSignature(payload []byte, timestampStr string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestampStr))
	mac.Write([]byte("."))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature validates that the provided signature matches the recomputed HMAC-SHA256,
// and that the timestamp is within the acceptable tolerance window.
func VerifySignature(
	payload []byte,
	signatureHex string,
	timestampStr string,
	secret []byte,
	tolerance time.Duration,
	now time.Time,
) error {
	if signatureHex == "" {
		return ErrMissingSignature
	}
	if timestampStr == "" {
		return ErrMissingTimestamp
	}

	tsUnix, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return ErrInvalidTimestamp
	}

	ts := time.Unix(tsUnix, 0).UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	diff := now.Sub(ts)
	if diff < 0 {
		diff = -diff
	}
	if tolerance > 0 && diff > tolerance {
		return ErrStaleTimestamp
	}

	expectedHex := ComputeSignature(payload, timestampStr, secret)
	expectedBytes, err := hex.DecodeString(expectedHex)
	if err != nil {
		return ErrInvalidSignature
	}

	actualBytes, err := hex.DecodeString(signatureHex)
	if err != nil {
		return ErrInvalidSignature
	}

	if !hmac.Equal(expectedBytes, actualBytes) {
		return ErrInvalidSignature
	}

	return nil
}
