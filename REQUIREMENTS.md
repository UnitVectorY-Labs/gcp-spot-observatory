# GCP Spot Capacity History Collector Requirements

## 1. Purpose

Build a small Go application that collects and preserves Google Cloud Platform Spot VM pricing and preemption history in PostgreSQL.

The primary motivation is long-term analysis of GCP capacity behavior, particularly changes in Spot pricing and preemption rates that may reflect supply pressure, demand pressure, and broader cloud capacity trends.

Google currently exposes historical Spot VM price and preemption information through the Compute Engine `capacityHistory` API. The API supports `PRICE` and `PREEMPTION` history for Spot VMs by machine type and location. citeturn953147search0

The application should prioritize:

- completeness of collected data
- long-term preservation
- storage efficiency
- API efficiency
- operational simplicity
- idempotency
- minimal dependencies and architecture

This is an MVP. The collection pipeline and database should be designed carefully because the resulting historical dataset is intended to live indefinitely. The first web interface is intentionally simple and expected to be replaced later.

---

# 2. Technology Constraints

The application shall use:

- Go
- PostgreSQL 18
- HTMX for interactive web behavior
- Chart.js for MVP charts
- server-rendered HTML
- an idiomatic Go CLI library such as Cobra
- an idiomatic Go PostgreSQL migration library
- Goose is the preferred migration library
- SQL migrations embedded into the compiled Go binary

Goose supports PostgreSQL and embedded migrations through Go's `embed.FS`, making it suitable for a self-contained binary. citeturn953147search3

The application shall not require:

- React
- Vue
- Angular
- a SPA frontend
- authentication
- authorization
- user accounts
- Redis
- a message queue
- background workers
- distributed crawling
- multiple crawler processes
- raw API response storage

The crawler shall operate single-threaded.

Container deployment is expected operationally, but container images, Dockerfiles, Kubernetes manifests, Cloud Run configuration, and deployment automation are outside the scope of this requirements document.

---

# 3. Command-Line Interface

The application shall expose three subcommands:

```text
<app> migrate
<app> crawl
<app> web
```

All relevant runtime configuration shall be available through both:

1. command-line flags
2. corresponding environment variables

Configuration precedence shall be:

```text
CLI flag
environment variable
built-in default
```

where a built-in default is appropriate.

A single consistent application-specific environment-variable prefix shall be used.

Generic environment names that are part of standard Google authentication behavior, such as `GOOGLE_APPLICATION_CREDENTIALS`, do not need to be renamed or duplicated.

---

# 4. Shared Database Configuration

All commands that access PostgreSQL shall use the same database configuration mechanism.

At minimum, the application shall support a PostgreSQL connection URL.

Conceptually:

```text
--database-url
<APP_PREFIX>_DATABASE_URL
```

The exact application prefix may be selected during implementation, but it shall be consistent across the application.

Secrets shall not be logged.

---

# 5. `migrate` Command

`migrate` shall:

1. connect to PostgreSQL
2. run all outstanding embedded database migrations
3. exit after migrations complete

Successful completion shall return exit code `0`.

Any connection or migration failure shall return a non-zero exit code.

The command shall not:

- crawl GCP
- start the web server
- modify application data beyond what migrations require

---

# 6. `crawl` Command

## 6.1 General Behavior

`crawl` is the primary data collection command.

A normal run shall:

1. connect to PostgreSQL
2. create or migrate the application schema if necessary
3. create a crawl-run record
4. discover relevant GCP regions, zones, and machine types
5. determine the valid machine/location combinations that should be queried
6. query current Spot pricing and preemption data
7. persist newly discovered metadata and observations
8. detect changes to previously recorded historical observations
9. record those changes when encountered
10. finalize the crawl-run record
11. return exit code `0` only if the required crawl completed successfully

The crawler shall be single-threaded.

---

# 7. GCP Authentication and Project Configuration

The GCP project ID exists only to provide the necessary API context, permissions, and quota context.

It shall:

- be supplied through runtime configuration
- never be persisted in the application database
- never be shown in the web interface
- never become part of the identity of collected data

The application should use Google's normal Application Default Credentials behavior where practical.

Explicit GCP authentication configuration should not duplicate functionality already provided by Google's standard authentication mechanisms without a clear need.

---

# 8. GCP Data Discovery

The crawler shall discover the available GCP topology rather than depending on a hard-coded list of regions, zones, or machine types.

Machine types can be discovered using Compute Engine machine type listing APIs, including aggregated machine-type discovery. Machine type metadata includes values such as machine name, vCPU count, memory, architecture, accelerator information, and deprecation state. citeturn953147search1turn953147search5

