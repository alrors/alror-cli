package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/cli/ui"
	"github.com/manaskumar3003/alror-cli/internal/credentials"
)

func loginCmd() *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Connect to an Alror workspace: save its URL and an API key",
		Long: "Verifies the key with GET /whoami and saves the server and key to the\n" +
			"credentials file (mode 0600). Prompts for missing values on a terminal.",
		Example: "  alror login --server http://localhost:3000 --key alr_live_…",
		RunE: func(*cobra.Command, []string) error {
			server := g.server
			if key == "" {
				key = g.apiKey
			}
			tty := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
			in := bufio.NewReader(os.Stdin)
			if server == "" {
				def := os.Getenv("ALROR_SERVER")
				if def == "" {
					if res, _, err := resolve(); err == nil {
						def = res.Server
					}
				}
				if !tty {
					if def == "" {
						return errors.New("--server is required (no terminal to prompt on)")
					}
					server = def
				} else {
					server = prompt(in, "Alror workspace URL", def)
				}
			}
			if key == "" {
				key = os.Getenv("ALROR_API_KEY")
			}
			if key == "" {
				if !tty {
					return errors.New("--key is required (no terminal to prompt on)")
				}
				key = promptSecret(in, "API key")
			}
			server, key = api.NormalizeServer(server), strings.TrimSpace(key)
			if server == "" || key == "" {
				return errors.New("a server URL and an API key are both required")
			}
			if !strings.HasPrefix(key, "alr_live_") {
				fmt.Fprintln(os.Stderr, ui.WarnLine("API keys usually start with alr_live_; trying anyway"))
			}

			client := newClient(credentials.Resolved{Server: server, Key: key}, clientSource())
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			spin := ui.Spin("Verifying the key with " + server)
			who, err := client.Whoami(ctx)
			spin.Stop("")
			if err != nil {
				return explainAPIError(server, err)
			}
			path, err := credentials.Path()
			if err != nil {
				return err
			}
			if err := credentials.Save(path, credentials.Credentials{Server: server, APIKey: key}); err != nil {
				return fmt.Errorf("save credentials: %w", err)
			}
			if g.json {
				return printJSON(map[string]any{"server": server, "whoami": who, "credentials": path})
			}
			fmt.Println(ui.OK(fmt.Sprintf("Logged in to %s %s as %s",
				ui.Bold.Render(who.Org.Name), ui.Dim.Render("("+who.Org.Slug+")"), ui.Bold.Render(actorLabel(who)))))
			fmt.Println(renderWhoami(server, who, key, path))
			fmt.Println()
			fmt.Println(ui.Hint("alror status", "list deployments in the workspace"))
			fmt.Println(ui.Hint("alror runner", "run deploys and rollbacks queued from the console"))
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "API key (alr_live_…); prompted for when missing")
	return cmd
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Disconnect: remove the saved workspace URL and API key",
		RunE: func(*cobra.Command, []string) error {
			path, err := credentials.Path()
			if err != nil {
				return err
			}
			existed, err := credentials.Remove(path)
			if err != nil {
				return err
			}
			if existed {
				fmt.Println(ui.OK("Logged out " + ui.Fainter.Render("removed "+path)))
			} else {
				fmt.Println(ui.Dim.Render("Not logged in (no " + path + ")."))
			}
			if os.Getenv("ALROR_API_KEY") != "" {
				fmt.Println(ui.WarnLine("ALROR_API_KEY is still set in this environment"))
			}
			return nil
		},
	}
}

func whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the connected workspace, actor and API key",
		RunE: func(*cobra.Command, []string) error {
			client, res, _, err := connectedClient(clientSource())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			who, err := client.Whoami(ctx)
			if err != nil {
				return explainAPIError(res.Server, err)
			}
			if g.json {
				return printJSON(map[string]any{"server": res.Server, "server_from": res.ServerFrom, "key_from": res.KeyFrom, "whoami": who})
			}
			fmt.Println(renderWhoami(res.Server, who, res.Key, ""))
			fmt.Println(ui.Fainter.Render(fmt.Sprintf("server from %s · key from %s", res.ServerFrom, res.KeyFrom)))
			return nil
		},
	}
}

func actorLabel(w *api.Whoami) string {
	if w.Actor.Label != "" {
		return w.Actor.Label
	}
	return w.Actor.Type + " " + w.Actor.ID
}

func renderWhoami(server string, w *api.Whoami, key, saved string) string {
	scopes := strings.Join(w.Scopes, ", ")
	if scopes == "" {
		scopes = "none"
	}
	rows := [][]string{
		{"server", server},
		{"org", w.Org.Name + ui.Dim.Render(" ("+w.Org.Slug+")")},
		{"actor", actorLabel(w) + ui.Dim.Render(" · "+w.Actor.Type)},
		{"key", keyPrefix(key)},
		{"scopes", scopes},
	}
	if saved != "" {
		rows = append(rows, []string{"saved", ui.Dim.Render(saved)})
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "  %s %s\n", ui.Fainter.Render(fmt.Sprintf("%-7s", r[0])), r[1])
	}
	return strings.TrimRight(b.String(), "\n")
}

// keyPrefix shows the first 12 characters, as the console does.
func keyPrefix(key string) string {
	if len(key) <= 12 {
		return strings.Repeat("•", len(key))
	}
	return key[:12] + "…"
}

func prompt(in *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Printf("%s %s: ", ui.Bold.Render(label), ui.Fainter.Render("["+def+"]"))
	} else {
		fmt.Printf("%s: ", ui.Bold.Render(label))
	}
	line, _ := in.ReadString('\n')
	if line = strings.TrimSpace(line); line == "" {
		return def
	}
	return line
}

func promptSecret(in *bufio.Reader, label string) string {
	fmt.Printf("%s: ", ui.Bold.Render(label))
	if raw, err := term.ReadPassword(int(os.Stdin.Fd())); err == nil {
		fmt.Println()
		return strings.TrimSpace(string(raw))
	}
	line, _ := in.ReadString('\n')
	return strings.TrimSpace(line)
}
