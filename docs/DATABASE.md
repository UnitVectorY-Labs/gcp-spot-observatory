# Database schema

GCP Spot Observatory targets PostgreSQL 18. Goose SQL migrations are embedded in the
compiled binary from `internal/database/migrations`. `migrate` and `crawl` apply them;
`web` only verifies that the expected version is already installed.

## Relationship overview

```text
regions ──< zones
   │          │
   └──< offerings >── machine_types
             │
             ├──< spot_price_intervals >── crawl_runs
             └──< preemption_observations >── crawl_runs
                         │
canonical observations ─┴──< observation_revisions >── crawl_runs
```

An offering joins one machine type to exactly one region or one zone. Regional
offerings carry regional pricing and preemption history. Zonal offerings preserve the
more specific preemption history returned for a zone.

## Tables

### `crawl_runs`

One row is created for every invocation of `crawl`, including failed and backfill runs.
It records start/completion times, `running`/`succeeded`/`failed` status, whether the run
was a backfill, API-call and write counters, a failure count, and a concise failure
summary. Detailed logs remain outside PostgreSQL.

The Google Cloud project ID is intentionally absent.

### `regions`

Stores each region name once with its current API status and optional deprecation state.
Region names are unique. Small integer identities keep observation relationships compact.

### `zones`

Stores each zone name once and relates it to its parent region. It also has optional
status and deprecation metadata. Previously collected zones are never deleted merely
because discovery no longer returns them.

### `machine_types`

Stores normalized interpretation metadata once per machine-type name:

- guest vCPU count
- memory in MB
- architecture, when reported
- a compact accelerator summary, when applicable
- deprecation state

The application does not mirror the complete Compute Engine machine-type resource and
does not retain raw JSON.

### `offerings`

Represents a valid machine/location relationship. Exactly one of `region_id` and
`zone_id` must be present. Partial unique indexes guarantee one regional and one zonal
offering per machine/location pair.

### `spot_price_intervals`

Stores canonical regional Spot price intervals. Identity is the offering plus inclusive
start and exclusive end timestamps. Currency uses an ISO 4217 code and the amount is a
signed 64-bit integer in nanodollars (`10^-9` currency units), preserving Google's
`Money.units` and `Money.nanos` exactly without floating-point rounding.

Each row references the crawl that first discovered it. Re-observing the same value does
not change that provenance or write another row.

### `preemption_observations`

Stores canonical daily preemption intervals at the scope returned or requested. Rates
use `numeric(12,10)`, are constrained to 0 through 1, and are never synthesized. Missing
data produces no row; it is not converted to zero.

Identity and first-seen provenance follow the same rules as price intervals.

### `observation_revisions`

An append-only audit of actual value changes. A revision identifies the canonical price
or preemption row, the crawl that detected the change, detection time, and the exact old
and new values. The old value is recorded before the canonical row is updated.

Unchanged observations never create revision records.

## Integrity and storage properties

- Unique constraints enforce canonical observation identity.
- Normalized foreign keys prevent repeated region, zone, and machine strings in history.
- Price values are exact integers; preemption values are exact PostgreSQL numerics.
- Price intervals are not expanded into redundant daily snapshots.
- Canonical observations always represent the latest value returned by Google.
- `first_seen_crawl_id` is immutable during unchanged observations and revisions.
- There are no TTLs, pruning jobs, or cascading deletion workflows.
- Raw API responses, credentials, access tokens, and GCP project IDs are never stored.

Indexes support offering/time chart queries and revision lookup. Schema changes should
be introduced through new forward migrations once a migration has shipped.
