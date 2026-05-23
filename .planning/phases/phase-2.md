# Phase 2 — Database: Migrations + sqlc
Status: COMPLETE
Completed: 2026-05-23

## What was built
- 3 migration files:
  - 00001_init.sql — all 10 tables, all custom PG types
  - 00002_seed_substances.sql — 10 reference substances with toxicity
  - 00003_indexes.sql — 14 indexes
- sqlc query files for all 9 tables (users, auth_challenges, apiaries, parcels, substances, spray_reports, alert_dispatches, damage_claims, push_subscriptions, ledger_events)
- sqlc.yaml configured for postgresql, output to internal/db/sqlc/
- Generated Go code in internal/db/sqlc/ (not committed, gitignored)

## Notes
- toxicity_level uses TEXT + CHECK (IN 'T-','T','T+') instead of PG ENUM
  because sqlc generates duplicate Go constant names for 'T-' and 'T' (both → ToxicityLevelT)
- UUID type: google/uuid (sqlc default) — gofrs/uuid caused import conflicts
- migrations applied successfully: `make migrate-up` ran all 3 in order
- `make sqlc-gen` exits 0, `go build ./...` exits 0
