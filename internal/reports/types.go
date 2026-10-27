// Package reports owns the private issue ledger and its deterministic boundaries.
package reports

import (
	"encoding/json"
	"time"
)

const DefaultRepository = "dk-code-factory/code-factory-plugin"

type Submission struct {
	IdempotencyKey      string    `json:"idempotencyKey"`
	ObservedAt          time.Time `json:"observedAt"`
	Surface             string    `json:"surface"`
	Expected            string    `json:"expected"`
	Actual              string    `json:"actual"`
	Steps               []string  `json:"steps,omitempty"`
	ReproductionUnknown string    `json:"reproductionUnknown,omitempty"`
	Impact              string    `json:"impact,omitempty"`
	Frequency           string    `json:"frequency,omitempty"`
	Errors              string    `json:"errors,omitempty"`
	ToolEvidence        string    `json:"toolEvidence,omitempty"`
	SuspectedCause      string    `json:"suspectedCause,omitempty"`
	PluginVersion       string    `json:"pluginVersion,omitempty"`
	RunIDs              []string  `json:"runIds,omitempty"`
	ItemIDs             []string  `json:"itemIds,omitempty"`
	EventAfter          int64     `json:"eventAfter,omitempty"`
	EventThrough        int64     `json:"eventThrough,omitempty"`
}
type EvidenceInput struct {
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotencyKey"`
	Text           string `json:"text"`
}
type Evidence struct {
	Origin    string    `json:"origin"`
	At        time.Time `json:"at"`
	SHA256    string    `json:"sha256,omitempty"`
	Bytes     int       `json:"bytes"`
	Omission  string    `json:"omission,omitempty"`
	Truncated bool      `json:"truncated"`
}
type Change struct {
	Status string    `json:"status"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail"`
}
type Findings struct {
	Classification     string   `json:"classification"` // confirmed, suspected, duplicate, insufficient_information, explained
	Summary            string   `json:"summary"`
	MissingInformation string   `json:"missingInformation"`
	Component          string   `json:"component"`
	Repository         string   `json:"repository"`
	SourcePath         string   `json:"sourcePath"`
	SourceCommit       string   `json:"sourceCommit"`
	OwnershipReason    string   `json:"ownershipReason"`
	DuplicateNumber    int      `json:"duplicateNumber"`
	Title              string   `json:"title"`
	Body               string   `json:"body"`
	Labels             []string `json:"labels"`
}
type Issue struct {
	Repository    string `json:"repository"`
	Number        int    `json:"number"`
	URL           string `json:"url"`
	State         string `json:"state"`
	ClosureReason string `json:"closureReason"`
}
type Fix struct {
	Repository string `json:"repository"`
	PR         string `json:"pr,omitempty"`
	Commit     string `json:"commit"`
	Component  string `json:"component"`
	Version    string `json:"version,omitempty"`
}
type Release struct {
	Repository       string    `json:"repository"`
	Version          string    `json:"version"`
	ComponentVersion string    `json:"componentVersion"`
	PublishedAt      time.Time `json:"publishedAt"`
	URL              string    `json:"url"`
	Prerelease       bool      `json:"prerelease"`
	Commit           string    `json:"commit"`
}
type Freshness struct {
	LastSuccess time.Time `json:"lastSuccess"`
	LastAttempt time.Time `json:"lastAttempt"`
	RetryAt     time.Time `json:"retryAt"`
	Error       string    `json:"error"`
}
type Report struct {
	ID               string            `json:"id"`
	Observation      Submission        `json:"observation"`
	Processing       string            `json:"processing"`
	Outcome          string            `json:"outcome"`
	Component        string            `json:"component"`
	Created          time.Time         `json:"created"`
	Updated          time.Time         `json:"updated"`
	Evidence         []Evidence        `json:"evidence"`
	Additions        []string          `json:"additions"`
	Versions         map[string]string `json:"versions"`
	WorkflowID       string            `json:"workflowId"`
	Findings         *Findings         `json:"findings,omitempty"`
	Issue            *Issue            `json:"issue,omitempty"`
	Fixes            []Fix             `json:"fixes"`
	Releases         []Release         `json:"releases"`
	InstalledVersion string            `json:"installedVersion"`
	History          []Change          `json:"history"`
	Message          string            `json:"message"`
	IssueSync        Freshness         `json:"issueSync"`
	ReleaseSync      Freshness         `json:"releaseSync"`
}
type ListInput struct {
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Status    string `json:"status,omitempty"`
	Component string `json:"component,omitempty"`
	Version   string `json:"version,omitempty"`
	Search    string `json:"search,omitempty"`
}
type Page struct {
	Items      []Report `json:"items"`
	NextCursor string   `json:"nextCursor"`
}
type ResumeInput struct {
	ID         string `json:"id"`
	Reconciled bool   `json:"reconciled,omitempty"`
	Note       string `json:"note,omitempty"`
}

// Runtime is stored separately so lifecycle tokens and private inputs never reach MCP.
type Runtime struct {
	OutputContract   int                   `json:"outputContract,omitempty"`
	Context          *investigationContext `json:"context,omitempty"`
	Generation       int                   `json:"generation"`
	Workspace        string                `json:"workspace"`
	Prompt           string                `json:"prompt"`
	Token            string                `json:"token"`
	Root             string                `json:"root"`
	Completion       string                `json:"completion"`
	SkillVersion     string                `json:"skillVersion"`
	Notice           string                `json:"notice"`
	AdmissionID      string                `json:"admissionId"`
	AdditionCount    int                   `json:"additionCount"` // append-only evidence included in this generation's immutable prompt
	Admitting        bool                  `json:"admitting"`
	Preparing        bool                  `json:"preparing"`
	ControllerErrors int                   `json:"controllerErrors"`
	ResumePending    bool                  `json:"resumePending"`
	ResumeID         string                `json:"resumeId"`
	ResumeUncertain  bool                  `json:"resumeUncertain"`
	UnusableResult   bool                  `json:"unusableResult"`
	Intent           *Findings             `json:"intent,omitempty"`
	CreateAttempted  bool                  `json:"createAttempted"`
}
type WorkflowState struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Outputs struct {
		Findings   Findings        `json:"findings"`
		Structured json.RawMessage `json:"structured,omitempty"`
	} `json:"outputs"`
	Error string `json:"error"`
}
