package web

import (
	_ "embed"
	"strings"
)

//go:embed dist/index.html
var HTML string

// SettingsHTML uses a separate document entry without iframe URLs or credentials.
func SettingsHTML() string {
	return strings.Replace(HTML, "<html", `<html data-code-factory-view="settings"`, 1)
}