The crawler shall collect whatever machine types and locations are relevant to the historical capacity API and return useful data.

The crawler shall not impose an artificial allowlist of machine families.

Deprecated or obsolete resources shall not cause previously collected information to be deleted.

If historical data exists for a resource, that data shall remain in PostgreSQL indefinitely.

---

# 9. Spot Capacity History

The Compute Engine capacity-history interface supports historical Spot VM data for:

- `PRICE`
- `PREEMPTION`

and can request the relevant history for a specific machine type and location. citeturn953147search0

The application shall preserve both data types.

## 9.1 Price Data

Spot price history shall be retained indefinitely.

The database shall preserve Google's effective historical intervals rather than expanding an unchanged price into redundant daily rows.

For example, if the same price applies for a continuous interval, that interval should normally be represented by one database record rather than one record per day.

Price values shall be stored exactly and without floating-point rounding.

Where appropriate, represent currency using an integer unit such as nanodollars or another exact compact integer representation.

Floating-point database types shall not be used for canonical monetary values.

## 9.2 Preemption Data

Preemption-rate history shall be retained indefinitely.

This is a core requirement because Google's publicly exposed historical preemption window is limited compared with the long-term history this project intends to build.

Preemption values shall be stored as the rate reported by Google.

The application shall not attempt to infer VM counts or fabricate numerator/denominator values that the API does not provide.

Missing preemption data must remain missing.

Missing data shall never be converted to a zero preemption rate.

---

# 10. Location Granularity

The schema shall preserve the location granularity required by Google's returned data.

Pricing and preemption information may have different useful location scopes.

The data model shall therefore distinguish:

- regions
- zones

without duplicating location strings throughout observation tables.

The system shall preserve enough information to correctly associate each observation with the location scope returned by the API.

---

# 11. Metadata Storage

The database shall store only useful normalized metadata.

The project shall not attempt to mirror the full Compute Engine API schema.

Machine type metadata should include basic fields useful for future interpretation, such as:

- machine type name
- vCPU count
- memory
- architecture where available
- relevant accelerator information where useful
- lifecycle or deprecation status

Google's machine type resource exposes this type of metadata and lifecycle state. citeturn953147search1

Region and zone records shall store only the basic metadata necessary to identify and interpret them.

Raw API responses shall never be stored.

---

# 12. Normalization and Storage Efficiency

Database normalization is a primary requirement.

Repeated strings and metadata shall not be copied into every historical observation.

Short internal identifiers should be used where they reduce storage without making the schema unnecessarily complicated.

A conceptual model is:

```text
machine_types
regions
zones
offerings / machine-location relationships
crawl_runs
spot_price_intervals
preemption_observations
observation_revisions
```

Exact table names and decomposition may vary during schema design, but the following principles are mandatory:

- machine type metadata is stored once
- location metadata is stored once
- observations reference normalized entities by foreign key
- unchanged historical values are not duplicated
- raw JSON is not retained
- project IDs are not retained
- crawl provenance should use foreign keys rather than repeated timestamps where doing so is more compact
- canonical history tables remain simple

PostgreSQL 18 is the target database version.

---

# 13. Crawl Runs

Every invocation of `crawl`, including a backfill crawl, shall create a distinct crawl-run record.

A crawl run should store compact operational metadata sufficient to understand whether collection completed successfully.

Useful fields include:

- crawl-run identifier
- start timestamp
- completion timestamp
- final status
- number of relevant API calls
- number of observations inserted
- number of observations revised
- number of failures
- concise failure information if applicable

The schema should avoid verbose logging data inside PostgreSQL.

Application logs remain the appropriate place for detailed operational diagnostics.

Crawl-run records are primarily intended to establish provenance and distinguish:

- data not reported by GCP
- data never queried because a crawl failed
- data successfully observed

---

# 14. Observation Provenance

When a canonical observation is first inserted, it shall reference the crawl run that first discovered it.

Repeated observation of exactly the same value shall not:

- create another observation
- change the first-seen crawl
- create an audit entry
- store another copy of the value

The association is with the crawl that first discovered the historical record.

There is no requirement to track every subsequent crawl that confirmed an unchanged value.

This is intentionally designed to minimize storage.

---

# 15. Historical Revisions

Google does not need to be assumed to rewrite historical data routinely, but the system shall defensively detect this situation.

When the crawler requests an observation whose logical identity already exists in PostgreSQL:

### If the value is unchanged

Do nothing to the canonical observation.

### If the value has changed

The crawler shall:

1. record the change in a dedicated audit table
2. preserve the previous value in that audit record
3. preserve the newly observed value in that audit record
4. record which crawl detected the change
5. record when the change was detected
6. update the canonical observation to the newly returned value

