package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/middleware"
)

const photoSignedGetTTL = 1 * time.Hour
const photoSignedPutTTL = 15 * time.Minute
const photoMaxBytes = 10 * 1024 * 1024 // 10 MiB

func registerDamage(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-damage-claim",
		Method:        http.MethodPost,
		Path:          "/api/v1/damage-claims",
		Summary:       "File a damage claim",
		Tags:          []string{"damage-claims"},
		DefaultStatus: http.StatusCreated,
	}, h.createDamageClaim)

	huma.Register(api, huma.Operation{
		OperationID: "list-damage-claims",
		Method:      http.MethodGet,
		Path:        "/api/v1/damage-claims",
		Summary:     "List damage claims",
		Tags:        []string{"damage-claims"},
	}, h.listDamageClaims)

	huma.Register(api, huma.Operation{
		OperationID: "get-damage-claim",
		Method:      http.MethodGet,
		Path:        "/api/v1/damage-claims/{id}",
		Summary:     "Get damage claim",
		Tags:        []string{"damage-claims"},
	}, h.getDamageClaim)

	huma.Register(api, huma.Operation{
		OperationID: "sign-upload",
		Method:      http.MethodPost,
		Path:        "/api/v1/uploads/sign",
		Summary:     "Mint a short-lived presigned PUT URL for a damage photo",
		Tags:        []string{"uploads"},
	}, h.signUpload)
}

type DamageClaimOutput struct {
	ID             string   `json:"id"`
	BeekeeperID    string   `json:"beekeeper_id"`
	ApiaryID       string   `json:"apiary_id"`
	RelatedSprayID *string  `json:"related_spray_id,omitempty"`
	Description    string   `json:"description"`
	HiveLossCount  int32    `json:"hive_loss_count"`
	GpsLat         float64  `json:"gps_lat"`
	GpsLng         float64  `json:"gps_lng"`
	Status         string   `json:"status"`
	Photos         []string `json:"photos"`
	LedgerHash     string   `json:"ledger_hash"`
	CreatedAt      string   `json:"created_at"`
}

// ---------------------------------------------------------------------------
// POST /uploads/sign — presigned PUT URL for a damage photo
// ---------------------------------------------------------------------------

type SignUploadInput struct {
	Body struct {
		Filename string `json:"filename"`
		Mime     string `json:"mime"`
		ByteSize int64  `json:"byte_size"`
	}
}

type SignUploadOutput struct {
	Body struct {
		UploadURL string `json:"upload_url"`
		Key       string `json:"key"`
		ExpiresIn int    `json:"expires_in"`
	}
}

func (h *Handlers) signUpload(ctx context.Context, input *SignUploadInput) (*SignUploadOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	if user.Role != domain.RoleApicultor {
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis pentru rolul dumneavoastră")
	}

	ext := extFromMime(input.Body.Mime)
	if ext == "" {
		return nil, huma.NewError(http.StatusBadRequest, "Tipul fișierului trebuie să fie JPEG, PNG sau WebP")
	}
	if input.Body.ByteSize <= 0 || input.Body.ByteSize > photoMaxBytes {
		return nil, huma.NewError(http.StatusBadRequest, "Fișierul trebuie să fie între 1 octet și 10 MB")
	}
	if h.storage == nil {
		return nil, huma.NewError(http.StatusServiceUnavailable, "Stocarea fotografiilor nu este configurată")
	}

	key := "photos/" + uuid.New().String() + ext
	uploadURL, err := h.storage.SignedPutURL(ctx, key, photoSignedPutTTL, input.Body.Mime)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare la generarea URL-ului de upload")
	}

	out := &SignUploadOutput{}
	out.Body.UploadURL = uploadURL
	out.Body.Key = key
	out.Body.ExpiresIn = int(photoSignedPutTTL.Seconds())
	return out, nil
}

func extFromMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

// ---------------------------------------------------------------------------
// POST /damage-claims — create a claim
// ---------------------------------------------------------------------------

type CreateDamageClaimInput struct {
	Body struct {
		ApiaryID       string   `json:"apiary_id"`
		RelatedSprayID *string  `json:"related_spray_id,omitempty"`
		Description    string   `json:"description"`
		HiveLossCount  int32    `json:"hive_loss_count"`
		Photos         []string `json:"photos"`
		GpsLat         float64  `json:"gps_lat"`
		GpsLng         float64  `json:"gps_lng"`
	}
}

type CreateDamageClaimOutput struct {
	Body struct {
		Claim      DamageClaimOutput `json:"claim"`
		LedgerHash string            `json:"ledger_hash"`
	}
}

