package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/config"
	"github.com/manaskumar3003/alror-cli/internal/credentials"
	"github.com/manaskumar3003/alror-cli/internal/store"
)

// workspace is what project() opened: the effective config, the store and,
// in connected mode, the workspace connection.
type workspace struct {
	cfg  *config.Config
	st   store.Store
	conn *connection // nil in local mode
}

type connection struct {
	client *api.Client
	res    credentials.Resolved
}

// ws is the workspace opened by the last project() call.
var ws *workspace

// stateLabel describes where state lives, for headers: ".alror/" or "acme workspace (http://…)".
func (w *workspace) stateLabel() string {
	if w == nil || w.conn == nil {
		return ".alror/"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if who, err := w.conn.client.Whoami(ctx); err == nil && who.Org.Slug != "" {
		return who.Org.Slug + " workspace (" + w.conn.res.Server + ")"
	}
	return "workspace (" + w.conn.res.Server + ")"
}

// configSource names where services and policy came from, for error messages.
func (w *workspace) configSource() string {
	if w == nil || w.conn == nil {
		return "alror.yaml"
	}
	return "the workspace config at " + w.conn.res.Server
}

// resolve finds the local alror.yaml (optional) and the server and key to use.
func resolve() (credentials.Resolved, *config.Config, error) {
	local, err := config.Read(g.dir)
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		return credentials.Resolved{}, nil, err
	}
	if g.local {
		return credentials.Resolved{}, local, nil
	}
	in := credentials.Inputs{FlagServer: g.server, FlagKey: g.apiKey}
	if local != nil {
		in.YAMLServer = local.Server
	}
	if path, perr := credentials.Path(); perr == nil {
		file, ferr := credentials.Load(path)
		if ferr != nil {
			return credentials.Resolved{}, nil, fmt.Errorf("read credentials: %w", ferr)
		}
		in.File = file
	}
	return credentials.Resolve(in), local, nil
}

// clientSource is sent as X-Alror-Source so the server can record where a write came from.
func clientSource() string {
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		return "ci"
	}
	return "cli"
}

func newClient(res credentials.Resolved, source string) *api.Client {
	return api.New(res.Server, res.Key,
		api.WithUserAgent("alror-cli/"+Version),
		api.WithHeader("X-Alror-Source", source))
}

// errNoKey explains how to get a key for the resolved server.
func errNoKey(res credentials.Resolved) error {
	msg := fmt.Sprintf("no API key for %s (server from %s)", res.Server, res.ServerFrom)
	if res.FileServerMismatch {
		msg += fmt.Sprintf("; your saved login is for %s", res.FileServer)
	}
	return fmt.Errorf("%s: run `alror login --server %s`, set ALROR_API_KEY, or pass --local", msg, res.Server)
}

// explainAPIError turns client errors into actionable CLI messages.
func explainAPIError(server string, err error) error {
	switch {
	case errors.Is(err, api.ErrUnauthorized):
		return fmt.Errorf("%s rejected the API key: run `alror login --server %s` again", server, server)
	case errors.Is(err, api.ErrForbidden):
		return fmt.Errorf("the API key is not allowed to do this (%v)", err)
	case api.IsTransient(err):
		return fmt.Errorf("%v (use --local to work offline)", err)
	}
	return err
}

// project opens the workspace: connected mode when a server and key resolve
// (config from GET /config, store.Remote), otherwise alror.yaml and .alror/.
func project() (*config.Config, store.Store, error) {
	res, local, err := resolve()
	if err != nil {
		return nil, nil, err
	}
	if res.Server == "" {
		if local == nil {
			return nil, nil, config.ErrNotFound
		}
		if err := local.Validate(); err != nil {
			return nil, nil, err
		}
		st, err := store.Open(local.StateDir())
		if err != nil {
			return nil, nil, fmt.Errorf("open state dir: %w", err)
		}
		ws = &workspace{cfg: local, st: st}
		return local, st, nil
	}
	if res.Key == "" {
		return nil, nil, errNoKey(res)
	}

	client := newClient(res, clientSource())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg, err := client.Config(ctx)
	if err != nil {
		return nil, nil, explainAPIError(res.Server, err)
	}
	if err := mergeWorkspaceConfig(cfg, local, res.Server); err != nil {
		return nil, nil, err
	}
	st := store.NewRemote(client)
	ws = &workspace{cfg: cfg, st: st, conn: &connection{client: client, res: res}}
	return cfg, st, nil
}

// mergeWorkspaceConfig completes a config fetched from the server: the server
// wins on everything except services[].paths, which a local alror.yaml may
// still provide for mapping git paths to services.
func mergeWorkspaceConfig(cfg, local *config.Config, server string) error {
	cfg.Server = server
	if local != nil {
		cfg.Dir = local.Dir
		for i, s := range cfg.Services {
			if l, ok := local.Service(s.Name); ok && len(l.Paths) > 0 {
				cfg.Services[i].Paths = l.Paths
			}
		}
	} else {
		dir, err := filepath.Abs(g.dir)
		if err != nil {
			return err
		}
		cfg.Dir = dir
	}
	cfg.ApplyDefaults()
	if len(cfg.Services) == 0 {
		return fmt.Errorf("%s has no services yet: add one in the console or run `alror config push`", server)
	}
	return cfg.Validate()
}

// remoteClient returns a client for commands that only make sense against a
// Alror workspace (whoami, runner, config pull/push).
func connectedClient(source string) (*api.Client, credentials.Resolved, *config.Config, error) {
	if g.local {
		return nil, credentials.Resolved{}, nil, errors.New("this command needs a connected Alror workspace; drop --local")
	}
	res, local, err := resolve()
	if err != nil {
		return nil, res, nil, err
	}
	if res.Server == "" {
		return nil, res, local, errors.New("not connected to an Alror workspace: run `alror login --server URL --key KEY` or set ALROR_SERVER and ALROR_API_KEY")
	}
	if res.Key == "" {
		return nil, res, local, errNoKey(res)
	}
	return newClient(res, source), res, local, nil
}
