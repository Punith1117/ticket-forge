package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client sends signed webhook HTTP requests to TicketForge's webhook endpoint.
type Client struct {
	httpClient *http.Client
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}
	return &Client{httpClient: httpClient}
}

// SendWebhook serializes the payload, generates HMAC signature headers, and POSTs to targetURL.
func (c *Client) SendWebhook(
	ctx context.Context,
	targetURL string,
	payload WebhookPayload,
	secret []byte,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("simulator: marshal webhook body: %w", err)
	}

	tsStr := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	sigHex := ComputeSignature(body, tsStr, secret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("simulator: create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderSignature, sigHex)
	req.Header.Set(HeaderTimestamp, tsStr)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("simulator: send webhook request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("simulator: webhook endpoint returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
