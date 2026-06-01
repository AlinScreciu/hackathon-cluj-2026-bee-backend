// seed-evaluator wipes the production DB and reseeds it with a rich demo
// dataset built around three evaluator accounts. Idempotent — running it
// twice produces the same result.
//
// Layout for the story:
//   - fermier@beelive.ro owns 4 parcels across Cluj county.
//   - apicultor@beelive.ro owns 5 apiaries (one near each parcel + one safe).
//   - inspector@beelive.ro will sign damage.inspected events.
//
// On top of the foundation, the script bakes in a storyline:
//   - 1 LIVE spray (in_progress) on Cluj Centru with dispatched alerts.
//   - 1 scheduled spray on Apahida (the demo trigger — no dispatches yet).
//   - 2 completed sprays in the past 2-5 days with mixed confirmation states.
//   - 1 cancelled spray.
//   - 3 damage claims (filed today / under_review / accepted last month).
//   - ~60 ledger events, chronologically ordered, with valid SHA-256 chain.
//
// Safety: requires CONFIRM_WIPE=yes to actually run, since the operation is
// destructive against whatever DB DB_CONN_STR points at.
//
// Usage:
//
//	DB_CONN_STR="postgres://...neon.tech/...?sslmode=require" \
//	CONFIRM_WIPE=yes \
//	    go run ./cmd/seed-evaluator
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"

	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/services"
)

const (
	day  = 24 * time.Hour
	hour = time.Hour

	defaultPassword = "parola123"

	// 7 km is the typical Decis Forte T+ risk radius. The dispatch logic
	// uses Haversine(parcel, apiary) <= radius, so any apiary within 7 km
	// of a T+ spray's parcel gets alerted.
	t_plus_radius_m = 7000.0
	tox_radius_m    = 4500.0
	t_minus_radius_m = 2000.0
)

// ── seed data ───────────────────────────────────────────────────────────────

type seedUser struct {
	FullName, Email, Phone, County, Locality string
	Role                                     dbsqlc.UserRole
}

type seedParcel struct {
	OwnerEmail, Name, CadastralNumber string
	Lat, Lng, SurfaceHa               float64
	DefaultCrop, County, Locality     string
}

type seedApiary struct {
	OwnerEmail, Name string
	Type             dbsqlc.ApiaryType
	Lat, Lng         float64
	HiveCount        int32
}

var users = []seedUser{
	{FullName: "Evaluator Fermier", Email: "fermier@beelive.ro", Phone: "+40700000001",
		Role: dbsqlc.UserRoleFermier, County: "Cluj", Locality: "Cluj-Napoca"},
	{FullName: "Evaluator Apicultor", Email: "apicultor@beelive.ro", Phone: "+40756881589",
		Role: dbsqlc.UserRoleApicultor, County: "Cluj", Locality: "Cluj-Napoca"},
	{FullName: "Evaluator Inspector", Email: "inspector@beelive.ro", Phone: "+40700000003",
		Role: dbsqlc.UserRoleInspector, County: "Cluj", Locality: "Cluj-Napoca"},
}

var parcels = []seedParcel{
	{OwnerEmail: "fermier@beelive.ro", Name: "Parcela Evaluator Apahida",
		CadastralNumber: "EVAL-001", Lat: 46.7700, Lng: 23.7250, SurfaceHa: 6.5,
		DefaultCrop: "rapiță", County: "Cluj", Locality: "Apahida"},
	{OwnerEmail: "fermier@beelive.ro", Name: "Parcela Evaluator Cluj Centru",
		CadastralNumber: "EVAL-002", Lat: 46.7700, Lng: 23.5900, SurfaceHa: 4.2,
		DefaultCrop: "porumb", County: "Cluj", Locality: "Cluj-Napoca"},
	{OwnerEmail: "fermier@beelive.ro", Name: "Parcela Evaluator Florești",
		CadastralNumber: "EVAL-003", Lat: 46.7445, Lng: 23.5000, SurfaceHa: 7.8,
		DefaultCrop: "grâu", County: "Cluj", Locality: "Florești"},
	{OwnerEmail: "fermier@beelive.ro", Name: "Parcela Evaluator Turda",
		CadastralNumber: "EVAL-004", Lat: 46.5750, Lng: 23.7800, SurfaceHa: 5.5,
		DefaultCrop: "floarea-soarelui", County: "Cluj", Locality: "Turda"},
}

