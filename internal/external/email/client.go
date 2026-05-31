// Package email wraps Resend's HTTPS API for outbound transactional mail.
//
// Why HTTPS not SMTP: Railway (and most modern PaaS providers) block outbound
// connections on ports 25/465/587 to prevent spam abuse. SMTP just times out.
// The Resend HTTPS endpoint at api.resend.com works over standard 443 like any
// other HTTP call.
package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type EmailClient struct {
	apiKey string
	from   string
	http   *http.Client
}

// NewClient constructs a Resend HTTPS client. apiKey is the RESEND_API_KEY
// (starts with "re_"). from must be an address on a verified Resend sender
// domain (e.g. "noreply@beelive.ro").
func NewClient(apiKey, from string) *EmailClient {
	return &EmailClient{
		apiKey: apiKey,
		from:   from,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

type sendRequest struct {
	From        string       `json:"from"`
	To          []string     `json:"to"`
	Subject     string       `json:"subject"`
	Text        string       `json:"text"`
	Attachments []attachment `json:"attachments,omitempty"`
}

// Attachment is a single email attachment, base64-encoded by Resend's API.
type Attachment struct {
	Filename string
	Content  []byte
}

type attachment struct {
	Filename string `json:"filename"`
	Content  string `json:"content"` // base64
}

type sendResponse struct {
	ID string `json:"id"`
}

type errorResponse struct {
	Name       string `json:"name"`
	Message    string `json:"message"`
	StatusCode int    `json:"statusCode"`
}

func (c *EmailClient) Send(ctx context.Context, to, subject, body string) error {
	return c.SendWithAttachments(ctx, to, subject, body, nil)
}

// SendWithAttachments posts an email through Resend with optional binary
// attachments (each base64-encoded as required by the Resend API).
func (c *EmailClient) SendWithAttachments(ctx context.Context, to, subject, body string, atts []Attachment) error {
	apiAtts := make([]attachment, 0, len(atts))
	for _, a := range atts {
		apiAtts = append(apiAtts, attachment{
			Filename: a.Filename,
			Content:  base64.StdEncoding.EncodeToString(a.Content),
		})
	}
	payload, err := json.Marshal(sendRequest{
		From:        c.from,
		To:          []string{to},
		Subject:     subject,
		Text:        body,
		Attachments: apiAtts,
	})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var errBody errorResponse
		_ = json.Unmarshal(respBody, &errBody)
		if errBody.Message != "" {
			return fmt.Errorf("resend %d %s: %s", resp.StatusCode, errBody.Name, errBody.Message)
		}
		return fmt.Errorf("resend %d: %s", resp.StatusCode, string(respBody))
	}

	var ok sendResponse
	if err := json.Unmarshal(respBody, &ok); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
