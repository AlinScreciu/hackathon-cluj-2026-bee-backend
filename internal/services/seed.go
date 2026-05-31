package services

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"

	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
)

func Seed(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	q := dbsqlc.New(sqlDB)

	hash, err := bcrypt.GenerateFromPassword([]byte("parola123"), 12)
	if err != nil {
		return err
	}
	h := string(hash)

	users := []dbsqlc.CreateUserParams{
		{ID: uuid.New(), Cnp: "1850101123456", FullName: "Andrei Berar", Email: "andrei.berar@test.com", Phone: "+40721000001", Role: dbsqlc.UserRoleApicultor, County: "Cluj", Locality: "Apahida", PasswordHash: h},
		{ID: uuid.New(), Cnp: "2900215654321", FullName: "Maria Costea", Email: "maria.costea@test.com", Phone: "+40721000002", Role: dbsqlc.UserRoleApicultor, County: "Cluj", Locality: "Florești", PasswordHash: h},
		{ID: uuid.New(), Cnp: "1780530987654", FullName: "Ioan Lupu", Email: "ioan.lupu@test.com", Phone: "+40721000003", Role: dbsqlc.UserRoleApicultor, County: "Cluj", Locality: "Turda", PasswordHash: h},
		{ID: uuid.New(), Cnp: "1920412111222", FullName: "Vasile Mureșan", Email: "vasile.muresan@test.com", Phone: "+40721000004", Role: dbsqlc.UserRoleFermier, County: "Cluj", Locality: "Câmpia Turzii", PasswordHash: h},
		{ID: uuid.New(), Cnp: "2880721333444", FullName: "Elena Popa", Email: "elena.popa@test.com", Phone: "+40721000005", Role: dbsqlc.UserRoleFermier, County: "Cluj", Locality: "Gherla", PasswordHash: h},
		{ID: uuid.New(), Cnp: "1751103555666", FullName: "Gheorghe Stan", Email: "gheorghe.stan@test.com", Phone: "+40721000006", Role: dbsqlc.UserRoleFermier, County: "Cluj", Locality: "Dej", PasswordHash: h},
		{ID: uuid.New(), Cnp: "1680808777888", FullName: "Inspector Județean Cluj", Email: "inspector@test.com", Phone: "+40721000007", Role: dbsqlc.UserRoleInspector, County: "Cluj", Locality: "Cluj-Napoca", PasswordHash: h},
		// Demo accounts — real emails/phones used during live presentation.
		{ID: uuid.New(), Cnp: "2800604121673", FullName: "Marius Guriță", Email: "mgurita@proton.me", Phone: "+40756881589", Role: dbsqlc.UserRoleApicultor, County: "Cluj", Locality: "Apahida", PasswordHash: h},
		{ID: uuid.New(), Cnp: "6020615124562", FullName: "Alexandra Marian", Email: "alexandramarian@proton.me", Phone: "+40731390001", Role: dbsqlc.UserRoleFermier, County: "Cluj", Locality: "Cluj-Napoca", PasswordHash: h},
		{ID: uuid.New(), Cnp: "6020615124567", FullName: "Alexandra Marian", Email: "alexandra_marian_2002@yahoo.com", Phone: "+40731390001", Role: dbsqlc.UserRoleInspector, County: "Cluj", Locality: "Cluj-Napoca", PasswordHash: h},
	}

	seeded := 0
	for _, u := range users {
		existing, err := q.GetUserByCNP(ctx, u.Cnp)
		if err == nil {
			slog.Debug("seed: user exists", "cnp_prefix", u.Cnp[:4], "id", existing.ID)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := q.CreateUser(ctx, u); err != nil {
			return err
		}
		seeded++
		slog.Info("seed: created user", "name", u.FullName, "role", u.Role)
	}
	slog.Info("seed complete", "created", seeded, "total", len(users))

	// Resolve user IDs by CNP for apiaries and parcels seeding.
	cnpToUser := map[string]*dbsqlc.User{}
	for _, cnp := range []string{
		"1850101123456", "2900215654321", "1780530987654",
		"1920412111222", "2880721333444", "1751103555666",
		"2800604121673", "6020615124562",
	} {
		u, err := q.GetUserByCNP(ctx, cnp)
		if err != nil {
			return err
		}
		cnpToUser[cnp] = &u
	}

	// ── Seed 6 apiaries ──────────────────────────────────────────────────────

	type apiarySpec struct {
		ownerCNP  string
		name      string
		apiType   dbsqlc.ApiaryType
		lat, lng  float64
		hiveCount int32
		startDate time.Time
		endDate   sql.NullTime
		notes     sql.NullString
	}

	apiaries := []apiarySpec{
		// Andrei Berar
		{
			ownerCNP:  "1850101123456",
			name:      "Stupina Apahida Sud",
			apiType:   dbsqlc.ApiaryTypePermanent,
			lat:       46.7830, lng: 23.7148,
			hiveCount: 24,
			startDate: time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			ownerCNP:  "1850101123456",
			name:      "Stupina Apahida Nord",
			apiType:   dbsqlc.ApiaryTypePermanent,
			lat:       46.7920, lng: 23.7220,
			hiveCount: 18,
			startDate: time.Date(2022, 5, 15, 0, 0, 0, 0, time.UTC),
		},
		// Maria Costea
		{
			ownerCNP:  "2900215654321",
			name:      "Stupina Florești Est",
			apiType:   dbsqlc.ApiaryTypePermanent,
			lat:       46.7432, lng: 23.4900,
			hiveCount: 32,
			startDate: time.Date(2021, 3, 20, 0, 0, 0, 0, time.UTC),
		},
		{
			ownerCNP: "2900215654321",
			name:     "Stupina Florești Vest",
			apiType:  dbsqlc.ApiaryTypePastoral,
			lat:      46.7385, lng: 23.4720,
			hiveCount: 15,
			startDate: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
			endDate:   sql.NullTime{Time: time.Date(2024, 10, 31, 0, 0, 0, 0, time.UTC), Valid: true},
		},
		// Ioan Lupu
		{
			ownerCNP:  "1780530987654",
			name:      "Stupina Turda Centru",
			apiType:   dbsqlc.ApiaryTypePermanent,
			lat:       46.5725, lng: 23.7865,
			hiveCount: 28,
			startDate: time.Date(2020, 6, 10, 0, 0, 0, 0, time.UTC),
		},
		{
			ownerCNP:  "1780530987654",
			name:      "Stupina Turda Nord",
			apiType:   dbsqlc.ApiaryTypePermanent,
			lat:       46.5890, lng: 23.7910,
			hiveCount: 20,
			startDate: time.Date(2023, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		// Marius Guriță — demo apicultor account. Placed in Apahida cluster
		// so it sits inside the risk radius of Alexandra's parcel below.
		{
			ownerCNP:  "2800604121673",
			name:      "Stupina Marius Apahida",
			apiType:   dbsqlc.ApiaryTypePermanent,
			lat:       46.7795, lng: 23.7175,
			hiveCount: 16,
			startDate: time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	// Group apiaries by owner to support per-owner idempotency check.
	apiariesByOwner := map[string][]apiarySpec{}
	for _, a := range apiaries {
		apiariesByOwner[a.ownerCNP] = append(apiariesByOwner[a.ownerCNP], a)
	}

	for cnp, specs := range apiariesByOwner {
		owner := cnpToUser[cnp]
		existing, err := q.ListApiariesByOwner(ctx, owner.ID)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			slog.Debug("seed: apiaries exist, skipping", "owner", owner.FullName)
			continue
		}
		for _, spec := range specs {
			params := dbsqlc.CreateApiaryParams{
				ID:        uuid.New(),
				OwnerID:   owner.ID,
				Name:      spec.name,
				Type:      spec.apiType,
				Lat:       spec.lat,
				Lng:       spec.lng,
				HiveCount: spec.hiveCount,
				StartDate: spec.startDate,
				EndDate:   spec.endDate,
				Notes:     spec.notes,
			}
			if _, err := q.CreateApiary(ctx, params); err != nil {
				return err
			}
			slog.Info("seed: created apiary", "name", params.Name)
		}
	}

	// ── Backfill ledger hash for any apiary missing it ───────────────────────
	// Seed historically inserted apiaries directly without emitting an
	// `apiary.registered` event, leaving `ledger_hash = ''`. Live POST
	// requests do it correctly; this loop fixes any seeded rows so the
	// frontend can always render the "Dovadă" chip on the detail page.
	// Idempotent: skips apiaries that already have a hash.
	ledgerSvc := NewLedgerService(pool)
	allApiaries, err := q.ListAllApiaries(ctx)
	if err != nil {
		return err
	}
	for _, a := range allApiaries {
		if a.LedgerHash != "" {
			continue
		}
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		txQ := dbsqlc.New(tx)
		actorID := a.OwnerID.String()
		startDateStr := a.StartDate.Format("2006-01-02")
		ledgerHash, err := ledgerSvc.Append(ctx, tx, "apiary.registered", &actorID, map[string]any{
			"apiary_id":  a.ID.String(),
			"owner_id":   a.OwnerID.String(),
			"name":       a.Name,
			"type":       string(a.Type),
			"lat":        a.Lat,
			"lng":        a.Lng,
			"hive_count": a.HiveCount,
			"start_date": startDateStr,
		})
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := txQ.UpdateApiaryLedgerHash(ctx, dbsqlc.UpdateApiaryLedgerHashParams{
			ID:         a.ID,
			LedgerHash: ledgerHash,
		}); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		slog.Info("seed: backfilled apiary ledger hash", "name", a.Name, "hash", ledgerHash[:8])
	}

	// ── Seed 7 parcels ───────────────────────────────────────────────────────

	type parcelSpec struct {
		ownerCNP        string
		name            string
		cadastralNumber string
		lat, lng        float64
		surfaceHa       float64
		defaultCrop     sql.NullString
		county          string
		locality        string
	}

	parcels := []parcelSpec{
		// Vasile Mureșan
		{
			ownerCNP: "1920412111222", name: "Parcela Câmpia Nord",
			cadastralNumber: "CB1001", lat: 46.5590, lng: 23.8810, surfaceHa: 5.5,
			defaultCrop: sql.NullString{String: "grâu", Valid: true},
			county: "Cluj", locality: "Câmpia Turzii",
		},
		{
			ownerCNP: "1920412111222", name: "Parcela Câmpia Sud",
			cadastralNumber: "CB1002", lat: 46.5490, lng: 23.8920, surfaceHa: 7.2,
			defaultCrop: sql.NullString{String: "rapiță", Valid: true},
			county: "Cluj", locality: "Câmpia Turzii",
		},
		// Elena Popa
		{
			ownerCNP: "2880721333444", name: "Parcela Gherla Est",
			cadastralNumber: "CJ2001", lat: 47.0212, lng: 23.9134, surfaceHa: 4.8,
			defaultCrop: sql.NullString{String: "porumb", Valid: true},
			county: "Cluj", locality: "Gherla",
		},
		{
			ownerCNP: "2880721333444", name: "Parcela Gherla Vest",
			cadastralNumber: "CJ2002", lat: 47.0180, lng: 23.8940, surfaceHa: 6.1,
			defaultCrop: sql.NullString{String: "floarea-soarelui", Valid: true},
			county: "Cluj", locality: "Gherla",
		},
		// Gheorghe Stan
		{
			ownerCNP: "1751103555666", name: "Parcela Dej 1",
			cadastralNumber: "CJ3001", lat: 47.1280, lng: 23.8710, surfaceHa: 8.3,
			defaultCrop: sql.NullString{String: "rapiță", Valid: true},
			county: "Cluj", locality: "Dej",
		},
		{
			ownerCNP: "1751103555666", name: "Parcela Dej 2",
			cadastralNumber: "CJ3002", lat: 47.1350, lng: 23.8800, surfaceHa: 5.0,
			defaultCrop: sql.NullString{String: "grâu", Valid: true},
			county: "Cluj", locality: "Dej",
		},
		{
			ownerCNP: "1751103555666", name: "Parcela Dej 3",
			cadastralNumber: "CJ3003", lat: 47.1180, lng: 23.8620, surfaceHa: 4.2,
			defaultCrop: sql.NullString{String: "porumb", Valid: true},
			county: "Cluj", locality: "Dej",
		},
		// Alexandra Marian — demo fermier. Parcel sits ~1.5 km from Stupina
		// Marius Apahida so a T+ spray here triggers his alert during the demo.
		{
			ownerCNP: "6020615124562", name: "Parcela Alexandra Apahida",
			cadastralNumber: "CJ4001", lat: 46.7700, lng: 23.7250, surfaceHa: 6.5,
			defaultCrop: sql.NullString{String: "rapiță", Valid: true},
			county: "Cluj", locality: "Apahida",
		},
	}

	// Group parcels by owner to support per-owner idempotency check.
	parcelsByOwner := map[string][]parcelSpec{}
	for _, p := range parcels {
		parcelsByOwner[p.ownerCNP] = append(parcelsByOwner[p.ownerCNP], p)
	}

	for cnp, specs := range parcelsByOwner {
		owner := cnpToUser[cnp]
		existing, err := q.ListParcelsByOwner(ctx, owner.ID)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			slog.Debug("seed: parcels exist, skipping", "owner", owner.FullName)
			continue
		}
		for _, spec := range specs {
			params := dbsqlc.CreateParcelParams{
				ID:              uuid.New(),
				OwnerID:         owner.ID,
				Name:            spec.name,
				CadastralNumber: spec.cadastralNumber,
				Lat:             spec.lat,
				Lng:             spec.lng,
				SurfaceHa:       spec.surfaceHa,
				DefaultCrop:     spec.defaultCrop,
				County:          spec.county,
				Locality:        spec.locality,
			}
			if _, err := q.CreateParcel(ctx, params); err != nil {
				return err
			}
			slog.Info("seed: created parcel", "name", params.Name)
		}
	}

	return nil
}
