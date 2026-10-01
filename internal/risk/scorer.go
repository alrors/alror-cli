// Package risk scores a code change from 0 to 100 and explains why.
//
// Scoring is deliberately rule-based: every point comes from a named factor,
// so a reviewer can always see (and argue with) the reasons. Language models
// never decide the score; they may only summarise it.
package risk

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// FileChange is one file in a diff.
type FileChange struct {
	Path    string
	Added   int
	Deleted int
}

// Change is everything the scorer needs to know about a proposed release.
type Change struct {
	Files   []FileChange
	Authors []string // "name <email>"
	Message string   // concatenated commit messages, including trailers
	// RecentRollbacks counts rollbacks per service in the last 30 days.
	RecentRollbacks map[string]int
}

var (
	sensitive = regexp.MustCompile(`(?i)(auth|security|crypto|secret|payment|billing|ledger|migration|schema|terraform|helm|iam|rbac)`)
	testFile  = regexp.MustCompile(`(?i)(_test\.go$|\.test\.[jt]sx?$|\.spec\.[jt]sx?$|(^|/)tests?/|test_.*\.py$)`)
	docFile   = regexp.MustCompile(`(?i)(\.md$|\.mdx$|\.txt$|(^|/)docs?/)`)
	aiAuthor  = regexp.MustCompile(`(?i)(\[bot\]|copilot|claude|devin|codex|cursor|agent|gpt)`)
	aiTrailer = regexp.MustCompile(`(?i)(co-authored-by:.*(claude|copilot|codex|cursor|devin|gpt|agent)|generated[- ]by:|ai-assisted:\s*true)`)
)

// Score computes the risk report for a change against a configuration.
func Score(cfg *config.Config, c Change) domain.RiskReport {
	var factors []domain.Factor
	add := func(name, detail string, pts int) {
		if pts != 0 {
			factors = append(factors, domain.Factor{Name: name, Detail: detail, Points: pts})
		}
	}

	lines, code, tests, docs := 0, 0, 0, 0
	sensitiveHits := map[string]bool{}
	services := map[string]config.Service{}
	for _, f := range c.Files {
		lines += f.Added + f.Deleted
		switch {
		case testFile.MatchString(f.Path):
			tests++
		case docFile.MatchString(f.Path):
			docs++
		default:
			code++
		}
		if m := sensitive.FindString(f.Path); m != "" {
			sensitiveHits[strings.ToLower(m)] = true
		}
		for _, s := range cfg.ServicesForPath(f.Path) {
			services[s.Name] = s
		}
	}

	// Docs-only changes are near-free.
	if len(c.Files) > 0 && docs == len(c.Files) {
		return finish([]domain.Factor{{Name: "Docs only", Detail: fmt.Sprintf("%d documentation files", docs), Points: 2}}, services, false)
	}

	// 1. Size of the change.
	switch {
	case lines >= 1000:
		add("Large diff", fmt.Sprintf("%d lines changed", lines), 30)
	case lines >= 500:
		add("Large diff", fmt.Sprintf("%d lines changed", lines), 22)
	case lines >= 200:
		add("Medium diff", fmt.Sprintf("%d lines changed", lines), 15)
	case lines >= 50:
		add("Small diff", fmt.Sprintf("%d lines changed", lines), 8)
	}
	if n := len(c.Files); n > 50 {
		add("Many files", fmt.Sprintf("%d files touched", n), 12)
	} else if n > 20 {
		add("Many files", fmt.Sprintf("%d files touched", n), 8)
	}

	// 2. Blast radius across services.
	if n := len(services); n > 1 {
		add("Blast radius", fmt.Sprintf("touches %d services: %s", n, strings.Join(names(services), ", ")), min(8*(n-1), 20))
	}
	for _, s := range services {
		if s.Critical {
			add("Critical service", s.Name+" is marked critical", 12)
			break
		}
	}

	// 3. Sensitive areas of the codebase.
	if n := len(sensitiveHits); n > 0 {
		add("Sensitive paths", "changes in "+strings.Join(keys(sensitiveHits), ", "), min(10*n, 20))
	}

	// 4. Test coverage of the change.
	if code > 0 && tests == 0 {
		add("No tests changed", fmt.Sprintf("%d code files, 0 test files", code), 10)
	} else if code > 0 && tests*2 >= code {
		add("Well tested", fmt.Sprintf("%d test files for %d code files", tests, code), -5)
	}

	// 5. Who (or what) wrote it. One input, never a block on its own.
	ai := aiTrailer.MatchString(c.Message)
	for _, a := range c.Authors {
		if aiAuthor.MatchString(a) {
			ai = true
		}
	}
	if ai {
		add("AI-authored", "commit authors or trailers indicate a coding agent", 10)
	}

	// 6. Recent history of the services involved.
	rollbacks := 0
	for name := range services {
		rollbacks += c.RecentRollbacks[name]
	}
	if rollbacks > 0 {
		add("Recent rollbacks", fmt.Sprintf("%d rollbacks on these services in 30 days", rollbacks), min(6*rollbacks, 18))
	}

	return finish(factors, services, ai)
}

func finish(factors []domain.Factor, services map[string]config.Service, ai bool) domain.RiskReport {
	score := 0
	for _, f := range factors {
		score += f.Points
	}
	score = max(0, min(score, 100))
	sort.SliceStable(factors, func(i, j int) bool { return factors[i].Points > factors[j].Points })
	return domain.RiskReport{Score: score, Level: LevelFor(score), Factors: factors, Services: names(services), AI: ai}
}

// LevelFor maps a score to a level.
func LevelFor(score int) domain.Level {
	switch {
	case score >= 70:
		return domain.LevelHigh
	case score >= 35:
		return domain.LevelMedium
	default:
		return domain.LevelLow
	}
}

func names(m map[string]config.Service) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Ext returns a file's extension without the dot (used by callers for display).
func Ext(p string) string { return strings.TrimPrefix(path.Ext(p), ".") }
