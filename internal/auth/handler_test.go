package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/security"
)

type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func newTestEncrypter(t *testing.T) *security.TokenEncrypter {
	t.Helper()
	hexKey, err := security.GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	rawKey, err := security.ParseKey(hexKey)
	if err != nil {
		t.Fatalf("failed to parse key: %v", err)
	}
	ring, err := security.NewKeyRing(1, map[int][]byte{1: rawKey})
	if err != nil {
		t.Fatalf("failed to create keyring: %v", err)
	}
	encrypter, err := security.NewTokenEncrypter(ring)
	if err != nil {
		t.Fatalf("failed to create encrypter: %v", err)
	}
	return encrypter
}

func TestHandler_HandleRegister_InvalidJSON(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewBufferString("{invalid-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleRegister(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rec.Code)
	}

	var errResp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Error.Code != "INVALID_REQUEST" {
		t.Errorf("expected error 'INVALID_REQUEST', got '%s'", errResp.Error)
	}
}

func TestHandler_HandleRegister_UnknownFieldsDisallowed(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)

	body := `{"email":"test@example.com","unknown_field":"malicious"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleRegister(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for unknown fields, got %d", rec.Code)
	}
}

func TestHandler_HandleRegister_BodySizeLimit(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)

	hugeEmail := strings.Repeat("a", 70*1024) + "@example.com"
	body, _ := json.Marshal(auth.RegisterRequest{Email: hugeEmail})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.HandleRegister(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for payload exceeding size limit, got %d", rec.Code)
	}
}

func TestHandler_HandleRegisterComplete_InvalidPassword(t *testing.T) {
	// A dummy service instance is fine since password validation happens before DB lookup
	service := &auth.Service{}
	handler := auth.NewHandler(service, nil, false)

	body, _ := json.Marshal(auth.RegisterCompleteRequest{
		Token:    "some-token",
		Password: "short", // Only 5 chars, requires >= 12
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/complete", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.HandleRegisterComplete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for short password, got %d", rec.Code)
	}

	var errResp errorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Error.Code != "INVALID_PASSWORD" {
		t.Errorf("expected error 'INVALID_PASSWORD', got '%s'", errResp.Error)
	}
}

func TestHandler_HandleRegisterComplete_EmptyToken(t *testing.T) {
	service := &auth.Service{}
	handler := auth.NewHandler(service, nil, false)

	body, _ := json.Marshal(auth.RegisterCompleteRequest{
		Token:    "   ",
		Password: "valid-password-12345",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/complete", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.HandleRegisterComplete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for empty token, got %d", rec.Code)
	}

	var errResp errorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Error.Code != "INVALID_TOKEN" {
		t.Errorf("expected error 'INVALID_TOKEN', got '%s'", errResp.Error)
	}
}

func TestNormalizeEmail(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
		valid    bool
	}{
		{"standard email", "user@example.com", "user@example.com", true},
		{"uppercase and whitespace", "  User.Name@Example.COM  ", "user.name@example.com", true},
		{"subdomain email", "user@mail.ticketforge.com", "user@mail.ticketforge.com", true},
		{"empty string", "", "", false},
		{"whitespace only", "   ", "", false},
		{"missing at symbol", "userexample.com", "", false},
		{"missing domain", "user@", "", false},
		{"missing local part", "@example.com", "", false},
		{"missing domain dot", "user@localhost", "", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			normalized, err := auth.NormalizeEmail(tc.input)
			if tc.valid {
				if err != nil {
					t.Errorf("expected valid email, got error: %v", err)
				}
				if normalized != tc.expected {
					t.Errorf("expected %q, got %q", tc.expected, normalized)
				}
			} else {
				if err == nil {
					t.Errorf("expected error for invalid email %q, got nil", tc.input)
				}
			}
		})
	}
}
