package services

// Demo reset: wipes spray-related rows + ledger and re-seeds with timestamps
// relative to NOW, so the inspector view always shows a "live system".
//
// Storyline (chronological, oldest → newest):
//
//   T-90d  20 apiary.registered (Andrei, Maria, Ioan owners)
//   T-40d  damage filed by Maria — later rejected
//   T-32d  damage filed by Ioan — later accepted
//   T-28d  spray #11 completed (Confidor T+, Gheorghe Dej 1)
//   T-25d  damage filed by Andrei — later accepted
//   T-18d  spray #10 completed (Karate Zeon T-, Vasile Câmpia Nord)
//   T-14d  spray #12 created then cancelled (Elena Gherla Est, rain)
//   T-8d   damage filed by Ioan — under_review
//   T-7d   spray #9 completed (Mospilan T, Vasile Câmpia Sud)
//   T-6d   damage filed by Maria — under_review
//   T-5d   spray #8 completed (Movento T-, Elena Gherla Vest)
//   T-3d   spray #6 completed (Actara T, Elena Gherla Est)
//   T-2d   damage filed by Maria (filed, awaiting inspection)
//   T-2d   spray #5 completed (Confidor T+, Vasile Câmpia Nord)
//   T-1d   damage filed by Andrei (filed)
//   T-5h   damage filed by Andrei — FRESH, awaits inspection (Gherla Vest)
//   T-4h   damage filed by Ioan — FRESH (Turda Centru)
//   T-2h   damage filed by Andrei — FRESH (Apahida Sud)
//   T-1h   spray #1 in_progress (Confidor T+, Vasile Câmpia Sud) — LIVE
//   T+16h  spray #2 scheduled (Decis T+, Elena)
//   T+48h  spray #3 scheduled (Mospilan T, Gheorghe)
//   T+96h  spray #4 scheduled (Karate Zeon T-, Vasile)

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
)

const (
	demoDay  = 24 * time.Hour
	demoHour = time.Hour
)

