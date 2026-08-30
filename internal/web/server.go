package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

type Server struct {
	db  *sql.DB
	log *slog.Logger
	tpl *template.Template
}

type RegionOption struct {
	ID   int64
	Name string
}
type MachineOption struct {
	OfferingID int64
	Name       string
}
type MachineDetails struct {
	Name, Memory, Architecture, Accelerators, Lifecycle string
	GuestCPUs                                           int
}
type Point struct {
	Time  time.Time
	Value float64
}
type chartData struct {
	PriceLabels, PriceValues, PreemptionLabels, PreemptionValues string
	Range                                                        string
}
type pageData struct {
	Regions          []RegionOption
	SelectedRegionID int64
	Explorer         template.HTML
}
type explorerData struct {
	RegionID           int64
	Machines           []MachineOption
	SelectedOfferingID int64
	Selection          template.HTML
}
type selectionData struct {
	Details MachineDetails
	Chart   template.HTML
}

func New(db *sql.DB, log *slog.Logger) (*Server, error) {
	tpl, err := template.New("root").Parse(rootTemplate + explorerTemplate + selectionTemplate + chartTemplate)
	if err != nil {
		return nil, err
	}
	return &Server{db: db, log: log, tpl: tpl}, nil
}

func (s *Server) ListenAndServe(ctx context.Context, address string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.root)
	mux.HandleFunc("GET /explorer", s.explorer)
	mux.HandleFunc("GET /selection", s.selection)
	httpServer := &http.Server{Addr: address, Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	s.log.Info("web server listening", "address", address)
	err := httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) regions(ctx context.Context) ([]RegionOption, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT r.id,r.name FROM regions r
		JOIN offerings o ON o.region_id=r.id
		WHERE EXISTS(SELECT 1 FROM spot_price_intervals p WHERE p.offering_id=o.id)
		   OR EXISTS(SELECT 1 FROM preemption_observations p WHERE p.offering_id=o.id)
		ORDER BY r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RegionOption
	for rows.Next() {
		var item RegionOption
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Server) machines(ctx context.Context, regionID int64) ([]MachineOption, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id,m.name FROM offerings o
		JOIN machine_types m ON m.id=o.machine_type_id WHERE o.region_id=$1
		AND (EXISTS(SELECT 1 FROM spot_price_intervals p WHERE p.offering_id=o.id)
		 OR EXISTS(SELECT 1 FROM preemption_observations p WHERE p.offering_id=o.id))
		ORDER BY m.name`, regionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []MachineOption
	for rows.Next() {
		var item MachineOption
		if err := rows.Scan(&item.OfferingID, &item.Name); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	regions, err := s.regions(r.Context())
	if err != nil {
		http.Error(w, "database query failed", 500)
		s.log.Error("list regions", "error", err)
		return
	}
	selectedRegion := int64(0)
	if len(regions) > 0 {
		selectedRegion = regions[0].ID
	}
	if requested, err := strconv.ParseInt(r.URL.Query().Get("region_id"), 10, 64); err == nil && requested > 0 {
		selectedRegion = requested
	}
	explorer, err := s.renderExplorer(r.Context(), selectedRegion)
	if err != nil {
		http.Error(w, "database query failed", 500)
		s.log.Error("render explorer", "error", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, "root", pageData{Regions: regions, SelectedRegionID: selectedRegion, Explorer: explorer}); err != nil {
		s.log.Error("render page", "error", err)
	}
}

func (s *Server) explorer(w http.ResponseWriter, r *http.Request) {
	regionID, err := strconv.ParseInt(r.URL.Query().Get("region_id"), 10, 64)
	if err != nil || regionID <= 0 {
		http.Error(w, "invalid region", 400)
		return
	}
	explorer, err := s.renderExplorer(r.Context(), regionID)
	if err != nil {
		http.Error(w, "database query failed", 500)
		s.log.Error("render explorer", "error", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(explorer))
}

func (s *Server) selection(w http.ResponseWriter, r *http.Request) {
	offeringID, err := strconv.ParseInt(r.URL.Query().Get("offering_id"), 10, 64)
	if err != nil || offeringID <= 0 {
		http.Error(w, "invalid offering", 400)
		return
	}
	rangeName := r.URL.Query().Get("range")
	selection, err := s.renderSelection(r.Context(), offeringID, rangeName)
	if err != nil {
		http.Error(w, "database query failed", 500)
		s.log.Error("render selection", "error", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(selection))
}

func (s *Server) renderExplorer(ctx context.Context, regionID int64) (template.HTML, error) {
	machines, err := s.machines(ctx, regionID)
	if err != nil {
		return "", err
	}
	selected := int64(0)
	if len(machines) > 0 {
		selected = machines[0].OfferingID
	}
	selection, err := s.renderSelection(ctx, selected, "30d")
	if err != nil {
		return "", err
	}
	var rendered templateBuffer
	if err := s.tpl.ExecuteTemplate(&rendered, "explorer", explorerData{RegionID: regionID, Machines: machines, SelectedOfferingID: selected, Selection: selection}); err != nil {
		return "", err
	}
	return template.HTML(rendered), nil
}

func (s *Server) renderSelection(ctx context.Context, offeringID int64, rangeName string) (template.HTML, error) {
	if offeringID == 0 {
		return template.HTML(`<div class="empty">No collected machine types are available in this region.</div>`), nil
	}
	details, err := s.machineDetails(ctx, offeringID)
	if err != nil {
		return "", err
	}
	chart, err := s.renderChart(ctx, offeringID, rangeName)
	if err != nil {
		return "", err
	}
	var rendered templateBuffer
	if err := s.tpl.ExecuteTemplate(&rendered, "selection", selectionData{Details: details, Chart: chart}); err != nil {
		return "", err
	}
	return template.HTML(rendered), nil
}

func (s *Server) machineDetails(ctx context.Context, offeringID int64) (MachineDetails, error) {
	var d MachineDetails
	var memoryMB int
	var architecture, accelerators, lifecycle sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT m.name,m.guest_cpus,m.memory_mb,m.architecture,m.accelerators,m.deprecated_state
		FROM offerings o JOIN machine_types m ON m.id=o.machine_type_id WHERE o.id=$1 AND o.region_id IS NOT NULL`, offeringID).
		Scan(&d.Name, &d.GuestCPUs, &memoryMB, &architecture, &accelerators, &lifecycle)
	if err != nil {
		return d, err
	}
	d.Memory = fmt.Sprintf("%.1f GiB (%d MB)", float64(memoryMB)/1024, memoryMB)
	d.Architecture = "Not reported"
	if architecture.Valid && architecture.String != "" {
		d.Architecture = architecture.String
	}
	d.Accelerators = "None"
	if accelerators.Valid && accelerators.String != "" {
		d.Accelerators = accelerators.String
	}
	d.Lifecycle = "Active"
	if lifecycle.Valid && lifecycle.String != "" {
		d.Lifecycle = lifecycle.String
	}
	return d, nil
}

