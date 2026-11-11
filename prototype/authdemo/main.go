// Command authdemo serves a one-page browser client for the kind prototype:
// get a Zitadel token for a test tenant, look inside it and call the API
// Gateway with it. Local use only.
//
// The page talks only to this server. It proxies /zitadel/* and /api/* to the
// public Gateway on 127.0.0.1:30080 with the right Host header, so the browser
// needs neither CORS nor DNS for *.localtest.me. /config hands the page the
// tenants written by prototype/bootstrap-tenants.sh, client secrets included.
package main

import (
	_ "embed"
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
)

//go:embed index.html
var page []byte

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "listen address; keep it on loopback, /config serves client secrets")
	gateway := flag.String("gateway", "127.0.0.1:30080", "public Gateway of the kind cluster")
	tenants := flag.String("tenants", "../.tenants.json", "file written by prototype/bootstrap-tenants.sh")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		// Read on every request, so a rerun of bootstrap-tenants.sh needs no restart.
		raw, err := os.ReadFile(*tenants)
		if err != nil {
			http.Error(w, err.Error()+" (run prototype/bootstrap-tenants.sh first)", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
	})
	mux.Handle("/zitadel/", http.StripPrefix("/zitadel", hostProxy(*gateway, "zitadel.localtest.me:30080")))
	mux.Handle("/api/", http.StripPrefix("/api", hostProxy(*gateway, "api.localtest.me:30080")))

	log.Printf("auth demo on http://%s (tenants from %s)", *addr, *tenants)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// hostProxy forwards to the Gateway at addr with Host set to host, which is
// what the Gateway routes on and what Zitadel puts into the token's issuer.
func hostProxy(addr, host string) http.Handler {
	target := &url.URL{Scheme: "http", Host: addr}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = host
		},
	}
}
