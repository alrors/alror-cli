package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/api/apitest"
	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/credentials"
	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// isolate points credentials at a temp file and clears env that changes modes.
func isolate(t *testing.T) string {
	t.Helper()
	creds := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("ALROR_CREDENTIALS", creds)
	t.Setenv("ALROR_SERVER", "")
	t.Setenv("ALROR_API_KEY", "")
	t.Setenv("ALROR_NO_ANIM", "1")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("CI", "")
	return creds
}

func mustRun(t *testing.T, want int, args ...string) {
	t.Helper()
	if code := run(args); code != want {
		t.Fatalf("alror %s: exit %d, want %d", strings.Join(args, " "), code, want)
	}
}

func TestRemoteModeEndToEnd(t *testing.T) {
	creds := isolate(t)
	f, srv := apitest.Start()
	defer srv.Close()
	f.ClaimWait = 100 * time.Millisecond
	repo := t.TempDir()

	mustRun(t, 1, "--no-anim", "login", "--server", srv.URL, "--key", "alr_live_wrong")
	if c, _ := credentials.Load(creds); c != nil {
		t.Fatal("a rejected key must not be saved")
	}
	mustRun(t, 0, "--no-anim", "login", "--server", srv.URL+"/api/v1", "--key", apitest.DefaultKey)
	c, _ := credentials.Load(creds)
	if c == nil || c.Server != srv.URL || c.APIKey != apitest.DefaultKey {
		t.Fatalf("credentials = %+v", c)
	}
	mustRun(t, 0, "whoami")

	// No alror.yaml at all: config comes from the server.
	mustRun(t, 0, "-C", repo, "--no-anim", "deploy", "-s", "checkout-api", "-i", "app:2", "--risk", "10", "--fast", "--env", "staging")
	var dep *domain.Deployment
	for _, r := range f.Requests() {
		if r.Method == "POST" && r.Path == "/api/v1/deployments" {
			if r.Source != "cli" {
				t.Fatalf("X-Alror-Source = %q", r.Source)
			}
		}
	}
	reqs := f.Requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if r := reqs[i]; r.Method == "PUT" && strings.HasPrefix(r.Path, "/api/v1/deployments/") {
			dep = f.Deployment(strings.TrimPrefix(r.Path, "/api/v1/deployments/"))
			break
		}
	}
	if dep == nil || dep.Status != domain.StatusPromoted || dep.Environment != "staging" || dep.Source != "cli" {
		t.Fatalf("deployment = %+v", dep)
	}
	if ev := f.Events(dep.ID); len(ev) < 3 || ev[len(ev)-1].Kind != domain.EventPromoted {
		t.Fatalf("events = %+v", ev)
	}

	mustRun(t, 0, "-C", repo, "status")
	mustRun(t, 0, "-C", repo, "status", dep.ID[:12])
	mustRun(t, 0, "-C", repo, "rollback", dep.ID, "--reason", "test")
	if d := f.Deployment(dep.ID); d.Status != domain.StatusRolledBack || d.Reason != "test" {
		t.Fatalf("after rollback: %+v", d)
	}
	mustRun(t, 1, "-C", repo, "deploy", "-s", "nope", "-i", "x")
	mustRun(t, 0, "-C", repo, "doctor")

	// config pull keeps the server: line; push sends local services.
	if err := os.WriteFile(filepath.Join(repo, "alror.yaml"), []byte("server: "+srv.URL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, 0, "-C", repo, "config", "pull")
	pulled, err := config.Load(repo)
	if err != nil || pulled.Server != srv.URL || len(pulled.Services) != 2 {
		t.Fatalf("pulled = %+v, %v", pulled, err)
	}
	pulled.Services = append(pulled.Services, config.Service{Name: "billing", Paths: []string{"billing/"}, Target: "simulated"})
	pulled.Policy.Alpha = 0.01
	if err := pulled.Save(pulled.Path()); err != nil {
		t.Fatal(err)
	}
	mustRun(t, 0, "-C", repo, "config", "push", "--dry-run")
	if len(f.Config().Services) != 2 {
		t.Fatal("dry run must not push")
	}
	mustRun(t, 0, "-C", repo, "config", "push")
	if got := f.Config(); len(got.Services) != 3 || got.Policy.Alpha != 0.01 {
		t.Fatalf("pushed config = %+v", got)
	}

	// A runner picks up a console-triggered job.
	j := f.Enqueue(api.JobDeploy, map[string]any{"service": "billing", "image": "billing:7", "risk_override": 5})
	mustRun(t, 0, "-C", repo, "runner", "--once", "--name", "ci")
	if got := f.Job(j.ID); got.Status != api.JobDone || got.ClaimedBy != "ci" {
		t.Fatalf("job = %+v", got)
	}

	// --local ignores the server and needs a complete alror.yaml.
	mustRun(t, 0, "-C", repo, "--local", "status")

	mustRun(t, 0, "logout")
	if c, _ := credentials.Load(creds); c != nil {
		t.Fatal("logout must remove credentials")
	}
	// alror.yaml still names the server, but there is no key any more.
	mustRun(t, 1, "-C", repo, "status")
}

func TestLocalModeUnchanged(t *testing.T) {
	isolate(t)
	repo := t.TempDir()
	mustRun(t, 0, "-C", repo, "--no-anim", "init")
	mustRun(t, 0, "-C", repo, "--no-anim", "deploy", "-s", "checkout-api", "-i", "app:2", "--risk", "10", "--fast")
	entries, err := os.ReadDir(filepath.Join(repo, ".alror", "deployments"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("local state: %v %v", entries, err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, ".alror", "deployments", entries[0].Name()))
	if strings.Contains(string(raw), `"environment"`) || strings.Contains(string(raw), `"source"`) {
		t.Fatalf("local files must not gain environment/source: %s", raw)
	}
	mustRun(t, 0, "-C", repo, "status")
	mustRun(t, 1, "-C", repo, "whoami") // needs a connected workspace
}

func TestRemoteConfigOverlaysLocalPaths(t *testing.T) {
	g = globals{dir: t.TempDir()}
	remote := config.Default("acme")
	remote.Services[0].Paths = []string{"server/path/"}
	local := &config.Config{Dir: g.dir, Services: []config.Service{{Name: "checkout-api", Paths: []string{"local/checkout/"}, Target: "kubernetes"}}}
	if err := mergeWorkspaceConfig(remote, local, "http://x"); err != nil {
		t.Fatal(err)
	}
	s, _ := remote.Service("checkout-api")
	if s.Paths[0] != "local/checkout/" || s.Target != "simulated" {
		t.Fatalf("paths should come from alror.yaml, everything else from the server: %+v", s)
	}
	if remote.Dir != g.dir || remote.Server != "http://x" {
		t.Fatalf("dir/server = %q %q", remote.Dir, remote.Server)
	}
}
