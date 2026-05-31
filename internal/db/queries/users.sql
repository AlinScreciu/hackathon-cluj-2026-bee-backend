-- name: GetUserByCNP :one
SELECT * FROM users WHERE cnp = $1 LIMIT 1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE LOWER(email) = LOWER($1) LIMIT 1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 LIMIT 1;

-- name: ListUsersByRole :many
SELECT * FROM users WHERE role = $1 ORDER BY full_name;

-- name: CreateUser :one
INSERT INTO users (id, cnp, full_name, email, phone, role, county, locality, password_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListAllUsers :many
SELECT * FROM users ORDER BY full_name;
