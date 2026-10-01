// Command alror-devserver serves an in-memory fake of the Alror workspace API
// (internal/api/apitest) for local demos and CLI development. State is lost
// on exit; it is not a real Alror workspace.
//
//	alror-devserver -addr 127.0.0.1:3100
//	alror login --server http://127.0.0.1:3100 --key alr_live_TestKey0123456789abcdefghijklmnop
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/manaskumar3003/alror-cli/internal/api/apitest"
	"github.com/manaskumar3003/alror-cli/internal/config"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:3100", "listen address")
	key := flag.String("key", apitest.DefaultKey, "API key to accept (all scopes)")
	seed := flag.String("config", "", "seed services and policy from this alror.yaml directory")
	flag.Parse()

	f := apitest.New()
	if *key != apitest.DefaultKey {
		f.AddKey(*key, "dev key", apitest.AllScopes...)
	}
	if *seed != "" {
		cfg, err := config.Load(*seed)
		if err != nil {
			log.Fatal(err)
		}
		f.SetConfig(cfg)
	}
	fmt.Fprintf(os.Stderr, "alror-devserver (in-memory fake Alror workspace) on http://%s/api/v1\n", *addr)
	fmt.Fprintf(os.Stderr, "  alror login --server http://%s --key %s\n", *addr, *key)
	log.Fatal(http.ListenAndServe(*addr, f))
}