func (h *Handlers) createDamageClaim(ctx context.Context, input *CreateDamageClaimInput) (*CreateDamageClaimOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	if user.Role != domain.RoleApicultor {
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis pentru rolul dumneavoastră")
	}

	beekeeperID, err := uuid.Parse(user.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă")
	}

	apiaryID, err := uuid.Parse(input.Body.ApiaryID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "ID stupină invalid")
	}

	desc := strings.TrimSpace(input.Body.Description)
	if desc == "" {
		return nil, huma.NewError(http.StatusBadRequest, "Descrierea pagubei este obligatorie")
	}
	if input.Body.HiveLossCount < 0 {
		return nil, huma.NewError(http.StatusBadRequest, "Numărul de stupi pierduți nu poate fi negativ")
	}
	if input.Body.GpsLat < -90 || input.Body.GpsLat > 90 || input.Body.GpsLng < -180 || input.Body.GpsLng > 180 {
		return nil, huma.NewError(http.StatusBadRequest, "Coordonate GPS invalide")
	}

	var relatedSprayID uuid.NullUUID
	if input.Body.RelatedSprayID != nil && *input.Body.RelatedSprayID != "" {
		id, err := uuid.Parse(*input.Body.RelatedSprayID)
		if err != nil {
			return nil, huma.NewError(http.StatusBadRequest, "ID raport stropire invalid")
		}
		relatedSprayID = uuid.NullUUID{UUID: id, Valid: true}
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	defer tx.Rollback()

	q := dbsqlc.New(tx)

	// Verify apiary ownership.
	apiary, err := q.GetApiary(ctx, apiaryID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "Stupina nu a fost găsită")
	}
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}
	if apiary.OwnerID != beekeeperID {
		return nil, huma.NewError(http.StatusForbidden, "Stupina nu vă aparține")
	}

	claimID := uuid.New()
	claim, err := q.CreateDamageClaim(ctx, dbsqlc.CreateDamageClaimParams{
		ID:             claimID,
		BeekeeperID:    beekeeperID,
		ApiaryID:       apiaryID,
		RelatedSprayID: relatedSprayID,
		Description:    desc,
		HiveLossCount:  input.Body.HiveLossCount,
		GpsLat:         input.Body.GpsLat,
		GpsLng:         input.Body.GpsLng,
		LedgerHash:     "",
	})
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	for _, key := range input.Body.Photos {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, err := q.AddDamagePhoto(ctx, dbsqlc.AddDamagePhotoParams{
			ID:            uuid.New(),
			DamageClaimID: claimID,
			Url:           key,
		}); err != nil {
			return nil, huma.NewError(http.StatusInternalServerError, "Eroare la salvarea fotografiei")
		}
	}

	actorID := user.ID
	ledgerHash, err := h.ledgerSvc.Append(ctx, tx, "damage.filed", &actorID, map[string]any{
		"claim_id":        claimID.String(),
		"beekeeper_id":    beekeeperID.String(),
		"apiary_id":       apiaryID.String(),
		"hive_loss_count": input.Body.HiveLossCount,
		"photo_count":     len(input.Body.Photos),
	})
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	// No sqlc query exists for updating damage_claims.ledger_hash — use raw SQL.
	if _, err := tx.ExecContext(ctx, "UPDATE damage_claims SET ledger_hash = $1 WHERE id = $2", ledgerHash, claimID); err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	if err := tx.Commit(); err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	claim.LedgerHash = ledgerHash

	photoURLs, err := h.signedPhotoURLs(ctx, claimID)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare la generarea URL-urilor fotografiilor")
	}

	out := &CreateDamageClaimOutput{}
	out.Body.Claim = mapDamageClaim(claim, photoURLs)
	out.Body.LedgerHash = ledgerHash
	return out, nil
}

// ---------------------------------------------------------------------------
// GET /damage-claims and /damage-claims/:id
// ---------------------------------------------------------------------------

type ListDamageClaimsOutput struct {
	Body struct {
		Items []DamageClaimOutput `json:"items"`
	}
}

func (h *Handlers) listDamageClaims(ctx context.Context, _ *struct{}) (*ListDamageClaimsOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)

	var rows []dbsqlc.DamageClaim
	switch user.Role {
	case domain.RoleApicultor:
		beekeeperID, err := uuid.Parse(user.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă")
		}
		rows, err = q.ListDamageClaimsByBeekeeper(ctx, beekeeperID)
		if err != nil {
			return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
		}
	case domain.RoleInspector:
		var err error
		rows, err = q.ListAllDamageClaims(ctx)
		if err != nil {
			return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
		}
	default:
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis pentru rolul dumneavoastră")
	}

	items := make([]DamageClaimOutput, 0, len(rows))
	for _, r := range rows {
		urls, err := h.signedPhotoURLs(ctx, r.ID)
		if err != nil {
			return nil, huma.NewError(http.StatusInternalServerError, "Eroare la generarea URL-urilor fotografiilor")
		}
		items = append(items, mapDamageClaim(r, urls))
	}

	out := &ListDamageClaimsOutput{}
	out.Body.Items = items
	return out, nil
}

