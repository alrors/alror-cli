package risk

import (
	"testing"

	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/domain"
)

func cfg() *config.Config {
	c := config.Default("t")
	c.Services = []config.Service{
		{Name: "checkout-api", Paths: []string{"services/checkout/"}, Critical: true},
		{Name: "web", Paths: []string{"web/"}},
	}
	return c
}

func TestDocsOnlyIsNearFree(t *testing.T) {
	r := Score(cfg(), Change{Files: []FileChange{{Path: "docs/guide.md", Added: 900}}})
	if r.Score != 2 || r.Level != domain.LevelLow {
		t.Fatalf("docs-only change scored %d (%s), want 2 (low)", r.Score, r.Level)
	}
}

func TestRiskyChangeAddsUp(t *testing.T) {
	c := Change{
		Files: []FileChange{
			{Path: "services/checkout/payment.go", Added: 400, Deleted: 200},
			{Path: "web/app.tsx", Added: 10},
		},
		Authors:         []string{"coding-agent[bot] <bot@example.com>"},
		RecentRollbacks: map[string]int{"checkout-api": 2},
	}
	r := Score(cfg(), c)
	// 22 (≥500 lines) + 8 (2 services) + 12 (critical) + 10 (payment) + 10 (no tests) + 10 (AI) + 12 (rollbacks)
	if r.Score != 84 || r.Level != domain.LevelHigh {
		t.Fatalf("got %d (%s), want 84 (high); factors: %+v", r.Score, r.Level, r.Factors)
	}
	if !r.AI {
		t.Fatal("expected AI authorship to be detected from a [bot] author")
	}
	if r.Factors[0].Points < r.Factors[len(r.Factors)-1].Points {
		t.Fatal("factors should be sorted by points, largest first")
	}
}

func TestAITrailerDetected(t *testing.T) {
	r := Score(cfg(), Change{
		Files:   []FileChange{{Path: "web/a.ts", Added: 5}},
		Message: "Fix\n\nCo-Authored-By: Claude <noreply@anthropic.com>",
	})
	if !r.AI {
		t.Fatal("Co-Authored-By: Claude trailer should mark the change AI-authored")
	}
}

func TestWellTestedLowersScore(t *testing.T) {
	base := Change{Files: []FileChange{{Path: "web/a.ts", Added: 60}}}
	tested := Change{Files: []FileChange{{Path: "web/a.ts", Added: 60}, {Path: "web/a.test.ts", Added: 40}}}
	if Score(cfg(), tested).Score >= Score(cfg(), base).Score {
		t.Fatal("adding tests should lower the score")
	}
}

func TestParseNumstat(t *testing.T) {
	files := parseNumstat("10\t2\ta.go\n-\t-\tlogo.png\n")
	if len(files) != 2 || files[0].Added != 10 || files[1].Added != 0 {
		t.Fatalf("unexpected parse: %+v", files)
	}
}
