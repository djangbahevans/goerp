package emailprovider

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ResendBaseURL is Resend's production API.
const ResendBaseURL = "https://api.resend.com"

const ProviderResend = "resend"

// Resend sends through Resend's POST /emails.
type Resend struct {
	APIKey string
	// BaseURL defaults to ResendBaseURL.
	BaseURL string
	Client  *http.Client
}

func (r *Resend) Name() string { return ProviderResend }

type resendRequest struct {
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	HTML    string            `json:"html"`
	Text    string            `json:"text"`
	ReplyTo string            `json:"reply_to,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type resendResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// Send posts msg with its IdempotencyKey as Resend's Idempotency-Key, so a
// retry of a send Resend already accepted returns that send's ID instead
// of sending again. Resend answers a key reused with a different body with
// 409 invalid_idempotent_request, reported as ErrAlreadySent: that key's
// earlier send went out.
func (r *Resend) Send(ctx context.Context, msg Message) (string, error) {
	headers := map[string]string{"Message-ID": msg.MessageID}
	for _, h := range msg.Headers {
		headers[h.Name] = h.Value
	}
	body, err := json.Marshal(resendRequest{
		From: msg.from(), To: []string{msg.To}, Subject: msg.Subject,
		HTML: msg.HTML, Text: msg.Text, ReplyTo: msg.ReplyTo, Headers: headers,
	}, json.Deterministic(true))
	if err != nil {
		return "", fmt.Errorf("encode resend request: %w", err)
	}

	base := r.BaseURL
	if base == "" {
		base = ResendBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/emails", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if msg.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", msg.IdempotencyKey)
	}

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("resend request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	var out resendResponse
	_ = json.Unmarshal(raw, &out)
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if out.ID == "" {
			return "", fmt.Errorf("resend accepted the email without an id: %s", raw)
		}
		return out.ID, nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", fmt.Errorf("%w: resend %d %s: %s", ErrQuotaExceeded, resp.StatusCode, out.Name, out.Message)
	case resp.StatusCode == http.StatusConflict && out.Name == "invalid_idempotent_request":
		return "", fmt.Errorf("%w: resend %d %s: %s", ErrAlreadySent, resp.StatusCode, out.Name, out.Message)
	case resp.StatusCode == http.StatusConflict && out.Name == "concurrent_idempotent_requests":
		return "", fmt.Errorf("resend %d %s: %s", resp.StatusCode, out.Name, out.Message)
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return "", fmt.Errorf("%w: resend %d %s: %s", ErrPermanent, resp.StatusCode, out.Name, out.Message)
	default:
		return "", fmt.Errorf("resend %d: %s", resp.StatusCode, raw)
	}
}
