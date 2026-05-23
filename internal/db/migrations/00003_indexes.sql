-- +goose Up

CREATE INDEX IF NOT EXISTS idx_apiaries_lat ON apiaries (lat);
CREATE INDEX IF NOT EXISTS idx_apiaries_lng ON apiaries (lng);
CREATE INDEX IF NOT EXISTS idx_apiaries_owner ON apiaries (owner_id);

CREATE INDEX IF NOT EXISTS idx_parcels_owner ON parcels (owner_id);

CREATE INDEX IF NOT EXISTS idx_spray_reports_scheduled ON spray_reports (scheduled_at);
CREATE INDEX IF NOT EXISTS idx_spray_reports_farmer ON spray_reports (farmer_id);

CREATE INDEX IF NOT EXISTS idx_alert_dispatches_spray ON alert_dispatches (spray_report_id);
CREATE INDEX IF NOT EXISTS idx_alert_dispatches_beekeeper ON alert_dispatches (beekeeper_id);
CREATE INDEX IF NOT EXISTS idx_alert_dispatches_call_sid ON alert_dispatches (twilio_call_sid) WHERE twilio_call_sid IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alert_dispatches_sms_sid ON alert_dispatches (twilio_sms_sid) WHERE twilio_sms_sid IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_ledger_events_prev_hash ON ledger_events (prev_hash);
CREATE INDEX IF NOT EXISTS idx_ledger_events_type ON ledger_events (type);
CREATE INDEX IF NOT EXISTS idx_ledger_events_actor ON ledger_events (actor_id) WHERE actor_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_damage_claims_beekeeper ON damage_claims (beekeeper_id);
CREATE INDEX IF NOT EXISTS idx_auth_challenges_user ON auth_challenges (user_id);

-- +goose Down

DROP INDEX IF EXISTS idx_auth_challenges_user;
DROP INDEX IF EXISTS idx_damage_claims_beekeeper;
DROP INDEX IF EXISTS idx_ledger_events_actor;
DROP INDEX IF EXISTS idx_ledger_events_type;
DROP INDEX IF EXISTS idx_ledger_events_prev_hash;
DROP INDEX IF EXISTS idx_alert_dispatches_sms_sid;
DROP INDEX IF EXISTS idx_alert_dispatches_call_sid;
DROP INDEX IF EXISTS idx_alert_dispatches_beekeeper;
DROP INDEX IF EXISTS idx_alert_dispatches_spray;
DROP INDEX IF EXISTS idx_spray_reports_farmer;
DROP INDEX IF EXISTS idx_spray_reports_scheduled;
DROP INDEX IF EXISTS idx_parcels_owner;
DROP INDEX IF EXISTS idx_apiaries_owner;
DROP INDEX IF EXISTS idx_apiaries_lng;
DROP INDEX IF EXISTS idx_apiaries_lat;
