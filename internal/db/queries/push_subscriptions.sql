-- name: CreatePushSubscription :one
INSERT INTO push_subscriptions (id, user_id, endpoint, p256dh, auth)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (endpoint) DO UPDATE
    SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, user_id = EXCLUDED.user_id
RETURNING *;

-- name: DeletePushSubscription :exec
DELETE FROM push_subscriptions WHERE id = $1 AND user_id = $2;

-- name: ListPushSubscriptionsByUser :many
SELECT * FROM push_subscriptions WHERE user_id = $1;

-- name: GetPushSubscription :one
SELECT * FROM push_subscriptions WHERE id = $1 LIMIT 1;
