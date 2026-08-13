package gcp

import (
	"encoding/json"
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
