package geoai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
)

// MockClient implements Client for local development when GEO_AI_BASE_URL is
// not set. Returns plausible Romanian-language explanations + a GeoJSON
// FeatureCollection with concentric A1–A4 circles around the parcel so the
// frontend's risk panel looks real without the Python service running.
type MockClient struct{}

func (m *MockClient) Assess(ctx context.Context, req Request) (*Result, error) {
	slog.Info("[MOCK GeoAI] assessing risk",
		"product", req.Product.CommercialName,
		"bee_toxicity", req.Product.BeeToxicity,
		"dose_kg_ha", req.Dose.AmountPerHectareKg,
		"area_ha", req.AreaHa,
		"center", req.Center)

	radius := selectRadius(req.Product.BeeToxicity, req.Product.CommercialName)
	// Dose nudge: heavier dose pushes the affected radius outward a bit so the
	// preview reacts to the user's input even in mock mode.
	if req.Dose.AmountPerHectareKg > 1 {
		radius += math.Min(2000, (req.Dose.AmountPerHectareKg-1)*400)
	}
	radiusKm := radius / 1000.0
	area := math.Pi * radiusKm * radiusKm

	severity := "low"
	score := 0.25
	switch {
	case radius >= 3000:
		severity = "high"
		score = 0.85
	case radius >= 1500:
		severity = "medium"
		score = 0.55
	}

	zones := mockZones(req.Center.Lat, req.Center.Lon, radius)

	explanation := fmt.Sprintf(
		"Substanța %s la doza %.1f kg/ha pe %.1f ha generează risc %s pentru albine în raza de %.0f m. Vântul actual influențează direcția zonei afectate.",
		req.Product.CommercialName, req.Dose.AmountPerHectareKg, req.AreaHa, severityLabelRO(severity), radius,
	)

	recommended := "Aplicați spre seară (după 21:00) când albinele nu sunt active. Anunțați apicultorii cu cel puțin 48h înainte."
	warnings := []string{}
	if severity == "high" {
		warnings = append(warnings, "Toxicitate ridicată pentru albine — sunați apicultorii direct dacă este posibil.")
	}
	if req.Dose.AmountPerHectareKg > 2 {
		warnings = append(warnings, "Doza este peste limita uzuală recomandată.")
	}

	return &Result{
		RiskRadiusM:       radius,
		AffectedAreaKm:    area,
		WindDirDeg:        45.0,
		WindSpeedKmh:      8.5,
		Severity:          severity,
		RiskScore:         score,
		ExplanationRO:     explanation,
		RecommendedAction: recommended,
		Warnings:          warnings,
		Zones:             zones,
	}, nil
}

func severityLabelRO(s string) string {
	switch s {
	case "high", "very_high":
		return "ridicat"
	case "medium":
		return "moderat"
	default:
		return "scăzut"
	}
}

// selectRadius returns risk radius in meters.
// Prefers explicit beeToxicity; falls back to commercial name keyword matching.
func selectRadius(beeToxicity, commercialName string) float64 {
	switch beeToxicity {
	case "very_high", "high":
		return 3000
	case "medium":
		return 1500
	case "low":
		return 750
	}
	name := strings.ToLower(commercialName)
	for _, kw := range []string{"confidor", "actara", "bulldock"} {
		if strings.Contains(name, kw) {
			return 3000
		}
	}
	for _, kw := range []string{"mospilan", "karate", "calypso", "nurelle"} {
		if strings.Contains(name, kw) {
			return 1500
		}
	}
	return 750
}

// mockZones returns a GeoJSON FeatureCollection with up to four concentric
// circular polygons (A1=7km baseline + A2/A3/A4 sized by toxicity radius).
// A1 is always present; A2–A4 scale with the spray's risk radius so the
// preview visibly grows as the user picks a more toxic substance.
func mockZones(lat, lng, radiusM float64) json.RawMessage {
	type ring struct {
		id      string
		radiusM float64
		color   string
	}
	rings := []ring{
		{"A1", 7000, "#16A34A"},
		{"A2", math.Max(radiusM*0.5, 750), "#EEA727"},
		{"A3", math.Max(radiusM, 1500), "#F97316"},
		{"A4", math.Max(radiusM*1.5, 2500), "#DC2626"},
	}
	features := make([]map[string]any, 0, len(rings))
	for _, r := range rings {
		coords := circlePolygon(lat, lng, r.radiusM, 48)
		features = append(features, map[string]any{
			"type": "Feature",
			"properties": map[string]any{
				"id":         r.id,
				"radius_m":   r.radiusM,
				"color":      r.color,
				"label":      r.id,
			},
			"geometry": map[string]any{
				"type":        "Polygon",
				"coordinates": [][][]float64{coords},
			},
		})
	}
	collection := map[string]any{
		"type":     "FeatureCollection",
		"features": features,
	}
	b, _ := json.Marshal(collection)
	return b
}

// circlePolygon returns a closed ring of [lng, lat] pairs approximating a
// circle of `radiusM` meters around (lat, lng). `segments` controls smoothness.
func circlePolygon(lat, lng, radiusM float64, segments int) [][]float64 {
	const earthR = 6371000.0
	d := radiusM / earthR
	lat1 := lat * math.Pi / 180
	lng1 := lng * math.Pi / 180
	ring := make([][]float64, 0, segments+1)
	for i := 0; i <= segments; i++ {
		brng := float64(i) / float64(segments) * 2 * math.Pi
		lat2 := math.Asin(math.Sin(lat1)*math.Cos(d) + math.Cos(lat1)*math.Sin(d)*math.Cos(brng))
		lng2 := lng1 + math.Atan2(
			math.Sin(brng)*math.Sin(d)*math.Cos(lat1),
			math.Cos(d)-math.Sin(lat1)*math.Sin(lat2),
		)
		ring = append(ring, []float64{lng2 * 180 / math.Pi, lat2 * 180 / math.Pi})
	}
	return ring
}
