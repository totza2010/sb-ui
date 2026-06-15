# sb-ui

Single-binary Go backend for the Saltbox web UI. Embeds the React frontend
(source in `frontend/`) and serves an HTTP + WebSocket API, all in one binary.

This is a ground-up Go port of the Python `saltbox-ui` backend — see
[`../saltbox-ui/GO_MIGRATION_PLAN.md`](../saltbox-ui/GO_MIGRATION_PLAN.md). The
Python backend is kept as a reference for now; the frontend source now lives
in-repo under `frontend/`.

## Status

Full Go port of the Python backend (Phases 0–7 ✅). All HTTP + WS endpoints match
the Python contract; verified against a real Saltbox host over SSH.

## Install (Saltbox host)

```bash
curl -fsSL https://raw.githubusercontent.com/saltyorg/saltbox/master/sb-ui/install.sh | sudo bash
```

Downloads the latest release binary to `/opt/saltbox-ui`, installs a systemd
service, and starts it on `:8000`. Open the UI and run the setup wizard.

```bash
journalctl -u sb-ui -f      # logs
systemctl status sb-ui      # status
```

## Build

```bash
./build.sh                 # linux/amd64 (Saltbox target)
./build.sh linux arm64     # arm release
./build.sh windows amd64   # dev build
VERSION=v0.7.0 ./build.sh  # bake an explicit version
```

Produces `./sb-ui` (or `sb-ui.exe`) with the frontend embedded and the version
baked in (`-X sb-ui/internal/buildinfo.Version`). A bare `go build` also works
(embeds whatever is in `web/`) — run `build.sh` to bundle a fresh UI.

## Run

```bash
SB_UI_ADDR=:8000 ./sb-ui   # default :8000
./sb-ui --version
```

## Frontend dev

```bash
cd frontend
npm install
npm run dev        # Vite dev server (proxy /api → running sb-ui)
```

`build.sh` runs `npm run build` and copies `frontend/dist` → `web/` for embedding.

## Release

Tag `vX.Y.Z` (or run the workflow manually) to build + publish the linux
amd64/arm64 binaries + checksums. CI: `.github/workflows/release.yml`.

## Layout

```
main.go            # server + embed + SPA fallback + --version
internal/…         # Go port of the backend (executor, jobs, apps, …)
frontend/          # React source (Vite); dev here
web/               # embedded frontend build (generated; gitignored)
build.sh           # frontend build + copy + versioned go build
install.sh         # one-liner host installer (binary + systemd)
sb-ui.service      # systemd unit template
```
