package web

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"
)

func TestSettingsDocumentHasIndependentEntry(t *testing.T) {
	settings := SettingsHTML()
	const marker = `<html data-code-factory-view="settings"`
	if settings == HTML || !strings.Contains(settings, marker) || strings.Contains(HTML, marker) {
		t.Fatal("settings must have a distinct document entry while inbox stays unchanged")
	}
	if strings.Replace(settings, marker, "<html", 1) != HTML {
		t.Fatal("settings entry changed the shared application bundle")
	}
}

func TestProductionShellPrecedesDeferredRuntime(t *testing.T) {
	navigation := strings.Index(HTML, `id="boot-nav" aria-label="Main navigation"`)
	controls := strings.Index(HTML, `dataset.ready="true"`)
	runtime := strings.Index(HTML, `id="factory-runtime"`)
	body := strings.Index(HTML, "<body>")
	if body < 0 || navigation < body || controls < navigation || runtime < controls {
		t.Fatal("the production shell and its handlers must precede the inert runtime payload")
	}
	if strings.Contains(HTML, `<script type="module"`) {
		t.Fatal("a production module must not parse before shell paint")
	}
	payload := regexp.MustCompile(`<script type="application/x-code-factory" id="factory-runtime" data-encoding="gzip-base64">([^<]+)</script>`).FindStringSubmatch(HTML)
	if len(payload) != 2 {
		t.Fatal("production runtime must be compressed after the shell")
	}
	packed, err := base64.StdEncoding.DecodeString(payload[1])
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(strings.NewReader(string(packed)))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	module, err := io.ReadAll(reader)
	var bundle struct{ JS, CSS string }
	if err != nil || json.Unmarshal(module, &bundle) != nil || !strings.Contains(bundle.JS, "react.transitional.element") || !strings.Contains(bundle.CSS, "box-sizing:border-box") || len(payload[1])*2 >= len(module) {
		t.Fatal("runtime must round-trip to the application and halve parser input", len(payload[1]), len(module), err)
	}
	if strings.Contains(HTML, `<style rel="stylesheet"`) {
		t.Fatal("secondary app styles must remain deferred until runtime startup")
	}
}