// SeedDemoReset wipes spray-related data + ledger events and reseeds with
// timestamps anchored to NOW. Users, apiaries and parcels are preserved.
// Run repeatedly between demo takes to "freshen" the dataset.
func SeedDemoReset(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)

	// ── Phase 1: wipe ────────────────────────────────────────────────────────
	if err := demoWipe(ctx, sqlDB); err != nil {
		return fmt.Errorf("demo-reset wipe: %w", err)
	}

	q := dbsqlc.New(sqlDB)

	// Resolve user IDs by CNP once.
	cnp := func(c string) dbsqlc.User {
		u, err := q.GetUserByCNP(ctx, c)
		if err != nil {
			panic(fmt.Sprintf("demo-reset: user with cnp %s missing — run `make seed` first", c))
		}
		return u
	}
	andrei := cnp("1850101123456")
	maria := cnp("2900215654321")
	ioan := cnp("1780530987654")
	vasile := cnp("1920412111222")
	elena := cnp("2880721333444")
	gheorghe := cnp("1751103555666")
	// Demo identities the live presentation walks the audience through.
	marius := cnp("2800604121673")        // apicultor (real phone + email)
	alexFermier := cnp("6020615124562")   // fermier (will trigger the LIVE cascade)
	alexInspector := cnp("6020615124567") // inspector (signs damage.inspected)
	_ = elena

	// Apiary + parcel lookups.
	apiariesByName, err := loadApiariesByName(ctx, q)
	if err != nil {
		return err
	}
	parcelsByName, err := loadParcelsByName(ctx, q)
	if err != nil {
		return err
	}

	// ── Phase 2: re-emit apiary.registered for every apiary ──────────────────
	t := time.Now().UTC().Add(-90 * demoDay)
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	apiaryNames := make([]string, 0, len(apiariesByName))
	for name := range apiariesByName {
		apiaryNames = append(apiaryNames, name)
	}
	sort.Strings(apiaryNames)
	for _, name := range apiaryNames {
		a := apiariesByName[name]
		actor := a.OwnerID.String()
		hash, err := appendEventAt(ctx, tx, "apiary.registered", &actor, map[string]any{
			"apiary_id":  a.ID.String(),
			"owner_id":   a.OwnerID.String(),
			"name":       a.Name,
			"type":       string(a.Type),
			"lat":        a.Lat,
			"lng":        a.Lng,
			"hive_count": a.HiveCount,
			"start_date": a.StartDate.Format("2006-01-02"),
		}, t)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("re-emit apiary.registered: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE apiaries SET ledger_hash = $1 WHERE id = $2", hash, a.ID); err != nil {
			tx.Rollback()
			return err
		}
		t = t.Add(2 * time.Minute)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit apiary phase: %w", err)
	}
	slog.Info("demo-reset: apiary.registered re-emitted", "count", len(apiariesByName))

	// ── Phase 3: storyline. Each op runs in its own transaction so a failure
	// in one item doesn't roll back everything. Chronological order matters
	// for the ledger chain integrity.
	now := time.Now().UTC()
	type damageSpec struct {
		beekeeper     dbsqlc.User
		apiary        string
		description   string
		hiveLoss      int32
		filedAt       time.Time
		finalStatus   dbsqlc.DamageClaimStatus // filed | under_review | accepted | rejected
		inspectedAt   *time.Time
		relatedSpray  uuid.NullUUID
	}
	type spraySpec struct {
		farmer        dbsqlc.User
		parcel        string
		crop          string
		substance     string
		toxicity      string
		surfaceHa     float64
		doseKgHa      float64
		durationHours float64
		scheduledAt   time.Time
		status        dbsqlc.SprayStatus
		confirmFrac   float64 // 0..1 of dispatches that confirm
		notes         string
	}

	sprayList := []spraySpec{
		// Historical, completed
		{gheorghe, "Parcela Dej 1", "rapiță", "Confidor Oil SC", "T+", 8.3, 0.40, 4.0, now.Add(-28 * demoDay), dbsqlc.SprayStatusCompleted, 0.65, ""},
		{vasile, "Parcela Câmpia Nord", "grâu", "Karate Zeon", "T-", 5.5, 0.10, 2.5, now.Add(-18 * demoDay), dbsqlc.SprayStatusCompleted, 0.75, ""},
		{elena, "Parcela Gherla Est", "porumb", "Mospilan 20 SG", "T", 4.8, 0.22, 3.0, now.Add(-14 * demoDay), dbsqlc.SprayStatusCancelled, 0, "Anulat — ploaie torențială."},
		{vasile, "Parcela Câmpia Sud", "rapiță", "Mospilan 20 SG", "T", 7.2, 0.25, 4.0, now.Add(-7 * demoDay), dbsqlc.SprayStatusCompleted, 0.80, "Aplicare matinală."},
		{elena, "Parcela Gherla Vest", "floarea-soarelui", "Movento OD", "T-", 6.1, 0.15, 4.0, now.Add(-5 * demoDay), dbsqlc.SprayStatusCompleted, 0.60, ""},
		{elena, "Parcela Gherla Est", "porumb", "Actara 25 WG", "T", 4.8, 0.20, 3.0, now.Add(-3 * demoDay), dbsqlc.SprayStatusCompleted, 0.70, ""},
		{vasile, "Parcela Câmpia Nord", "grâu", "Confidor Oil SC", "T+", 5.5, 0.40, 4.0, now.Add(-2 * demoDay), dbsqlc.SprayStatusCompleted, 0.85, "Tratament urgent — atac larvar."},
		// Live + scheduled — Alexandra's tomorrow spray is the demo's headline event
		// (T+ Decis on her Apahida parcel will fire Marius's apiary alert during the
		// live presentation).
		{vasile, "Parcela Câmpia Sud", "rapiță", "Confidor Oil SC", "T+", 7.2, 0.40, 4.0, now.Add(-1 * demoHour), dbsqlc.SprayStatusInProgress, 0.50, "În curs de aplicare."},
		{alexFermier, "Parcela Alexandra Apahida", "rapiță", "Decis Forte", "T+", 6.5, 0.30, 3.5, now.Add(16 * demoHour), dbsqlc.SprayStatusScheduled, 0, "Programat mâine dimineață — demo principal."},
		{elena, "Parcela Gherla Vest", "floarea-soarelui", "Mospilan 20 SG", "T", 6.1, 0.22, 3.5, now.Add(28 * demoHour), dbsqlc.SprayStatusScheduled, 0, ""},
		{gheorghe, "Parcela Dej 2", "grâu", "Mospilan 20 SG", "T", 5.0, 0.22, 4.0, now.Add(48 * demoHour), dbsqlc.SprayStatusScheduled, 0, ""},
		{vasile, "Parcela Câmpia Nord", "grâu", "Karate Zeon", "T-", 5.5, 0.10, 2.5, now.Add(96 * demoHour), dbsqlc.SprayStatusScheduled, 0, ""},
	}

	damageList := []damageSpec{
		{maria, "Stupina Florești Vest", "T- aplicat fără anunț prealabil. 1 stup pierdut prin moarte subită a culegătoarelor.", 1, now.Add(-40 * demoDay), dbsqlc.DamageClaimStatusRejected, ptrTime(now.Add(-39 * demoDay)), uuid.NullUUID{}},
		{ioan, "Stupina Mihai Viteazu", "2 familii cu albine paralizate după tratamentul T în zona vecină.", 2, now.Add(-32 * demoDay), dbsqlc.DamageClaimStatusAccepted, ptrTime(now.Add(-30 * demoDay)), uuid.NullUUID{}},
		{andrei, "Stupina Apahida Nord", "1 stup pierdut după aplicarea unei stropiri neanunțate la 800m.", 1, now.Add(-25 * demoDay), dbsqlc.DamageClaimStatusAccepted, ptrTime(now.Add(-23 * demoDay)), uuid.NullUUID{}},
		{ioan, "Stupina Câmpia Turzii Est", "3 stupi cu mortalitate masivă a culegătoarelor după Confidor T+. Probe recoltate.", 3, now.Add(-8 * demoDay), dbsqlc.DamageClaimStatusUnderReview, ptrTime(now.Add(-6 * demoDay)), uuid.NullUUID{}},
		{maria, "Stupina Cluj-Napoca Sud", "2 familii distruse — semne clare de intoxicație neurotoxică.", 2, now.Add(-6 * demoDay), dbsqlc.DamageClaimStatusUnderReview, ptrTime(now.Add(-5 * demoDay)), uuid.NullUUID{}},
		{maria, "Stupina Florești Est", "Reducere drastică a populației la 2 familii — semne clare de intoxicație.", 2, now.Add(-2 * demoDay), dbsqlc.DamageClaimStatusFiled, nil, uuid.NullUUID{}},
		{andrei, "Stupina Bonțida Sud", "Populație redusă la jumătate după tratamentul T+ aplicat în Câmpia Turzii.", 1, now.Add(-1 * demoDay), dbsqlc.DamageClaimStatusFiled, nil, uuid.NullUUID{}},
		{andrei, "Stupina Gherla Vest", "4 stupi cu mortalitate ridicată după Decis programat aseară.", 4, now.Add(-5 * demoHour), dbsqlc.DamageClaimStatusFiled, nil, uuid.NullUUID{}},
		{ioan, "Stupina Turda Centru", "1 stup mort + 2 cu paralizie. Suspect tratament T din parcela vecină.", 1, now.Add(-4 * demoHour), dbsqlc.DamageClaimStatusFiled, nil, uuid.NullUUID{}},
		{andrei, "Stupina Apahida Sud", "3 stupi cu mortalitate masivă a culegătoarelor după aplicarea T+ de azi-dimineață.", 3, now.Add(-2 * demoHour), dbsqlc.DamageClaimStatusFiled, nil, uuid.NullUUID{}},
		// Marius's fresh damage — the one the inspector will mark as inspected
		// during the live demo, sending an email + PDF to the primărie.
		{marius, "Stupina Marius Apahida", "2 stupi pierduți după stropire neanunțată în zonă. Probe recoltate.", 2, now.Add(-3 * demoHour), dbsqlc.DamageClaimStatusFiled, nil, uuid.NullUUID{}},
	}

	// Build ordered op list. Each op carries a timestamp; we sort then execute.
	// run() receives the (potentially monotonically-bumped) time so equal
	// initial timestamps across sprays/damages don't break the SHA chain.
	type op struct {
		when time.Time
		run  func(t time.Time) error
	}
	ops := make([]op, 0, len(sprayList)*4+len(damageList)*3)

	// IDs are needed up-front so dispatches and damages can reference them.
	sprayIDs := make([]uuid.UUID, len(sprayList))
	for i := range sprayList {
		sprayIDs[i] = uuid.New()
	}
	damageIDs := make([]uuid.UUID, len(damageList))
	for i := range damageList {
		damageIDs[i] = uuid.New()
	}

	// Helper to enqueue spray creation + dispatches + (pdf/email if completed).
	enqueueSpray := func(idx int, sp spraySpec) {
		sprayID := sprayIDs[idx]
		parcel := parcelsByName[sp.parcel]
		actorID := sp.farmer.ID.String()

		// Compute createdAt: for completed/cancelled = scheduledAt - 1d (announced ahead);
		// for scheduled = NOW - 2h..6h (announced today/recently); for in_progress = scheduledAt.
		var createdAt time.Time
		switch sp.status {
		case dbsqlc.SprayStatusScheduled:
			createdAt = now.Add(-2 * demoHour)
		case dbsqlc.SprayStatusInProgress:
			createdAt = sp.scheduledAt // i.e. now-1h
		default:
			createdAt = sp.scheduledAt.Add(-1 * demoDay)
		}

		ops = append(ops, op{when: createdAt, run: func(t time.Time) error {
			tx, err := sqlDB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()

			sprayHash, err := appendEventAt(ctx, tx, "spray.created", &actorID, map[string]any{
				"spray_id":   sprayID.String(),
				"substance":  sp.substance,
				"toxicity":   sp.toxicity,
				"parcel_id":  parcel.ID.String(),
				"surface_ha": sp.surfaceHa,
			}, t)
			if err != nil {
				return err
			}
			var notes sql.NullString
			if sp.notes != "" {
				notes = sql.NullString{String: sp.notes, Valid: true}
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO spray_reports
				  (id, farmer_id, parcel_id, crop, substance, toxicity, surface_ha,
				   dose_kg_ha, scheduled_at, duration_hours, notes, ledger_hash, status, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
				sprayID, sp.farmer.ID, parcel.ID, sp.crop, sp.substance, sp.toxicity, sp.surfaceHa,
				sp.doseKgHa, sp.scheduledAt, sp.durationHours, notes, sprayHash, string(sp.status), t,
			); err != nil {
				return err
			}
			return tx.Commit()
		}})

		// Cancelled sprays: emit cancellation event a few hours after creation, no dispatches.
		if sp.status == dbsqlc.SprayStatusCancelled {
			cancelAt := createdAt.Add(6 * demoHour)
			ops = append(ops, op{when: cancelAt, run: func(t time.Time) error {
				tx, err := sqlDB.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err := appendEventAt(ctx, tx, "spray.cancelled", &actorID, map[string]any{
					"spray_id": sprayID.String(),
				}, t); err != nil {
					return err
				}
				return tx.Commit()
			}})
			return
		}

		// Dispatches: scheduled sprays don't have dispatches yet (planned, not yet fired).
		if sp.status == dbsqlc.SprayStatusScheduled {
			return
		}

		// Compute nearby apiaries within 15 km of the parcel.
		type aWithDist struct {
			a    dbsqlc.Apiary
			dist float64
		}
		var nearby []aWithDist
		for _, a := range apiariesByName {
			d := Haversine(parcel.Lat, parcel.Lng, a.Lat, a.Lng)
			if d <= 15000 {
				nearby = append(nearby, aWithDist{a, d})
			}
		}
		sort.Slice(nearby, func(i, j int) bool { return nearby[i].dist < nearby[j].dist })

		dispatchSpawnAt := createdAt.Add(2 * time.Minute)
		affected := 0
		for i, n := range nearby {
			dispatchID := uuid.New()
			beekeeperID := n.a.OwnerID
			// Final status based on confirmFrac (deterministic by index).
			var finalStatus dbsqlc.NullFinalStatus
			callState := dbsqlc.CallStateQueued
			smsState := dbsqlc.SmsStateSent
			if sp.toxicity == "T-" {
				callState = dbsqlc.CallStateSkipped
			}
			dispatchedAt := dispatchSpawnAt.Add(time.Duration(i) * 30 * time.Second)
			confirmedAt := dispatchedAt.Add(15 * time.Minute)

			// Roll based on (i / len(nearby)) < confirmFrac
			shouldConfirm := float64(i) < float64(len(nearby))*sp.confirmFrac
			if shouldConfirm && sp.status == dbsqlc.SprayStatusCompleted {
				switch i % 3 {
				case 0:
					finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedCall, Valid: true}
					callState = dbsqlc.CallStateConfirmed
				case 1:
					finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedSms, Valid: true}
					smsState = dbsqlc.SmsStateConfirmed
				case 2:
					finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusConfirmedApp, Valid: true}
				}
			} else if sp.status == dbsqlc.SprayStatusCompleted {
				finalStatus = dbsqlc.NullFinalStatus{FinalStatus: dbsqlc.FinalStatusUnconfirmed, Valid: true}
			}

			downwind := i%2 == 0
			distM := n.dist

			ops = append(ops, op{when: dispatchedAt, run: func(t time.Time) error {
				tx, err := sqlDB.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				dispatchHash, err := appendEventAt(ctx, tx, "alert.dispatched", &actorID, map[string]any{
					"dispatch_id": dispatchID.String(),
					"spray_id":    sprayID.String(),
					"apiary_id":   n.a.ID.String(),
					"distance_m":  distM,
					"toxicity":    sp.toxicity,
				}, t); if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO alert_dispatches
					  (id, spray_report_id, beekeeper_id, apiary_id, distance_m, downwind,
					   call_state, sms_state, ledger_hash, final_status, created_at)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
					dispatchID, sprayID, beekeeperID, n.a.ID, distM, downwind,
					string(callState), string(smsState), dispatchHash,
					sql.NullString{String: string(finalStatus.FinalStatus), Valid: finalStatus.Valid},
					t,
				); err != nil {
					return err
				}
				return tx.Commit()
			}})

			if finalStatus.Valid && finalStatus.FinalStatus != dbsqlc.FinalStatusUnconfirmed {
				beekeeperActor := beekeeperID.String()
				method := "app"
				switch finalStatus.FinalStatus {
				case dbsqlc.FinalStatusConfirmedCall:
					method = "call"
				case dbsqlc.FinalStatusConfirmedSms:
					method = "sms"
				}
				ops = append(ops, op{when: confirmedAt, run: func(t time.Time) error {
					tx, err := sqlDB.BeginTx(ctx, nil)
					if err != nil {
						return err
					}
					defer tx.Rollback()
					if _, err := appendEventAt(ctx, tx, "alert.confirmed", &beekeeperActor, map[string]any{
						"dispatch_id": dispatchID.String(),
						"spray_id":    sprayID.String(),
						"method":      method,
					}, t); err != nil {
						return err
					}
					return tx.Commit()
				}})
			}

			affected++
		}

		// Persist affected_count after dispatches finish processing.
		if affected > 0 {
			finalizeAt := dispatchSpawnAt.Add(time.Duration(affected)*30*time.Second + time.Minute)
			ops = append(ops, op{when: finalizeAt, run: func(_ time.Time) error {
				_, err := sqlDB.ExecContext(ctx,
					"UPDATE spray_reports SET affected_apiaries_count = $1 WHERE id = $2",
					affected, sprayID)
				return err
			}})
		}

		// Completed sprays: pdf.generated + email.sent ~30min after creation.
		if sp.status == dbsqlc.SprayStatusCompleted {
			pdfAt := createdAt.Add(20 * time.Minute)
			emailAt := pdfAt.Add(2 * time.Minute)
			ops = append(ops, op{when: pdfAt, run: func(t time.Time) error {
				tx, err := sqlDB.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err := appendEventAt(ctx, tx, "pdf.generated", &actorID, map[string]any{
					"spray_id": sprayID.String(),
					"path":     fmt.Sprintf("./uploads/pdfs/%s.pdf", sprayID),
				}, t); err != nil {
					return err
				}
				return tx.Commit()
			}})
			ops = append(ops, op{when: emailAt, run: func(t time.Time) error {
				tx, err := sqlDB.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err := appendEventAt(ctx, tx, "email.sent", &actorID, map[string]any{
					"spray_id":  sprayID.String(),
					"recipient": "primarie@example.ro",
				}, t); err != nil {
					return err
				}
				return tx.Commit()
			}})
		}
	}

	enqueueDamage := func(idx int, d damageSpec) {
		damageID := damageIDs[idx]
		apiary := apiariesByName[d.apiary]
		actorID := d.beekeeper.ID.String()

		ops = append(ops, op{when: d.filedAt, run: func(t time.Time) error {
			tx, err := sqlDB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			ledgerHash, err := appendEventAt(ctx, tx, "damage.filed", &actorID, map[string]any{
				"claim_id":        damageID.String(),
				"beekeeper_id":    d.beekeeper.ID.String(),
				"apiary_id":       apiary.ID.String(),
				"hive_loss_count": d.hiveLoss,
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
				damageID, d.beekeeper.ID, apiary.ID, d.relatedSpray,
				d.description, d.hiveLoss, apiary.Lat, apiary.Lng,
				string(dbsqlc.DamageClaimStatusFiled), ledgerHash, t,
			); err != nil {
				return err
			}
			// Demo photos (2 per claim).
			for i := 0; i < 2; i++ {
				key := fmt.Sprintf("https://picsum.photos/seed/demo-%s-%d/800/600", damageID.String()[:8], i)
				if _, err := tx.ExecContext(ctx,
					"INSERT INTO damage_photos (id, damage_claim_id, url) VALUES ($1, $2, $3)",
					uuid.New(), damageID, key,
				); err != nil {
					return err
				}
			}
			return tx.Commit()
		}})

		if d.finalStatus != dbsqlc.DamageClaimStatusFiled && d.inspectedAt != nil {
			inspectedAt := *d.inspectedAt
			finalStatus := d.finalStatus
			inspectorID := alexInspector.ID.String()
			ops = append(ops, op{when: inspectedAt, run: func(t time.Time) error {
				tx, err := sqlDB.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err := appendEventAt(ctx, tx, "damage.inspected", &inspectorID, map[string]any{
					"claim_id":     damageID.String(),
					"apiary_id":    apiary.ID.String(),
					"beekeeper_id": d.beekeeper.ID.String(),
					"inspector_id": inspectorID,
				}, t); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx,
					"UPDATE damage_claims SET status = $1 WHERE id = $2",
					string(finalStatus), damageID,
				); err != nil {
					return err
				}
				return tx.Commit()
			}})
		}
	}

	for i, sp := range sprayList {
		enqueueSpray(i, sp)
	}
	for i, d := range damageList {
		enqueueDamage(i, d)
	}

	// Sort ops chronologically — required for valid hash chain.
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].when.Before(ops[j].when) })

	// Enforce strict monotonicity: any op whose timestamp is ≤ the previous
	// op's gets bumped to (prev + 1µs). Necessary because spec data may share
	// timestamps across sprays/damages (e.g. T-8d for both) — equal
	// created_at values give ListLedgerEventsOrdered a non-deterministic
	// sort that can break chain verification.
	if len(ops) > 1 {
		for i := 1; i < len(ops); i++ {
			if !ops[i].when.After(ops[i-1].when) {
				ops[i].when = ops[i-1].when.Add(time.Microsecond)
			}
		}
	}

	for i, o := range ops {
		if err := o.run(o.when); err != nil {
			return fmt.Errorf("demo-reset op %d (when=%s): %w", i, o.when.Format(time.RFC3339), err)
		}
	}

	slog.Info("demo-reset complete",
		"apiaries", len(apiariesByName),
		"sprays", len(sprayList),
		"damages", len(damageList),
		"ops_executed", len(ops),
	)
	return nil
}

