package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/cli/ui"
	"github.com/manaskumar3003/alror-cli/internal/runner"
)

func runnerCmd() *cobra.Command {
	var (
		name        string
		once        bool
		concurrency int
	)
	cmd := &cobra.Command{
		Use:   "runner",
		Short: "Run deploys and rollbacks queued from the Alror console, in your own infrastructure",
		Long: "Claims jobs with POST /jobs/claim (long-poll), runs them with the rollout\n" +
			"engine against the Alror workspace, and reports each outcome. Ctrl-C (SIGINT or\n" +
			"SIGTERM) stops claiming and lets the current job finish; a second Ctrl-C aborts\n" +
			"it, which rolls the canary back. Needs an API key with the jobs:run scope.",
		Example: "  alror runner\n  alror runner --name ci-runner-1 --concurrency 2\n  alror runner --once      # run at most one job, then exit",
		RunE: func(*cobra.Command, []string) error {
			client, res, _, err := connectedClient("runner")
			if err != nil {
				return err
			}
			if name == "" {
				name, _ = os.Hostname()
				if name == "" {
					name = "runner"
				}
			}
			log := newRunnerLog(g.json)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			who, err := client.Whoami(ctx)
			cancel()
			switch {
			case err == nil && !who.HasScope(api.ScopeJobsRun):
				return fmt.Errorf("the API key %s lacks the jobs:run scope (has: %v)", keyPrefix(res.Key), who.Scopes)
			case err == nil:
				log.line(runner.Info, fmt.Sprintf("runner %s · %s workspace (%s) · concurrency %d%s", name, who.Org.Slug, res.Server, concurrency, onceNote(once)))
			case api.IsTransient(err):
				log.line(runner.Warn, fmt.Sprintf("runner %s · %s not reachable yet (%v); will keep trying", name, res.Server, err))
			default:
				return explainAPIError(res.Server, err)
			}

			claimCtx, stopClaiming := context.WithCancel(context.Background())
			defer stopClaiming()
			jobCtx, abortJobs := context.WithCancel(context.Background())
			defer abortJobs()

			sigs := make(chan os.Signal, 2)
			signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(sigs)
			go func() {
				n := 0
				for range sigs {
					n++
					if n == 1 {
						log.line(runner.Warn, "stopping: no new jobs; the current job will finish (Ctrl-C again to abort it)")
						stopClaiming()
					} else {
						log.line(runner.Error, "aborting the current job; the engine rolls the canary back")
						abortJobs()
					}
				}
			}()

			w := &runner.Runner{Client: client, Name: name, Concurrency: concurrency, Log: log.line, JobContext: jobCtx}
			if err := w.Run(claimCtx, once); err != nil {
				return err
			}
			if !once {
				log.line(runner.OK, "runner stopped")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "runner name reported as claimed_by (default: hostname)")
	cmd.Flags().BoolVar(&once, "once", false, "claim at most one job (one long-poll), run it and exit")
	cmd.Flags().IntVar(&concurrency, "concurrency", 1, "jobs to run in parallel")
	return cmd
}

func onceNote(once bool) string {
	if once {
		return " · once"
	}
	return ""
}

// runnerLog prints one line per event: "15:04:05  ✓ message". No animations:
// the runner is a daemon whose output usually ends up in a log file.
type runnerLog struct {
	mu   sync.Mutex
	json bool
}

func newRunnerLog(asJSON bool) *runnerLog { return &runnerLog{json: asJSON} }

func (l *runnerLog) line(lv runner.Level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.json {
		levels := map[runner.Level]string{runner.Info: "info", runner.OK: "ok", runner.Warn: "warn", runner.Error: "error"}
		raw, _ := json.Marshal(map[string]string{"at": now.UTC().Format(time.RFC3339Nano), "level": levels[lv], "msg": msg})
		fmt.Println(string(raw))
		return
	}
	ts := ui.Fainter.Render(now.Format("15:04:05"))
	switch lv {
	case runner.OK:
		fmt.Printf("%s  %s\n", ts, ui.OK(msg))
	case runner.Warn:
		fmt.Printf("%s  %s\n", ts, ui.WarnLine(msg))
	case runner.Error:
		fmt.Printf("%s  %s\n", ts, ui.Fail(msg))
	default:
		fmt.Printf("%s  %s %s\n", ts, ui.Fainter.Render("·"), msg)
	}
}
