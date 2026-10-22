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
