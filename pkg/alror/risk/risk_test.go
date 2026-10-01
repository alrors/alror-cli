package risk_test

import (
	"fmt"
	"testing"

	"github.com/manaskumar3003/alror-cli/pkg/alror"
	"github.com/manaskumar3003/alror-cli/pkg/alror/risk"
)

func cfg() *alror.Config {
	return &alror.Config{
		Project: "shop",
		Services: []alror.Service{
			{Name: "checkout-api", Paths: []string{"services/checkout/"}, Target: "simulated", Critical: true},
			{Name: "web", Paths: []string{"web/"}, Target: "simulated"},
		},
	}
}

func TestScoreIsExplainable(t *testing.T) {
	r := risk.Score(cfg(), risk.Change{
		Files:           []risk.FileChange{{Path: "services/checkout/payment.go", Added: 400, Deleted: 50}},
		RecentRollbacks: map[string]int{"checkout-api": 2},
	})
	if r.Score <= 34 || len(r.Factors) == 0 || r.Services[0] != "checkout-api" {
		t.Fatalf("report = %+v", r)
	}
	sum := 0
	for _, f := range r.Factors {
		sum += f.Points
	}
	if min(sum, 100) != r.Score {
		t.Fatalf("score %d is not the sum of its factors (%d)", r.Score, sum)
	}
	if risk.LevelFor(r.Score) != r.Level {
		t.Fatalf("level mismatch")
	}
	if p := risk.PlanFor(r); p.Steps[len(p.Steps)-1].Weight != 100 {
		t.Fatalf("plan = %+v", p)
	}
}

func TestFromGitOutsideARepo(t *testing.T) {
	if _, err := risk.FromGit(t.TempDir(), "main"); err == nil {
		t.Fatal("want an error outside a git repository")
	}
}

func ExampleScore() {
	report := risk.Score(cfg(), risk.Change{
		Files: []risk.FileChange{{Path: "web/README.md", Added: 3}},
	})
	fmt.Println(report.Score, report.Level)
	// Output: 2 low
}
