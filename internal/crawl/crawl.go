package crawl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/UnitVectorY-Labs/gcp-spot-observatory/internal/gcp"
)

type Config struct {
	Backfill     bool
	Regions      []string
	MachineTypes []string
}

type Summary struct {
	RunID                int64
	APICalls             int
	ObservationsInserted int
	ObservationsRevised  int
	Failures             int
	Unsupported          int
}

type collector struct {
	db       *sql.DB
	client   *gcp.Client
	log      *slog.Logger
	config   Config
	summary  Summary
	failures []string
}

type zoneRecord struct {
	id         int64
	regionID   int64
	regionName string
}
type machineRecord struct {
	id      int64
	machine gcp.MachineType
}

type progressReporter struct {
	log   *slog.Logger
	phase string
	total int
	last  time.Time
}

func (p *progressReporter) report(completed int, summary Summary) {
	now := time.Now()
	if completed != 1 && completed != p.total && !p.last.IsZero() && now.Sub(p.last) < 10*time.Second {
		return
	}
	p.log.Info("crawl progress", "phase", p.phase, "completed", completed, "total", p.total,
		"api_calls", summary.APICalls, "inserted", summary.ObservationsInserted,
		"revised", summary.ObservationsRevised, "failures", summary.Failures,
		"unsupported", summary.Unsupported)
	p.last = now
}

func Run(ctx context.Context, db *sql.DB, client *gcp.Client, config Config, log *slog.Logger) (Summary, error) {
	c := &collector{db: db, client: client, config: config, log: log}
	if err := db.QueryRowContext(ctx, `INSERT INTO crawl_runs (backfill) VALUES ($1) RETURNING id`, config.Backfill).Scan(&c.summary.RunID); err != nil {
		return c.summary, fmt.Errorf("create crawl run: %w", err)
	}
	log.Info("crawl started", "run_id", c.summary.RunID, "backfill", config.Backfill)
	err := c.collect(ctx)
	if err != nil {
		c.summary.Failures++
		c.failures = append(c.failures, "crawl discovery or setup failed")
	}
	c.summary.APICalls = client.Calls()
	status := "succeeded"
	if err != nil || c.summary.Failures > 0 {
		status = "failed"
	}
	errorSummary := strings.Join(c.failures, "; ")
	if len(errorSummary) > 2000 {
		errorSummary = errorSummary[:2000]
	}
	_, finishErr := db.ExecContext(ctx, `UPDATE crawl_runs SET completed_at=now(), status=$2, api_calls=$3,
		observations_inserted=$4, observations_revised=$5, failures=$6, error_summary=NULLIF($7, '') WHERE id=$1`,
		c.summary.RunID, status, c.summary.APICalls, c.summary.ObservationsInserted, c.summary.ObservationsRevised, c.summary.Failures, errorSummary)
	if finishErr != nil && err == nil {
		err = fmt.Errorf("finalize crawl run: %w", finishErr)
	}
	log.Info("crawl completed", "run_id", c.summary.RunID, "status", status, "api_calls", c.summary.APICalls,
		"inserted", c.summary.ObservationsInserted, "revised", c.summary.ObservationsRevised,
		"failures", c.summary.Failures, "unsupported", c.summary.Unsupported)
	if err != nil {
		return c.summary, err
	}
	if c.summary.Failures > 0 {
		return c.summary, fmt.Errorf("crawl incomplete: %d API requests failed", c.summary.Failures)
	}
	return c.summary, nil
}

