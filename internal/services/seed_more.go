package services

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
)

// SeedMore populates a rich demo dataset on top of the base Seed():
//
//   - 10 spray reports across the 3 seeded farmers, mixing toxicities
//     (T-, T, T+) and statuses (scheduled, in_progress, completed,
//     cancelled), scheduled across a -45d..+14d window.
//   - Alert dispatches for every active/completed spray, fanning out to
//     every seeded apiary within 12 km of the parcel. A mix of confirmation
//     outcomes (call, SMS, in-app, unconfirmed).
//   - 2 damage claims tied to completed sprays.
//   - Properly chained ledger events (spray.created, alert.dispatched,
//     alert.confirmed, pdf.generated, email.sent, damage.filed) so
//     /events/verify returns valid:true.
//
// Idempotent: if Vasile already has spray reports, the function logs and
// returns without doing anything.
func SeedMore(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	q := dbsqlc.New(sqlDB)
	ledgerSvc := NewLedgerService(pool)

	vasile, err := q.GetUserByCNP(ctx, "1920412111222")
	if err != nil {
		return fmt.Errorf("seed-more: GetUserByCNP vasile: %w", err)
	}
	elena, err := q.GetUserByCNP(ctx, "2880721333444")
	if err != nil {
		return fmt.Errorf("seed-more: GetUserByCNP elena: %w", err)
	}
	gheorghe, err := q.GetUserByCNP(ctx, "1751103555666")
	if err != nil {
		return fmt.Errorf("seed-more: GetUserByCNP gheorghe: %w", err)
	}

	// ── Extra apiaries: spread across Cluj county so all 3 farmers' parcels
	// have nearby beekeepers to dispatch to. Idempotent per (owner, name).
	if err := seedExtraApiaries(ctx, sqlDB, q, ledgerSvc); err != nil {
		return err
	}

	// ── Backfill demo photos for any damage that has none. Runs every
	// invocation so adding photos to old damages doesn't require a wipe.
	if err := seedDemoPhotos(ctx, sqlDB, q); err != nil {
		return err
	}

	existingSprays, err := q.ListSprayReportsByFarmer(ctx, vasile.ID)
	if err != nil {
		return fmt.Errorf("seed-more: ListSprayReportsByFarmer: %w", err)
	}
	if len(existingSprays) > 0 {
		slog.Info("seed-more: spray reports already exist for Vasile — skipping spray phase", "count", len(existingSprays))
		return nil
	}

	parcels, err := q.ListAllParcels(ctx)
	if err != nil {
		return fmt.Errorf("seed-more: list parcels: %w", err)
	}
	parcelByName := map[string]dbsqlc.Parcel{}
	for _, p := range parcels {
		parcelByName[p.Name] = p
	}

	apiaries, err := q.ListAllApiaries(ctx)
	if err != nil {
		return fmt.Errorf("seed-more: list apiaries: %w", err)
	}

	now := time.Now().UTC()

	type spraySpec struct {
		farmer        dbsqlc.User
		parcelName    string
		crop          string
		substance     string
		toxicity      string
		surfaceHa     float64
		doseKgHa      float64
		scheduledAt   time.Time
		durationHours float64
		status        dbsqlc.SprayStatus
		notes         string
	}

	sprays := []spraySpec{
		// Vasile — completed historicals + one active + one upcoming
		{vasile, "Parcela Câmpia Nord", "grâu", "Mospilan 20 SG", "T", 5.5, 0.25,
			now.AddDate(0, 0, -28), 4.0, dbsqlc.SprayStatusCompleted,
			"Aplicare matinală, vânt scăzut."},
		{vasile, "Parcela Câmpia Sud", "rapiță", "Decis Forte", "T+", 7.2, 0.30,
			now.AddDate(0, 0, -14), 3.5, dbsqlc.SprayStatusCompleted,
			"Tratament de înflorire — atenție la pierderi."},
		{vasile, "Parcela Câmpia Nord", "grâu", "Karate Zeon", "T-", 5.5, 0.10,
			now.AddDate(0, 0, -5), 2.5, dbsqlc.SprayStatusCompleted, ""},
		{vasile, "Parcela Câmpia Sud", "rapiță", "Confidor Oil SC", "T+", 7.2, 0.40,
			now.AddDate(0, 0, 3), 4.0, dbsqlc.SprayStatusScheduled,
			"Programat pentru săptămâna viitoare."},

		// Elena — mix of statuses including a cancelled one
		{elena, "Parcela Gherla Est", "porumb", "Actara 25 WG", "T", 4.8, 0.20,
			now.AddDate(0, 0, -21), 3.0, dbsqlc.SprayStatusCompleted, ""},
		{elena, "Parcela Gherla Vest", "floarea-soarelui", "Movento OD", "T-", 6.1, 0.15,
			now.AddDate(0, 0, -8), 4.0, dbsqlc.SprayStatusCompleted, ""},
		{elena, "Parcela Gherla Est", "porumb", "Mospilan 20 SG", "T", 4.8, 0.22,
			now.AddDate(0, 0, -10), 3.0, dbsqlc.SprayStatusCancelled,
			"Anulat — ploaie torentială."},
		{elena, "Parcela Gherla Vest", "floarea-soarelui", "Decis Forte", "T+", 6.1, 0.28,
			now.AddDate(0, 0, 7), 3.5, dbsqlc.SprayStatusScheduled, ""},

		// Gheorghe — one in progress, one completed
		{gheorghe, "Parcela Dej 1", "rapiță", "Confidor Oil SC", "T+", 8.3, 0.40,
			now.Add(-2 * time.Hour), 5.0, dbsqlc.SprayStatusInProgress,
			"În curs de aplicare."},
		{gheorghe, "Parcela Dej 2", "grâu", "Karate Zeon", "T-", 5.0, 0.10,
			now.AddDate(0, 0, -35), 2.5, dbsqlc.SprayStatusCompleted, ""},
	}

	created := 0
	for _, sp := range sprays {
		parcel, ok := parcelByName[sp.parcelName]
		if !ok {
			slog.Warn("seed-more: parcel not found, skipping spray", "parcel", sp.parcelName)
			continue
		}

		sprayID := uuid.New()
		actorID := sp.farmer.ID.String()

		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("seed-more: begin tx: %w", err)
		}
		qtx := dbsqlc.New(tx)

		var notes sql.NullString
		if sp.notes != "" {
			notes = sql.NullString{String: sp.notes, Valid: true}
		}

		sprayHash, err := ledgerSvc.Append(ctx, tx, "spray.created", &actorID, map[string]any{
			"spray_id":   sprayID.String(),
			"substance":  sp.substance,
			"toxicity":   sp.toxicity,
			"parcel_id":  parcel.ID.String(),
			"surface_ha": sp.surfaceHa,
		})
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("seed-more: ledger spray.created: %w", err)
		}

		if _, err := qtx.CreateSprayReport(ctx, dbsqlc.CreateSprayReportParams{
			ID:            sprayID,
			FarmerID:      sp.farmer.ID,
			ParcelID:      parcel.ID,
			Crop:          sp.crop,
			Substance:     sp.substance,
			Toxicity:      sp.toxicity,
			SurfaceHa:     sp.surfaceHa,
			DoseKgHa:      sp.doseKgHa,
			ScheduledAt:   sp.scheduledAt,
			DurationHours: sp.durationHours,
			Notes:         notes,
			LedgerHash:    sprayHash,
		}); err != nil {
			tx.Rollback()
			return fmt.Errorf("seed-more: create spray: %w", err)
		}

		if sp.status != dbsqlc.SprayStatusScheduled {
			if err := qtx.UpdateSprayReportStatus(ctx, dbsqlc.UpdateSprayReportStatusParams{
				ID:     sprayID,
				Status: sp.status,
			}); err != nil {
				tx.Rollback()
				return fmt.Errorf("seed-more: update status: %w", err)
			}
		}

		// Cancelled sprays don't generate dispatches — they aborted before
		// notifying anyone.
		if sp.status == dbsqlc.SprayStatusCancelled {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("seed-more: commit cancelled spray: %w", err)
			}
			created++
			continue
		}

		// Fan out to every apiary within 12 km. Sort by distance so the
		// closest gets the "primary" dispatch (and any confirmed status).
		type aWithDist struct {
			a    dbsqlc.Apiary
			dist float64
		}
		var nearby []aWithDist
		for _, a := range apiaries {
			d := Haversine(parcel.Lat, parcel.Lng, a.Lat, a.Lng)
			if d <= 12000 {
				nearby = append(nearby, aWithDist{a, d})
			}
		}
		sort.Slice(nearby, func(i, j int) bool { return nearby[i].dist < nearby[j].dist })

		affected := 0
		for i, n := range nearby {
			dispatchID := uuid.New()

			// Decide a confirmation outcome based on position + status.
			// Closest apiary on a completed spray confirms via call/sms/app
			// (rotating); further ones may stay unconfirmed.
			finalStatus := dbsqlc.NullFinalStatus{}
			callState := dbsqlc.CallStateQueued
			smsState := dbsqlc.SmsStateSent
			if sp.toxicity == "T-" {
				callState = dbsqlc.CallStateSkipped
			}

			if sp.status == dbsqlc.SprayStatusCompleted {
				switch i % 4 {
				case 0:
					finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedCall, Valid: true}
					callState = dbsqlc.CallStateConfirmed
				case 1:
					finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedSms, Valid: true}
					smsState = dbsqlc.SmsStateConfirmed
				case 2:
					finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedApp, Valid: true}
				case 3:
					finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusUnconfirmed, Valid: true}
				}
			}

			dispatchHash, err := ledgerSvc.Append(ctx, tx, "alert.dispatched", &actorID, map[string]any{
				"dispatch_id": dispatchID.String(),
				"spray_id":    sprayID.String(),
				"apiary_id":   n.a.ID.String(),
				"distance_m":  n.dist,
				"toxicity":    sp.toxicity,
			})
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("seed-more: ledger alert.dispatched: %w", err)
			}

			if _, err := qtx.CreateAlertDispatch(ctx, dbsqlc.CreateAlertDispatchParams{
				ID:            dispatchID,
				SprayReportID: sprayID,
				BeekeeperID:   n.a.OwnerID,
				ApiaryID:      n.a.ID,
				DistanceM:     n.dist,
				Downwind:      i%2 == 0,
				CallState:     callState,
				SmsState:      smsState,
				LedgerHash:    dispatchHash,
			}); err != nil {
				tx.Rollback()
				return fmt.Errorf("seed-more: create dispatch: %w", err)
			}

			if finalStatus.Valid {
				if err := qtx.UpdateFinalStatus(ctx, dbsqlc.UpdateFinalStatusParams{
					ID:          dispatchID,
					FinalStatus: finalStatus,
				}); err != nil {
					tx.Rollback()
					return fmt.Errorf("seed-more: update final status: %w", err)
				}

				if finalStatus.FinalStatus != dbsqlc.FinalStatusUnconfirmed {
					beekeeperActor := n.a.OwnerID.String()
					method := "app"
					switch finalStatus.FinalStatus {
					case dbsqlc.FinalStatusConfirmedCall:
						method = "call"
					case dbsqlc.FinalStatusConfirmedSms:
						method = "sms"
					}
					if _, err := ledgerSvc.Append(ctx, tx, "alert.confirmed", &beekeeperActor, map[string]any{
						"dispatch_id": dispatchID.String(),
						"spray_id":    sprayID.String(),
						"method":      method,
					}); err != nil {
						tx.Rollback()
						return fmt.Errorf("seed-more: ledger alert.confirmed: %w", err)
					}
				}
			}

			affected++
		}

		if affected > 0 {
			if err := qtx.UpdateSprayReportAffectedCount(ctx, dbsqlc.UpdateSprayReportAffectedCountParams{
				ID:                    sprayID,
				AffectedApiariesCount: int32(affected),
			}); err != nil {
				tx.Rollback()
				return fmt.Errorf("seed-more: update affected count: %w", err)
			}
		}

		// PDF + email events for completed sprays only (in_progress sprays
		// haven't generated paperwork yet).
		if sp.status == dbsqlc.SprayStatusCompleted {
			if _, err := ledgerSvc.Append(ctx, tx, "pdf.generated", &actorID, map[string]any{
				"spray_id": sprayID.String(),
				"path":     fmt.Sprintf("./uploads/pdfs/%s.pdf", sprayID.String()),
			}); err != nil {
				tx.Rollback()
				return fmt.Errorf("seed-more: ledger pdf.generated: %w", err)
			}
			if _, err := ledgerSvc.Append(ctx, tx, "email.sent", &actorID, map[string]any{
				"spray_id":  sprayID.String(),
				"recipient": "primarie@example.ro",
			}); err != nil {
				tx.Rollback()
				return fmt.Errorf("seed-more: ledger email.sent: %w", err)
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("seed-more: commit spray %s: %w", sp.parcelName, err)
		}
		created++
		slog.Info("seed-more: created spray", "substance", sp.substance,
			"status", sp.status, "parcel", sp.parcelName, "dispatches", affected)
	}

	// ── Damage claims ────────────────────────────────────────────────────────
	// Tie 2 damage claims to historical completed sprays so the inspector view
	// has something to render.
	allSprays, err := q.ListAllSprayReports(ctx)
	if err != nil {
		return fmt.Errorf("seed-more: list all sprays: %w", err)
	}
	var completedSprays []dbsqlc.SprayReport
	for _, s := range allSprays {
		if s.Status == dbsqlc.SprayStatusCompleted {
			completedSprays = append(completedSprays, s)
		}
	}

	type damageSpec struct {
		beekeeperCNP string
		apiaryName   string
		description  string
		hiveLoss     int32
	}
	damages := []damageSpec{
		{"1850101123456", "Stupina Apahida Sud",
			"3 stupi cu mortalitate masivă a culegătoarelor după aplicarea T+. " +
				"Probe de albine moarte recoltate dimineața.", 3},
		{"2900215654321", "Stupina Florești Est",
			"Reducere drastică a populației la 2 familii — semne clare de intoxicație.", 2},
		{"1780530987654", "Stupina Turda Centru",
			"O familie pierdută complet, 2 cu albine paralizate. Coincidență cu " +
				"stropirea T din parcela vecină.", 1},
		{"1850101123456", "Stupina Bonțida Sud",
			"Populație redusă la jumătate după tratamentul T+ aplicat în Câmpia Turzii.", 2},
	}

	damageCreated := 0
	for i, d := range damages {
		if i >= len(completedSprays) {
			break
		}
		beekeeper, err := q.GetUserByCNP(ctx, d.beekeeperCNP)
		if err != nil {
			slog.Warn("seed-more: damage beekeeper missing", "cnp", d.beekeeperCNP)
			continue
		}
		var apiary dbsqlc.Apiary
		found := false
		for _, a := range apiaries {
			if a.Name == d.apiaryName {
				apiary = a
				found = true
				break
			}
		}
		if !found {
			slog.Warn("seed-more: damage apiary missing", "apiary", d.apiaryName)
			continue
		}

		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("seed-more: begin damage tx: %w", err)
		}
		qtx := dbsqlc.New(tx)
		actorID := beekeeper.ID.String()
		damageID := uuid.New()
		sprayRef := completedSprays[i]

		damageHash, err := ledgerSvc.Append(ctx, tx, "damage.filed", &actorID, map[string]any{
			"damage_id":  damageID.String(),
			"apiary_id":  apiary.ID.String(),
			"spray_id":   sprayRef.ID.String(),
			"hive_loss":  d.hiveLoss,
		})
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("seed-more: ledger damage.filed: %w", err)
		}

		if _, err := qtx.CreateDamageClaim(ctx, dbsqlc.CreateDamageClaimParams{
			ID:             damageID,
			BeekeeperID:    beekeeper.ID,
			ApiaryID:       apiary.ID,
			RelatedSprayID: uuid.NullUUID{UUID: sprayRef.ID, Valid: true},
			Description:    d.description,
			HiveLossCount:  d.hiveLoss,
			GpsLat:         apiary.Lat,
			GpsLng:         apiary.Lng,
			LedgerHash:     damageHash,
		}); err != nil {
			tx.Rollback()
			return fmt.Errorf("seed-more: create damage: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("seed-more: commit damage: %w", err)
		}
		damageCreated++
		slog.Info("seed-more: created damage claim",
			"beekeeper", beekeeper.FullName, "apiary", apiary.Name, "hive_loss", d.hiveLoss)
	}

	// Photos backfill ran earlier (before spray phase) so it covers existing
	// damages even when this is a re-run.
	if err := seedDemoPhotos(ctx, sqlDB, q); err != nil {
		return err
	}

	slog.Info("seed-more complete", "sprays", created, "damages", damageCreated)
	return nil
}

