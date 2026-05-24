-- +goose Up

-- Dose: user-supplied at create time. Pesticide reporting requires it, and the
-- AI risk model uses it to size the application's bee impact.
ALTER TABLE spray_reports
    ADD COLUMN dose_kg_ha DOUBLE PRECISION NOT NULL DEFAULT 0;

-- AI assessment surfacing: the Python AI service already computes risk score,
-- a Romanian-language explanation, a recommended action, warnings and a
-- GeoJSON FeatureCollection of risk zones (A1–A4). The Go layer used to throw
-- those away and only keep risk radius + severity. Persist them so the
-- frontend can render the AI's reasoning on spray-report detail pages, and
-- so audits in the ledger can be reconstructed.
ALTER TABLE spray_reports
    ADD COLUMN ai_risk_score          DOUBLE PRECISION,
    ADD COLUMN ai_risk_level          TEXT,
    ADD COLUMN ai_explanation_ro      TEXT,
    ADD COLUMN ai_recommended_action  TEXT,
    ADD COLUMN ai_warnings            JSONB,
    ADD COLUMN ai_zones               JSONB;

-- +goose Down

ALTER TABLE spray_reports
    DROP COLUMN ai_zones,
    DROP COLUMN ai_warnings,
    DROP COLUMN ai_recommended_action,
    DROP COLUMN ai_explanation_ro,
    DROP COLUMN ai_risk_level,
    DROP COLUMN ai_risk_score,
    DROP COLUMN dose_kg_ha;