func (s *Server) renderChart(ctx context.Context, offeringID int64, rangeName string) (template.HTML, error) {
	duration, normalized := rangeDuration(rangeName)
	var cutoff any
	if duration == 0 {
		cutoff = nil
	} else {
		cutoff = time.Now().Add(-duration)
	}
	price, err := s.pricePoints(ctx, offeringID, cutoff)
	if err != nil {
		return "", err
	}
	preemption, err := s.preemptionPoints(ctx, offeringID, cutoff)
	if err != nil {
		return "", err
	}
	data := chartData{PriceLabels: jsonValue(times(price)), PriceValues: jsonValue(values(price)), PreemptionLabels: jsonValue(times(preemption)), PreemptionValues: jsonValue(values(preemption)), Range: normalized}
	var rendered templateBuffer
	if err := s.tpl.ExecuteTemplate(&rendered, "chart", data); err != nil {
		return "", err
	}
	return template.HTML(rendered), nil
}

func (s *Server) pricePoints(ctx context.Context, offeringID int64, cutoff any) ([]Point, error) {
	rows, err := s.db.QueryContext(ctx, `WITH selected AS (
		SELECT o.machine_type_id, COALESCE(o.region_id,z.region_id) AS region_id
		FROM offerings o LEFT JOIN zones z ON z.id=o.zone_id WHERE o.id=$1
	), price_offering AS (
		SELECT o.id FROM offerings o JOIN selected s ON s.machine_type_id=o.machine_type_id AND s.region_id=o.region_id
	)
	SELECT start_time,price_nanodollars::double precision/1000000000.0
	FROM spot_price_intervals WHERE offering_id=(SELECT id FROM price_offering)
	AND ($2::timestamptz IS NULL OR end_time >= $2) ORDER BY start_time`, offeringID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Time, &p.Value); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *Server) preemptionPoints(ctx context.Context, offeringID int64, cutoff any) ([]Point, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT start_time,preemption_rate::double precision FROM preemption_observations WHERE offering_id=$1 AND ($2::timestamptz IS NULL OR end_time >= $2) ORDER BY start_time`, offeringID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Time, &p.Value); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func rangeDuration(value string) (time.Duration, string) {
	switch value {
	case "7d":
		return 7 * 24 * time.Hour, "7d"
	case "90d":
		return 90 * 24 * time.Hour, "90d"
	case "1y":
		return 365 * 24 * time.Hour, "1y"
	case "all":
		return 0, "all"
	default:
		return 30 * 24 * time.Hour, "30d"
	}
}
func times(points []Point) []string {
	r := make([]string, len(points))
	for i, p := range points {
		r[i] = p.Time.Format("2006-01-02")
	}
	return r
}
func values(points []Point) []float64 {
	r := make([]float64, len(points))
	for i, p := range points {
		r[i] = p.Value
	}
	return r
}
func jsonValue(value any) string { encoded, _ := json.Marshal(value); return string(encoded) }

type templateBuffer []byte

func (b *templateBuffer) Write(p []byte) (int, error) { *b = append(*b, p...); return len(p), nil }

const rootTemplate = `{{define "root"}}<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>GCP Spot Observatory</title><meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'><script src="https://unpkg.com/htmx.org@4.0.0/dist/htmx.min.js" integrity="sha384-BvJpBiO8Kh31EqtJe5DRIeWrHWnCGkwytKs9NKFi86Hhw96dEqdEMzZDeK9iEGTc" crossorigin="anonymous"></script><script src="https://cdn.jsdelivr.net/npm/chart.js@4.5.1"></script>
<style>:root{font-family:ui-sans-serif,system-ui;color:#172033;background:#f4f7fb}body{margin:0}.shell{max-width:1100px;margin:auto;padding:40px 24px}h1{margin:0 0 6px;font-size:28px}.subtitle{color:#60708a;margin:0 0 28px}.panel{background:white;border:1px solid #dce4ef;border-radius:14px;padding:22px;box-shadow:0 8px 28px #2538580d}.region-control{max-width:520px;margin-bottom:16px}.controls{display:grid;grid-template-columns:minmax(260px,1fr) 180px;gap:16px;margin-bottom:20px}label{display:block;font-size:13px;font-weight:650;color:#52637d;margin-bottom:7px}select{width:100%;padding:10px 12px;border:1px solid #b9c6d8;border-radius:8px;background:white;font-size:15px}.details{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:1px;background:#dce4ef;border:1px solid #dce4ef;border-radius:10px;overflow:hidden;margin:0 0 26px}.detail{background:#f8fafc;padding:14px}.detail dt{font-size:12px;color:#65758d;margin-bottom:5px}.detail dd{font-size:15px;font-weight:650;margin:0;overflow-wrap:anywhere}.charts{display:grid;gap:22px}.chart{height:300px;position:relative;border-top:1px solid #e8edf4;padding-top:16px}.empty{padding:48px;text-align:center;color:#687991}.htmx-indicator{color:#65758d;font-size:13px}@media(max-width:800px){.details{grid-template-columns:repeat(2,1fr)}}@media(max-width:650px){.controls{grid-template-columns:1fr}.shell{padding:24px 12px}}</style></head>
<body><main class="shell"><h1>GCP Spot Observatory</h1><p class="subtitle">Canonical Spot VM price and preemption history</p><section class="panel">
{{if .Regions}}<div class="region-control"><label for="region">Region</label><select id="region" name="region_id" hx-get="/explorer" hx-target="#explorer" hx-swap="innerHTML settle:25ms" hx-sync="#explorer:replace" hx-indicator="#loading">{{range .Regions}}<option value="{{.ID}}" {{if eq .ID $.SelectedRegionID}}selected{{end}}>{{.Name}}</option>{{end}}</select></div>
<span id="loading" class="htmx-indicator">Loading…</span><div id="explorer">{{.Explorer}}</div>{{else}}<div class="empty">No observations have been collected yet.</div>{{end}}
</section></main><script>
let chartGeneration=0;
let spotCharts=[];
function destroySpotCharts(){
  chartGeneration++;
  for(const chart of spotCharts){if(chart)chart.destroy()}
  spotCharts=[];
  for(const id of ['priceChart','preemptionChart']){
    const canvas=document.getElementById(id);
    if(!canvas)continue;
    const chart=Chart.getChart(canvas);
    if(chart)chart.destroy();
  }
}
function scheduleSpotCharts(){
  const generation=++chartGeneration;
  requestAnimationFrame(function(){
    if(generation!==chartGeneration)return;
    const data=document.getElementById('chartData');
    const priceCanvas=document.getElementById('priceChart');
    const preemptionCanvas=document.getElementById('preemptionChart');
    if(!data||!priceCanvas||!preemptionCanvas||!document.contains(data))return;
    for(const canvas of [priceCanvas,preemptionCanvas]){
      const existing=Chart.getChart(canvas);
      if(existing)existing.destroy();
    }
    spotCharts=[
      new Chart(priceCanvas,{type:'line',data:{labels:JSON.parse(data.dataset.priceLabels),datasets:[{label:'Spot price (USD/hour)',data:JSON.parse(data.dataset.priceValues),borderColor:'#2563eb',backgroundColor:'#2563eb22',stepped:true,tension:0,pointRadius:3}]},options:{responsive:true,maintainAspectRatio:false,animation:false,scales:{y:{beginAtZero:true}}}}),
      new Chart(preemptionCanvas,{type:'line',data:{labels:JSON.parse(data.dataset.preemptionLabels),datasets:[{label:'Preemption rate',data:JSON.parse(data.dataset.preemptionValues),borderColor:'#d946ef',backgroundColor:'#d946ef22',tension:.15,pointRadius:3}]},options:{responsive:true,maintainAspectRatio:false,animation:false,scales:{y:{beginAtZero:true,max:1}}}})
    ];
  });
}
document.addEventListener('DOMContentLoaded',scheduleSpotCharts);
document.body.addEventListener('htmx:before:swap',function(event){
  const target=event.detail.ctx.target;
  if(target&&((target.matches&&target.matches('#explorer,#selection'))||(target.querySelector&&target.querySelector('canvas'))))destroySpotCharts();
});
document.body.addEventListener('htmx:after:settle',scheduleSpotCharts);
</script></body></html>{{end}}`

const explorerTemplate = `{{define "explorer"}}{{if .Machines}}<form class="controls" hx-get="/selection" hx-target="#selection" hx-trigger="change changed" hx-swap="innerHTML settle:25ms" hx-sync="#explorer:replace" hx-indicator="#loading"><input type="hidden" name="region_id" value="{{.RegionID}}">
<div><label for="machine">Machine type</label><select id="machine" name="offering_id">{{range .Machines}}<option value="{{.OfferingID}}" {{if eq .OfferingID $.SelectedOfferingID}}selected{{end}}>{{.Name}}</option>{{end}}</select></div>
<div><label for="range">Date range</label><select id="range" name="range"><option value="7d">7 days</option><option value="30d" selected>30 days</option><option value="90d">90 days</option><option value="1y">1 year</option><option value="all">All available</option></select></div></form>
<div id="selection">{{.Selection}}</div>{{else}}<div class="empty">No collected machine types are available in this region.</div>{{end}}{{end}}`

const selectionTemplate = `{{define "selection"}}<dl class="details"><div class="detail"><dt>Machine type</dt><dd>{{.Details.Name}}</dd></div><div class="detail"><dt>vCPUs</dt><dd>{{.Details.GuestCPUs}}</dd></div><div class="detail"><dt>Memory</dt><dd>{{.Details.Memory}}</dd></div><div class="detail"><dt>Architecture</dt><dd>{{.Details.Architecture}}</dd></div><div class="detail"><dt>Lifecycle / accelerators</dt><dd>{{.Details.Lifecycle}} · {{.Details.Accelerators}}</dd></div></dl>{{.Chart}}{{end}}`

const chartTemplate = `{{define "chart"}}<div id="chartData" data-price-labels="{{.PriceLabels}}" data-price-values="{{.PriceValues}}" data-preemption-labels="{{.PreemptionLabels}}" data-preemption-values="{{.PreemptionValues}}"></div><div class="charts"><div class="chart"><canvas id="priceChart"></canvas></div><div class="chart"><canvas id="preemptionChart"></canvas></div></div>{{end}}`
