package geoai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// HTTPClient calls the real AI geo service over HTTP.
// Only used when GEO_AI_BASE_URL is set; MockClient is used in development.
type HTTPClient struct {
	BaseURL    string
	httpClient *http.Client
}

func newHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		BaseURL:    baseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *HTTPClient) Assess(ctx context.Context, req Request) (*Result, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/ai/risk-assess", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("geoai http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("geoai http status %d: %s", resp.StatusCode, body)
	}

	var apiResp struct {
		RiskScore                    float64         `json:"riskScore"`
		RiskLevel                    string          `json:"riskLevel"`
		Zones                        json.RawMessage `json:"zones"`
		NotifyBeekeepersWithinMeters float64         `json:"notifyBeekeepersWithinMeters"`
		WeatherUsed                  struct {
			WindSpeedKmh         float64 `json:"windSpeedKmh"`
			WindDirectionDegrees float64 `json:"windDirectionDegrees"`
		} `json:"weatherUsed"`
		Warnings          []string `json:"warnings"`
		ExplanationRo     string   `json:"explanationRo"`
		RecommendedAction string   `json:"recommendedAction"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("geoai decode: %w", err)
	}

	radiusKm := apiResp.NotifyBeekeepersWithinMeters / 1000.0
	return &Result{
		RiskRadiusM:       apiResp.NotifyBeekeepersWithinMeters,
		AffectedAreaKm:    math.Pi * radiusKm * radiusKm,
		WindDirDeg:        apiResp.WeatherUsed.WindDirectionDegrees,
		WindSpeedKmh:      apiResp.WeatherUsed.WindSpeedKmh,
		Severity:          apiResp.RiskLevel,
		RiskScore:         apiResp.RiskScore,
		ExplanationRO:     apiResp.ExplanationRo,
		RecommendedAction: apiResp.RecommendedAction,
		Warnings:          apiResp.Warnings,
		Zones:             apiResp.Zones,
	}, nil
}
