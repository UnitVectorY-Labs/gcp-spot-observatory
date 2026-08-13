package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
)

const computeScope = "https://www.googleapis.com/auth/compute.readonly"

type Client struct {
	http    *http.Client
	project string
	limiter *rate.Limiter
	baseURL string
	calls   int
	log     *slog.Logger
}

type Deprecated struct {
	State string `json:"state"`
}

type Region struct {
	Name       string      `json:"name"`
	Status     string      `json:"status"`
	Zones      []string    `json:"zones"`
	Deprecated *Deprecated `json:"deprecated"`
}

type Accelerator struct {
	Type  string `json:"guestAcceleratorType"`
	Count int64  `json:"guestAcceleratorCount"`
}

type MachineType struct {
	Name         string        `json:"name"`
	GuestCPUs    int           `json:"guestCpus"`
	MemoryMB     int           `json:"memoryMb"`
	Architecture string        `json:"architecture"`
	Accelerators []Accelerator `json:"accelerators"`
	Deprecated   *Deprecated   `json:"deprecated"`
	Zone         string        `json:"-"`
}

type Interval struct {
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
}

type Money struct {
	CurrencyCode string `json:"currencyCode"`
	Units        int64  `json:"-"`
	Nanos        int64  `json:"-"`
}

func (m *Money) UnmarshalJSON(data []byte) error {
	var raw struct {
		CurrencyCode string          `json:"currencyCode"`
		Units        json.RawMessage `json:"units"`
		Nanos        json.RawMessage `json:"nanos"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var err error
	m.CurrencyCode = raw.CurrencyCode
	if m.Units, err = parseJSONInt(raw.Units); err != nil {
		return fmt.Errorf("money units: %w", err)
	}
	if m.Nanos, err = parseJSONInt(raw.Nanos); err != nil {
		return fmt.Errorf("money nanos: %w", err)
	}
	return nil
}

func parseJSONInt(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	s := strings.Trim(string(raw), `"`)
	return strconv.ParseInt(s, 10, 64)
}

func (m Money) Nanodollars() (int64, error) {
	const billion = int64(1_000_000_000)
	if m.Units > (1<<63-1)/billion || m.Units < (-1<<63)/billion {
		return 0, fmt.Errorf("money value overflows nanodollars")
	}
	return m.Units*billion + m.Nanos, nil
}

type PricePoint struct {
	Interval  Interval `json:"interval"`
	ListPrice Money    `json:"listPrice"`
}

type PreemptionPoint struct {
	Interval       Interval    `json:"interval"`
	PreemptionRate json.Number `json:"preemptionRate"`
}

type CapacityHistory struct {
	MachineType       string            `json:"machineType"`
	Location          string            `json:"location"`
	PriceHistory      []PricePoint      `json:"priceHistory"`
	PreemptionHistory []PreemptionPoint `json:"preemptionHistory"`
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GCP API returned HTTP %d: %s", e.StatusCode, e.Message)
}

func NewClient(ctx context.Context, project string, requestsPerSecond float64, timeout time.Duration) (*Client, error) {
	if strings.TrimSpace(project) == "" {
		return nil, fmt.Errorf("GCP project is required (--gcp-project or SPOT_OBSERVATORY_GCP_PROJECT)")
	}
	if requestsPerSecond <= 0 {
		return nil, fmt.Errorf("GCP request rate must be greater than zero")
	}
	httpClient, err := google.DefaultClient(ctx, computeScope)
	if err != nil {
		return nil, fmt.Errorf("load Application Default Credentials: %w", err)
	}
	httpClient.Timeout = timeout
	return &Client{
		http: httpClient, project: project,
		limiter: rate.NewLimiter(rate.Limit(requestsPerSecond), 1),
		baseURL: "https://compute.googleapis.com/compute/beta",
	}, nil
}

func (c *Client) Calls() int { return c.calls }

func (c *Client) SetLogger(log *slog.Logger) { c.log = log }

func (c *Client) ListRegions(ctx context.Context) ([]Region, error) {
	var all []Region
	pageToken := ""
	page := 0
	for {
		page++
		q := url.Values{"maxResults": {"500"}}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		var response struct {
			Items         []Region `json:"items"`
			NextPageToken string   `json:"nextPageToken"`
		}
		path := fmt.Sprintf("/projects/%s/regions?%s", url.PathEscape(c.project), q.Encode())
		if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		all = append(all, response.Items...)
		pageToken = response.NextPageToken
		if c.log != nil {
			c.log.Info("GCP discovery progress", "resource", "regions", "page", page, "records", len(all), "api_calls", c.calls, "complete", pageToken == "")
		}
		if pageToken == "" {
			return all, nil
		}
	}
}

func (c *Client) ListMachineTypes(ctx context.Context) ([]MachineType, error) {
	var all []MachineType
	pageToken := ""
	page := 0
	for {
		page++
		q := url.Values{"maxResults": {"500"}}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		var response struct {
			Items map[string]struct {
				MachineTypes []MachineType `json:"machineTypes"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		path := fmt.Sprintf("/projects/%s/aggregated/machineTypes?%s", url.PathEscape(c.project), q.Encode())
		if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		for scope, scoped := range response.Items {
			if !strings.HasPrefix(scope, "zones/") {
				continue
			}
			zone := strings.TrimPrefix(scope, "zones/")
			for _, mt := range scoped.MachineTypes {
				mt.Zone = zone
				all = append(all, mt)
			}
		}
		pageToken = response.NextPageToken
		if c.log != nil {
			c.log.Info("GCP discovery progress", "resource", "machine_types", "page", page, "records", len(all), "api_calls", c.calls, "complete", pageToken == "")
		}
		if pageToken == "" {
			return all, nil
		}
	}
}

func (c *Client) GetMachineType(ctx context.Context, zone, name string) (MachineType, error) {
	path := fmt.Sprintf("/projects/%s/zones/%s/machineTypes/%s", url.PathEscape(c.project), url.PathEscape(zone), url.PathEscape(name))
	var machine MachineType
	if err := c.request(ctx, http.MethodGet, path, nil, &machine); err != nil {
		return MachineType{}, err
	}
	machine.Zone = zone
	return machine, nil
}

func (c *Client) CapacityHistory(ctx context.Context, region, zone, machineType string, includePrice bool) (CapacityHistory, error) {
	types := []string{"PREEMPTION"}
	if includePrice {
		types = append(types, "PRICE")
	}
	body := map[string]any{
		"types": types,
		"instanceProperties": map[string]any{
			"scheduling":  map[string]string{"provisioningModel": "SPOT"},
			"machineType": machineType,
		},
	}
	if zone != "" {
		body["locationPolicy"] = map[string]string{"location": "zones/" + zone}
	}
	path := fmt.Sprintf("/projects/%s/regions/%s/advice/capacityHistory", url.PathEscape(c.project), url.PathEscape(region))
	var response CapacityHistory
	if err := c.request(ctx, http.MethodPost, path, body, &response); err != nil {
		return CapacityHistory{}, err
	}
	return response, nil
}

func (c *Client) request(ctx context.Context, method, path string, body any, target any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	const attempts = 5
	for attempt := 0; attempt < attempts; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}
		var reader io.Reader
		if encoded != nil {
			reader = bytes.NewReader(encoded)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		c.calls++
		resp, err := c.http.Do(req)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
			resp.Body.Close()
			if readErr != nil {
				return readErr
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				decoder := json.NewDecoder(bytes.NewReader(data))
				decoder.UseNumber()
				if err := decoder.Decode(target); err != nil {
					return fmt.Errorf("decode GCP response: %w", err)
				}
				return nil
			}
			apiErr := decodeAPIError(resp.StatusCode, data)
			if !retryableStatus(resp.StatusCode) || attempt == attempts-1 {
				return apiErr
			}
		} else if attempt == attempts-1 || (!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, io.ErrUnexpectedEOF)) {
			return fmt.Errorf("GCP request: %w", err)
		}
		delay := time.Duration(1<<attempt)*time.Second + time.Duration(rand.IntN(250))*time.Millisecond
		if c.log != nil {
			c.log.Warn("retrying GCP API request", "attempt", attempt+2, "max_attempts", attempts, "backoff", delay, "api_calls", c.calls)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("GCP request exhausted retries")
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == 500 || status == 502 || status == 503 || status == 504
}

func decodeAPIError(status int, data []byte) error {
	var wrapped struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	message := strings.TrimSpace(http.StatusText(status))
	if json.Unmarshal(data, &wrapped) == nil && strings.TrimSpace(wrapped.Error.Message) != "" {
		message = wrapped.Error.Message
	}
	if len(message) > 500 {
		message = message[:500]
	}
	return &APIError{StatusCode: status, Message: message}
}

func BaseName(resourceURL string) string {
	resourceURL = strings.TrimSuffix(resourceURL, "/")
	if i := strings.LastIndex(resourceURL, "/"); i >= 0 {
		return resourceURL[i+1:]
	}
	return resourceURL
}
