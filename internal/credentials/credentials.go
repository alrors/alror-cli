// Package credentials stores the Alror workspace server and API key for the
// current user and resolves which server and key a command should use.
//
// The file lives at os.UserConfigDir()/alror/credentials.json (for example
// ~/.config/alror/credentials.json or %APPDATA%\alror\credentials.json) with
// mode 0600. ALROR_CREDENTIALS overrides the path.
//
// Precedence, highest first:
//
//	server: --server flag > ALROR_SERVER > alror.yaml `server:` > credentials file
//	key:    --api-key flag > ALROR_API_KEY > credentials file
//
// A key from the credentials file is only sent to the server it was saved
// for, so pointing alror.yaml at another server never leaks the key there.
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manaskumar3003/alror-cli/internal/api"
)

// Credentials is what `alror login` saves.
type Credentials struct {
	Server string `json:"server"`
	APIKey string `json:"api_key"`
}

// Path returns where credentials are stored.
func Path() (string, error) {
	if p := os.Getenv("ALROR_CREDENTIALS"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, "alror", "credentials.json"), nil
}

// Load reads the credentials file. A missing file returns (nil, nil).
func Load(path string) (*Credentials, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Save writes the credentials file with mode 0600, creating its directory.
func Save(path string, c Credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// Remove deletes the credentials file. It reports whether a file existed.
func Remove(path string) (bool, error) {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Source names where a resolved value came from.
type Source string

const (
	FromNone  Source = ""
	FromFlag  Source = "flag"
	FromEnv   Source = "env"
	FromYAML  Source = "alror.yaml"
	FromFile  Source = "credentials file"
	envServer        = "ALROR_SERVER"
	envKey           = "ALROR_API_KEY"
)

// Inputs are every place a server or key can come from.
type Inputs struct {
	FlagServer, FlagKey string
	Getenv              func(string) string // nil = os.Getenv
	YAMLServer          string
	File                *Credentials
}

// Resolved is the server and key a command should use.
type Resolved struct {
	Server, Key         string
	ServerFrom, KeyFrom Source
	FileServerMismatch  bool // the credentials file holds a key for a different server
	FileServer          string
}

// Remote reports whether both a server and a key are available.
func (r Resolved) Remote() bool { return r.Server != "" && r.Key != "" }

// Resolve applies the precedence rules.
func Resolve(in Inputs) Resolved {
	getenv := in.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var r Resolved
	file := in.File
	if file == nil {
		file = &Credentials{}
	}
	fileServer := api.NormalizeServer(file.Server)

	switch {
	case strings.TrimSpace(in.FlagServer) != "":
		r.Server, r.ServerFrom = in.FlagServer, FromFlag
	case strings.TrimSpace(getenv(envServer)) != "":
		r.Server, r.ServerFrom = getenv(envServer), FromEnv
	case strings.TrimSpace(in.YAMLServer) != "":
		r.Server, r.ServerFrom = in.YAMLServer, FromYAML
	case fileServer != "":
		r.Server, r.ServerFrom = fileServer, FromFile
	}
	r.Server = api.NormalizeServer(r.Server)

	switch {
	case strings.TrimSpace(in.FlagKey) != "":
		r.Key, r.KeyFrom = strings.TrimSpace(in.FlagKey), FromFlag
	case strings.TrimSpace(getenv(envKey)) != "":
		r.Key, r.KeyFrom = strings.TrimSpace(getenv(envKey)), FromEnv
	case file.APIKey != "" && r.Server != "":
		if fileServer == r.Server {
			r.Key, r.KeyFrom = file.APIKey, FromFile
		} else {
			r.FileServerMismatch, r.FileServer = true, fileServer
		}
	}
	return r
}
