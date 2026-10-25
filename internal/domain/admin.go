package domain

import "time"

// Plugin status is observed from the pinned provider, never inferred from an install request.
type CodexPlugin struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Marketplace      string `json:"marketplace"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	AvailableVersion string `json:"availableVersion,omitempty"`
	Installed        bool   `json:"installed"`
	Enabled          bool   `json:"enabled"`
	UpdateStatus     string `json:"updateStatus"`
	InstallPolicy    string `json:"installPolicy,omitempty"`
	AuthPolicy       string `json:"authPolicy,omitempty"`
	SourceType       string `json:"sourceType,omitempty"`
}
type PluginMarketplace struct {
	Name       string `json:"name"`
	SourceType string `json:"sourceType,omitempty"`
}
type PluginInventory struct {
	LastAttempt  time.Time           `json:"lastAttempt"`
	ID           string              `json:"id"`
	CheckedAt    time.Time           `json:"checkedAt"`
	Error        string              `json:"error,omitempty"`
	Plugins      []CodexPlugin       `json:"plugins"`
	Marketplaces []PluginMarketplace `json:"marketplaces"`
}
type PluginOperation struct {
	ID       string    `json:"id"`
	Action   string    `json:"action"`
	Target   string    `json:"target"`
	Status   string    `json:"status"`
	Error    string    `json:"error,omitempty"`
	Created  time.Time `json:"created"`
	Finished time.Time `json:"finished,omitempty"`
}
