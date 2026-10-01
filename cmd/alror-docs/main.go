// Command alror-docs serves the Alror documentation (embedded, works offline).
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/manaskumar3003/alror-cli/internal/docsite"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4100", "address to listen on")
	flag.Parse()
	log.Printf("alror docs on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, docsite.Handler()))
}
