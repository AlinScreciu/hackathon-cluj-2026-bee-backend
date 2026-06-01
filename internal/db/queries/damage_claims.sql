-- name: CreateDamageClaim :one
INSERT INTO damage_claims (id, beekeeper_id, apiary_id, related_spray_id, description, hive_loss_count, gps_lat, gps_lng, ledger_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetDamageClaim :one
SELECT * FROM damage_claims WHERE id = $1 LIMIT 1;

-- name: ListDamageClaimsByBeekeeper :many
SELECT * FROM damage_claims WHERE beekeeper_id = $1 ORDER BY created_at DESC;

-- name: ListAllDamageClaims :many
SELECT * FROM damage_claims ORDER BY created_at DESC;

-- name: UpdateDamageClaimStatus :exec
UPDATE damage_claims SET status = $2 WHERE id = $1;

-- name: AddDamagePhoto :one
INSERT INTO damage_photos (id, damage_claim_id, url)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListDamagePhotos :many
SELECT * FROM damage_photos WHERE damage_claim_id = $1;
