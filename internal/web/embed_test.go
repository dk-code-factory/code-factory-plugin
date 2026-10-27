package web

import (
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
}
