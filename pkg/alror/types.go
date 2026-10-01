package alror

import (
	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/domain"
	"github.com/manaskumar3003/alror-cli/internal/store"
)

// Deployment is the persisted record of one release: its risk, plan,
// status and current canary weight.
type Deployment = domain.Deployment

// Event is one append-only entry in a deployment's history.
type Event = domain.Event

// EventKind classifies an Event.
type EventKind = domain.EventKind

// Verdict is the decision taken after a bake period, with one MetricResult per metric.
type Verdict = domain.Verdict

// MetricResult is the canary-versus-baseline comparison for one metric at one step.
type MetricResult = domain.MetricResult

// RiskReport is the output of the risk scorer: a 0-100 score, a level and the factors behind it.
type RiskReport = domain.RiskReport

// Factor is one named signal that contributed points to a RiskReport.
type Factor = domain.Factor

// Plan is a progressive rollout plan: a strategy and its steps.
type Plan = domain.Plan

// Step is one stage of a Plan: a canary traffic weight and a bake time
// (JSON: integer nanoseconds).
type Step = domain.Step

// Status is the lifecycle state of a Deployment.
type Status = domain.Status

// Level buckets a risk score into low, medium or high.
type Level = domain.Level

// Deployment statuses.
const (
	// StatusPending means the deployment is recorded but traffic has not moved yet.
	StatusPending = domain.StatusPending
	// StatusRolling means a canary is taking a share of traffic.
	StatusRolling = domain.StatusRolling
	// StatusPromoted means every step verified and the release has all traffic.
	StatusPromoted = domain.StatusPromoted
	// StatusRolledBack means a step failed verification (or an operator asked) and traffic returned to stable.
	StatusRolledBack = domain.StatusRolledBack
	// StatusFailed means a driver or provider error stopped the rollout; a rollback was attempted.
	StatusFailed = domain.StatusFailed
)

// Risk levels.
const (
	// LevelLow is a score of 0-34: a short two-step rollout.
	LevelLow = domain.LevelLow
	// LevelMedium is a score of 35-69: a four-step canary.
	LevelMedium = domain.LevelMedium
	// LevelHigh is a score of 70-100: a five-step canary starting at 1%.
	LevelHigh = domain.LevelHigh
)

// Event kinds.
const (
	// EventCreated is the first event of a rollout.
	EventCreated = domain.EventCreated
	// EventStep records a traffic shift to a new canary weight.
	EventStep = domain.EventStep
	// EventVerdict records a verification result (or a shadow-mode recommendation).
	EventVerdict = domain.EventVerdict
	// EventPromoted records promotion to 100%.
	EventPromoted = domain.EventPromoted
	// EventRolledBack records a rollback.
	EventRolledBack = domain.EventRolledBack
	// EventError records a driver or provider error.
	EventError = domain.EventError
)

// Config is the alror.yaml shape, also returned by GET /config: project,
// services, metrics, policy and notify settings.
type Config = config.Config

// Service maps source paths to a deployable unit and its target.
type Service = config.Service

// Metrics selects and configures the metrics provider used for verification.
type Metrics = config.Metrics

// Policy holds verification thresholds and the auto-rollback switch.
type Policy = config.Policy

// Notify configures outbound notifications.
type Notify = config.Notify

// Job is a unit of work (deploy or rollback) queued from the Alror console
// or API and executed by a runner (`alror runner`).
type Job = api.Job

// DeployRequest is the payload of a deploy job. RiskOverride skips risk
// scoring: "low", "medium" or "high" (as the console sends; a runner scores
// them 20, 50 and 85) or a numeric score "0" to "100". Without it a runner,
// which has no git context, assumes medium risk. Environment defaults to the
// server's choice (usually production) and is recorded on the Deployment.
type DeployRequest = api.DeployPayload

// RollbackRequest is the payload of a rollback job.
type RollbackRequest = api.RollbackPayload

// JobResult reports a job's outcome to FinishJob: status "done" or "failed",
// an optional error and the deployment it produced.
type JobResult = api.FinishRequest

// ListOptions filters ListDeployments. Zero values mean "no filter" and the
// server's default limit (50, maximum 1000). Before is a deployment id or an
// RFC 3339 time; only older deployments are returned, for paging.
type ListOptions = api.ListOptions

// WhoAmI describes the caller: its organisation, actor and scopes.
type WhoAmI = api.Whoami

// Org identifies an organisation (workspace).
type Org = api.Org

// Actor identifies who is calling: an API key, a user or the system.
type Actor = api.Actor

// Job kinds.
const (
	// JobKindDeploy runs a progressive rollout.
	JobKindDeploy = api.JobDeploy
	// JobKindRollback rolls an existing deployment back.
	JobKindRollback = api.JobRollback
)

// Job statuses.
const (
	// JobQueued is waiting for a runner.
	JobQueued = api.JobQueued
	// JobClaimed is being executed by a runner.
	JobClaimed = api.JobClaimed
	// JobDone finished; a rolled-back release is still done (an outcome, not a failure).
	JobDone = api.JobDone
	// JobFailed could not be executed.
	JobFailed = api.JobFailed
	// JobCanceled was cancelled before a runner claimed it.
	JobCanceled = api.JobCanceled
)

// API key scopes.
const (
	// ScopeDeployRead allows reading config, deployments, events and jobs.
	ScopeDeployRead = api.ScopeDeployRead
	// ScopeDeployWrite allows writing deployments and events and enqueueing jobs.
	ScopeDeployWrite = api.ScopeDeployWrite
	// ScopeJobsRun allows claiming and finishing jobs (runners).
	ScopeJobsRun = api.ScopeJobsRun
	// ScopeConfigWrite allows PUT /config (admin keys).
	ScopeConfigWrite = api.ScopeConfigWrite
)

// NewDeploymentID returns a new sortable deployment id: dep_<UTC time>_<6 hex>.
func NewDeploymentID() string { return store.NewID() }

// LoadConfig finds alror.yaml in dir or a parent, parses and validates it.
func LoadConfig(dir string) (*Config, error) { return config.Load(dir) }
