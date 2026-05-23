-- name: ListSubstances :many
SELECT * FROM substances ORDER BY label;

-- name: GetSubstanceByLabel :one
SELECT * FROM substances WHERE label = $1 LIMIT 1;