func (c *collector) collect(ctx context.Context) error {
	regions, err := c.client.ListRegions(ctx)
	if err != nil {
		return fmt.Errorf("discover regions: %w", err)
	}
	regionFilter := stringSet(c.config.Regions)
	machineFilter := stringSet(c.config.MachineTypes)
	regionIDs := map[string]int64{}
	zones := map[string]zoneRecord{}
	for _, region := range regions {
		if len(regionFilter) > 0 && !regionFilter[region.Name] {
			continue
		}
		deprecated := ""
		if region.Deprecated != nil {
			deprecated = region.Deprecated.State
		}
		var regionID int64
		err := c.db.QueryRowContext(ctx, `INSERT INTO regions(name,status,deprecated_state) VALUES($1,NULLIF($2,''),NULLIF($3,''))
			ON CONFLICT(name) DO UPDATE SET status=EXCLUDED.status, deprecated_state=EXCLUDED.deprecated_state RETURNING id`,
			region.Name, region.Status, deprecated).Scan(&regionID)
		if err != nil {
			return fmt.Errorf("store region %s: %w", region.Name, err)
		}
		regionIDs[region.Name] = regionID
		for _, zoneURL := range region.Zones {
			name := gcp.BaseName(zoneURL)
			var zoneID int64
			if err := c.db.QueryRowContext(ctx, `INSERT INTO zones(region_id,name) VALUES($1,$2)
				ON CONFLICT(name) DO UPDATE SET region_id=EXCLUDED.region_id RETURNING id`, regionID, name).Scan(&zoneID); err != nil {
				return fmt.Errorf("store zone %s: %w", name, err)
			}
			zones[name] = zoneRecord{id: zoneID, regionID: regionID, regionName: region.Name}
		}
	}
	if len(regionIDs) == 0 {
		return fmt.Errorf("no regions matched the configured scope")
	}
	c.log.Info("region discovery complete", "discovered", len(regions), "selected", len(regionIDs), "zones", len(zones), "api_calls", c.client.Calls())

	var machineTypes []gcp.MachineType
	if len(machineFilter) > 0 {
		total := len(zones) * len(machineFilter)
		progress := progressReporter{log: c.log, phase: "machine type discovery", total: total}
		completed := 0
		for zoneName := range zones {
			for machineName := range machineFilter {
				machine, getErr := c.client.GetMachineType(ctx, zoneName, machineName)
				completed++
				c.summary.APICalls = c.client.Calls()
				progress.report(completed, c.summary)
				if getErr != nil {
					var apiErr *gcp.APIError
					if errors.As(getErr, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
						continue
					}
					return fmt.Errorf("discover machine type %s in %s: %w", machineName, zoneName, getErr)
				}
				machineTypes = append(machineTypes, machine)
			}
		}
	} else {
		machineTypes, err = c.client.ListMachineTypes(ctx)
		if err != nil {
			return fmt.Errorf("discover machine types: %w", err)
		}
	}
	c.log.Info("machine type discovery complete", "records", len(machineTypes), "api_calls", c.client.Calls())
	byZone := map[string][]machineRecord{}
	byRegion := map[string]map[string]machineRecord{}
	metadataProgress := progressReporter{log: c.log, phase: "machine metadata", total: len(machineTypes)}
	for i, machine := range machineTypes {
		c.summary.APICalls = c.client.Calls()
		metadataProgress.report(i+1, c.summary)
		zone, ok := zones[machine.Zone]
		if !ok || strings.HasPrefix(machine.Name, "custom-") || (len(machineFilter) > 0 && !machineFilter[machine.Name]) {
			continue
		}
		deprecated := ""
		if machine.Deprecated != nil {
			deprecated = machine.Deprecated.State
		}
		accelerators := acceleratorSummary(machine.Accelerators)
		var machineID int64
		if err := c.db.QueryRowContext(ctx, `INSERT INTO machine_types(name,guest_cpus,memory_mb,architecture,accelerators,deprecated_state)
			VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,'')) ON CONFLICT(name) DO UPDATE SET
			guest_cpus=EXCLUDED.guest_cpus,memory_mb=EXCLUDED.memory_mb,architecture=EXCLUDED.architecture,
			accelerators=EXCLUDED.accelerators,deprecated_state=EXCLUDED.deprecated_state RETURNING id`,
			machine.Name, machine.GuestCPUs, machine.MemoryMB, machine.Architecture, accelerators, deprecated).Scan(&machineID); err != nil {
			return fmt.Errorf("store machine type %s: %w", machine.Name, err)
		}
		record := machineRecord{id: machineID, machine: machine}
		byZone[machine.Zone] = append(byZone[machine.Zone], record)
		if byRegion[zone.regionName] == nil {
			byRegion[zone.regionName] = map[string]machineRecord{}
		}
		byRegion[zone.regionName][machine.Name] = record
	}
	if len(byZone) == 0 {
		return fmt.Errorf("no machine/location combinations matched the configured scope")
	}

	regionalTotal := 0
	for _, machines := range byRegion {
		regionalTotal += len(machines)
	}
	zonalTotal := 0
	for _, machines := range byZone {
		zonalTotal += len(machines)
	}
	c.log.Info("capacity history work planned", "regional_requests", regionalTotal, "zonal_requests", zonalTotal, "total_requests", regionalTotal+zonalTotal)

	regionNames := sortedKeys(byRegion)
	regionalProgress := progressReporter{log: c.log, phase: "regional capacity history", total: regionalTotal}
	regionalCompleted := 0
	for _, regionName := range regionNames {
		names := sortedKeys(byRegion[regionName])
		for _, name := range names {
			machine := byRegion[regionName][name]
			offeringID, err := c.regionOffering(ctx, machine.id, regionIDs[regionName])
			if err != nil {
				return err
			}
			history, err := c.client.CapacityHistory(ctx, regionName, "", name, true)
			if err != nil {
				if c.unsupported(err) {
					regionalCompleted++
					c.summary.APICalls = c.client.Calls()
					regionalProgress.report(regionalCompleted, c.summary)
					continue
				}
				c.failure(regionName+"/"+name, err)
				regionalCompleted++
				c.summary.APICalls = c.client.Calls()
				regionalProgress.report(regionalCompleted, c.summary)
				continue
			}
			if err := c.persistHistory(ctx, offeringID, history, true); err != nil {
				c.failure(regionName+"/"+name, err)
			}
			regionalCompleted++
			c.summary.APICalls = c.client.Calls()
			regionalProgress.report(regionalCompleted, c.summary)
		}
	}
	zoneNames := sortedKeys(byZone)
	zonalProgress := progressReporter{log: c.log, phase: "zonal capacity history", total: zonalTotal}
	zonalCompleted := 0
	for _, zoneName := range zoneNames {
		zone := zones[zoneName]
		sort.Slice(byZone[zoneName], func(i, j int) bool { return byZone[zoneName][i].machine.Name < byZone[zoneName][j].machine.Name })
		for _, machine := range byZone[zoneName] {
			offeringID, err := c.zoneOffering(ctx, machine.id, zone.id)
			if err != nil {
				return err
			}
			history, err := c.client.CapacityHistory(ctx, zone.regionName, zoneName, machine.machine.Name, false)
			if err != nil {
				if c.unsupported(err) {
					zonalCompleted++
					c.summary.APICalls = c.client.Calls()
					zonalProgress.report(zonalCompleted, c.summary)
					continue
				}
				c.failure(zoneName+"/"+machine.machine.Name, err)
				zonalCompleted++
				c.summary.APICalls = c.client.Calls()
				zonalProgress.report(zonalCompleted, c.summary)
				continue
			}
			if err := c.persistHistory(ctx, offeringID, history, false); err != nil {
				c.failure(zoneName+"/"+machine.machine.Name, err)
			}
			zonalCompleted++
			c.summary.APICalls = c.client.Calls()
			zonalProgress.report(zonalCompleted, c.summary)
		}
	}
	return nil
}