The preferred conceptual name for this table is:

```text
observation_revisions
```

The table should record only actual changes.

It shall not contain duplicate snapshots of every observation.

The canonical history tables shall always represent the latest value returned by Google.

The combination of the canonical table and revision records must make it possible to determine what value had previously been stored.

The MVP web interface does not expose revision data.

---

# 16. Idempotency

Both normal crawls and backfill crawls shall be idempotent.

Running the same crawl repeatedly against unchanged GCP data shall not create duplicate:

- machine types
- regions
- zones
- machine/location relationships
- price observations
- preemption observations

Uniqueness should be enforced at the database level where practical, not solely through application logic.

If the same historical data is returned repeatedly, the resulting canonical database state shall remain unchanged.

A new `crawl_runs` record may be created for each invocation.

---

# 17. Backfill Mode

The crawler shall support:

```text
crawl --backfill
```

with a corresponding environment-variable configuration option.

Backfill shall never happen implicitly merely because the database is empty or newly created.

A normal first crawl behaves exactly like any later normal crawl.

## 17.1 Backfill Behavior

`--backfill` is additive.

It shall perform:

1. the complete normal crawl
2. the historical price backfill available through GCP
3. the historical preemption backfill available through GCP

Backfill shall be idempotent.

Historical observations already present in PostgreSQL shall not be duplicated.

If a backfill response contains a historical observation already stored with the same value, nothing shall be written for that observation.

If it contains a different value for an existing historical observation, normal revision behavior applies.

Running `--backfill` multiple times must be safe.

---

# 18. API Efficiency

Efficient use of GCP APIs is an explicit requirement.

The crawler shall avoid unnecessary calls whenever practical.

In particular, it should:

- perform discovery efficiently
- avoid querying clearly invalid machine/location combinations
- avoid multiple API requests where one request can retrieve the required information
- avoid unnecessary polling
- avoid refetching additional historical data beyond what the API forces it to return
- avoid unnecessary database writes after receiving repeated data

The application should not sacrifice discovery completeness solely to reduce request count.

The goal is comprehensive collection without waste.

---

# 19. Rate Limiting

The crawler shall include configurable client-side rate limiting for GCP API calls.

Google recommends client-side rate limiting and exponential backoff for Compute Engine API clients. citeturn953147search2

At minimum, the application should support configuration conceptually equivalent to:

```text
--gcp-request-rate
<APP_PREFIX>_GCP_REQUEST_RATE
```

A burst setting may be supported if the implementation uses a rate limiter that benefits from it, but because the crawler is single-threaded, burst behavior should remain conservative.

The default shall intentionally favor API courtesy over crawl speed.

The crawler is not time-sensitive.

---

# 20. Retries

Retryable GCP failures shall use exponential backoff.

Where the official Google Go client already provides suitable retry handling, the implementation should use that behavior rather than layering redundant retry loops over it.

Additional application retry logic may be added only where needed.

Retry behavior shall distinguish retryable and permanent failures based on structured error codes rather than matching human-readable messages.

Google explicitly recommends retry loops with exponential backoff and relying on canonical error codes instead of message text. citeturn953147search2

Retries must eventually terminate.

---

# 21. Partial Crawl Failure

The crawler shall be best-effort within a single run.

A failure involving one machine/location combination shall not automatically discard successful observations collected earlier in the crawl.

Successfully collected data shall be committed and retained.

After retries are exhausted, the crawler shall:

- record the failure
- continue with unrelated crawl work when safe
- complete as much of the comprehensive crawl as possible

The overall process shall return exit code `0` only if the required crawl was fully successful.

Any incomplete required crawl shall return a non-zero exit code.

The crawler shall not wrap the entire crawl in a single transaction whose rollback would discard otherwise valid collected observations.

---

# 22. Logging

The application shall produce human-readable operational logs to stdout/stderr.

Logs should identify:

- command startup
- migration results
- crawl start/completion
- API failures
- retries when operationally useful
- revision detection
- database errors
- final crawl summary
- web server startup/failure

Logs shall not contain:

- database passwords
- service account private keys
- access tokens
- credential file contents

A configurable log level is appropriate.

Logging infrastructure should remain simple.

---

# 23. `web` Command

The `web` command shall:

1. connect to PostgreSQL
2. verify that the required application schema is present and current
3. fail clearly if the database does not exist, cannot be reached, or has not been migrated appropriately
4. start the HTTP server

Unlike `crawl`, `web` shall not automatically run migrations.

The default listen address shall be:

```text
0.0.0.0:8080
```

The listen address shall be configurable using both a CLI flag and corresponding environment variable.

---

# 24. MVP Web Interface

