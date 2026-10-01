// Package risk exposes Alror's rule-based change risk scorer for library use.
//
// Scoring is deterministic: every point comes from a named Factor (diff size,
// blast radius, critical services, sensitive paths, missing tests, AI
// authorship, recent rollbacks), so a reviewer can always see why. Collect a
// Change from git with FromGit, or build one yourself, then call Score with
// the project's configuration (alror.LoadConfig or alror.Client.Config).
package risk

import (
	"github.com/manaskumar3003/alror-cli/internal/risk"
	"github.com/manaskumar3003/alror-cli/internal/rollout"
	"github.com/manaskumar3003/alror-cli/pkg/alror"
)

// Change is everything the scorer needs to know about a proposed release:
// changed files, commit authors and messages, and recent rollbacks per service.
type Change = risk.Change

// FileChange is one file in a diff with its added and deleted line counts.
type FileChange = risk.FileChange

// Score returns the risk report for a change under cfg. cfg.Services maps
// changed paths to services; critical services raise the score.
func Score(cfg *alror.Config, c Change) alror.RiskReport { return risk.Score(cfg, c) }

// FromGit collects the change between base and HEAD in the repository at dir
// using the git CLI. An empty base picks origin/main, then main, then master.
// When nothing is committed past base, the uncommitted diff is used.
func FromGit(dir, base string) (Change, error) { return risk.FromGit(dir, base) }

// LevelFor buckets a 0-100 score into low (0-34), medium (35-69) or high (70-100).
func LevelFor(score int) alror.Level { return risk.LevelFor(score) }

// PlanFor returns the rollout plan Alror uses for a risk report.
func PlanFor(r alror.RiskReport) alror.Plan { return rollout.PlanFor(r) }