func (c *collector) unsupported(err error) bool {
	var apiErr *gcp.APIError
	if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusBadRequest || apiErr.StatusCode == http.StatusNotFound) {
		c.summary.Unsupported++
		c.log.Debug("capacity history unsupported", "error", err)
		return true
	}
	return false
}

func (c *collector) failure(scope string, err error) {
	c.summary.Failures++
	message := scope + ": collection error"
	var apiErr *gcp.APIError
	if errors.As(err, &apiErr) {
		message = fmt.Sprintf("%s: HTTP %d", scope, apiErr.StatusCode)
	}
	c.failures = append(c.failures, message)
	c.log.Error("crawl request failed", "scope", scope, "error", err)
}

func (c *collector) regionOffering(ctx context.Context, machineID, regionID int64) (int64, error) {
	var id int64
	err := c.db.QueryRowContext(ctx, `INSERT INTO offerings(machine_type_id,region_id) VALUES($1,$2)
		ON CONFLICT(machine_type_id,region_id) WHERE region_id IS NOT NULL DO UPDATE SET machine_type_id=EXCLUDED.machine_type_id RETURNING id`, machineID, regionID).Scan(&id)
	return id, err
}

func (c *collector) zoneOffering(ctx context.Context, machineID, zoneID int64) (int64, error) {
	var id int64
	err := c.db.QueryRowContext(ctx, `INSERT INTO offerings(machine_type_id,zone_id) VALUES($1,$2)
		ON CONFLICT(machine_type_id,zone_id) WHERE zone_id IS NOT NULL DO UPDATE SET machine_type_id=EXCLUDED.machine_type_id RETURNING id`, machineID, zoneID).Scan(&id)
	return id, err
}

