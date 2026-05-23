package geoai

import "context"

type Center struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Product struct {
	CommercialName  string `json:"commercialName"`
	ActiveSubstance string `json:"activeSubstance"`
	BeeToxicity     string `json:"beeToxicity,omitempty"` // "low"|"medium"|"high"|"very_high"
}

type Dose struct {
	AmountPerHectareKg float64 `json:"amountPerHectareKg"`
	TotalAmountKg      float64 `json:"totalAmountKg"`
}

// Request matches the POST /ai/risk-assess payload.
type Request struct {
	Crop              string  `json:"crop"`
	ParcelID          string  `json:"parcelId"`
	Product           Product `json:"product"`
	Dose              Dose    `json:"dose"`
	ApplicationMethod string  `json:"applicationMethod,omitempty"` // default: "ground_boom"
	AppliedAt         string  `json:"appliedAt"`                   // ISO 8601
	DurationHours     float64 `json:"durationHours,omitempty"`     // default: 1.0
	AreaHa            float64 `json:"areaHa"`
	Center            Center  `json:"center"`
	Locale            string  `json:"locale,omitempty"` // default: "ro"
}

// Result is the internal representation of the AI service response.
// Mapped from the richer API response — only the fields needed by cascade.
type Result struct {
	RiskRadiusM    float64 `json:"risk_radius_m"`
	AffectedAreaKm float64 `json:"affected_area_km2"`
	WindDirDeg     float64 `json:"wind_direction_deg"`
	Severity       string  `json:"severity"` // "low" | "medium" | "high" | "very_high"
}

// Client is the interface implemented by both MockClient and HTTPClient.
type Client interface {
	Assess(ctx context.Context, req Request) (*Result, error)
}

// NewClient returns MockClient if baseURL is empty, HTTPClient otherwise.
func NewClient(baseURL string) Client {
	if baseURL == "" {
		return &MockClient{}
	}
	return newHTTPClient(baseURL)
}
