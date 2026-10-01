package credentials

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPrecedence(t *testing.T) {
	file := &Credentials{Server: "http://file:3000", APIKey: "alr_live_file"}
	cases := []struct {
		name                string
		in                  Inputs
		server, key         string
		serverFrom, keyFrom Source
	}{
		{"nothing", Inputs{Getenv: env(nil)}, "", "", FromNone, FromNone},
		{"file only", Inputs{Getenv: env(nil), File: file}, "http://file:3000", "alr_live_file", FromFile, FromFile},
		{"yaml server matches file", Inputs{Getenv: env(nil), YAMLServer: "http://file:3000/", File: file}, "http://file:3000", "alr_live_file", FromYAML, FromFile},
		{"yaml beats file", Inputs{Getenv: env(nil), YAMLServer: "http://yaml:3000", File: file}, "http://yaml:3000", "", FromYAML, FromNone},
		{"env beats yaml", Inputs{Getenv: env(map[string]string{"ALROR_SERVER": "http://env:3000", "ALROR_API_KEY": "alr_live_env"}), YAMLServer: "http://yaml:3000", File: file},
			"http://env:3000", "alr_live_env", FromEnv, FromEnv},
		{"flags beat env", Inputs{FlagServer: "http://flag:3000", FlagKey: "alr_live_flag", Getenv: env(map[string]string{"ALROR_SERVER": "http://env:3000", "ALROR_API_KEY": "alr_live_env"}), File: file},
			"http://flag:3000", "alr_live_flag", FromFlag, FromFlag},
		{"env key with file server", Inputs{Getenv: env(map[string]string{"ALROR_API_KEY": "alr_live_env"}), File: file}, "http://file:3000", "alr_live_env", FromFile, FromEnv},
		{"key without server", Inputs{Getenv: env(map[string]string{"ALROR_API_KEY": "k"})}, "", "k", FromNone, FromEnv},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Resolve(c.in)
			if r.Server != c.server || r.Key != c.key || r.ServerFrom != c.serverFrom || r.KeyFrom != c.keyFrom {
				t.Fatalf("got server=%q(%s) key=%q(%s), want %q(%s) %q(%s)", r.Server, r.ServerFrom, r.Key, r.KeyFrom, c.server, c.serverFrom, c.key, c.keyFrom)
			}
		})
	}
	if r := Resolve(Inputs{Getenv: env(nil), YAMLServer: "http://yaml:3000", File: file}); !r.FileServerMismatch || r.Remote() {
		t.Fatalf("file key must not be sent to another server: %+v", r)
	}
}

func TestSaveLoadRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alror", "credentials.json")
	if c, err := Load(path); err != nil || c != nil {
		t.Fatalf("missing file: %v %v", c, err)
	}
	if err := Save(path, Credentials{Server: "http://x", APIKey: "alr_live_k"}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil || c.Server != "http://x" || c.APIKey != "alr_live_k" {
		t.Fatalf("load: %+v %v", c, err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, want 0600", st.Mode().Perm())
		}
	}
	if ok, err := Remove(path); !ok || err != nil {
		t.Fatalf("remove: %v %v", ok, err)
	}
	if ok, err := Remove(path); ok || err != nil {
		t.Fatalf("second remove: %v %v", ok, err)
	}
}

func TestPathOverride(t *testing.T) {
	t.Setenv("ALROR_CREDENTIALS", "/tmp/x.json")
	if p, _ := Path(); p != "/tmp/x.json" {
		t.Fatalf("path = %s", p)
	}
}
