// Package domain holds the types shared by every Alror component:
// the CLI, the Alror workspace (web platform), the runner and the docs.
package domain

import "time"

// Status is the lifecycle state of a deployment.
type Status string

const (
	StatusPending    Status = "pending"
	StatusRolling    Status = "rolling"
	StatusPromoted   Status = "promoted"
	StatusRolledBack Status = "rolled_back"
	StatusFailed     Status = "failed"
)

// Terminal reports whether no further transitions are possible.
func (s Status) Terminal() bool {
	return s == StatusPromoted || s == StatusRolledBack || s == StatusFailed
}

// Level buckets a risk score.
type Level string

const (
	LevelLow    Level = "low"
	LevelMedium Level = "medium"
	LevelHigh   Level = "high"
)

// Factor is one signal that contributed to a risk score.
type Factor struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
	Points int    `json:"points"`
}

// RiskReport is the output of the risk scorer for one change.
type RiskReport struct {
	Score    int      `json:"score"` // 0..100
	Level    Level    `json:"level"`
	Factors  []Factor `json:"factors"`
	Services []string `json:"services"`
	AI       bool     `json:"ai_authored"`
}

// Step is one stage of a progressive rollout.
type Step struct {
	Weight int           `json:"weight"` // percent of traffic on the canary
	Bake   time.Duration `json:"bake"`   // how long to observe before deciding
}

// Plan is the rollout plan derived from a risk report.
type Plan struct {
	Strategy string `json:"strategy"` // canary | blue-green
	Steps    []Step `json:"steps"`
}

// MetricResult is the verdict for one metric at one step.
type MetricResult struct {
	Metric   string  `json:"metric"`
	Canary   float64 `json:"canary"`
	Baseline float64 `json:"baseline"`
	Delta    float64 `json:"delta"` // relative change, canary vs baseline
	PValue   float64 `json:"p_value"`
	Pass     bool    `json:"pass"`
	Reason   string  `json:"reason,omitempty"`
}

// Verdict is the decision taken after a bake period.
type Verdict struct {
	Pass    bool           `json:"pass"`
	Results []MetricResult `json:"results"`
	Summary string         `json:"summary"`
}

// Deployment is the persisted record of one release.
type Deployment struct {
	ID        string     `json:"id"`
	Service   string     `json:"service"`
	Image     string     `json:"image"`
	Ref       string     `json:"ref,omitempty"` // PR number or commit
	Risk      RiskReport `json:"risk"`
	Plan      Plan       `json:"plan"`
	Status    Status     `json:"status"`
	StepIndex int        `json:"step_index"`
	Weight    int        `json:"weight"`
	Reason    string     `json:"reason,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	// Environment is the target environment (e.g. production). Set in
	// connected mode; empty in local mode.
	Environment string `json:"environment,omitempty"`
	// Source records where the deployment started: cli, ci, console or
	// runner. Set in connected mode; empty in local mode.
	Source string `json:"source,omitempty"`
}

// EventKind classifies a deployment event.
type EventKind string

const (
	EventCreated    EventKind = "created"
	EventStep       EventKind = "step"
	EventVerdict    EventKind = "verdict"
	EventPromoted   EventKind = "promoted"
	EventRolledBack EventKind = "rolled_back"
	EventError      EventKind = "error"
)

// Event is one append-only entry in a deployment's history.
type Event struct {
	At      time.Time `json:"at"`
	Kind    EventKind `json:"kind"`
	Message string    `json:"message"`
	Weight  int       `json:"weight,omitempty"`
	Verdict *Verdict  `json:"verdict,omitempty"`
}
