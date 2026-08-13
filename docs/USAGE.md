# Usage

GCP Spot Observatory is a Go command-line application with three subcommands:

```text
gcp-spot-observatory migrate
gcp-spot-observatory crawl
gcp-spot-observatory web
```

Configuration precedence is always:

```text
command-line flag > environment variable > built-in default
```

Application environment variables use the `SPOT_OBSERVATORY_` prefix. Standard
Google authentication variables such as `GOOGLE_APPLICATION_CREDENTIALS` retain
their normal meanings.

## Prerequisites

- PostgreSQL 18
- Application Default Credentials with Compute Engine read permissions
- A Google Cloud project with the Compute Engine API already enabled

For local user credentials:

```sh
gcloud auth application-default login
```

The minimum practical predefined role is Compute Viewer (`roles/compute.viewer`).
The project is API context only and is never persisted by the application.

## Shared configuration

| Flag | Environment variable | Default | Commands | Description |
|---|---|---:|---|---|
| `--database-url` | `SPOT_OBSERVATORY_DATABASE_URL` | none | all | PostgreSQL connection URL. Required. |
| `--log-level` | `SPOT_OBSERVATORY_LOG_LEVEL` | `info` | all | `debug`, `info`, `warn`, or `error`. |

Database URLs may contain credentials and are never logged by the application.

## `migrate`

Connects to PostgreSQL, applies all outstanding embedded Goose migrations, and exits.
It does not crawl Google Cloud or modify collected application data.

```sh
gcp-spot-observatory migrate \
  --database-url 'postgres://postgres:postgres@127.0.0.1:5432/spot_observatory?sslmode=disable'
```

| Flag | Environment variable | Default |
|---|---|---:|
| `--database-url` | `SPOT_OBSERVATORY_DATABASE_URL` | none |
| `--log-level` | `SPOT_OBSERVATORY_LOG_LEVEL` | `info` |

Exit status is zero only when the database connection and every migration succeed.

## `crawl`

Runs migrations, creates a crawl-run record, discovers Compute Engine topology and
machine types, retrieves Spot capacity history, persists observations, and finalizes
the run. The crawler is single-threaded.

```sh
gcp-spot-observatory crawl \
  --database-url "$SPOT_OBSERVATORY_DATABASE_URL" \
  --gcp-project my-quota-project
```

| Flag | Environment variable | Default | Description |
|---|---|---:|---|
| `--database-url` | `SPOT_OBSERVATORY_DATABASE_URL` | none | PostgreSQL connection URL. |
| `--gcp-project` | `SPOT_OBSERVATORY_GCP_PROJECT` | none | Project used for auth, permissions, and quota context. |
| `--backfill` | `SPOT_OBSERVATORY_BACKFILL` | `false` | Store every historical interval returned by Google in addition to the normal crawl. |
| `--gcp-request-rate` | `SPOT_OBSERVATORY_GCP_REQUEST_RATE` | `2` | Maximum API requests per second. Burst is fixed at one. |
| `--gcp-request-timeout` | `SPOT_OBSERVATORY_GCP_REQUEST_TIMEOUT` | `30s` | Timeout for each API attempt, expressed as a Go duration. |
| `--region` | `SPOT_OBSERVATORY_REGIONS` | all | Limit collection to a region. Repeat the flag; use a comma-separated environment value. |
| `--machine-type` | `SPOT_OBSERVATORY_MACHINE_TYPES` | all | Limit collection to a machine type. Repeat the flag; use a comma-separated environment value. |
| `--log-level` | `SPOT_OBSERVATORY_LOG_LEVEL` | `info` | Logging verbosity. |

### Normal collection

A normal crawl stores only the newest price and preemption observation in each
capacity-history response. Google still returns its historical window; normal mode
deliberately avoids inserting that older material.

```sh
gcp-spot-observatory crawl
```

With no region or machine filters, discovery and collection are comprehensive and can
make many API calls. Runtime depends on the number of currently available combinations
and the configured request rate.

At the default `info` log level, the command reports each paginated discovery response,
then periodic `completed`/`total` progress for machine discovery, metadata processing,
regional history, and zonal history. Progress records also include cumulative API calls,
inserts, revisions, failures, and unsupported combinations. Retry warnings include the
attempt number and bounded backoff. This makes long comprehensive crawls observable
without enabling verbose request logging or exposing credentials.

### Scoped collection

Filters are useful for development or for operating an intentionally focused archive:

```sh
gcp-spot-observatory crawl \
  --region us-central1 \
  --machine-type e2-micro
```

When machine-type filters are present, the collector uses direct per-zone lookups
instead of fetching the complete aggregated machine-type catalog.

### Backfill

Backfill is always explicit and is safe to repeat:

```sh
gcp-spot-observatory crawl \
  --region us-central1 \
  --machine-type e2-micro \
  --backfill
```

It stores the full historical windows returned by `advice.capacityHistory`: currently
about one year of price intervals and 30 days of daily preemption rates where Google
has data. Existing identical observations are untouched. Changed historical values are
audited before the canonical row is updated.

The command continues after failures for individual machine/location combinations and
retains successful writes. Its final exit status is nonzero if any required collection
work was incomplete.

## `web`

Verifies that PostgreSQL has the current schema and starts the HTTP server. It never
runs migrations automatically.

```sh
gcp-spot-observatory web
```

| Flag | Environment variable | Default |
|---|---|---:|
| `--database-url` | `SPOT_OBSERVATORY_DATABASE_URL` | none |
| `--listen-address` | `SPOT_OBSERVATORY_LISTEN_ADDRESS` | `0.0.0.0:8080` |
| `--log-level` | `SPOT_OBSERVATORY_LOG_LEVEL` | `info` |

The UI presents only region/machine combinations with stored canonical observations.
It shows machine metadata, regional Spot pricing, regional preemption rates, and the
presets 7 days, 30 days, 90 days, 1 year, and all available history.

## Local PostgreSQL 18 with Apple `container`

On macOS with Apple's container runtime:

```sh
container run -d \
  --name gcp-spot-observatory-postgres \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=spot_observatory \
  -p 127.0.0.1:55432:5432 \
  postgres:18

export SPOT_OBSERVATORY_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55432/spot_observatory?sslmode=disable'
```

## Build and test

```sh
go build ./...
go test ./...
go vet ./...
```