func (c *collector) persistHistory(ctx context.Context, offeringID int64, history gcp.CapacityHistory, allowPrice bool) error {
	prices := history.PriceHistory
	preemptions := history.PreemptionHistory
	if !c.config.Backfill {
		prices = latest(prices, func(p gcp.PricePoint) time.Time { return p.Interval.EndTime })
		preemptions = latest(preemptions, func(p gcp.PreemptionPoint) time.Time { return p.Interval.EndTime })
	}
	if allowPrice {
		for _, point := range prices {
			nanos, err := point.ListPrice.Nanodollars()
			if err != nil {
				return err
			}
			inserted, revised, err := c.storePrice(ctx, offeringID, point.Interval, point.ListPrice.CurrencyCode, nanos)
			if err != nil {
				return err
			}
			c.summary.ObservationsInserted += boolInt(inserted)
			c.summary.ObservationsRevised += boolInt(revised)
		}
	}
	for _, point := range preemptions {
		if point.PreemptionRate == "" {
			continue
		}
		inserted, revised, err := c.storePreemption(ctx, offeringID, point.Interval, point.PreemptionRate.String())
		if err != nil {
			return err
		}
		c.summary.ObservationsInserted += boolInt(inserted)
		c.summary.ObservationsRevised += boolInt(revised)
	}
	return nil
}

func (c *collector) storePrice(ctx context.Context, offeringID int64, interval gcp.Interval, currency string, nanos int64) (bool, bool, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	var id, old int64
	var oldCurrency string
	err = tx.QueryRowContext(ctx, `SELECT id,currency_code,price_nanodollars FROM spot_price_intervals WHERE offering_id=$1 AND start_time=$2 AND end_time=$3 FOR UPDATE`, offeringID, interval.StartTime, interval.EndTime).Scan(&id, &oldCurrency, &old)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO spot_price_intervals(offering_id,start_time,end_time,currency_code,price_nanodollars,first_seen_crawl_id) VALUES($1,$2,$3,$4,$5,$6)`, offeringID, interval.StartTime, interval.EndTime, currency, nanos, c.summary.RunID)
		if err != nil {
			return false, false, err
		}
		return true, false, tx.Commit()
	}
	if err != nil {
		return false, false, err
	}
	if old == nanos && oldCurrency == currency {
		return false, false, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO observation_revisions(observation_kind,observation_id,crawl_id,old_price_nanodollars,new_price_nanodollars) VALUES('price',$1,$2,$3,$4)`, id, c.summary.RunID, old, nanos)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE spot_price_intervals SET currency_code=$2,price_nanodollars=$3 WHERE id=$1`, id, currency, nanos)
	}
	if err != nil {
		return false, false, err
	}
	return false, true, tx.Commit()
}

func (c *collector) storePreemption(ctx context.Context, offeringID int64, interval gcp.Interval, value string) (bool, bool, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	var id int64
	var old string
	err = tx.QueryRowContext(ctx, `SELECT id,preemption_rate::text FROM preemption_observations WHERE offering_id=$1 AND start_time=$2 AND end_time=$3 FOR UPDATE`, offeringID, interval.StartTime, interval.EndTime).Scan(&id, &old)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO preemption_observations(offering_id,start_time,end_time,preemption_rate,first_seen_crawl_id) VALUES($1,$2,$3,$4,$5)`, offeringID, interval.StartTime, interval.EndTime, value, c.summary.RunID)
		if err != nil {
			return false, false, err
		}
		return true, false, tx.Commit()
	}
	if err != nil {
		return false, false, err
	}
	var equal bool
	if err := tx.QueryRowContext(ctx, `SELECT $1::numeric = $2::numeric`, old, value).Scan(&equal); err != nil {
		return false, false, err
	}
	if equal {
		return false, false, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO observation_revisions(observation_kind,observation_id,crawl_id,old_preemption_rate,new_preemption_rate) VALUES('preemption',$1,$2,$3,$4)`, id, c.summary.RunID, old, value)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE preemption_observations SET preemption_rate=$2 WHERE id=$1`, id, value)
	}
	if err != nil {
		return false, false, err
	}
	return false, true, tx.Commit()
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result[value] = true
		}
	}
	return result
}
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func acceleratorSummary(items []gcp.Accelerator) string {
	parts := make([]string, 0, len(items))
	for _, a := range items {
		parts = append(parts, fmt.Sprintf("%s:%d", a.Type, a.Count))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func latest[T any](items []T, end func(T) time.Time) []T {
	if len(items) == 0 {
		return nil
	}
	best := items[0]
	for _, item := range items[1:] {
		if end(item).After(end(best)) {
			best = item
		}
	}
	return []T{best}
}
