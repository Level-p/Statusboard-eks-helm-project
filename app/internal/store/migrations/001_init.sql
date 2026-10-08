-- Components are the parts of your service that users care about (Website, API, Payments...)
CREATE TABLE components (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT        NOT NULL UNIQUE,
    description TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'operational'
                CHECK (status IN ('operational', 'degraded', 'partial_outage', 'major_outage')),
    position    INT         NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- An incident is something going wrong, with a history of updates
CREATE TABLE incidents (
    id          BIGSERIAL PRIMARY KEY,
    title       TEXT        NOT NULL,
    impact      TEXT        NOT NULL CHECK (impact IN ('minor', 'major', 'critical')),
    status      TEXT        NOT NULL DEFAULT 'investigating'
                CHECK (status IN ('investigating', 'identified', 'monitoring', 'resolved')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX incidents_open_idx ON incidents (created_at DESC) WHERE status <> 'resolved';

CREATE TABLE incident_updates (
    id          BIGSERIAL PRIMARY KEY,
    incident_id BIGINT      NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    status      TEXT        NOT NULL,
    message     TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX incident_updates_incident_idx ON incident_updates (incident_id, created_at DESC);
