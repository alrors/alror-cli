package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Whoami is the response of GET /whoami.
type Whoami struct {
	Org    Org      `json:"org"`
	Actor  Actor    `json:"actor"`
	Scopes []string `json:"scopes"`
}

// Org identifies the organisation implied by the key or session.
type Org struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Actor is who is calling: an API key, a user (session) or the system.
type Actor struct {
	Type  string `json:"type"` // api_key | user | system
	ID    string `json:"id"`
	Label string `json:"label"`
}

// HasScope reports whether the caller holds scope. Sessions (no scopes listed
// for user actors) are treated as holding every scope; the server decides.
func (w *Whoami) HasScope(scope string) bool {
	if w.Actor.Type == "user" && len(w.Scopes) == 0 {
		return true
	}
	for _, s := range w.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// API key scopes defined by the contract.
const (
	ScopeDeployRead  = "deploy:read"
	ScopeDeployWrite = "deploy:write"
	ScopeJobsRun     = "jobs:run"
	ScopeConfigWrite = "config:write"
)

// Job kinds and statuses.
const (
	JobDeploy   = "deploy"
	JobRollback = "rollback"

	JobQueued   = "queued"
	JobClaimed  = "claimed"
	JobDone     = "done"
	JobFailed   = "failed"
	JobCanceled = "canceled"
)

// Job is a unit of work enqueued by the console or the API and executed by `alror runner`.
type Job struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"` // deploy | rollback
	Payload      json.RawMessage `json:"payload"`
	Status       string          `json:"status"` // queued | claimed | done | failed | canceled
	ClaimedBy    string          `json:"claimed_by,omitempty"`
	DeploymentID string          `json:"deployment_id,omitempty"`
	Error        string          `json:"error,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	ClaimedAt    *time.Time      `json:"claimed_at,omitempty"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`
}

// DeployPayload is the payload of a deploy job.
type DeployPayload struct {
	Service     string `json:"service"`
	Image       string `json:"image"`
	Ref         string `json:"ref,omitempty"`
	Environment string `json:"environment,omitempty"`
	Shadow      bool   `json:"shadow,omitempty"`
	// RiskOverride skips risk scoring: a level ("low", "medium" or "high",
	// as the console sends) or a numeric score ("0" to "100"). Runners have
	// no git context, so without it a deploy job assumes medium risk.
	RiskOverride string `json:"risk_override,omitempty"`
}

// Representative scores used when risk_override is a level.
var levelScores = map[string]int{"low": 20, "medium": 50, "high": 85}

// RiskOverrideScore parses RiskOverride. ok is false when it is empty.
func (p DeployPayload) RiskOverrideScore() (score int, ok bool, err error) {
	v := strings.ToLower(strings.TrimSpace(p.RiskOverride))
	if v == "" {
		return 0, false, nil
	}
	if s, isLevel := levelScores[v]; isLevel {
		return s, true, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 100 {
		return 0, false, fmt.Errorf("risk_override %q: want low, medium, high or a score 0-100", p.RiskOverride)
	}
	return n, true, nil
}

// UnmarshalJSON accepts risk_override as a string (level or score), a
// number, or an object with a "score" or "level" field, so console and API
// clients can be lenient.
func (p *DeployPayload) UnmarshalJSON(b []byte) error {
	type plain DeployPayload
	var aux struct {
		plain
		RiskOverride json.RawMessage `json:"risk_override,omitempty"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*p = DeployPayload(aux.plain)
	p.RiskOverride = ""
	raw := strings.TrimSpace(string(aux.RiskOverride))
	if raw == "" || raw == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(aux.RiskOverride, &s); err == nil {
		p.RiskOverride = s
		return nil
	}
	var n float64
	if err := json.Unmarshal(aux.RiskOverride, &n); err == nil {
		p.RiskOverride = strconv.Itoa(int(n))
		return nil
	}
	var obj struct {
		Score *int   `json:"score"`
		Level string `json:"level"`
	}
	if err := json.Unmarshal(aux.RiskOverride, &obj); err == nil {
		switch {
		case obj.Score != nil:
			p.RiskOverride = strconv.Itoa(*obj.Score)
			return nil
		case obj.Level != "":
			p.RiskOverride = obj.Level
			return nil
		}
	}
	return fmt.Errorf("risk_override: want low, medium, high or a score 0-100, got %s", raw)
}

// RollbackPayload is the payload of a rollback job.
type RollbackPayload struct {
	DeploymentID string `json:"deployment_id"`
	Reason       string `json:"reason,omitempty"`
}

// EnqueueRequest is the body of POST /jobs.
type EnqueueRequest struct {
	Kind    string `json:"kind"`
	Payload any    `json:"payload"`
}

// FinishRequest is the body of POST /jobs/{id}/finish.
type FinishRequest struct {
	Status       string `json:"status"` // done | failed
	Error        string `json:"error,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
}

// ClaimRequest is the body of POST /jobs/claim and POST /jobs/{id}/heartbeat.
// The contract names the field "worker"; in Go it is the runner's name.
type ClaimRequest struct {
	Runner string `json:"worker"`
}

// ListOptions filters GET /deployments.
type ListOptions struct {
	Limit int // 0 = server default (50); the server caps it at 1000
	// Before pages backwards: only deployments older than this deployment id
	// or RFC 3339 time are returned.
	Before  string
	Status  string // pending | rolling | promoted | rolled_back | failed
	Service string
}
