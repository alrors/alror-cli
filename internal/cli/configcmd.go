package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/cli/ui"
	"github.com/manaskumar3003/alror-cli/internal/config"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Sync alror.yaml with the connected Alror workspace",
	}
	cmd.AddCommand(configPullCmd(), configPushCmd())
	return cmd
}

func configPullCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Write alror.yaml from the workspace config (keeps the server: line)",
		RunE: func(*cobra.Command, []string) error {
			client, res, local, err := connectedClient(clientSource())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			remote, err := client.Config(ctx)
			if err != nil {
				return explainAPIError(res.Server, err)
			}
			remote.ApplyDefaults()

			path := ""
			old := &config.Config{}
			if local != nil {
				path, old = local.Path(), local
				remote.Server = local.Server // preserve the server: line
			} else {
				dir, err := filepath.Abs(g.dir)
				if err != nil {
					return err
				}
				path = filepath.Join(dir, config.FileName)
			}
			changes := diffConfig(old, remote, true)
			if g.json {
				if !dryRun {
					if err := remote.Save(path); err != nil {
						return err
					}
				}
				return printJSON(map[string]any{"path": path, "changes": changes, "written": !dryRun, "config": remote})
			}
			printChanges("alror.yaml ← "+res.Server, changes)
			if dryRun {
				fmt.Println(ui.Dim.Render("Dry run: nothing written."))
				return nil
			}
			if err := remote.Save(path); err != nil {
				return err
			}
			fmt.Println(ui.OK(fmt.Sprintf("Wrote %s %s", ui.Bold.Render(path), ui.Dim.Render(fmt.Sprintf("(%d services)", len(remote.Services))))))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without writing")
	return cmd
}

func configPushCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Upload services and policy from alror.yaml (admin keys only)",
		Long: "Upserts every service in alror.yaml by name and replaces the org policy with\n" +
			"PUT /config. Services that exist only on the server are kept; metrics and\n" +
			"notification settings are left as they are. Needs the config:write scope.",
		RunE: func(*cobra.Command, []string) error {
			client, res, local, err := connectedClient(clientSource())
			if err != nil {
				return err
			}
			if local == nil {
				return config.ErrNotFound
			}
			if err := local.Validate(); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			current, err := client.Config(ctx)
			if err != nil {
				return explainAPIError(res.Server, err)
			}
			current.ApplyDefaults()

			body := &config.Config{
				Project:  current.Project,
				Services: local.Services,
				Policy:   local.Policy,
				Metrics:  current.Metrics,
				Notify:   current.Notify,
			}
			if body.Project == "" {
				body.Project = local.Project
			}
			changes := diffConfig(current, body, false)
			if g.json && dryRun {
				return printJSON(map[string]any{"changes": changes, "pushed": false})
			}
			if !g.json {
				printChanges(res.Server+" ← alror.yaml", changes)
			}
			if dryRun {
				fmt.Println(ui.Dim.Render("Dry run: nothing pushed."))
				return nil
			}
			if !hasChanges(changes) {
				if g.json {
					return printJSON(map[string]any{"changes": changes, "pushed": false})
				}
				fmt.Println(ui.OK("Already up to date"))
				return nil
			}
			out, err := client.PutConfig(ctx, body)
			if errors.Is(err, api.ErrForbidden) {
				return fmt.Errorf("config push needs an admin key with the config:write scope (%v)", err)
			}
			if err != nil {
				return explainAPIError(res.Server, err)
			}
			if g.json {
				return printJSON(map[string]any{"changes": changes, "pushed": true, "config": out})
			}
			fmt.Println(ui.OK(fmt.Sprintf("Pushed %d services and the policy to %s", len(local.Services), res.Server)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without pushing")
	return cmd
}

// change is one line of a config diff: op is + (added), - (removed),
// ~ (changed) or = (kept as is).
type change struct {
	Op     string `json:"op"`
	What   string `json:"what"`
	Detail string `json:"detail,omitempty"`
}

func hasChanges(cs []change) bool {
	for _, c := range cs {
		if c.Op != "=" {
			return true
		}
	}
	return false
}

// diffConfig compares services and policy. With prune, services missing from
// next are removed (pull rewrites the file); without, they are kept (push upserts).
func diffConfig(prev, next *config.Config, prune bool) []change {
	var out []change
	for _, s := range next.Services {
		old, ok := prev.Service(s.Name)
		if !ok {
			out = append(out, change{"+", "service " + s.Name, s.Target})
			continue
		}
		if d := serviceDiff(old, s); d != "" {
			out = append(out, change{"~", "service " + s.Name, d})
		}
	}
	for _, s := range prev.Services {
		if _, ok := next.Service(s.Name); ok {
			continue
		}
		if prune {
			out = append(out, change{"-", "service " + s.Name, ""})
		} else {
			out = append(out, change{"=", "service " + s.Name, "only on the server, kept"})
		}
	}
	out = append(out, policyDiff(prev.Policy, next.Policy)...)
	if prune {
		if prev.Metrics.Provider != next.Metrics.Provider || prev.Metrics.URL != next.Metrics.URL {
			out = append(out, change{"~", "metrics", fmt.Sprintf("%s → %s", metricsLabel(prev.Metrics), metricsLabel(next.Metrics))})
		}
		if (prev.Notify.SlackWebhook == "") != (next.Notify.SlackWebhook == "") {
			out = append(out, change{"~", "notify.slack_webhook", setLabel(prev.Notify.SlackWebhook) + " → " + setLabel(next.Notify.SlackWebhook)})
		}
	}
	return out
}

func serviceDiff(a, b config.Service) string {
	var parts []string
	if !slices.Equal(a.Paths, b.Paths) {
		parts = append(parts, fmt.Sprintf("paths %v → %v", a.Paths, b.Paths))
	}
	if a.Target != b.Target {
		parts = append(parts, fmt.Sprintf("target %s → %s", a.Target, b.Target))
	}
	if a.Cluster != b.Cluster {
		parts = append(parts, fmt.Sprintf("cluster %q → %q", a.Cluster, b.Cluster))
	}
	if a.Namespace != b.Namespace {
		parts = append(parts, fmt.Sprintf("namespace %q → %q", a.Namespace, b.Namespace))
	}
	if a.Critical != b.Critical {
		parts = append(parts, fmt.Sprintf("critical %t → %t", a.Critical, b.Critical))
	}
	return strings.Join(parts, ", ")
}

func policyDiff(a, b config.Policy) []change {
	var out []change
	keys := map[string]bool{}
	for k := range a.MaxRegression {
		keys[k] = true
	}
	for k := range b.MaxRegression {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		av, aok := a.MaxRegression[k]
		bv, bok := b.MaxRegression[k]
		switch {
		case !aok:
			out = append(out, change{"+", "policy.max_regression." + k, fmt.Sprint(bv)})
		case !bok:
			out = append(out, change{"-", "policy.max_regression." + k, fmt.Sprint(av)})
		case av != bv:
			out = append(out, change{"~", "policy.max_regression." + k, fmt.Sprintf("%v → %v", av, bv)})
		}
	}
	if a.Alpha != b.Alpha {
		out = append(out, change{"~", "policy.alpha", fmt.Sprintf("%v → %v", a.Alpha, b.Alpha)})
	}
	if a.AutoRollback != b.AutoRollback {
		out = append(out, change{"~", "policy.auto_rollback", fmt.Sprintf("%t → %t", a.AutoRollback, b.AutoRollback)})
	}
	if a.BakeScale != b.BakeScale {
		out = append(out, change{"~", "policy.bake_scale", fmt.Sprintf("%v → %v", a.BakeScale, b.BakeScale)})
	}
	return out
}

func metricsLabel(m config.Metrics) string {
	if m.URL != "" {
		return m.Provider + " (" + m.URL + ")"
	}
	return m.Provider
}

func setLabel(s string) string {
	if s == "" {
		return "unset"
	}
	return "set"
}

func printChanges(title string, cs []change) {
	fmt.Println(ui.Section("Config changes") + "  " + ui.Fainter.Render(title))
	fmt.Println()
	if !hasChanges(cs) {
		fmt.Println(ui.Dim.Render("  no changes"))
	}
	for _, c := range cs {
		var op string
		switch c.Op {
		case "+":
			op = ui.Green.Render("+")
		case "-":
			op = ui.Red.Render("-")
		case "~":
			op = ui.Amber.Render("~")
		default:
			op = ui.Fainter.Render("=")
		}
		line := "  " + op + " " + c.What
		if c.Detail != "" {
			line += "  " + ui.Dim.Render(c.Detail)
		}
		fmt.Println(line)
	}
	fmt.Println()
}