var apiaries = []seedApiary{
	{OwnerEmail: "apicultor@beelive.ro", Name: "Stupina Apahida",
		Type: dbsqlc.ApiaryTypePermanent, Lat: 46.7795, Lng: 23.7175, HiveCount: 16},
	{OwnerEmail: "apicultor@beelive.ro", Name: "Stupina Cluj-Napoca Sud",
		Type: dbsqlc.ApiaryTypePermanent, Lat: 46.7400, Lng: 23.5950, HiveCount: 12},
	{OwnerEmail: "apicultor@beelive.ro", Name: "Stupina Florești Vest",
		Type: dbsqlc.ApiaryTypePermanent, Lat: 46.7440, Lng: 23.4900, HiveCount: 10},
	{OwnerEmail: "apicultor@beelive.ro", Name: "Stupina Turda Centru",
		Type: dbsqlc.ApiaryTypePermanent, Lat: 46.5725, Lng: 23.7865, HiveCount: 18},
	{OwnerEmail: "apicultor@beelive.ro", Name: "Stupina Bonțida",
		Type: dbsqlc.ApiaryTypePastoral, Lat: 46.8500, Lng: 23.7900, HiveCount: 8},
}

// ── storyline ───────────────────────────────────────────────────────────────

type spraySpec struct {
	ParcelName    string
	Crop          string
	Substance     string
	Toxicity      string // "T-", "T", "T+"
	SurfaceHa     float64
	DoseKgHa      float64
	DurationHours float64
	OffsetFromNow time.Duration   // negative = past, positive = future
	Status        dbsqlc.SprayStatus
	ConfirmFrac   float64         // 0..1 of dispatches that get confirmed
	Notes         string
}

type damageSpec struct {
	ApiaryName     string
	Description    string
	HiveLoss       int32
	OffsetFromNow  time.Duration // when it was filed (negative)
	FinalStatus    dbsqlc.DamageClaimStatus
	InspectedDelta time.Duration // positive — inspection happens after filing
}

func sprays(now time.Time) []spraySpec {
	return []spraySpec{
		// Spray 1 — LIVE in_progress now (Cluj Centru). Confidor T+, 4h duration,
		// started 1h ago.
		{
			ParcelName: "Parcela Evaluator Cluj Centru",
			Crop:       "porumb", Substance: "Confidor Oil SC", Toxicity: "T+",
			SurfaceHa: 4.2, DoseKgHa: 0.40, DurationHours: 4.0,
			OffsetFromNow: -1 * hour, Status: dbsqlc.SprayStatusInProgress,
			ConfirmFrac: 0.50, Notes: "Tratament urgent — atac de dăunători.",
		},
		// Spray 2 — scheduled +24h on Apahida (DEMO TRIGGER).
		{
			ParcelName: "Parcela Evaluator Apahida",
			Crop:       "rapiță", Substance: "Decis Forte", Toxicity: "T+",
			SurfaceHa: 6.5, DoseKgHa: 0.30, DurationHours: 3.5,
			OffsetFromNow: 24 * hour, Status: dbsqlc.SprayStatusScheduled,
			ConfirmFrac: 0, Notes: "Programat pentru mâine dimineață.",
		},
		// Spray 3 — completed 2 days ago (Florești, T-).
		{
			ParcelName: "Parcela Evaluator Florești",
			Crop:       "grâu", Substance: "Karate Zeon", Toxicity: "T-",
			SurfaceHa: 7.8, DoseKgHa: 0.10, DurationHours: 2.5,
			OffsetFromNow: -2 * day, Status: dbsqlc.SprayStatusCompleted,
			ConfirmFrac: 0.80, Notes: "",
		},
		// Spray 4 — completed 5 days ago (Turda, T) — unconfirmed dispatch.
		{
			ParcelName: "Parcela Evaluator Turda",
			Crop:       "floarea-soarelui", Substance: "Mospilan 20 SG", Toxicity: "T",
			SurfaceHa: 5.5, DoseKgHa: 0.22, DurationHours: 4.0,
			OffsetFromNow: -5 * day, Status: dbsqlc.SprayStatusCompleted,
			ConfirmFrac: 0, Notes: "",
		},
		// Spray 5 — cancelled 7 days ago (Apahida).
		{
			ParcelName: "Parcela Evaluator Apahida",
			Crop:       "rapiță", Substance: "Confidor Oil SC", Toxicity: "T+",
			SurfaceHa: 6.5, DoseKgHa: 0.40, DurationHours: 4.0,
			OffsetFromNow: -7 * day, Status: dbsqlc.SprayStatusCancelled,
			ConfirmFrac: 0, Notes: "Anulat — ploaie torențială.",
		},
	}
}

