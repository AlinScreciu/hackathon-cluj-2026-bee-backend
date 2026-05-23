package elevenlabs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type Client struct {
	apiKey     string
	voiceID    string
	httpClient *http.Client
}

func NewClient(apiKey, voiceID string) *Client {
	return &Client{
		apiKey:     apiKey,
		voiceID:    voiceID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) TextToSpeech(ctx context.Context, text string) ([]byte, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
	cachePath := filepath.Join("uploads", "voice", hash+".mp3")

	if data, err := os.ReadFile(cachePath); err == nil {
		return data, nil
	}

	body, err := json.Marshal(map[string]any{
		"text":     text,
		"model_id": "eleven_multilingual_v2",
		"voice_settings": map[string]any{
			"stability":        0.5,
			"similarity_boost": 0.75,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.elevenlabs.io/v1/text-to-speech/"+c.voiceID, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: build request: %w", err)
	}
	req.Header.Set("xi-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("elevenlabs: status %d", resp.StatusCode)
	}

	mp3, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: read body: %w", err)
	}

	if err := os.WriteFile(cachePath, mp3, 0644); err != nil {
		slog.Warn("elevenlabs: failed to cache mp3", "path", cachePath, "err", err)
	}

	return mp3, nil
}

func (c *Client) FileURL(baseURL, text string) string {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
	return baseURL + "/uploads/voice/" + hash + ".mp3"
}
