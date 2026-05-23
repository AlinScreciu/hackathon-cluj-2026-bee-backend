package elevenlabs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/radarul-albinelor/api/internal/storage"
)

type Client struct {
	apiKey        string
	voiceID       string
	agentID       string
	phoneNumberID string
	httpClient    *http.Client

	store     storage.Storage
	sf        singleflight.Group
	knownKeys sync.Map // string -> struct{} — keys confirmed stored in this process
}

func NewClient(apiKey, voiceID string, store storage.Storage) *Client {
	return &Client{
		apiKey:     apiKey,
		voiceID:    voiceID,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		store:      store,
	}
}

func (c *Client) WithOutboundCall(agentID, phoneNumberID string) *Client {
	c.agentID = agentID
	c.phoneNumberID = phoneNumberID
	return c
}

// signedURLTTL is how long an audio URL handed to Twilio remains valid.
// Twilio fetches it within seconds of receiving the TwiML, so any value
// over a few minutes is plenty; 1 hour absorbs clock skew and retries.
const signedURLTTL = time.Hour

// TextToSpeechURL returns a (potentially short-lived signed) URL pointing
// at the audio for `text`, generating + storing it via the configured
// Storage if absent. Concurrent callers for the same text share a single
// generation via singleflight. Once a key is confirmed stored in this
// process, subsequent calls skip the Exists probe but still mint a fresh
// signed URL — so R2 can stay private.
func (c *Client) TextToSpeechURL(ctx context.Context, text string) (string, error) {
	key := c.cacheKey(text)

	if _, ok := c.knownKeys.Load(key); ok {
		return c.store.SignedURL(ctx, key, signedURLTTL)
	}

	_, err, _ := c.sf.Do(key, func() (any, error) {
		if exists, err := c.store.Exists(ctx, key); err == nil && exists {
			c.knownKeys.Store(key, struct{}{})
			return nil, nil
		} else if err != nil {
			slog.Warn("elevenlabs: storage exists probe failed", "err", err, "key", key)
		}

		mp3, err := c.fetchTTS(ctx, text)
		if err != nil {
			return nil, err
		}
		if err := c.store.Put(ctx, key, mp3, "audio/mpeg"); err != nil {
			return nil, fmt.Errorf("elevenlabs: store put: %w", err)
		}
		c.knownKeys.Store(key, struct{}{})
		slog.Info("elevenlabs: TTS generated + stored", "key", key, "bytes", len(mp3))
		return nil, nil
	})
	if err != nil {
		return "", err
	}
	return c.store.SignedURL(ctx, key, signedURLTTL)
}

// Prewarm asynchronously ensures each text is stored, so the first runtime
// caller pays neither ElevenLabs latency nor cost. Panics are recovered.
func (c *Client) Prewarm(ctx context.Context, texts []string) {
	for _, t := range texts {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("elevenlabs prewarm panic", "recover", r)
				}
			}()
			pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if _, err := c.TextToSpeechURL(pctx, t); err != nil {
				slog.Warn("elevenlabs prewarm failed", "err", err)
			}
		}()
	}
}

func (c *Client) cacheKey(text string) string {
	sum := sha256.Sum256([]byte(c.voiceID + ":" + text))
	return "voice/" + hex.EncodeToString(sum[:]) + ".mp3"
}

func (c *Client) fetchTTS(ctx context.Context, text string) ([]byte, error) {
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
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("elevenlabs: status %d: %s", resp.StatusCode, respBody)
	}

	mp3, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: read body: %w", err)
	}
	return mp3, nil
}

// OutboundCall initiates an outbound voice call via ElevenLabs Conversational AI
// using the configured SIP trunk phone number. Returns the sip_call_id.
func (c *Client) OutboundCall(ctx context.Context, toNumber string) (string, error) {
	if c.agentID == "" || c.phoneNumberID == "" {
		return "", fmt.Errorf("elevenlabs: agent_id and phone_number_id required for outbound calls")
	}

	body, err := json.Marshal(map[string]any{
		"agent_id":              c.agentID,
		"agent_phone_number_id": c.phoneNumberID,
		"to_number":             toNumber,
	})
	if err != nil {
		return "", fmt.Errorf("elevenlabs: marshal outbound call body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.elevenlabs.io/v1/convai/sip-trunk/outbound-call", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("elevenlabs: build outbound call request: %w", err)
	}
	req.Header.Set("xi-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("elevenlabs: outbound call request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("elevenlabs: outbound call status %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Success   bool   `json:"success"`
		Message   string `json:"message"`
		SIPCallID string `json:"sip_call_id"`
		ConvID    string `json:"conversation_id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("elevenlabs: decode outbound call response: %w", err)
	}
	if !result.Success {
		return "", fmt.Errorf("elevenlabs: outbound call failed: %s", result.Message)
	}

	callID := result.SIPCallID
	if callID == "" {
		callID = result.ConvID
	}
	slog.Info("elevenlabs: outbound call initiated", "to", toNumber, "sip_call_id", callID)
	return callID, nil
}