// seedDemoPhotos attaches 2 placeholder photos to every existing damage_claim
// that has zero photos. Uses stable Picsum URLs so the inspector view shows
// thumbnails without needing actual R2 uploads.
func seedDemoPhotos(ctx context.Context, sqlDB *sql.DB, q *dbsqlc.Queries) error {
	allClaims, err := q.ListAllDamageClaims(ctx)
	if err != nil {
		return fmt.Errorf("seed-more photos: list claims: %w", err)
	}
	// Two distinct seeds per claim so each gets its own pair of stable images.
	urlBank := []string{
		"https://picsum.photos/seed/beehive1/800/600",
		"https://picsum.photos/seed/beehive2/800/600",
		"https://picsum.photos/seed/beehive3/800/600",
		"https://picsum.photos/seed/beehive4/800/600",
		"https://picsum.photos/seed/beehive5/800/600",
		"https://picsum.photos/seed/beehive6/800/600",
		"https://picsum.photos/seed/beehive7/800/600",
		"https://picsum.photos/seed/beehive8/800/600",
	}
	added := 0
	for i, c := range allClaims {
		existing, err := q.ListDamagePhotos(ctx, c.ID)
		if err != nil {
			return fmt.Errorf("seed-more photos: list photos: %w", err)
		}
		if len(existing) > 0 {
			continue
		}
		url1 := urlBank[(i*2)%len(urlBank)]
		url2 := urlBank[(i*2+1)%len(urlBank)]
		for _, u := range []string{url1, url2} {
			if _, err := q.AddDamagePhoto(ctx, dbsqlc.AddDamagePhotoParams{
				ID:            uuid.New(),
				DamageClaimID: c.ID,
				Url:           u,
			}); err != nil {
				return fmt.Errorf("seed-more photos: add: %w", err)
			}
			added++
		}
	}
	slog.Info("seed-more: demo photos backfilled", "added", added)
	return nil
}

