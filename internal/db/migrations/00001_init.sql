-- +goose Up

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE user_role AS ENUM ('apicultor', 'fermier', 'inspector');
CREATE TYPE apiary_type AS ENUM ('permanent', 'pastoral');
-- toxicity_level uses TEXT + CHECK to avoid sqlc name-collision with 'T-' vs 'T'
CREATE TYPE spray_status AS ENUM ('scheduled', 'in_progress', 'completed', 'cancelled');
CREATE TYPE push_state AS ENUM ('pending', 'sent', 'delivered', 'opened');
CREATE TYPE call_state AS ENUM ('skipped', 'queued', 'ringing', 'answered', 'confirmed', 'no_input', 'no_answer', 'busy', 'failed', 'hung_up');
CREATE TYPE sms_state AS ENUM ('skipped', 'queued', 'sent', 'delivered', 'confirmed', 'no_reply', 'failed');
CREATE TYPE final_status AS ENUM ('confirmed_call', 'confirmed_sms', 'confirmed_app', 'unconfirmed', 'failed');
CREATE TYPE in_app_action AS ENUM ('move_hives', 'seal_in_place');
CREATE TYPE damage_claim_status AS ENUM ('filed', 'under_review', 'accepted', 'rejected');
CREATE TYPE auth_method AS ENUM ('push', 'sms', 'email');

CREATE TABLE users (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cnp          VARCHAR(13) NOT NULL UNIQUE,
    full_name    TEXT NOT NULL,
    email        TEXT NOT NULL,
    phone        TEXT NOT NULL,
    role         user_role NOT NULL,
    county       TEXT NOT NULL DEFAULT '',
    locality     TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE auth_challenges (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method       auth_method NOT NULL,
    code_hash    TEXT NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    verified_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE apiaries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    type         apiary_type NOT NULL DEFAULT 'permanent',
    lat          DOUBLE PRECISION NOT NULL,
    lng          DOUBLE PRECISION NOT NULL,
    hive_count   INTEGER NOT NULL DEFAULT 0,
    start_date   DATE NOT NULL,
    end_date     DATE,
    notes        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE parcels (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    cadastral_number  TEXT NOT NULL,
    lat               DOUBLE PRECISION NOT NULL,
    lng               DOUBLE PRECISION NOT NULL,
    surface_ha        DOUBLE PRECISION NOT NULL,
    default_crop      TEXT,
    county            TEXT NOT NULL DEFAULT '',
    locality          TEXT NOT NULL DEFAULT ''
);

CREATE TABLE substances (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    label     TEXT NOT NULL UNIQUE,
    toxicity  TEXT NOT NULL CHECK (toxicity IN ('T-', 'T', 'T+'))
);

CREATE TABLE spray_reports (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    farmer_id               UUID NOT NULL REFERENCES users(id),
    parcel_id               UUID NOT NULL REFERENCES parcels(id),
    crop                    TEXT NOT NULL,
    substance               TEXT NOT NULL,
    toxicity                TEXT NOT NULL CHECK (toxicity IN ('T-', 'T', 'T+')),
    surface_ha              DOUBLE PRECISION NOT NULL,
    scheduled_at            TIMESTAMPTZ NOT NULL,
    duration_hours          DOUBLE PRECISION NOT NULL,
    notes                   TEXT,
    status                  spray_status NOT NULL DEFAULT 'scheduled',
    affected_apiaries_count INTEGER NOT NULL DEFAULT 0,
    ledger_hash             TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE alert_dispatches (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    spray_report_id     UUID NOT NULL REFERENCES spray_reports(id) ON DELETE CASCADE,
    beekeeper_id        UUID NOT NULL REFERENCES users(id),
    apiary_id           UUID NOT NULL REFERENCES apiaries(id),
    distance_m          DOUBLE PRECISION NOT NULL,
    downwind            BOOLEAN NOT NULL DEFAULT FALSE,
    push_state          push_state NOT NULL DEFAULT 'pending',
    push_at             TIMESTAMPTZ,
    call_state          call_state NOT NULL DEFAULT 'queued',
    call_at             TIMESTAMPTZ,
    call_attempts       INTEGER NOT NULL DEFAULT 0,
    sms_state           sms_state NOT NULL DEFAULT 'queued',
    sms_at              TIMESTAMPTZ,
    in_app_confirmed_at TIMESTAMPTZ,
    in_app_action       in_app_action,
    final_status        final_status,
    twilio_call_sid     TEXT,
    twilio_sms_sid      TEXT,
    ledger_hash         TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE damage_claims (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    beekeeper_id     UUID NOT NULL REFERENCES users(id),
    apiary_id        UUID NOT NULL REFERENCES apiaries(id),
    related_spray_id UUID REFERENCES spray_reports(id),
    description      TEXT NOT NULL,
    hive_loss_count  INTEGER NOT NULL DEFAULT 0,
    gps_lat          DOUBLE PRECISION NOT NULL,
    gps_lng          DOUBLE PRECISION NOT NULL,
    status           damage_claim_status NOT NULL DEFAULT 'filed',
    ledger_hash      TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE damage_photos (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    damage_claim_id  UUID NOT NULL REFERENCES damage_claims(id) ON DELETE CASCADE,
    url              TEXT NOT NULL
);

CREATE TABLE push_subscriptions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint   TEXT NOT NULL UNIQUE,
    p256dh     TEXT NOT NULL,
    auth       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE ledger_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    hash       TEXT NOT NULL UNIQUE,
    prev_hash  TEXT,
    type       TEXT NOT NULL,
    actor_id   UUID REFERENCES users(id),
    payload    JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down

DROP TABLE IF EXISTS ledger_events;
DROP TABLE IF EXISTS push_subscriptions;
DROP TABLE IF EXISTS damage_photos;
DROP TABLE IF EXISTS damage_claims;
DROP TABLE IF EXISTS alert_dispatches;
DROP TABLE IF EXISTS spray_reports;
DROP TABLE IF EXISTS substances;
DROP TABLE IF EXISTS parcels;
DROP TABLE IF EXISTS apiaries;
DROP TABLE IF EXISTS auth_challenges;
DROP TABLE IF EXISTS users;
DROP TYPE IF EXISTS damage_claim_status;
DROP TYPE IF EXISTS in_app_action;
DROP TYPE IF EXISTS final_status;
DROP TYPE IF EXISTS sms_state;
DROP TYPE IF EXISTS call_state;
DROP TYPE IF EXISTS push_state;
DROP TYPE IF EXISTS spray_status;
DROP TYPE IF EXISTS apiary_type;
DROP TYPE IF EXISTS auth_method;
DROP TYPE IF EXISTS user_role;
