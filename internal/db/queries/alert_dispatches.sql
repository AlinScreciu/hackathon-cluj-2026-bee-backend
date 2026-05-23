-- name: CreateAlertDispatch :one
INSERT INTO alert_dispatches (id, spray_report_id, beekeeper_id, apiary_id, distance_m, downwind, call_state, sms_state, ledger_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetAlertDispatch :one
SELECT * FROM alert_dispatches WHERE id = $1 LIMIT 1;

-- name: ListDispatchesBySpray :many
SELECT * FROM alert_dispatches WHERE spray_report_id = $1 ORDER BY created_at;

-- name: ListDispatchSiblings :many
-- All dispatches belonging to the same beekeeper for the same spray, ordered
-- closest-apiary-first. Used to coalesce per-beekeeper notifications and to
-- propagate confirmation state from the primary dispatch to siblings.
SELECT * FROM alert_dispatches
WHERE spray_report_id = $1 AND beekeeper_id = $2
ORDER BY distance_m ASC;

-- name: ListActiveAlertsByBeekeeper :many
SELECT ad.* FROM alert_dispatches ad
WHERE ad.beekeeper_id = $1 AND ad.final_status IS NULL
ORDER BY ad.created_at DESC;

-- name: ListAllAlertsByBeekeeper :many
SELECT * FROM alert_dispatches WHERE beekeeper_id = $1 ORDER BY created_at DESC;

-- name: GetDispatchByTwilioCallSID :one
SELECT * FROM alert_dispatches WHERE twilio_call_sid = $1 LIMIT 1;

-- name: GetDispatchByTwilioSMSSID :one
SELECT * FROM alert_dispatches WHERE twilio_sms_sid = $1 LIMIT 1;

-- name: UpdatePushState :exec
UPDATE alert_dispatches SET push_state = $2, push_at = NOW() WHERE id = $1;

-- name: UpdateCallState :exec
UPDATE alert_dispatches SET call_state = $2, call_at = NOW(), call_attempts = call_attempts + 1 WHERE id = $1;

-- name: SetTwilioCallSID :exec
UPDATE alert_dispatches SET twilio_call_sid = $2, call_state = 'queued', call_at = NOW() WHERE id = $1;

-- name: SetTwilioSMSSID :exec
UPDATE alert_dispatches SET twilio_sms_sid = $2, sms_state = 'queued', sms_at = NOW() WHERE id = $1;

-- name: UpdateSMSState :exec
UPDATE alert_dispatches SET sms_state = $2, sms_at = NOW() WHERE id = $1;

-- name: UpdateSMSStateByTwilioSID :exec
UPDATE alert_dispatches SET sms_state = $2, sms_at = NOW() WHERE twilio_sms_sid = $1;

-- name: UpdateCallStateByTwilioSID :exec
UPDATE alert_dispatches SET call_state = $2, call_at = NOW() WHERE twilio_call_sid = $1;

-- name: UpdateFinalStatus :exec
UPDATE alert_dispatches SET final_status = $2 WHERE id = $1 AND final_status IS NULL;

-- name: UpdateFinalStatusByCallSID :exec
UPDATE alert_dispatches SET final_status = $2 WHERE twilio_call_sid = $1 AND final_status IS NULL;

-- name: UpdateFinalStatusBySMSSID :exec
UPDATE alert_dispatches SET final_status = $2 WHERE twilio_sms_sid = $1 AND final_status IS NULL;

-- name: SetInAppConfirmed :exec
UPDATE alert_dispatches
SET in_app_confirmed_at = NOW(), in_app_action = $2, final_status = $3
WHERE id = $1 AND final_status IS NULL;

-- name: UpdateDispatchLedgerHash :exec
UPDATE alert_dispatches SET ledger_hash = $2 WHERE id = $1;
