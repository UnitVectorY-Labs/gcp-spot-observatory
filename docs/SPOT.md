# GCP Spot VMs and collected data

## What Spot VMs are

[Spot VMs](https://cloud.google.com/compute/docs/instances/spot) use otherwise
available Compute Engine capacity at prices that can be substantially lower than
standard on-demand VMs. In exchange, Compute Engine can preempt a Spot VM whenever it
needs to reclaim that capacity. Spot VMs have no Compute Engine SLA and are appropriate
for fault-tolerant, restartable workloads.

Google documents discounts of up to 91% for many resources. Spot prices are variable
and can change up to once per day. With the default preemption behavior, a workload has
a best-effort shutdown period of up to 30 seconds after Compute Engine begins stopping
the VM. See [Create and use Spot VMs](https://cloud.google.com/compute/docs/instances/create-use-spot)
for operational guidance.

GCP Spot Observatory does not create or operate Spot VMs. It only reads public capacity
history and machine/location metadata.

## What the history means

The beta capacity-history API returns two independent series:

### Price history

`priceHistory` contains the hourly list price and the interval during which it was
effective. Pricing is regional, expressed as a Google `Money` value, and currently has
about a one-year public history window. Price changes are set at midnight Pacific Time,
and gaps can exist when data is unavailable.

The collector converts `Money.units` and `Money.nanos` into exact integer nanodollars.
It does not call the Cloud Billing Catalog API and does not attempt to calculate custom
contract prices.

### Preemption history

`preemptionHistory` contains daily rates for a machine type and location, currently for
about the previous 30 days. Google defines the rate as the number of Spot VMs preempted
divided by all Spot VMs of that machine type and location that stopped that day. A rate
of `0.50` means half of those stopped Spot VMs were preempted; it does not reveal VM
counts. Day boundaries are midnight Pacific Time, and the current day's rate can change.

The collector stores only the rate Google reports. Missing history remains missing.
See Google's [preemption rate and pricing guide](https://cloud.google.com/compute/docs/instances/view-spot-preemption-price).

## Compute Engine APIs called

All requests use Application Default Credentials with the
`https://www.googleapis.com/auth/compute.readonly` OAuth scope and the beta Compute
Engine REST endpoint. The configured project supplies IAM and quota context only.

### 1. Discover regions and zones

```http
GET /compute/beta/projects/{project}/regions
```

This paginated call discovers available regions. Each region resource includes its
zone URLs, which provide the region-to-zone topology without a hard-coded location list.

Documentation: [`regions.list`](https://cloud.google.com/compute/docs/reference/rest/beta/regions/list)

### 2. Discover all machine types

```http
GET /compute/beta/projects/{project}/aggregated/machineTypes
```

An unfiltered crawl pages through the aggregated catalog and associates every returned
predefined machine type with its zone. Custom machine types are excluded because they
are synthesized configurations and are unsupported by capacity history.

Documentation: [`machineTypes.aggregatedList`](https://cloud.google.com/compute/docs/reference/rest/beta/machineTypes/aggregatedList)

### 3. Discover explicitly filtered machine types

```http
GET /compute/beta/projects/{project}/zones/{zone}/machineTypes/{machineType}
```

When one or more `--machine-type` filters are supplied, direct lookups replace the much
larger aggregated catalog request. One lookup is made for each selected machine type in
each zone of the selected regions. This makes focused crawls and backfills efficient.

Documentation: [`machineTypes.get`](https://cloud.google.com/compute/docs/reference/rest/beta/machineTypes/get)

### 4. Retrieve regional capacity history

```http
POST /compute/beta/projects/{project}/regions/{region}/advice/capacityHistory
```

For every discovered region/machine combination, the body requests `PREEMPTION` and
`PRICE` with `provisioningModel` set to `SPOT`. This produces regional price history and
regional preemption history in one request.

### 5. Retrieve zonal preemption history

The collector calls the same `advice.capacityHistory` endpoint once for every valid
zone/machine combination, requests only `PREEMPTION`, and supplies:

```json
{
  "locationPolicy": {
    "location": "zones/ZONE"
  }
}
```

Pricing is not requested again because it is regional.

Documentation for both request shapes:
[`advice.capacityHistory`](https://cloud.google.com/compute/docs/reference/rest/beta/advice/capacityHistory)

The capacity-history feature is currently documented as Preview/Pre-GA and requires the
`compute.advice.capacityHistory` permission. Google lists N1 machine types with attached
GPUs, custom machine types, and TPUs as unsupported for this history interface.

## Normal crawl versus backfill

The API returns its available historical window on each request; it does not expose a
separate backfill endpoint. The application controls how much of the response is stored:

- A normal crawl persists only the newest price and preemption entry from each response.
- `crawl --backfill` persists every returned historical entry.

Both paths use identical logical identities, so backfill is additive and idempotent.
A scoped backfill such as `--region us-central1 --machine-type e2-micro --backfill`
retrieves a small, targeted history without performing global machine-type discovery.

## API courtesy and failure handling

The client is single-threaded, uses a configurable token-bucket limiter with a burst of
one, paginates discovery, and retries HTTP 429 and selected 5xx responses up to five
attempts with exponential backoff and jitter. Permanent errors are not retried. Google
also recommends client-side rate limiting and exponential backoff in its
[Compute Engine API best practices](https://cloud.google.com/compute/docs/api/best-practices).

Unsupported 400/404 capacity-history combinations are skipped. Other per-combination
failures are recorded while unrelated collection continues; any such incomplete crawl
still exits nonzero.
