package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// fakeGitHub records requests and serves a PR with one existing Alror comment.
type fakeGitHub struct {
	mu       sync.Mutex
	checks   []CheckRun
	patched  map[string]string
	created  []string
	comments []map[string]any
}

func (f *fakeGitHub) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /repos/acme/shop/check-runs", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var run CheckRun
		_ = json.NewDecoder(r.Body).Decode(&run)
		f.mu.Lock()
		f.checks = append(f.checks, run)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"html_url": "https://github.test/check/1"})
	})
	mux.HandleFunc("GET /repos/acme/shop/issues/7/comments", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(f.comments)
	})
	mux.HandleFunc("PATCH /repos/acme/shop/issues/comments/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.patched[r.PathValue("id")] = body["body"]
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 2, "html_url": "https://github.test/c/2"})
	})
	mux.HandleFunc("POST /repos/acme/shop/issues/7/comments", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.created = append(f.created, body["body"])
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "html_url": "https://github.test/c/9"})
	})
	return mux
}

func report() domain.RiskReport {
	return domain.RiskReport{Score: 62, Level: domain.LevelMedium, AI: true, Services: []string{"checkout-api"},
		Factors: []domain.Factor{{Name: "Critical service", Detail: "checkout-api | critical", Points: 12}}}
}

func TestCheckRunAndStickyComment(t *testing.T) {
	f := &fakeGitHub{patched: map[string]string{}, comments: []map[string]any{
		{"id": 1, "body": "unrelated"},
		{"id": 2, "body": CommentMarker + "\nold score"},
	}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	c := &Client{Base: srv.URL, Token: "tok", HTTP: srv.Client()}
	ctx := context.Background()

	run := CheckRun{Name: CheckName, HeadSHA: "abc", Status: "completed", Conclusion: Conclusion(report(), 0)}
	run.Output.Title = CheckTitle(report())
	if _, err := c.CreateCheckRun(ctx, "acme", "shop", run); err != nil {
		t.Fatal(err)
	}
	if f.checks[0].Conclusion != "neutral" || f.checks[0].Output.Title != "Medium risk (62/100)" {
		t.Fatalf("check run: %+v", f.checks[0])
	}

	if _, err := c.UpsertComment(ctx, "acme", "shop", 7, CommentMarker, "new score"); err != nil {
		t.Fatal(err)
	}
	if got := f.patched["2"]; !strings.Contains(got, "new score") || len(f.created) != 0 {
		t.Fatalf("existing comment should be edited, not duplicated: patched=%v created=%v", f.patched, f.created)
	}

	f.comments = nil // no Alror comment yet → create one
	if _, err := c.UpsertComment(ctx, "acme", "shop", 7, CommentMarker, "first score"); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 || !strings.HasPrefix(f.created[0], CommentMarker) {
		t.Fatalf("expected one new marked comment, got %v", f.created)
	}
}

func TestForbiddenIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, Token: "tok", HTTP: srv.Client()}
	_, err := c.CreateCheckRun(context.Background(), "acme", "shop", CheckRun{})
	if !IsForbidden(err) {
		t.Fatalf("want forbidden, got %v", err)
	}
}

func TestConclusionGate(t *testing.T) {
	low := domain.RiskReport{Score: 10, Level: domain.LevelLow}
	high := domain.RiskReport{Score: 85, Level: domain.LevelHigh}
	if Conclusion(low, 0) != "success" || Conclusion(high, 0) != "neutral" || Conclusion(high, 80) != "failure" {
		t.Fatal("unexpected conclusions")
	}
}

func TestMarkdownEscapesTableCells(t *testing.T) {
	md := RiskMarkdown(report(), domain.Plan{Strategy: "canary", Steps: []domain.Step{{Weight: 5}, {Weight: 100}}}, 3, "")
	if !strings.Contains(md, `checkout-api \| critical`) {
		t.Fatalf("pipe in detail must be escaped:\n%s", md)
	}
	if !strings.Contains(md, "canary 5% → 100%") || !strings.Contains(md, "AI-authored") {
		t.Fatalf("missing plan or AI flag:\n%s", md)
	}
}

func TestFromEnvReadsPullRequestEvent(t *testing.T) {
	dir := t.TempDir()
	event := filepath.Join(dir, "event.json")
	_ = os.WriteFile(event, []byte(`{"pull_request":{"number":7,"head":{"sha":"headsha"},"base":{"ref":"main"}}}`), 0o644)
	t.Setenv("GITHUB_REPOSITORY", "acme/shop")
	t.Setenv("GITHUB_SHA", "mergesha")
	t.Setenv("GITHUB_EVENT_PATH", event)
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GITHUB_BASE_REF", "")
	e, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if e.PR != 7 || e.SHA != "headsha" || e.BaseRef != "main" || !e.CanPost() {
		t.Fatalf("env: %+v", e)
	}
}

func TestOutputsAndSummary(t *testing.T) {
	dir := t.TempDir()
	e := Env{SummaryPath: filepath.Join(dir, "sum.md"), OutputPath: filepath.Join(dir, "out")}
	if err := e.SetOutputs(map[string]string{"score": "62", "md": "a\nb"}); err != nil {
		t.Fatal(err)
	}
	_ = e.AppendSummary("### hi")
	out, _ := os.ReadFile(e.OutputPath)
	if !strings.Contains(string(out), "score=62") || !strings.Contains(string(out), "md<<ALROR_EOF_") {
		t.Fatalf("outputs: %s", out)
	}
	sum, _ := os.ReadFile(e.SummaryPath)
	if !strings.Contains(string(sum), "### hi") {
		t.Fatal("summary not written")
	}
}