func damages(now time.Time) []damageSpec {
	return []damageSpec{
		{
			ApiaryName:  "Stupina Turda Centru",
			Description: "2 stupi pierduți după aplicarea Mospilan T în zona vecină. Probe recoltate dimineața.",
			HiveLoss:    2, OffsetFromNow: -2 * hour,
			FinalStatus: dbsqlc.DamageClaimStatusFiled,
		},
		{
			ApiaryName:  "Stupina Cluj-Napoca Sud",
			Description: "1 stup pierdut după stropire T+ neanunțată în Cluj-Napoca. Albine paralizate.",
			HiveLoss:    1, OffsetFromNow: -3 * day,
			FinalStatus:    dbsqlc.DamageClaimStatusUnderReview,
			InspectedDelta: 2 * day,
		},
		{
			ApiaryName:  "Stupina Florești Vest",
			Description: "3 stupi cu mortalitate masivă după aplicarea T+ vecină. Daune confirmate.",
			HiveLoss:    3, OffsetFromNow: -30 * day,
			FinalStatus:    dbsqlc.DamageClaimStatusAccepted,
			InspectedDelta: 5 * day,
		},
	}
}

// ── main ────────────────────────────────────────────────────────────────────

func main() {
	conn := os.Getenv("DB_CONN_STR")
	if conn == "" {
		fatal("DB_CONN_STR is required")
	}
	if os.Getenv("CONFIRM_WIPE") != "yes" {
		fatal("refusing to wipe — set CONFIRM_WIPE=yes to proceed (this DELETES all users, parcels, apiaries, sprays, alerts, ledger, etc. — schema and substances stay)")
	}
	password := os.Getenv("SEED_PASSWORD")
	if password == "" {
		password = defaultPassword
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, conn)
	if err != nil {
		fatal("connect: %v", err)
	}
	defer pool.Close()
	db := stdlib.OpenDBFromPool(pool)
	q := dbsqlc.New(db)

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		fatal("bcrypt: %v", err)
	}

	// Phase 0 — ensure migration 00006 is in place.
	if err := ensureMigration6(ctx, db); err != nil {
		fatal("migration 00006: %v", err)
	}

	// Phase 1 — wipe.
	if err := wipe(ctx, db); err != nil {
		fatal("wipe: %v", err)
	}
	fmt.Println("wiped: users, parcels, apiaries, spray_reports, alert_dispatches, ledger_events, push_subscriptions, damage_claims, damage_photos, auth_challenges")
	fmt.Println("kept:  substances, goose_db_version")
	fmt.Println()

	// Phase 2 — users.
	ownerIDByEmail := map[string]uuid.UUID{}
	for _, u := range users {
		id, cnp, err := createUser(ctx, q, u, string(hash))
		if err != nil {
			fatal("user %s: %v", u.Email, err)
		}
		ownerIDByEmail[u.Email] = id
		fmt.Printf("user    created  %-22s  %-32s  cnp=%s\n", u.Role, u.Email, cnp)
	}
	fmt.Println()

	// Phase 3 — parcels.
	parcelIDByName := map[string]uuid.UUID{}
	parcelByName := map[string]seedParcel{}
	for _, p := range parcels {
		ownerID, ok := ownerIDByEmail[p.OwnerEmail]
		if !ok {
			fatal("parcel %s: owner %s missing", p.Name, p.OwnerEmail)
		}
		id, err := createParcel(ctx, q, ownerID, p)
		if err != nil {
			fatal("parcel %s: %v", p.Name, err)
		}
		parcelIDByName[p.Name] = id
		parcelByName[p.Name] = p
		fmt.Printf("parcel  created  %-32s  %.4f,%.4f  %.1f ha\n",
			p.Name, p.Lat, p.Lng, p.SurfaceHa)
	}
	fmt.Println()

	// Phase 4 — apiaries. Build the storyline ops slice as we go because
	// apiary.registered events need to anchor the chain.
	type op struct {
		when time.Time
		run  func(t time.Time) error
	}
	var ops []op

	apiaryIDByName := map[string]uuid.UUID{}
	apiaryByName := map[string]seedApiary{}
	apicultorID := ownerIDByEmail["apicultor@beelive.ro"]
	apicultorActor := apicultorID.String()
	now := time.Now().UTC()
	apiaryStartT := now.Add(-90 * day)

	for i, a := range apiaries {
		id, err := createApiary(ctx, q, apicultorID, a, apiaryStartT.Add(time.Duration(i)*7*day))
		if err != nil {
			fatal("apiary %s: %v", a.Name, err)
		}
		apiaryIDByName[a.Name] = id
		apiaryByName[a.Name] = a
		// Emit apiary.registered at ~ apiary's start date.
		registeredAt := apiaryStartT.Add(time.Duration(i) * 4 * hour)
		spec := a
		idForClosure := id
		ops = append(ops, op{when: registeredAt, run: func(t time.Time) error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			hash, err := appendEventAt(ctx, tx, "apiary.registered", &apicultorActor, map[string]any{
				"apiary_id":  idForClosure.String(),
				"owner_id":   apicultorID.String(),
				"name":       spec.Name,
				"type":       string(spec.Type),
				"lat":        spec.Lat,
				"lng":        spec.Lng,
				"hive_count": spec.HiveCount,
				"start_date": apiaryStartT.Add(time.Duration(i) * 7 * day).Format("2006-01-02"),
			}, t)
			if err != nil {
				return fmt.Errorf("apiary.registered ledger: %w", err)
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE apiaries SET ledger_hash = $1 WHERE id = $2",
				hash, idForClosure,
			); err != nil {
				return err
			}
			return tx.Commit()
		}})
		fmt.Printf("apiary  created  %-28s  %.4f,%.4f  hives=%d\n",
			a.Name, a.Lat, a.Lng, a.HiveCount)
	}
	fmt.Println()

	// Phase 5 — sprays.
	farmerID := ownerIDByEmail["fermier@beelive.ro"]
	farmerActor := farmerID.String()
	inspectorID := ownerIDByEmail["inspector@beelive.ro"]
	inspectorActor := inspectorID.String()

	for _, sp := range sprays(now) {
		parcel, ok := parcelByName[sp.ParcelName]
		if !ok {
			fatal("spray %s: parcel %s missing", sp.Substance, sp.ParcelName)
		}
		parcelID := parcelIDByName[sp.ParcelName]
		sprayID := uuid.New()

		var createdAt time.Time
		switch sp.Status {
		case dbsqlc.SprayStatusScheduled:
			createdAt = now.Add(-2 * hour)
		case dbsqlc.SprayStatusInProgress:
			createdAt = now.Add(sp.OffsetFromNow) // offset is negative (e.g. -1h)
		default:
			createdAt = now.Add(sp.OffsetFromNow).Add(-1 * day) // announced 1 day ahead
		}
		scheduledAt := now.Add(sp.OffsetFromNow)

		spLocal := sp
		parcelLocal := parcel
		ops = append(ops, op{when: createdAt, run: func(t time.Time) error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			sprayHash, err := appendEventAt(ctx, tx, "spray.created", &farmerActor, map[string]any{
				"spray_id":   sprayID.String(),
				"substance":  spLocal.Substance,
				"toxicity":   spLocal.Toxicity,
				"parcel_id":  parcelID.String(),
				"surface_ha": spLocal.SurfaceHa,
			}, t)
			if err != nil {
				return err
			}
			var notes sql.NullString
			if spLocal.Notes != "" {
				notes = sql.NullString{String: spLocal.Notes, Valid: true}
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO spray_reports
				  (id, farmer_id, parcel_id, crop, substance, toxicity, surface_ha,
				   dose_kg_ha, scheduled_at, duration_hours, notes, ledger_hash, status, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
				sprayID, farmerID, parcelID,
				spLocal.Crop, spLocal.Substance, spLocal.Toxicity, spLocal.SurfaceHa,
				spLocal.DoseKgHa, scheduledAt, spLocal.DurationHours, notes,
				sprayHash, string(spLocal.Status), t,
			); err != nil {
				return err
			}
			_ = parcelLocal
			return tx.Commit()
		}})

		// Cancelled sprays only emit spray.cancelled afterwards (no dispatches).
		if sp.Status == dbsqlc.SprayStatusCancelled {
			cancelAt := createdAt.Add(6 * hour)
			ops = append(ops, op{when: cancelAt, run: func(t time.Time) error {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err := appendEventAt(ctx, tx, "spray.cancelled", &farmerActor, map[string]any{
					"spray_id": sprayID.String(),
				}, t); err != nil {
					return err
				}
				return tx.Commit()
			}})
			continue
		}

		// Scheduled sprays don't yet have dispatches.
		if sp.Status == dbsqlc.SprayStatusScheduled {
			continue
		}

		// Compute risk radius based on toxicity (used for dispatch filtering).
		var radius float64
		switch sp.Toxicity {
		case "T+":
			radius = t_plus_radius_m
		case "T":
			radius = tox_radius_m
		default:
			radius = t_minus_radius_m
		}

		// Find apiaries in radius.
		type aWithDist struct {
			id   uuid.UUID
			a    seedApiary
			dist float64
		}
		var nearby []aWithDist
		for _, a := range apiaries {
			d := services.Haversine(parcel.Lat, parcel.Lng, a.Lat, a.Lng)
			if d <= radius {
				nearby = append(nearby, aWithDist{id: apiaryIDByName[a.Name], a: a, dist: d})
			}
		}
		sort.Slice(nearby, func(i, j int) bool { return nearby[i].dist < nearby[j].dist })

		// Dispatch each, with chronological ledger events.
		dispatchSpawnAt := createdAt.Add(2 * time.Minute)
		affected := 0
		for i, n := range nearby {
			dispatchID := uuid.New()
			callState := dbsqlc.CallStateQueued
			smsState := dbsqlc.SmsStateSent
			if sp.Toxicity == "T-" {
				callState = dbsqlc.CallStateSkipped
			}
			dispatchedAt := dispatchSpawnAt.Add(time.Duration(i) * 30 * time.Second)
			confirmedAt := dispatchedAt.Add(15 * time.Minute)

			shouldConfirm := float64(i) < float64(len(nearby))*sp.ConfirmFrac
			var finalStatus dbsqlc.NullFinalStatus
			confirmMethod := "app"
			if shouldConfirm && sp.Status == dbsqlc.SprayStatusCompleted {
				switch i % 3 {
				case 0:
					finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedCall, Valid: true}
					callState = dbsqlc.CallStateConfirmed
					confirmMethod = "call"
				case 1:
					finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedSms, Valid: true}
					smsState = dbsqlc.SmsStateConfirmed
					confirmMethod = "sms"
				case 2:
					finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedApp, Valid: true}
					confirmMethod = "app"
				}
			} else if sp.Status == dbsqlc.SprayStatusCompleted {
				finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusUnconfirmed, Valid: true}
			}

			nLocal := n
			finalStatusLocal := finalStatus
			callStateLocal := callState
			smsStateLocal := smsState
			toxLocal := sp.Toxicity
			ops = append(ops, op{when: dispatchedAt, run: func(t time.Time) error {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				dispatchHash, err := appendEventAt(ctx, tx, "alert.dispatched", &farmerActor, map[string]any{
					"dispatch_id": dispatchID.String(),
					"spray_id":    sprayID.String(),
					"apiary_id":   nLocal.id.String(),
					"distance_m":  nLocal.dist,
					"toxicity":    toxLocal,
				}, t)
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO alert_dispatches
					  (id, spray_report_id, beekeeper_id, apiary_id, distance_m, downwind,
					   call_state, sms_state, ledger_hash, final_status, created_at)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
					dispatchID, sprayID, apicultorID, nLocal.id, nLocal.dist, i%2 == 0,
					string(callStateLocal), string(smsStateLocal), dispatchHash,
					sql.NullString{String: string(finalStatusLocal.FinalStatus), Valid: finalStatusLocal.Valid},
					t,
				); err != nil {
					return err
				}
				return tx.Commit()
			}})

			if finalStatus.Valid && finalStatus.FinalStatus != dbsqlc.FinalStatusUnconfirmed {
				methodLocal := confirmMethod
				ops = append(ops, op{when: confirmedAt, run: func(t time.Time) error {
					tx, err := db.BeginTx(ctx, nil)
					if err != nil {
						return err
					}
					defer tx.Rollback()
					if _, err := appendEventAt(ctx, tx, "alert.confirmed", &apicultorActor, map[string]any{
						"dispatch_id": dispatchID.String(),
						"spray_id":    sprayID.String(),
						"method":      methodLocal,
					}, t); err != nil {
						return err
					}
					return tx.Commit()
				}})
			}
			affected++
		}

		if affected > 0 {
			finalizeAt := dispatchSpawnAt.Add(time.Duration(affected)*30*time.Second + time.Minute)
			affectedLocal := affected
			ops = append(ops, op{when: finalizeAt, run: func(_ time.Time) error {
				_, err := db.ExecContext(ctx,
					"UPDATE spray_reports SET affected_apiaries_count = $1 WHERE id = $2",
					affectedLocal, sprayID,
				)
				return err
			}})
		}

		// Completed sprays emit pdf.generated + email.sent.
		if sp.Status == dbsqlc.SprayStatusCompleted {
			pdfAt := createdAt.Add(20 * time.Minute)
			emailAt := pdfAt.Add(2 * time.Minute)
			ops = append(ops, op{when: pdfAt, run: func(t time.Time) error {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err := appendEventAt(ctx, tx, "pdf.generated", &farmerActor, map[string]any{
					"spray_id": sprayID.String(),
					"path":     fmt.Sprintf("./uploads/pdfs/%s.pdf", sprayID),
				}, t); err != nil {
					return err
				}
				return tx.Commit()
			}})
			ops = append(ops, op{when: emailAt, run: func(t time.Time) error {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err := appendEventAt(ctx, tx, "email.sent", &farmerActor, map[string]any{
					"spray_id":  sprayID.String(),
					"recipient": "primarie@example.ro",
				}, t); err != nil {
					return err
				}
				return tx.Commit()
			}})
		}

		fmt.Printf("spray   created  %-20s  %-3s  %-22s  status=%s  nearby=%d\n",
			sp.Substance, sp.Toxicity, sp.ParcelName, sp.Status, len(nearby))
	}

	// Phase 6 — damages.
	for _, d := range damages(now) {
		damageID := uuid.New()
		apiaryID, ok := apiaryIDByName[d.ApiaryName]
		if !ok {
			fatal("damage: apiary %s missing", d.ApiaryName)
		}
		apiarySpec := apiaryByName[d.ApiaryName]
		filedAt := now.Add(d.OffsetFromNow)
		descLocal := d.Description
		hiveLossLocal := d.HiveLoss
		ops = append(ops, op{when: filedAt, run: func(t time.Time) error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			ledgerHash, err := appendEventAt(ctx, tx, "damage.filed", &apicultorActor, map[string]any{
				"claim_id":        damageID.String(),
				"beekeeper_id":    apicultorID.String(),
				"apiary_id":       apiaryID.String(),
				"hive_loss_count": hiveLossLocal,
				"photo_count":     2,
			}, t)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO damage_claims
				  (id, beekeeper_id, apiary_id, related_spray_id, description,
				   hive_loss_count, gps_lat, gps_lng, status, ledger_hash, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				damageID, apicultorID, apiaryID, uuid.NullUUID{},
				descLocal, hiveLossLocal, apiarySpec.Lat, apiarySpec.Lng,
				string(dbsqlc.DamageClaimStatusFiled), ledgerHash, t,
			); err != nil {
				return err
			}
			// 2 placeholder photos via stable Picsum URLs.
			for i := 0; i < 2; i++ {
				key := fmt.Sprintf("https://picsum.photos/seed/eval-%s-%d/800/600", damageID.String()[:8], i)
				if _, err := tx.ExecContext(ctx,
					"INSERT INTO damage_photos (id, damage_claim_id, url) VALUES ($1, $2, $3)",
					uuid.New(), damageID, key,
				); err != nil {
					return err
				}
			}
			return tx.Commit()
		}})

		if d.FinalStatus != dbsqlc.DamageClaimStatusFiled {
			inspectedAt := filedAt.Add(d.InspectedDelta)
			finalStatusLocal := d.FinalStatus
			ops = append(ops, op{when: inspectedAt, run: func(t time.Time) error {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err := appendEventAt(ctx, tx, "damage.inspected", &inspectorActor, map[string]any{
					"claim_id":     damageID.String(),
					"apiary_id":    apiaryID.String(),
					"beekeeper_id": apicultorID.String(),
					"inspector_id": inspectorActor,
				}, t); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx,
					"UPDATE damage_claims SET status = $1 WHERE id = $2",
					string(finalStatusLocal), damageID,
				); err != nil {
					return err
				}
				return tx.Commit()
			}})
		}

		fmt.Printf("damage  created  %-30s  hives=%d  status=%s\n",
			d.ApiaryName, d.HiveLoss, d.FinalStatus)
	}
	fmt.Println()

	// Phase 7 — sort + execute ops in strictly monotonic chronological order.
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].when.Before(ops[j].when) })
	if len(ops) > 1 {
		for i := 1; i < len(ops); i++ {
			if !ops[i].when.After(ops[i-1].when) {
				ops[i].when = ops[i-1].when.Add(time.Microsecond)
			}
		}
	}
	for i, o := range ops {
		if err := o.run(o.when); err != nil {
			fatal("op %d (when=%s): %v", i, o.when.Format(time.RFC3339), err)
		}
	}

	fmt.Printf("\ndone — %d ledger ops executed; chain integrity should be valid.\n", len(ops))
	fmt.Println()
	fmt.Println("Login URLs:")
	fmt.Println("  https://beelive.ro/login")
	fmt.Println()
	fmt.Println("Accounts (parolă: " + password + "):")
	for _, u := range users {
		fmt.Printf("  %-9s  %s\n", u.Role, u.Email)
	}
	fmt.Println()
	fmt.Println("2FA: 000000 (bypass activ global per Alin's recent commit)")
}

