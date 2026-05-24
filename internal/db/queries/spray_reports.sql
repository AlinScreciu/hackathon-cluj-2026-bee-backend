-- name: CreateSprayReport :one
INSERT INTO spray_reports (id, farmer_id, parcel_id, crop, substance, toxicity, surface_ha, dose_kg_ha, scheduled_at, duration_hours, notes, ledger_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: UpdateSprayAIAssessment :exec
UPDATE spray_reports SET
    ai_risk_score         = $2,
    ai_risk_level         = $3,
    ai_explanation_ro     = $4,
    ai_recommended_action = $5,
    ai_warnings           = $6,
    ai_zones              = $7
WHERE id = $1;

-- name: GetSprayReport :one
SELECT * FROM spray_reports WHERE id = $1 LIMIT 1;

-- name: ListSprayReportsByFarmer :many
SELECT * FROM spray_reports WHERE farmer_id = $1 ORDER BY created_at DESC;

-- name: ListActiveSprayReports :many
SELECT * FROM spray_reports WHERE status IN ('scheduled', 'in_progress') ORDER BY scheduled_at;

-- name: ListAllSprayReports :many
SELECT * FROM spray_reports ORDER BY created_at DESC;

-- name: UpdateSprayReportStatus :exec
UPDATE spray_reports SET status = $2 WHERE id = $1;

-- name: UpdateSprayReportLedgerHash :exec
UPDATE spray_reports SET ledger_hash = $2 WHERE id = $1;

-- name: UpdateSprayReportAffectedCount :exec
UPDATE spray_reports SET affected_apiaries_count = $2 WHERE id = $1;
