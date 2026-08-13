[![License](https://img.shields.io/badge/license-MIT-blue.svg)](https://opensource.org/licenses/MIT) [![Work In Progress](https://img.shields.io/badge/Status-Work%20In%20Progress-yellow)](https://guide.unitvectorylabs.com/bestpractices/status/#work-in-progress) 

# gcp-spot-observatory

Tracks historical GCP Spot VM pricing and preemption rates across regions and machine types.

See the [project overview](docs/README.md), [usage guide](docs/USAGE.md),
[database reference](docs/DATABASE.md), and [Spot/API reference](docs/SPOT.md).

## Quick start

The application uses Application Default Credentials and never stores the GCP project ID.

```sh
export SPOT_OBSERVATORY_DATABASE_URL='postgres://postgres:postgres@localhost:5432/spot_observatory?sslmode=disable'
export SPOT_OBSERVATORY_GCP_PROJECT='your-project-id'

go run . migrate
go run . crawl
go run . web
```

`crawl` stores only the most recent price and preemption observations returned by Google. Use
`crawl --backfill` explicitly to retain the full historical windows. For bounded development
checks, repeat `--region` and `--machine-type`; without those flags the crawl discovers all
available combinations.

Configuration is available through flags and the corresponding `SPOT_OBSERVATORY_*`
environment variables. Run `go run . <command> --help` for the full list.
