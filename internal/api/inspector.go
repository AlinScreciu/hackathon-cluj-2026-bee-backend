package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/middleware"
)

func registerInspector(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "inspector-map-data",
		Method:      http.MethodGet,
		Path:        "/api/v1/inspector/map-data",
		Summary:     "Get map data for inspector",
		Tags:        []string{"inspector"},
	}, h.inspectorMapData)

	huma.Register(api, huma.Operation{
		OperationID: "inspector-list-farmers",
		Method:      http.MethodGet,
		Path:        "/api/v1/inspector/farmers",
		Summary:     "List all farmers",
		Tags:        []string{"inspector"},
	}, h.inspectorListFarmers)

	huma.Register(api, huma.Operation{
		OperationID: "inspector-get-farmer",
		Method:      http.MethodGet,
		Path:        "/api/v1/inspector/farmers/{id}",
		Summary:     "Get farmer details",
		Tags:        []string{"inspector"},
	}, h.inspectorGetFarmer)
}

// ---------------------------------------------------------------------------
// GET /inspector/map-data
// ---------------------------------------------------------------------------

type inspectorMapApiary struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	OwnerID   string  `json:"owner_id"`
	OwnerName string  `json:"owner_name"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	HiveCount int32   `json:"hive_count"`
	Status    string  `json:"status"`
}

type inspectorMapSpray struct {
	ID            string    `json:"id"`
	FarmerID      string    `json:"farmer_id"`
	ParcelID      string    `json:"parcel_id"`
	Substance     string    `json:"substance"`
	Toxicity      string    `json:"toxicity"`
	SurfaceHa     float64   `json:"surface_ha"`
	ScheduledAt   time.Time `json:"scheduled_at"`
	DurationHours float64   `json:"duration_hours"`
	Status        string    `json:"status"`
}

type inspectorMapClaim struct {
	ID            string  `json:"id"`
	BeekeeperID   string  `json:"beekeeper_id"`
	ApiaryID      string  `json:"apiary_id"`
	Status        string  `json:"status"`
	HiveLossCount int32   `json:"hive_loss_count"`
	GpsLat        float64 `json:"gps_lat"`
	GpsLng        float64 `json:"gps_lng"`
}

type InspectorMapDataOutput struct {
	Body struct {
		Apiaries     []inspectorMapApiary `json:"apiaries"`
		ActiveSprays []inspectorMapSpray  `json:"active_sprays"`
		DamageClaims []inspectorMapClaim  `json:"damage_claims"`
	}
}

func (h *Handlers) inspectorMapData(ctx context.Context, input *struct {
	BBox string `query:"bbox" required:"false"`
}) (*InspectorMapDataOutput, error) {
	if err := requireInspector(ctx); err != nil {
		return nil, err
	}

	minLat, minLng, maxLat, maxLng, bboxValid := parseBBox(input.BBox)

	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))

	allApiaries, err := q.ListAllApiaries(ctx)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	allUsers, err := q.ListAllUsers(ctx)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	userByID := make(map[uuid.UUID]dbsqlc.User, len(allUsers))
	for _, u := range allUsers {
		userByID[u.ID] = u
	}

	allClaims, err := q.ListAllDamageClaims(ctx)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	apiaryHasOpenClaim := make(map[uuid.UUID]bool)
	for _, c := range allClaims {
		if c.Status == dbsqlc.DamageClaimStatusFiled || c.Status == dbsqlc.DamageClaimStatusUnderReview {
			apiaryHasOpenClaim[c.ApiaryID] = true
		}
	}

	activeSprays, err := q.ListActiveSprayReports(ctx)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	apiariesOut := make([]inspectorMapApiary, 0, len(allApiaries))
	for _, a := range allApiaries {
		if bboxValid && (a.Lat < minLat || a.Lat > maxLat || a.Lng < minLng || a.Lng > maxLng) {
			continue
		}
		status := "safe"
		if apiaryHasOpenClaim[a.ID] {
			status = "damaged"
		}
		owner := userByID[a.OwnerID]
		apiariesOut = append(apiariesOut, inspectorMapApiary{
			ID:        a.ID.String(),
			Name:      a.Name,
			OwnerID:   a.OwnerID.String(),
			OwnerName: owner.FullName,
			Lat:       a.Lat,
			Lng:       a.Lng,
			HiveCount: a.HiveCount,
			Status:    status,
		})
	}

	sprayOut := make([]inspectorMapSpray, 0, len(activeSprays))
	for _, s := range activeSprays {
		sprayOut = append(sprayOut, inspectorMapSpray{
			ID:            s.ID.String(),
			FarmerID:      s.FarmerID.String(),
			ParcelID:      s.ParcelID.String(),
			Substance:     s.Substance,
			Toxicity:      s.Toxicity,
			SurfaceHa:     s.SurfaceHa,
			ScheduledAt:   s.ScheduledAt,
			DurationHours: s.DurationHours,
			Status:        string(s.Status),
		})
	}

	claimsOut := make([]inspectorMapClaim, 0, len(allClaims))
	for _, c := range allClaims {
		if c.Status != dbsqlc.DamageClaimStatusFiled && c.Status != dbsqlc.DamageClaimStatusUnderReview {
			continue
		}
		if bboxValid && (c.GpsLat < minLat || c.GpsLat > maxLat || c.GpsLng < minLng || c.GpsLng > maxLng) {
			continue
		}
		claimsOut = append(claimsOut, inspectorMapClaim{
			ID:            c.ID.String(),
			BeekeeperID:   c.BeekeeperID.String(),
			ApiaryID:      c.ApiaryID.String(),
			Status:        string(c.Status),
			HiveLossCount: c.HiveLossCount,
			GpsLat:        c.GpsLat,
			GpsLng:        c.GpsLng,
		})
	}

	out := &InspectorMapDataOutput{}
	out.Body.Apiaries = apiariesOut
	out.Body.ActiveSprays = sprayOut
	out.Body.DamageClaims = claimsOut
	return out, nil
}

// ---------------------------------------------------------------------------
// GET /inspector/farmers
// ---------------------------------------------------------------------------

type inspectorFarmerSummary struct {
	ID          string `json:"id"`
	FullName    string `json:"full_name"`
	County      string `json:"county"`
	Locality    string `json:"locality"`
	SprayCount  int    `json:"spray_count"`
	DamageCount int    `json:"damage_count"`
}

type InspectorListFarmersOutput struct {
	Body struct {
		Items []inspectorFarmerSummary `json:"items"`
	}
}

func (h *Handlers) inspectorListFarmers(ctx context.Context, _ *struct{}) (*InspectorListFarmersOutput, error) {
	if err := requireInspector(ctx); err != nil {
		return nil, err
	}

	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))

	farmers, err := q.ListUsersByRole(ctx, dbsqlc.UserRoleFermier)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	allSprays, err := q.ListAllSprayReports(ctx)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	sprayCountByFarmer := make(map[uuid.UUID]int)
	sprayToFarmer := make(map[uuid.UUID]uuid.UUID, len(allSprays))
	for _, s := range allSprays {
		sprayCountByFarmer[s.FarmerID]++
		sprayToFarmer[s.ID] = s.FarmerID
	}

	allClaims, err := q.ListAllDamageClaims(ctx)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	damageCountByFarmer := make(map[uuid.UUID]int)
	for _, c := range allClaims {
		if !c.RelatedSprayID.Valid {
			continue
		}
		if farmerID, ok := sprayToFarmer[c.RelatedSprayID.UUID]; ok {
			damageCountByFarmer[farmerID]++
		}
	}

	items := make([]inspectorFarmerSummary, 0, len(farmers))
	for _, f := range farmers {
		items = append(items, inspectorFarmerSummary{
			ID:          f.ID.String(),
			FullName:    f.FullName,
			County:      f.County,
			Locality:    f.Locality,
			SprayCount:  sprayCountByFarmer[f.ID],
			DamageCount: damageCountByFarmer[f.ID],
		})
	}

	out := &InspectorListFarmersOutput{}
	out.Body.Items = items
	return out, nil
}

// ---------------------------------------------------------------------------
// GET /inspector/farmers/:id
// ---------------------------------------------------------------------------

type inspectorFarmerDetail struct {
	ID       string `json:"id"`
	CNP      string `json:"cnp"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	County   string `json:"county"`
	Locality string `json:"locality"`
}

