// sb-ui — Go backend for the Saltbox web UI.
//
// Single binary: serves the embedded React frontend (SPA) plus the HTTP + WS API,
// driving a Saltbox host locally or over SSH.
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"sb-ui/internal/api"
	"sb-ui/internal/buildinfo"
	"sb-ui/internal/config"
	"sb-ui/internal/customsets"
	"sb-ui/internal/docker"
	"sb-ui/internal/executor"
	"sb-ui/internal/jobs"
)

//go:embed all:web
var webFS embed.FS

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v" || os.Args[1] == "version") {
		fmt.Printf("sb-ui %s\n", buildinfo.Version)
		return
	}

	cfg := config.Load()
	config.Set(cfg)
	executor.Set(executor.Make(cfg))

	// Password management runs as a one-off command, on the host, over a terminal — never
	// through the web API, so a caller who is already inside cannot lock the owner out.
	if len(os.Args) > 1 && (os.Args[1] == "--set-password" || os.Args[1] == "--new-token") {
		runAuthCommand(os.Args[1])
		return
	}

	if cfg.IsRemote() {
		log.Printf("sb-ui → ssh %s@%s:%d", cfg.User, cfg.Host, cfg.Port)
	} else {
		log.Printf("sb-ui → local mode")
	}

	if cfg.Configured {
		jobs.LoadHistory()
		docker.LoadCache()
		customsets.Load()
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Authentication guards /api and /ws. It sits above the routes rather than inside Mount so
	// there is exactly one place that decides who gets in, and no route can be added later
	// that quietly sits outside it. The SPA itself stays reachable so the login page can load.
	api.SetTrustLoopback(os.Getenv(api.TrustLoopbackEnv) != "false")
	r.Use(api.RequireAuth)

	// API + WS routes
	api.Mount(r)

	// Static frontend + SPA fallback for everything else.
	dist, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed web: %v", err)
	}
	r.NotFound(spaHandler(dist))

	addr := os.Getenv("SB_UI_ADDR")
	if addr == "" {
		// Loopback by default; the deployed unit sets SB_UI_ADDR=:9180 to be reachable
		// through Traefik. Exposing the port is no longer the only thing standing between a
		// stranger and the API — RequireAuth above denies /api and /ws without a session or
		// token — but loopback remains the right default for a local run.
		addr = api.DefaultAddr
	}
	api.SetListenAddr(addr)    // so the UI can show the real webhook port
	go api.EnsureRoleCurrent() // keep our saltbox_mod role in step with this binary
	log.Printf("sb-ui %s listening on %s", buildinfo.Version, addr)
	log.Fatal(http.ListenAndServe(addr, r))
}

func spaHandler(dist fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(dist))
	index, _ := fs.ReadFile(dist, "index.html")
	return func(w http.ResponseWriter, req *http.Request) {
		// Unmatched API routes must 404 as JSON — never fall through to index.html,
		// or the client tries to JSON-parse HTML ("Unexpected token '<'").
		if strings.HasPrefix(req.URL.Path, "/api/") {
			http.Error(w, `{"error":"not found: `+req.URL.Path+`"}`, http.StatusNotFound)
			return
		}
		p := strings.TrimPrefix(req.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, statErr := fs.Stat(dist, p); statErr != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(index)
			return
		}
		fileServer.ServeHTTP(w, req)
	}
}
