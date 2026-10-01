package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/cli/ui"
	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/credentials"
	"github.com/manaskumar3003/alror-cli/internal/docsite"
	"github.com/manaskumar3003/alror-cli/internal/metrics"
)

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check configuration, tools and connections",
		RunE: func(*cobra.Command, []string) error {
			fmt.Println(ui.Section("Doctor"))
			fmt.Println()
			problems := 0
			check := func(ok bool, good, bad string) {
				if ok {
					fmt.Println(ui.OK(good))
				} else {
					problems++
					fmt.Println(ui.Fail(bad))
				}
			}

			_, gitErr := exec.LookPath("git")
			check(gitErr == nil, "git found", "git not found on PATH (needed for risk scoring)")

			res, local, rerr := resolve()
			if rerr != nil {
				check(false, "", rerr.Error())
				return fmt.Errorf("%d problem(s) found", problems)
			}

			var cfg *config.Config
			if res.Server == "" {
				// Local mode: alror.yaml and .alror/.
				var err error
				if local == nil {
					err = config.ErrNotFound
				} else {
					err = local.Validate()
				}
				check(err == nil, "alror.yaml is valid", fmt.Sprint("alror.yaml: ", err))
				if err != nil {
					return fmt.Errorf("%d problem(s) found", problems)
				}
				cfg = local
				probe := filepath.Join(cfg.StateDir(), ".probe")
				werr := os.MkdirAll(cfg.StateDir(), 0o755)
				if werr == nil {
					werr = os.WriteFile(probe, []byte("ok"), 0o644)
					_ = os.Remove(probe)
				}
				check(werr == nil, "state dir writable: "+cfg.StateDir(), fmt.Sprint("state dir not writable: ", werr))
			} else {
				fmt.Println(ui.OK(fmt.Sprintf("connected mode: %s %s", res.Server, ui.Fainter.Render("(server from "+string(res.ServerFrom)+")"))))
				if cfg = doctorConnected(res, local, check); cfg == nil {
					fmt.Println()
					return fmt.Errorf("%d problem(s) found", problems)
				}
			}

			for _, s := range cfg.Services {
				switch s.Target {
				case "kubernetes":
					_, kerr := exec.LookPath("kubectl")
					check(kerr == nil, s.Name+": kubectl found", s.Name+": kubectl not found (needed for the kubernetes target)")
				case "ecs":
					fmt.Println(ui.WarnLine(s.Name + ": ECS target is planned for phase 2"))
				default:
					fmt.Println(ui.OK(s.Name + ": simulated target"))
				}
			}

			if _, err := metrics.New(cfg); err != nil {
				check(false, "", "metrics: "+err.Error())
			} else if cfg.Metrics.Provider == "prometheus" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Metrics.URL+"/-/ready", nil)
				res, herr := http.DefaultClient.Do(req)
				if res != nil {
					res.Body.Close()
				}
				check(herr == nil && res.StatusCode < 300, "prometheus reachable at "+cfg.Metrics.URL, "prometheus not reachable at "+cfg.Metrics.URL)
			} else {
				fmt.Println(ui.OK("metrics provider: " + cfg.Metrics.Provider))
			}

			if cfg.Notify.SlackWebhook == "" {
				fmt.Println(ui.WarnLine("no Slack webhook: outcomes are only shown in the CLI"))
			}
			check(len(docsite.Pages()) > 0, fmt.Sprintf("docs bundled (%d pages)", len(docsite.Pages())), "docs missing from the binary")

			fmt.Println()
			if problems > 0 {
				return fmt.Errorf("%d problem(s) found", problems)
			}
			fmt.Println(ui.Green.Bold(true).Render("All good."))
			return nil
		},
	}
}

// doctorConnected checks the workspace: reachable, key valid, scopes, config.
// It returns the effective config, or nil when the checks cannot continue.
func doctorConnected(res credentials.Resolved, local *config.Config, check func(bool, string, string)) *config.Config {
	if res.Key == "" {
		check(false, "", errNoKey(res).Error())
		return nil
	}
	client := newClient(res, clientSource())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	who, err := client.Whoami(ctx)
	switch {
	case api.IsTransient(err):
		check(false, "", fmt.Sprintf("Alror workspace not reachable at %s: %v", res.Server, err))
		return nil
	case err != nil:
		check(true, "workspace reachable at "+res.Server, "")
		check(false, "", explainAPIError(res.Server, err).Error())
		return nil
	}
	check(true, fmt.Sprintf("connected to %s at %s", who.Org.Slug, res.Server), "")
	check(true, fmt.Sprintf("API key valid: %s %s", actorLabel(who), ui.Fainter.Render("(key from "+string(res.KeyFrom)+")")), "")

	for _, sc := range []struct{ scope, why string }{
		{api.ScopeDeployRead, "status, risk and config"},
		{api.ScopeDeployWrite, "deploy and rollback"},
	} {
		check(who.HasScope(sc.scope), "scope "+sc.scope, fmt.Sprintf("scope %s missing (needed for %s)", sc.scope, sc.why))
	}
	for _, sc := range []struct{ scope, why string }{
		{api.ScopeJobsRun, "alror runner"},
		{api.ScopeConfigWrite, "alror config push"},
	} {
		if who.HasScope(sc.scope) {
			fmt.Println(ui.OK("scope " + sc.scope))
		} else {
			fmt.Println(ui.WarnLine(fmt.Sprintf("scope %s not granted (only needed for %s)", sc.scope, sc.why)))
		}
	}

	cfg, err := client.Config(ctx)
	if err != nil {
		check(false, "", "GET /config: "+explainAPIError(res.Server, err).Error())
		return nil
	}
	if err := mergeWorkspaceConfig(cfg, local, res.Server); err != nil {
		check(false, "", err.Error())
		return nil
	}
	check(true, fmt.Sprintf("config fetched: %d services", len(cfg.Services)), "")
	if local != nil && len(local.Services) > 0 {
		fmt.Println(ui.OK("local alror.yaml supplies service paths for risk scoring"))
	}
	return cfg
}
