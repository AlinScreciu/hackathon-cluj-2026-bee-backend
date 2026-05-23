-- name: CreateChallenge :one
INSERT INTO auth_challenges (id, user_id, method, code_hash, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetChallenge :one
SELECT * FROM auth_challenges WHERE id = $1 LIMIT 1;

-- name: MarkChallengeVerified :exec
UPDATE auth_challenges SET verified_at = NOW() WHERE id = $1;

-- name: DeleteExpiredChallenges :exec
DELETE FROM auth_challenges WHERE expires_at < NOW() AND verified_at IS NULL;
