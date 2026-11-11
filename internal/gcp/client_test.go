package gcp

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"golang.org/x/time/rate"
	"testing"
)

func TestMoneyExactNanodollars(t *testing.T) {
	var m Money
	if err := json.Unmarshal([]byte(`{"currencyCode":"USD","units":"2","nanos":"478720000"}`), &m); err != nil {
		t.Fatal(err)
	}
	got, err := m.Nanodollars()
	if err != nil {
		t.Fatal(err)
	}
	if got != 2_478_720_000 {
		t.Fatalf("got %d", got)
	}
}

func TestBaseName(t *testing.T) {
	if got := BaseName("https://compute.googleapis.com/compute/v1/projects/p/zones/us-central1-a"); got != "us-central1-a" {
		t.Fatalf("got %q", got)
	}
}

func TestCapacityHistoryJSON(t *testing.T) {
	var history CapacityHistory
	data := `{"priceHistory":[{"listPrice":{"currencyCode":"USD","units":"2","nanos":478720000}}],"preemptionHistory":[{"preemptionRate":0.1234567890123456789},{"preemptionRate":"1e-9"}]}`
	if err := json.Unmarshal([]byte(data), &history); err != nil {
		t.Fatal(err)
	}
	money := history.PriceHistory[0].ListPrice
	if money.Units != 2 || money.Nanos != 478720000 || money.CurrencyCode != "USD" {
		t.Fatalf("unexpected money: %+v", money)
	}
	if got := history.PreemptionHistory[0].PreemptionRate.String(); got != "0.1234567890123456789" {
		t.Fatalf("lost precision: %s", got)
	}
	if got := history.PreemptionHistory[1].PreemptionRate.String(); got != "1e-9" {
		t.Fatalf("quoted number: %s", got)
	}
	for _, invalid := range []string{`"not a number"`, `" 1 "`, `true`, `[]`} {
		var point PreemptionPoint
		if err := json.Unmarshal([]byte(`{"preemptionRate":`+invalid+`}`), &point); err == nil {
			t.Errorf("accepted invalid preemption rate %s", invalid)
		}
	}
}

func TestRequestStrictJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantError  bool
		wantName   string
	}{
		{"valid", `{"name":"correct"}`, false, "correct"},
		{"duplicate", `{"name":"a","name":"b"}`, true, ""},
		{"invalid UTF-8", "{\"name\":\"\xff\"}", true, ""},
		{"trailing value", `{"name":"a"} {}`, true, ""},
		{"case sensitive", `{"Name":"wrong"}`, false, ""},
		{"nested duplicate", `{"priceHistory":[{"listPrice":{"units":"1","units":"2"}}]}`, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer server.Close()
			client := &Client{http: server.Client(), baseURL: server.URL, limiter: rate.NewLimiter(rate.Every(time.Millisecond), 1)}
			var target struct {
				Name         string       `json:"name"`
				PriceHistory []PricePoint `json:"priceHistory"`
			}
			err := client.request(context.Background(), http.MethodGet, "/", nil, &target)
			if (err != nil) != tc.wantError || (err != nil && !strings.Contains(err.Error(), "decode GCP response")) {
				t.Fatalf("unexpected error: %v", err)
			}
			if err == nil && target.Name != tc.wantName {
				t.Fatalf("name = %q", target.Name)
			}
		})
	}
}