type inspectorRecentSpray struct {
	ID            string    `json:"id"`
	ParcelID      string    `json:"parcel_id"`
	Substance     string    `json:"substance"`
	Toxicity      string    `json:"toxicity"`
	SurfaceHa     float64   `json:"surface_ha"`
	ScheduledAt   time.Time `json:"scheduled_at"`
	Status        string    `json:"status"`
	AffectedCount int32     `json:"affected_apiaries_count"`
}

type InspectorGetFarmerOutput struct {
	Body struct {
		Farmer            inspectorFarmerDetail  `json:"farmer"`
		SpraysLast30d     []inspectorRecentSpray `json:"sprays_last_30d"`
		SpraysTotal       int                    `json:"sprays_total"`
		DamagesFiledCount int                    `json:"damages_filed_against"`
	}
}

func (h *Handlers) inspectorGetFarmer(ctx context.Context, input *struct {
	ID string `path:"id"`
}) (*InspectorGetFarmerOutput, error) {
	if err := requireInspector(ctx); err != nil {
		return nil, err
	}

	farmerID, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "ID invalid")
	}

	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))

	farmer, err := q.GetUserByID(ctx, farmerID)
	if err != nil {
		return nil, huma.NewError(http.StatusNotFound, "Fermierul nu a fost găsit")
	}
	if farmer.Role != dbsqlc.UserRoleFermier {
		return nil, huma.NewError(http.StatusNotFound, "Utilizatorul nu este fermier")
	}

	allSprays, err := q.ListSprayReportsByFarmer(ctx, farmerID)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	recent := make([]inspectorRecentSpray, 0)
	sprayIDs := make(map[uuid.UUID]bool, len(allSprays))
	for _, s := range allSprays {
		sprayIDs[s.ID] = true
		if s.ScheduledAt.Before(cutoff) && s.CreatedAt.Before(cutoff) {
			continue
		}
		recent = append(recent, inspectorRecentSpray{
			ID:            s.ID.String(),
			ParcelID:      s.ParcelID.String(),
			Substance:     s.Substance,
			Toxicity:      s.Toxicity,
			SurfaceHa:     s.SurfaceHa,
			ScheduledAt:   s.ScheduledAt,
			Status:        string(s.Status),
			AffectedCount: s.AffectedApiariesCount,
		})
	}

	allClaims, err := q.ListAllDamageClaims(ctx)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	damageCount := 0
	for _, c := range allClaims {
		if c.RelatedSprayID.Valid && sprayIDs[c.RelatedSprayID.UUID] {
			damageCount++
		}
	}

	out := &InspectorGetFarmerOutput{}
	out.Body.Farmer = inspectorFarmerDetail{
		ID:       farmer.ID.String(),
		CNP:      farmer.Cnp,
		FullName: farmer.FullName,
		Email:    farmer.Email,
		Phone:    farmer.Phone,
		County:   farmer.County,
		Locality: farmer.Locality,
	}
	out.Body.SpraysLast30d = recent
	out.Body.SpraysTotal = len(allSprays)
	out.Body.DamagesFiledCount = damageCount
	return out, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func requireInspector(ctx context.Context) error {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	if user.Role != domain.RoleInspector {
		return huma.NewError(http.StatusForbidden, "Acces interzis pentru rolul dumneavoastră")
	}
	return nil
}

// parseBBox extracts (minLat, minLng, maxLat, maxLng, valid) from a
// "lat1,lng1,lat2,lng2" query string. The min/max are normalised so callers
// don't have to remember which corner is sent first. A malformed input
// returns valid=false and the caller skips the filter.
func parseBBox(raw string) (float64, float64, float64, float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, 0, 0, false
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return 0, 0, 0, 0, false
	}
	values := make([]float64, 4)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return 0, 0, 0, 0, false
		}
		values[i] = v
	}
	lat1, lng1, lat2, lng2 := values[0], values[1], values[2], values[3]
	minLat, maxLat := lat1, lat2
	if minLat > maxLat {
		minLat, maxLat = maxLat, minLat
	}
	minLng, maxLng := lng1, lng2
	if minLng > maxLng {
		minLng, maxLng = maxLng, minLng
	}
	return minLat, minLng, maxLat, maxLng, true
}
