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

## Install

Two paths — pick one.

### A. Standalone (any host)

```bash
curl -fsSL https://raw.githubusercontent.com/totza2010/sb-ui/master/install.sh | sudo bash
```

Binary → `/opt/saltbox-ui`, raw systemd unit, listens on `:8000`. No Traefik /
SSO / DNS. Open `http://host:8000` and run the setup wizard.

```bash
journalctl -u sb-ui -f      # logs
systemctl status sb-ui      # status
```

### B. Saltbox-native (mod role) — recommended on a Saltbox host

Installs sb-ui as a [saltbox_mod](https://github.com/saltyorg/saltbox_mod) role
(modelled on the autoplow role): binary → `/srv/binaries/sbui`, with a Traefik
subdomain + Authelia SSO + DNS + `saltbox_managed_sbui` systemd unit. Run as
**your Saltbox user** (not root):

```bash
curl -fsSL https://raw.githubusercontent.com/totza2010/sb-ui/master/bootstrap-mod.sh | bash
```

It ensures `saltbox_mod` is installed, drops the versioned role tarball into
`/opt/saltbox_mod/roles/sbui`, registers it in `saltbox_mod.yml`, and runs
`sb install mod-sbui`. Reachable at `https://sbui.<your-domain>`.

### Updating

- **In-UI** — the sidebar shows an "Update to vX.Y.Z" button when a newer
  release exists; it swaps the binary in place and restarts (works for both A
  and B).
- **`sb install mod-sbui`** (path B) — re-pulls the latest binary and re-applies
  Traefik/DNS/systemd.
- **Re-run `bootstrap-mod.sh`** (path B) — when the *role itself* changes
  (tasks/defaults), to fetch the new role tarball, then `sb install mod-sbui`.

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

## Dev (hot reload)

No more rebuilding the `.exe` by hand. Two watchers run together:

- **Vite** serves the React app with HMR on `http://localhost:5173` and proxies
  `/api` + `/ws` to the Go backend on `:8000`.
- **[air](https://github.com/air-verse/air)** rebuilds + restarts the Go backend
  (~1s) on any `.go` change. The backend reads `.env` for the SSH/local
  connection, same as the built binary.

One command (Windows) starts both — open `http://localhost:5173`:

```powershell
./dev.ps1
```

Or run them in two terminals:

```bash
air                       # Go backend, hot-reload, :8000   (go install github.com/air-verse/air@latest)
cd frontend && npm run dev # Vite + HMR, :5173
```

For a one-off production-style bundle, `build.sh` runs `npm run build` and copies
`frontend/dist` → `web/` for embedding into the binary.

## Release

Tag `vX.Y.Z` (or run the workflow manually) to build + publish the linux
amd64/arm64 binaries, the `sb-ui-role.tar.gz` mod role, and checksums.
CI: `.github/workflows/release.yml`.

## Layout

```
main.go            # server + embed + SPA fallback + --version
internal/…         # Go port of the backend (executor, jobs, apps, …)
frontend/          # React source (Vite); dev here
web/               # embedded frontend build (generated; gitignored)
build.sh           # frontend build + copy + versioned go build
install.sh         # standalone host installer (binary + systemd)
bootstrap-mod.sh   # Saltbox-native installer (saltbox_mod role)
sb-ui.service      # standalone systemd unit template
deploy/saltbox_mod/roles/sbui/   # the mod role (autoplow-style), shipped as sb-ui-role.tar.gz
```
