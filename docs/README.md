# GCP Spot Observatory

GCP Spot Observatory builds a durable history of Google Cloud Spot VM prices and
preemption rates. It collects the short-lived history exposed by Compute Engine,
stores it efficiently in PostgreSQL, and provides a small browser interface for
inspecting one region and machine type at a time.

Google Cloud publishes useful Spot capacity signals, but the public history is
bounded: preemption history covers roughly 30 days and price history covers roughly
one year. GCP Spot Observatory is designed to run repeatedly so those observations
can be retained indefinitely for later capacity and pricing analysis.

## What it provides

- Automatic discovery of Compute Engine regions, zones, and predefined machine types.
- Regional Spot price history and regional or zonal preemption-rate history.
- Exact monetary storage in integer nanodollars—never floating-point canonical prices.
- Compact interval storage instead of redundant daily price snapshots.
- Idempotent crawls with database-enforced observation identities.
- Audit records when Google revises a previously observed historical value.
- A single-threaded, rate-limited API client with bounded exponential-backoff retries.
- A server-rendered HTMX and Chart.js explorer with region, machine type, and date-range selectors.
- Embedded PostgreSQL migrations in one self-contained Go binary.

## How it works

1. `migrate` initializes or upgrades PostgreSQL.
2. `crawl` discovers valid machine/location relationships and collects the newest data.
3. An explicit `crawl --backfill` stores every historical interval returned by Google.
4. Scheduled, repeated crawls preserve new observations while ignoring unchanged data.
5. `web` exposes the stored canonical history without starting a crawler or changing the schema.

The GCP project supplied to the crawler is used only for authentication, permissions,
and API quota context. It is never stored and never appears in the web interface.
The collector only reads Compute Engine metadata and capacity history; it does not
create, start, stop, or modify virtual machines.

## Design priorities

The collection pipeline favors long-term correctness over crawl speed. Canonical
observations retain first-seen provenance, missing data remains missing, historical
records are never automatically pruned, and successful work survives partial crawl
failures. The crawler is deliberately single-threaded and polite to the Compute API.

## Documentation

- [Usage and configuration](USAGE.md)
- [Database schema](DATABASE.md)
- [Spot VMs and Google APIs](SPOT.md)

This project is an MVP. The collection model is intended to be durable; the web
interface is intentionally small and can evolve independently.
