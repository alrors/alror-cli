package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/manaskumar3003/alror-cli/internal/cli/ui"
	"github.com/manaskumar3003/alror-cli/internal/config"
)

func initCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create alror.yaml and the .alror state directory",
		RunE: func(*cobra.Command, []string) error {
			dir, err := filepath.Abs(g.dir)
			if err != nil {
				return err
			}
			path := filepath.Join(dir, config.FileName)
			if _, err := os.Stat(path); err == nil && !force {
				return errors.New("alror.yaml already exists (use --force to overwrite)")
			}

			cfg := config.Default(filepath.Base(dir))
			cfg.Policy.BakeScale = 0.003 // simulated targets: a 10-minute bake runs in ~2 s
			if err := cfg.Save(path); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Join(dir, ".alror"), 0o755); err != nil {
				return err
			}
			ignored := ensureGitignore(dir)

			ui.Reveal(ui.BigName(), 40*time.Millisecond)
			fmt.Println()

			fmt.Println(ui.OK("Created " + ui.Bold.Render(config.FileName) + ui.Dim.Render(" for project "+cfg.Project)))
			fmt.Println(ui.OK("Created " + ui.Bold.Render(".alror/") + ui.Dim.Render(" (deployments and event logs)")))
			if ignored {
				fmt.Println(ui.OK("Added .alror/ to .gitignore"))
			}
			fmt.Println()
			fmt.Println(ui.Section("Next"))
			fmt.Println(ui.Hint("edit alror.yaml", "map your services to their source paths"))
			fmt.Println(ui.Hint("alror risk", "score your current change"))
			fmt.Println(ui.Hint("alror deploy -s checkout-api -i app:v2", "try a simulated rollout"))
			fmt.Println()
			fmt.Println(ui.Fainter.Render("bake_scale is 0.003 for the simulated target. Remove it before using real targets."))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing alror.yaml")
	return cmd
}

func ensureGitignore(dir string) bool {
	path := filepath.Join(dir, ".gitignore")
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), ".alror") {
		return false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()
	prefix := ""
	if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + ".alror/\n")
	return err == nil
}
