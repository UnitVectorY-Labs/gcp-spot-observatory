# AGENTS.md

## Project

GCP Spot Observatory is a Go CLI that preserves Google Cloud Spot VM price and
preemption history in PostgreSQL 18 and exposes a minimal server-rendered HTMX/Chart.js
viewer. Start with `docs/README.md`; operational, schema, and API details live in the
other files under `docs/`.

## Commands and layout

- `go run . migrate`: apply embedded Goose migrations.
- `go run . crawl`: single-threaded discovery and collection; runs migrations first.
- `go run . web`: serve the UI; must not run migrations.
- `internal/command`: Cobra commands and `SPOT_OBSERVATORY_*` configuration.
- `internal/gcp`: ADC-authenticated, rate-limited Compute REST client.
- `internal/crawl`: discovery, persistence, idempotency, and revision handling.
- `internal/database`: connection handling and embedded SQL migrations.
- `internal/web`: server-rendered UI and database queries.

## Non-obvious invariants

- The GCP project is quota/IAM context only. Never persist it or expose it in the UI.
- Normal crawls store only the newest returned entries; historical storage requires
  explicit `--backfill`. Both modes must remain idempotent.
- Canonical money is integer nanodollars, never floating point. Missing preemption data
  is absence, never zero.
- Every canonical observation retains its first-seen crawl. Changed historical values
  must be audited before updating the canonical row; unchanged values write nothing.
- Preserve all collected history. Do not add pruning, TTLs, or implicit deletion.
- Keep crawling single-threaded, rate-limited, retry-bounded, and best-effort per
  machine/location. Any incomplete required crawl exits nonzero without rolling back
  earlier successful observations.
- Do not store raw API responses, credentials, access tokens, or verbose logs in PostgreSQL.

## Development

- Add forward Goose migrations; do not rewrite shipped migrations.
- Keep the web command read-only with respect to schema and collected data.
- Run `gofmt -w`, `go test ./...`, `go vet ./...`, and `go build ./...` before handoff.
- On macOS, use Apple's `container` command—not Docker or Podman—for PostgreSQL 18 tests.
- Preserve user changes in a dirty worktree and avoid unrelated formatting or cleanup.
