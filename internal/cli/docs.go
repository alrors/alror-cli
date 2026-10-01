package cli

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/manaskumar3003/alror-cli/internal/cli/ui"
	"github.com/manaskumar3003/alror-cli/internal/docsite"
)

func docsCmd() *cobra.Command {
	var addr string
	var open bool
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Serve the documentation locally (bundled in the binary, works offline)",
		RunE: func(*cobra.Command, []string) error {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			url := "http://" + ln.Addr().String()
			fmt.Println(ui.OK("Docs at " + ui.Code.Render(url) + ui.Fainter.Render("  (Ctrl-C to stop)")))
			if open {
				openBrowser(url)
			}
			return http.Serve(ln, docsite.Handler())
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:4100", "address to listen on")
	cmd.Flags().BoolVar(&open, "open", false, "open the docs in a browser")
	return cmd
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		c = exec.Command("open", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	_ = c.Start()
}