// seedExtraApiaries adds 14 apiaries spread across Cluj county on top of the 6
// from the base seed, so every seeded parcel has neighbours within ~12 km. Each
// new apiary emits an apiary.registered ledger event so the chain stays valid.
// Idempotent: skips any (owner, name) pair that already exists.
func seedExtraApiaries(ctx context.Context, sqlDB *sql.DB, q *dbsqlc.Queries, ledgerSvc *LedgerService) error {
	type extraSpec struct {
		ownerCNP  string
		name      string
		apiType   dbsqlc.ApiaryType
		lat, lng  float64
		hiveCount int32
		startYear int
	}
	specs := []extraSpec{
		// Around Apahida / Bonțida — Andrei
		{"1850101123456", "Stupina Apahida Vest", dbsqlc.ApiaryTypePermanent, 46.7785, 23.6920, 22, 2022},
		{"1850101123456", "Stupina Bonțida Sud", dbsqlc.ApiaryTypePastoral, 46.8530, 23.7910, 18, 2024},

		// Around Florești / Gilău / Cluj-Napoca — Maria
		{"2900215654321", "Stupina Florești Centru", dbsqlc.ApiaryTypePermanent, 46.7448, 23.5012, 30, 2021},
		{"2900215654321", "Stupina Gilău Est", dbsqlc.ApiaryTypePermanent, 46.7405, 23.4310, 25, 2020},
		{"2900215654321", "Stupina Cluj-Napoca Sud", dbsqlc.ApiaryTypePastoral, 46.7401, 23.5912, 14, 2023},

		// Around Turda / Mihai Viteazu — Ioan
		{"1780530987654", "Stupina Câmpia Turzii Est", dbsqlc.ApiaryTypePermanent, 46.5610, 23.9080, 24, 2022},
		{"1780530987654", "Stupina Câmpia Turzii Vest", dbsqlc.ApiaryTypePastoral, 46.5520, 23.8615, 20, 2024},
		{"1780530987654", "Stupina Mihai Viteazu", dbsqlc.ApiaryTypePermanent, 46.5102, 23.6790, 28, 2021},

		// Near Gherla — so Elena's parcels dispatch to someone
		{"1850101123456", "Stupina Gherla Vest", dbsqlc.ApiaryTypePermanent, 47.0260, 23.8930, 18, 2023},
		{"1850101123456", "Stupina Iclod", dbsqlc.ApiaryTypePastoral, 47.0015, 23.8528, 14, 2024},

		// Near Dej — so Gheorghe's parcels dispatch
		{"2900215654321", "Stupina Dej Sud", dbsqlc.ApiaryTypePermanent, 47.1108, 23.8505, 22, 2022},
		{"2900215654321", "Stupina Mintiu Gherlii", dbsqlc.ApiaryTypePastoral, 47.0690, 23.8710, 16, 2024},

		// Extra Turda-area coverage for Câmpia Turzii sprays — Ioan
		{"1780530987654", "Stupina Tureni", dbsqlc.ApiaryTypePermanent, 46.6210, 23.8010, 26, 2020},
		{"1780530987654", "Stupina Petreștii de Jos", dbsqlc.ApiaryTypePermanent, 46.6505, 23.7420, 19, 2023},
	}

	created := 0
	for _, sp := range specs {
		owner, err := q.GetUserByCNP(ctx, sp.ownerCNP)
		if err != nil {
			slog.Warn("seed-more: extra apiary owner missing", "cnp_prefix", sp.ownerCNP[:4])
			continue
		}
		existing, err := q.ListApiariesByOwner(ctx, owner.ID)
		if err != nil {
			return fmt.Errorf("seed-more: list owner apiaries: %w", err)
		}
		hasName := false
		for _, e := range existing {
			if e.Name == sp.name {
				hasName = true
				break
			}
		}
		if hasName {
			continue
		}

		apiaryID := uuid.New()
		actorID := owner.ID.String()
		startDate := time.Date(sp.startYear, 4, 15, 0, 0, 0, 0, time.UTC)

		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("seed-more: begin extra apiary tx: %w", err)
		}
		qtx := dbsqlc.New(tx)

		ledgerHash, err := ledgerSvc.Append(ctx, tx, "apiary.registered", &actorID, map[string]any{
			"apiary_id":  apiaryID.String(),
			"owner_id":   owner.ID.String(),
			"name":       sp.name,
			"type":       string(sp.apiType),
			"lat":        sp.lat,
			"lng":        sp.lng,
			"hive_count": sp.hiveCount,
			"start_date": startDate.Format("2006-01-02"),
		})
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("seed-more: ledger apiary.registered: %w", err)
		}

		if _, err := qtx.CreateApiary(ctx, dbsqlc.CreateApiaryParams{
			ID:        apiaryID,
			OwnerID:   owner.ID,
			Name:      sp.name,
			Type:      sp.apiType,
			Lat:       sp.lat,
			Lng:       sp.lng,
			HiveCount: sp.hiveCount,
			StartDate: startDate,
		}); err != nil {
			tx.Rollback()
			return fmt.Errorf("seed-more: create extra apiary: %w", err)
		}
		if err := qtx.UpdateApiaryLedgerHash(ctx, dbsqlc.UpdateApiaryLedgerHashParams{
			ID:         apiaryID,
			LedgerHash: ledgerHash,
		}); err != nil {
			tx.Rollback()
			return fmt.Errorf("seed-more: update apiary ledger hash: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("seed-more: commit extra apiary: %w", err)
		}
		created++
		slog.Info("seed-more: created extra apiary", "name", sp.name, "owner", owner.FullName)
	}
	slog.Info("seed-more: extra apiaries phase", "created", created, "total_specs", len(specs))
	return nil
}
