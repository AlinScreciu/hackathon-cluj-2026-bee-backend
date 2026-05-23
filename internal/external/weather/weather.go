package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Result struct {
	WindDirectionDeg float64
	WindSpeedMs      float64
	TemperatureC     float64
	FetchedAt        time.Time
}

func Fetch(ctx context.Context, lat, lng float64) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.6f&longitude=%.6f&current=wind_speed_10m,wind_direction_10m,temperature_2m&wind_speed_unit=ms",
		lat, lng,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw struct {
		Current struct {
			WindSpeed     float64 `json:"wind_speed_10m"`
			WindDirection float64 `json:"wind_direction_10m"`
			Temperature   float64 `json:"temperature_2m"`
		} `json:"current"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	return &Result{
		WindDirectionDeg: raw.Current.WindDirection,
		WindSpeedMs:      raw.Current.WindSpeed,
		TemperatureC:     raw.Current.Temperature,
		FetchedAt:        time.Now().UTC(),
	}, nil
}
