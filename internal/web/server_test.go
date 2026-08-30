package web

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRootTemplateUsesHTMX4(t *testing.T) {
	server, err := New(nil, slog.Default())
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}

	var rendered bytes.Buffer
	if err := server.tpl.ExecuteTemplate(&rendered, "root", pageData{}); err != nil {
		t.Fatalf("render root template: %v", err)
	}

	output := rendered.String()
	for _, want := range []string{
		`https://unpkg.com/htmx.org@4.0.0/dist/htmx.min.js`,
		`<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'>`,
		`integrity="sha384-BvJpBiO8Kh31EqtJe5DRIeWrHWnCGkwytKs9NKFi86Hhw96dEqdEMzZDeK9iEGTc"`,
		`crossorigin="anonymous"`,
		`htmx:before:swap`,
		`htmx:after:settle`,
		`event.detail.ctx.target`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("rendered template does not contain %q", want)
		}
	}

	for _, unwanted := range []string{
		`https://unpkg.com/htmx.org@2.0.8`,
		"htmx:before" + "Swap",
		"htmx:after" + "Settle",
		`event.detail.target`,
	} {
		if strings.Contains(output, unwanted) {
			t.Errorf("rendered template still contains %q", unwanted)
		}
	}
}
