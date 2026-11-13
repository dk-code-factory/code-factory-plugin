// Package factory routes GitHub observations into retained native Codex sessions.
// It deliberately has no review engine: installed skills own the work.
package factory

import (
	"codefactory/internal/domain"
	"encoding/json"
	"time"
)

type Agent struct {
	Profile string `json:"profile,omitempty"`
	// Definition names a native agents.<name> entry, never an application prompt.
	Definition string `json:"definition,omitempty"`
}
type Repository struct {
	ID           string `json:"id"`
	Checkout     string `json:"checkout,omitempty"`
	Base         string `json:"base,omitempty"`
	Agent        Agent  `json:"agent"`
	QuietSeconds *int   `json:"quietSeconds,omitempty"`
	Paused       bool   `json:"paused,omitempty"`
}
type Rule struct {
	ID           string   `json:"id"`
	Repositories []string `json:"repositories"`
	Types        []string `json:"types"`
	Actions      []string `json:"actions,omitempty"`
	Labels       []string `json:"labels,omitempty"`
	Author       string   `json:"author,omitempty"`
	Draft        *bool    `json:"draft,omitempty"`
	Instructions string   `json:"instructions"`
	Skills       []string `json:"skills,omitempty"`
	Disabled     bool     `json:"disabled,omitempty"`
}
type Settings struct {
	Version                     int              `json:"version"`
	QuietSeconds                int              `json:"quietSeconds"`
	Agent                       Agent            `json:"agent"`
	ItemAgents                  map[string]Agent `json:"itemAgents,omitempty"`
	GitHubRequestsPerHour       int              `json:"githubRequestsPerHour,omitempty"`
	GitHubReserve               *int             `json:"githubReserve,omitempty"`
	GitHubPollSeconds           int              `json:"githubPollSeconds,omitempty"`
	GitHubRepositoryPollSeconds int              `json:"githubRepositoryPollSeconds,omitempty"`
	GitHubDetailPollSeconds     int              `json:"githubDetailPollSeconds,omitempty"`
	ConfigurationAgent          Agent            `json:"configurationAgent"`
	PriorityAgent               Agent            `json:"priorityAgent"`
	PrioritySkill               string           `json:"prioritySkill,omitempty"`
	PriorityLabels              map[string]int   `json:"priorityLabels"`
	PriorityInstructions        string           `json:"priorityInstructions"`
	Repositories                []Repository     `json:"repositories"`
	Rules                       []Rule           `json:"rules"`
}

func Defaults() Settings {
	return Settings{Version: 1, QuietSeconds: 120, PriorityLabels: map[string]int{"P0": 0, "P1": 1, "P2": 2, "P3": 3}, PriorityInstructions: "Assess urgency from P0 (critical) to P3 (low). Explain the evidence.", Repositories: []Repository{}, Rules: []Rule{}}
}

type Event struct {
	Details    json.RawMessage `json:"details,omitempty"`
	ID         string          `json:"id"`
	Repository string          `json:"repository"`
	ItemID     string          `json:"itemId"`
	Type       string          `json:"type"`
	Action     string          `json:"action"`
	Facet      string          `json:"facet"`
	Value      string          `json:"value"`
	Source     string          `json:"source"`
	Delivery   string          `json:"delivery,omitempty"`
	At         time.Time       `json:"at"`
	Context    domain.Item     `json:"context"`
}
type Action struct {
	RuleID       string   `json:"ruleId"`
	EventID      string   `json:"eventId"`
	Instructions string   `json:"instructions"`
	Skills       []string `json:"skills"`
}
type Pending struct {
	Event   Event    `json:"event"`
	Actions []Action `json:"actions"`
}
type Session struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	ItemID         string    `json:"itemId,omitempty"`
	Repository     string    `json:"repository,omitempty"`
	Agent          Agent     `json:"agent"`
	ThreadID       string    `json:"threadId,omitempty"`
	Communication  bool      `json:"communicationTools,omitempty"`
	ThreadPending  bool      `json:"threadPending,omitempty"`
	TurnID         string    `json:"turnId,omitempty"`
	Worktree       string    `json:"worktree,omitempty"`
	WorktreeReady  bool      `json:"worktreeReady,omitempty"`
	WorktreeGitDir string    `json:"worktreeGitDir,omitempty"`
	Execution      string    `json:"execution"`
	Reported       string    `json:"reported,omitempty"`
	Summary        string    `json:"summary,omitempty"`
	Hold           bool      `json:"hold"`
	Error          string    `json:"error,omitempty"`
	Pending        []Pending `json:"pending"`
	Due            time.Time `json:"due"`
	BatchID        string    `json:"batchId,omitempty"`
	Epoch          string    `json:"epoch,omitempty"`
	Created        time.Time `json:"created"`
}
type Batch struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionId"`
	State     string      `json:"state"`
	TurnID    string      `json:"turnId,omitempty"`
	Events    []Event     `json:"events"`
	Actions   []Action    `json:"actions"`
	Context   domain.Item `json:"context"`
	Message   string      `json:"message,omitempty"`
	At        time.Time   `json:"at"`
}
type Question struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionId"`
	Epoch     string          `json:"epoch"`
	RequestID json.RawMessage `json:"requestId"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	Blocking  bool            `json:"blocking"`
	Status    string          `json:"status"`
	Created   time.Time       `json:"created"`
}
type Proposal struct {
	ID      string      `json:"id"`
	State   string      `json:"state"`
	Base    string      `json:"base"`
	Before  Settings    `json:"before"`
	After   Settings    `json:"after"`
	Diff    string      `json:"diff"`
	Native  *NativeEdit `json:"native,omitempty"`
	Created time.Time   `json:"created"`
}
type NativeEdit struct {
	CWD     string          `json:"cwd"`
	File    string          `json:"file"`
	Version string          `json:"version"`
	Edits   []ConfigEdit    `json:"edits"`
	Before  json.RawMessage `json:"before"`
}
type ConfigEdit struct {
	KeyPath       string `json:"keyPath"`
	Value         any    `json:"value"`
	MergeStrategy string `json:"mergeStrategy"`
}