The initial web interface is deliberately minimal and temporary.

Its purpose is to verify that collected information can be inspected easily.

It shall not be treated as the final analytical product.

The web interface shall have:

- no authentication
- no authorization
- no accounts
- no administration UI
- no revision-history UI
- no crawl-management UI
- no statistics dashboard
- no derived analytics
- no multi-series comparison tooling

---

# 25. MVP Data Selection

The user shall be able to select a single relevant machine/location combination.

The UI may expose selectors for:

- region
- zone where relevant
- machine type

Only combinations represented by stored data should be presented.

HTMX should be used to update dependent selections and displayed content without requiring a SPA.

---

# 26. MVP Charts

For the selected machine/location combination, the UI shall show:

1. Spot price history
2. preemption-rate history

Both shall be rendered as line charts using Chart.js.

The charts shall display the stored canonical values only.

Historical revision/audit records shall not appear in the MVP.

No derived statistics shall be calculated or displayed.

---

# 27. Date Ranges

The MVP shall provide preset date ranges:

- 7 days
- 30 days
- 90 days
- 1 year
- all available history

The default shall be:

```text
30 days
```

A requested preset must be constrained by the oldest data actually available for the selected series.

The UI shall not imply that data exists before the earliest stored observation.

There is no requirement for arbitrary custom start/end date input in the MVP.

---

# 28. Data Retention

All collected historical capacity data shall be retained indefinitely.

The application shall contain no:

- pruning job
- TTL mechanism
- historical deletion process
- automated retention limit

Historical machine types and locations must remain available in the database even if they later cease to appear in GCP discovery results.

Storage efficiency shall come from normalization and compact representation, not deletion.

---

# 29. Explicit Non-Goals for the MVP

The following are out of scope:

- deployment automation
- Dockerfile requirements
- Kubernetes manifests
- infrastructure-as-code
- authentication
- authorization
- user management
- revision-history UI
- statistical analysis
- correlation analysis
- capacity scoring
- price volatility calculations
- preemption summaries
- cross-region comparison
- multi-machine comparison charts
- alerting
- notifications
- API endpoints intended for third parties
- raw API archival
- GCP project tracking
- automatic data pruning
- parallel crawling
- distributed crawling
- background workers
- custom-machine price synthesis
- speculative reconstruction of missing observations

These may be revisited after the collection pipeline has proven reliable.

---

# 30. Core Correctness Invariants

The implementation shall preserve the following invariants.

### Data identity

A logical historical observation must exist no more than once in its canonical table.

### Idempotency

Repeating the same crawl against the same source data does not increase canonical historical row counts.

### Provenance

Every canonical historical observation can be traced to the crawl that first discovered it.

### Revisions

If a previously observed historical value changes, that change is explicitly recorded before the canonical value is replaced.

### Missing data

Absence of data is not interpreted as zero.

### Exact pricing

Money is never stored using an inexact floating-point representation.

### Retention

Collected historical observations are not automatically removed.

### API courtesy

The crawler limits request rate and retries transient failures responsibly.

### Crawl status

Exit code `0` means the complete required crawl succeeded.

### Project independence

The GCP project used to access the API never becomes part of the collected dataset.

---

# 31. MVP Success Criteria

The MVP is complete when all of the following are true:

1. A fresh PostgreSQL 18 database can be initialized with `migrate`.

2. `crawl` can initialize/migrate the schema automatically if needed.

3. A normal `crawl` discovers relevant Compute Engine machine/location data and stores current Spot price and preemption information.

4. `crawl --backfill` additionally imports the available historical Spot price and preemption windows.

5. Repeating either form of crawl creates no redundant canonical historical observations.

6. Repeated identical source values cause no historical data writes other than creation/finalization of the new crawl-run record.

7. If Google returns a changed value for an already stored historical observation, the change is recorded in `observation_revisions` and the canonical value is updated.

8. Temporary API failures are retried using appropriate exponential backoff.

9. GCP API calls are constrained by a configurable client-side rate limiter.

10. Partial crawl failures preserve successfully collected data but cause the command to exit non-zero.

11. `web` refuses to start against an absent or inadequately migrated database.

12. `web` starts on `0.0.0.0:8080` by default.

13. The MVP web UI lets a user select one stored machine/location combination.

14. The UI displays both Spot price history and preemption-rate history.

15. The UI supports the preset historical ranges of 7 days, 30 days, 90 days, 1 year, and all available data.

16. The default displayed range is 30 days.

17. No raw GCP API payloads are stored.

18. Historical data is retained indefinitely.

19. The resulting database is compact, normalized, and suitable for later analytical work without requiring the collection pipeline to be redesigned.