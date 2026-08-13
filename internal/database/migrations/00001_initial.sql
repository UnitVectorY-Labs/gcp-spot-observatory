-- +goose Up
CREATE TYPE crawl_status AS ENUM ('running', 'succeeded', 'failed');
CREATE TYPE observation_kind AS ENUM ('price', 'preemption');

CREATE TABLE crawl_runs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    status crawl_status NOT NULL DEFAULT 'running',
    backfill boolean NOT NULL DEFAULT false,
    api_calls integer NOT NULL DEFAULT 0,
    observations_inserted integer NOT NULL DEFAULT 0,
    observations_revised integer NOT NULL DEFAULT 0,
    failures integer NOT NULL DEFAULT 0,
    error_summary text
);

CREATE TABLE regions (
    id smallint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE,
    status text,
    deprecated_state text
);

CREATE TABLE zones (
    id smallint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    region_id smallint NOT NULL REFERENCES regions(id),
    name text NOT NULL UNIQUE,
    status text,
    deprecated_state text
);

CREATE TABLE machine_types (
    id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE,
    guest_cpus integer NOT NULL,
    memory_mb integer NOT NULL,
    architecture text,
    accelerators text,
    deprecated_state text
);

CREATE TABLE offerings (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    machine_type_id integer NOT NULL REFERENCES machine_types(id),
    region_id smallint REFERENCES regions(id),
    zone_id smallint REFERENCES zones(id),
    CHECK ((region_id IS NOT NULL)::integer + (zone_id IS NOT NULL)::integer = 1)
);
CREATE UNIQUE INDEX offerings_region_uq ON offerings(machine_type_id, region_id) WHERE region_id IS NOT NULL;
CREATE UNIQUE INDEX offerings_zone_uq ON offerings(machine_type_id, zone_id) WHERE zone_id IS NOT NULL;

CREATE TABLE spot_price_intervals (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    offering_id bigint NOT NULL REFERENCES offerings(id),
    start_time timestamptz NOT NULL,
    end_time timestamptz NOT NULL,
    currency_code char(3) NOT NULL,
    price_nanodollars bigint NOT NULL,
    first_seen_crawl_id bigint NOT NULL REFERENCES crawl_runs(id),
    CHECK (end_time > start_time),
    UNIQUE (offering_id, start_time, end_time)
);

CREATE TABLE preemption_observations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    offering_id bigint NOT NULL REFERENCES offerings(id),
    start_time timestamptz NOT NULL,
    end_time timestamptz NOT NULL,
    preemption_rate numeric(12, 10) NOT NULL,
    first_seen_crawl_id bigint NOT NULL REFERENCES crawl_runs(id),
    CHECK (end_time > start_time),
    CHECK (preemption_rate >= 0 AND preemption_rate <= 1),
    UNIQUE (offering_id, start_time, end_time)
);

CREATE TABLE observation_revisions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    observation_kind observation_kind NOT NULL,
    observation_id bigint NOT NULL,
    crawl_id bigint NOT NULL REFERENCES crawl_runs(id),
    detected_at timestamptz NOT NULL DEFAULT now(),
    old_price_nanodollars bigint,
    new_price_nanodollars bigint,
    old_preemption_rate numeric(12, 10),
    new_preemption_rate numeric(12, 10),
    CHECK (
      (observation_kind = 'price' AND old_price_nanodollars IS NOT NULL AND new_price_nanodollars IS NOT NULL
       AND old_preemption_rate IS NULL AND new_preemption_rate IS NULL)
      OR
      (observation_kind = 'preemption' AND old_preemption_rate IS NOT NULL AND new_preemption_rate IS NOT NULL
       AND old_price_nanodollars IS NULL AND new_price_nanodollars IS NULL)
    )
);

CREATE INDEX spot_price_intervals_lookup_idx ON spot_price_intervals(offering_id, start_time);
CREATE INDEX preemption_observations_lookup_idx ON preemption_observations(offering_id, start_time);
CREATE INDEX observation_revisions_lookup_idx ON observation_revisions(observation_kind, observation_id);

-- +goose Down
DROP TABLE observation_revisions;
DROP TABLE preemption_observations;
DROP TABLE spot_price_intervals;
DROP TABLE offerings;
DROP TABLE machine_types;
DROP TABLE zones;
DROP TABLE regions;
DROP TABLE crawl_runs;
DROP TYPE observation_kind;
DROP TYPE crawl_status;
