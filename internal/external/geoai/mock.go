package geoai

import (
	"context"
	"log/slog"
	"math"
	"strings"
)

// MockClient implements Client for local development.
// Toxicity keyword matching selects risk radius.
type MockClient struct{}

func (m *MockClient) Assess(ctx context.Context, req Request) (*Result, error) {
	slog.Info("[MOCK GeoAI] assessing risk",
		"product", req.Product.CommercialName,
		"bee_toxicity", req.Product.BeeToxicity,
		"area_ha", req.AreaHa,
		"center", req.Center)

	radius := selectRadius(req.Product.BeeToxicity, req.Product.CommercialName)
	radiusKm := radius / 1000.0
	area := math.Pi * radiusKm * radiusKm

	severity := "low"
	if radius >= 3000 {
		severity = "high"
	} else if radius >= 1500 {
		severity = "medium"
	}

	return &Result{
		RiskRadiusM:    radius,
		AffectedAreaKm: area,
		WindDirDeg:     45.0,
		Severity:       severity,
	}, nil
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
