-- name: GetParcel :one
SELECT * FROM parcels WHERE id = $1 LIMIT 1;

-- name: ListParcelsByOwner :many
SELECT * FROM parcels WHERE owner_id = $1 ORDER BY name;

-- name: CreateParcel :one
INSERT INTO parcels (id, owner_id, name, cadastral_number, lat, lng, surface_ha, default_crop, county, locality)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;