// SeedDemoTamper mutates the payload of one historical ledger event so that
// the chain verification fails. Choose a damage.filed event in the middle of
// the chain so the break is obvious and lands on the inspector's audit page.
func SeedDemoTamper(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	q := dbsqlc.New(sqlDB)

	events, err := q.ListLedgerEventsByType(ctx, dbsqlc.ListLedgerEventsByTypeParams{
		Column1: "damage.filed",
		Limit:   100,
		Offset:  0,
	})
	if err != nil {
		return fmt.Errorf("demo-tamper: list damage events: %w", err)
	}
	if len(events) < 2 {
		return fmt.Errorf("demo-tamper: need at least 2 damage.filed events (run `make demo-reset` first)")
	}

	// Pick the one in the middle of the list (oldest-by-DESC is index 0; we
	// want something visible but not the latest, to make the break dramatic).
	target := events[len(events)/2]

	// Bump hive_loss_count to a wildly different number.
	if _, err := sqlDB.ExecContext(ctx, `
		UPDATE ledger_events
		SET payload = jsonb_set(payload, '{hive_loss_count}', to_jsonb(999))
		WHERE id = $1
	`, target.ID); err != nil {
		return fmt.Errorf("demo-tamper: update payload: %w", err)
	}
	slog.Warn("demo-tamper: ledger event payload mutated",
		"event_id", target.ID, "hash_prefix", target.Hash[:12])
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// demoWipe clears spray-related and ledger data. Apiaries/parcels/users
// are preserved; apiary.ledger_hash is reset to '' so the next phase can
// re-emit apiary.registered events as a new chain.
func demoWipe(ctx context.Context, db *sql.DB) error {
	for _, stmt := range []string{
		`DELETE FROM damage_claims`,         // cascades damage_photos
		`DELETE FROM spray_reports`,         // cascades alert_dispatches
		`DELETE FROM ledger_events`,
		`UPDATE apiaries SET ledger_hash = ''`,
		`UPDATE spray_reports SET ledger_hash = '' WHERE FALSE`, // no-op placeholder
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("wipe stmt %q: %w", stmt, err)
		}
	}
	slog.Info("demo-reset: data wiped")
	return nil
}

// appendEventAt is like ledger.Append but takes an explicit createdAt so we
// can backdate seed events while keeping the chain valid. Must be called in
// chronological order (latest event always extends the current tip).
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
		if parsed, parseErr := uuid.Parse(*actorID); parseErr == nil {
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

func loadApiariesByName(ctx context.Context, q *dbsqlc.Queries) (map[string]dbsqlc.Apiary, error) {
	rows, err := q.ListAllApiaries(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]dbsqlc.Apiary, len(rows))
	for _, a := range rows {
		m[a.Name] = a
	}
	return m, nil
}

func loadParcelsByName(ctx context.Context, q *dbsqlc.Queries) (map[string]dbsqlc.Parcel, error) {
	rows, err := q.ListAllParcels(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]dbsqlc.Parcel, len(rows))
	for _, p := range rows {
		m[p.Name] = p
	}
	return m, nil
}

func ptrTime(t time.Time) *time.Time { return &t }

// canonicalJSON and sha256hex are reused from ledger.go (same package).
