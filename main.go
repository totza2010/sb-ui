// sb-ui — Go backend for the Saltbox web UI.
//
// Single binary: serves the embedded React frontend (SPA) plus the HTTP + WS API.
// The API contract matches the Python backend so the frontend runs unchanged.
// See ../saltbox-ui/GO_MIGRATION_PLAN.md.
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
		addr = ":8000"
	}
	log.Printf("sb-ui %s listening on %s", buildinfo.Version, addr)
	log.Fatal(http.ListenAndServe(addr, r))
}

func spaHandler(dist fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(dist))
	index, _ := fs.ReadFile(dist, "index.html")
	return func(w http.ResponseWriter, req *http.Request) {
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