// ── wipe ────────────────────────────────────────────────────────────────────

func wipe(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		TRUNCATE TABLE
			alert_dispatches,
			push_subscriptions,
			damage_photos,
			damage_claims,
			ledger_events,
			spray_reports,
			auth_challenges,
			apiaries,
			parcels,
			users
		RESTART IDENTITY CASCADE
	`)
	return err
}

// ── migration 00006 ────────────────────────────────────────────────────────

func ensureMigration6(ctx context.Context, db *sql.DB) error {
	var lastVersion int64
	if err := db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version",
	).Scan(&lastVersion); err != nil {
		return fmt.Errorf("check goose version: %w", err)
	}
	if lastVersion >= 6 {
		fmt.Println("migration 00006 — already applied")
		return nil
	}
	if _, err := db.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_key ON users (LOWER(email))`,
	); err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (6, true, NOW())`,
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	fmt.Println("migration 00006 — applied")
	return nil
}

// ── user/parcel/apiary creation ────────────────────────────────────────────

func createUser(ctx context.Context, q *dbsqlc.Queries, u seedUser, hash string) (uuid.UUID, string, error) {
	cnp := randomCNP()
	out, err := q.CreateUser(ctx, dbsqlc.CreateUserParams{
		ID: uuid.New(), Cnp: cnp,
		FullName: u.FullName, Email: u.Email, Phone: u.Phone,
		Role: u.Role, County: u.County, Locality: u.Locality,
		PasswordHash: hash,
	})
	return out.ID, cnp, err
}

func createParcel(ctx context.Context, q *dbsqlc.Queries, ownerID uuid.UUID, p seedParcel) (uuid.UUID, error) {
	var crop sql.NullString
	if p.DefaultCrop != "" {
		crop = sql.NullString{String: p.DefaultCrop, Valid: true}
	}
	out, err := q.CreateParcel(ctx, dbsqlc.CreateParcelParams{
		ID: uuid.New(), OwnerID: ownerID,
		Name: p.Name, CadastralNumber: p.CadastralNumber,
		Lat: p.Lat, Lng: p.Lng, SurfaceHa: p.SurfaceHa,
		DefaultCrop: crop, County: p.County, Locality: p.Locality,
	})
	return out.ID, err
}

func createApiary(ctx context.Context, q *dbsqlc.Queries, ownerID uuid.UUID, a seedApiary, startDate time.Time) (uuid.UUID, error) {
	out, err := q.CreateApiary(ctx, dbsqlc.CreateApiaryParams{
		ID: uuid.New(), OwnerID: ownerID,
		Name: a.Name, Type: a.Type,
		Lat: a.Lat, Lng: a.Lng,
		HiveCount: a.HiveCount,
		StartDate: startDate,
		EndDate:   sql.NullTime{},
		Notes:     sql.NullString{},
	})
	return out.ID, err
}

// ── ledger helpers (chain-safe insert at explicit time) ────────────────────

func appendEventAt(
	ctx context.Context, tx *sql.Tx,
	eventType string, actorID *string, payload any,
	createdAt time.Time,
) (string, error) {
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(42)"); err != nil {
		return "", fmt.Errorf("advisory lock: %w", err)
	}
	q := dbsqlc.New(tx)
	prevHash := ""
	last, err := q.GetLastLedgerEvent(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil {
		prevHash = last.Hash
	}
	canonical, err := canonicalJSON(payload)
	if err != nil {
		return "", err
	}
	hash := sha256hex(prevHash + canonical + createdAt.UTC().Format(time.RFC3339))

	var prevHashNull sql.NullString
	if prevHash != "" {
		prevHashNull = sql.NullString{String: prevHash, Valid: true}
	}
	var actorIDNull uuid.NullUUID
	if actorID != nil {
		if parsed, perr := uuid.Parse(*actorID); perr == nil {
			actorIDNull = uuid.NullUUID{UUID: parsed, Valid: true}
		}
	}
	payloadJSON, _ := json.Marshal(payload)
	if _, err := q.InsertLedgerEvent(ctx, dbsqlc.InsertLedgerEventParams{
		ID:        uuid.New(),
		Hash:      hash,
		PrevHash:  prevHashNull,
		Type:      eventType,
		ActorID:   actorIDNull,
		Payload:   json.RawMessage(payloadJSON),
		CreatedAt: createdAt.UTC(),
	}); err != nil {
		return "", fmt.Errorf("insert event: %w", err)
	}
	return hash, nil
}

func canonicalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var m any
	if err := json.Unmarshal(b, &m); err != nil {
		return "", err
	}
	b2, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b2), nil
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum)
}

// ── helpers ─────────────────────────────────────────────────────────────────

func randomCNP() string {
	sex := randInt(1, 2)
	year := randInt(80, 99)
	month := randInt(1, 12)
	day := randInt(1, 28)
	jj := 12
	nnn := randInt(1, 999)
	check := randInt(0, 9)
	return fmt.Sprintf("%d%02d%02d%02d%02d%03d%d", sex, year, month, day, jj, nnn, check)
}

func randInt(min, max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	return int(n.Int64()) + min
}

func fatal(format string, args ...any) {
	slog.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}

// binary trick to silence unused import warning for binary
var _ = binary.BigEndian