type GetDamageClaimOutput struct {
	Body struct {
		Claim DamageClaimOutput `json:"claim"`
	}
}

func (h *Handlers) getDamageClaim(ctx context.Context, input *struct {
	ID string `path:"id"`
}) (*GetDamageClaimOutput, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}

	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "ID invalid")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)

	claim, err := q.GetDamageClaim(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.NewError(http.StatusNotFound, "Reclamația nu a fost găsită")
	}
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	switch user.Role {
	case domain.RoleApicultor:
		if claim.BeekeeperID.String() != user.ID {
			return nil, huma.NewError(http.StatusNotFound, "Reclamația nu a fost găsită")
		}
	case domain.RoleInspector:
		// inspector sees everything
	default:
		return nil, huma.NewError(http.StatusForbidden, "Acces interzis pentru rolul dumneavoastră")
	}

	urls, err := h.signedPhotoURLs(ctx, claim.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare la generarea URL-urilor fotografiilor")
	}

	out := &GetDamageClaimOutput{}
	out.Body.Claim = mapDamageClaim(claim, urls)
	return out, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// signedPhotoURLs mints fresh presigned GETs for every photo attached to a
// claim. The `url` column in damage_photos actually stores the storage key —
// we never persist a URL with a baked-in TTL.
func (h *Handlers) signedPhotoURLs(ctx context.Context, claimID uuid.UUID) ([]string, error) {
	if h.storage == nil {
		return []string{}, nil
	}
	q := dbsqlc.New(stdlib.OpenDBFromPool(h.pool))
	photos, err := q.ListDamagePhotos(ctx, claimID)
	if err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}
	urls := make([]string, 0, len(photos))
	for _, p := range photos {
		signed, err := h.storage.SignedURL(ctx, p.Url, photoSignedGetTTL)
		if err != nil {
			return nil, fmt.Errorf("sign photo: %w", err)
		}
		urls = append(urls, signed)
	}
	return urls, nil
}

func mapDamageClaim(row dbsqlc.DamageClaim, photoURLs []string) DamageClaimOutput {
	var relatedSpray *string
	if row.RelatedSprayID.Valid {
		s := row.RelatedSprayID.UUID.String()
		relatedSpray = &s
	}
	return DamageClaimOutput{
		ID:             row.ID.String(),
		BeekeeperID:    row.BeekeeperID.String(),
		ApiaryID:       row.ApiaryID.String(),
		RelatedSprayID: relatedSpray,
		Description:    row.Description,
		HiveLossCount:  row.HiveLossCount,
		GpsLat:         row.GpsLat,
		GpsLng:         row.GpsLng,
		Status:         string(row.Status),
		Photos:         photoURLs,
		LedgerHash:     row.LedgerHash,
		CreatedAt:      row.CreatedAt.Format(time.RFC3339),
	}
}

// ---------------------------------------------------------------------------
// rawUploadPut — dev-only raw PUT receiver used by the Local storage backend.
// Wired in router.go only when storage is NOT R2. In production the client
// PUTs directly to R2 via a presigned URL and this handler is unreachable.
// ---------------------------------------------------------------------------

func (h *Handlers) rawUploadPut(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "*")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	// Defence in depth: confine writes to the photos/ prefix and reject path
	// escapes — chi already strips ".." but explicit checks beat clever
	// callers.
	clean := filepath.Clean(key)
	if clean != key || strings.Contains(key, "..") || !strings.HasPrefix(key, "photos/") {
		http.Error(w, "invalid key", http.StatusBadRequest)
		return
	}
	dst := filepath.Join("uploads", clean)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		http.Error(w, "mkdir failed", http.StatusInternalServerError)
		return
	}
	f, err := os.Create(dst)
	if err != nil {
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	limited := io.LimitReader(r.Body, photoMaxBytes+1)
	n, err := io.Copy(f, limited)
	if err != nil {
		_ = os.Remove(dst)
		http.Error(w, "write failed", http.StatusInternalServerError)
		return
	}
	if n > photoMaxBytes {
		_ = os.Remove(dst)
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
