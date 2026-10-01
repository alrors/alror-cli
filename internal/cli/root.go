// Package cli implements the `alror` command.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/manaskumar3003/alror-cli/internal/cli/ui"
)

// Version is set at build time with -ldflags "-X github.com/manaskumar3003/alror-cli/internal/cli.Version=…".
var Version = "0.1.0-dev"

type globals struct {
	dir     string
	json    bool
	noColor bool
	color   string
	noAnim  bool
	server  string // Alror workspace URL (overrides ALROR_SERVER, alror.yaml and credentials)
	apiKey  string // API key (overrides ALROR_API_KEY and credentials)
	local   bool   // force local mode even when a server is configured
}

var g globals

// Execute runs the CLI and returns the process exit code.
func Execute() int { return run(os.Args[1:]) }

// run executes one command line; tests call it directly.
func run(args []string) int {
	g, ws = globals{}, nil
	root := &cobra.Command{
		Use:   "alror",
		Short: "Ship every change at the speed of AI, safely",
		Long: "Alror scores the risk of each change, rolls it out in steps, verifies it\n" +
			"against live metrics and rolls it back on its own when it hurts users.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(*cobra.Command, []string) {
			switch {
			case g.noColor || g.color == "never" || os.Getenv("NO_COLOR") != "":
				lipgloss.SetColorProfile(termenv.Ascii)
			case g.color == "always":
				lipgloss.SetColorProfile(termenv.TrueColor) // e.g. CI logs that render ANSI
			}
			plain := g.noColor || g.color == "never" || os.Getenv("NO_COLOR") != ""
			ui.SetAnimated(ui.Interactive() && !g.json && !g.noAnim && !plain &&
				os.Getenv("CI") == "" && os.Getenv("ALROR_NO_ANIM") == "")
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ui.Reveal(ui.Banner(Version), 30*time.Millisecond)
			fmt.Println()
			fmt.Println(ui.Section("Get started"))
			fmt.Println(ui.Hint("alror init", "create alror.yaml in this repo"))
			fmt.Println(ui.Hint("alror risk", "score the current change"))
			fmt.Println(ui.Hint("alror deploy -s checkout-api -i app:v2", "run a verified rollout"))
			fmt.Println(ui.Hint("alror docs", "open the documentation"))
			fmt.Println()
			fmt.Println(ui.Fainter.Render("Run `alror --help` for every command."))
			return nil
		},
	}
	root.PersistentFlags().StringVarP(&g.dir, "dir", "C", ".", "run as if started in this directory")
	root.PersistentFlags().BoolVar(&g.json, "json", false, "machine-readable JSON output")
	root.PersistentFlags().BoolVar(&g.noColor, "no-color", false, "disable colours (also honours NO_COLOR)")
	root.PersistentFlags().StringVar(&g.color, "color", "auto", "colour output: auto | always | never")
	root.PersistentFlags().BoolVar(&g.noAnim, "no-anim", false, "disable terminal animations (also ALROR_NO_ANIM=1; off automatically in CI)")
	root.PersistentFlags().StringVar(&g.server, "server", "", "Alror workspace URL to connect to (default: ALROR_SERVER, alror.yaml server:, then saved login)")
	root.PersistentFlags().StringVar(&g.apiKey, "api-key", "", "workspace API key (default: ALROR_API_KEY, then saved login)")
	root.PersistentFlags().BoolVar(&g.local, "local", false, "force local mode: alror.yaml and .alror/ even when a server is configured")

	root.AddCommand(initCmd(), riskCmd(), deployCmd(), statusCmd(), rollbackCmd(), githubCmd(), doctorCmd(), docsCmd(), versionCmd(),
		loginCmd(), logoutCmd(), whoamiCmd(), runnerCmd(), configCmd())

	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		if errors.Is(err, errRolledBack) {
			return 2
		}
		if errors.Is(err, errRiskGate) {
			fmt.Fprintln(os.Stderr, ui.Fail(err.Error()))
			return 3
		}
		fmt.Fprintln(os.Stderr, ui.Fail(err.Error()))
		return 1
	}
	return 0
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(*cobra.Command, []string) {
			fmt.Println(ui.Logo(), ui.Dim.Render(Version))
		},
	}
}
