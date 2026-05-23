package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/stdlib"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/external/weather"
)

func registerReference(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "list-substances",
		Method:      http.MethodGet,
		Path:        "/api/v1/reference/substances",
		Summary:     "List pesticide substances",
		Tags:        []string{"reference"},
	}, h.listSubstances)

	huma.Register(api, huma.Operation{
		OperationID: "get-weather",
		Method:      http.MethodGet,
		Path:        "/api/v1/reference/weather",
		Summary:     "Get current weather at location",
		Tags:        []string{"reference"},
	}, h.getWeather)
}

type SubstanceOutput struct {
	Label    string `json:"label"`
	Toxicity string `json:"toxicity"`
}

func (h *Handlers) listSubstances(ctx context.Context, _ *struct{}) (*struct {
	Body struct {
		Items []SubstanceOutput `json:"items"`
	}
}, error) {
	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)
	rows, err := q.ListSubstances(ctx)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	items := make([]SubstanceOutput, len(rows))
	for i, r := range rows {
		items[i] = SubstanceOutput{Label: r.Label, Toxicity: r.Toxicity}
	}
	out := &struct {
		Body struct {
			Items []SubstanceOutput `json:"items"`
		}
	}{}
	out.Body.Items = items
	return out, nil
}

type WeatherOutput struct {
	WindDirectionDeg float64 `json:"wind_direction_deg"`
	WindSpeedMs      float64 `json:"wind_speed_ms"`
	TemperatureC     float64 `json:"temperature_c"`
	FetchedAt        string  `json:"fetched_at"`
}

func (h *Handlers) getWeather(ctx context.Context, input *struct {
	Lat float64 `query:"lat"`
	Lng float64 `query:"lng"`
}) (*struct {
	Body WeatherOutput
}, error) {
	result, err := weather.Fetch(ctx, input.Lat, input.Lng)
	if err != nil {
		return nil, huma.NewError(http.StatusBadGateway, "Nu s-a putut obține vremea")
	}
	out := &struct {
		Body WeatherOutput
	}{}
	out.Body = WeatherOutput{
		WindDirectionDeg: result.WindDirectionDeg,
		WindSpeedMs:      result.WindSpeedMs,
		TemperatureC:     result.TemperatureC,
		FetchedAt:        result.FetchedAt.Format(time.RFC3339),
	}
	return out, nil
}
