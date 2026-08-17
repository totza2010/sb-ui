# Dev hot-reload — launches the Go backend (air) and the Vite frontend (HMR),
# each in its OWN window so they're independent: Ctrl-C in either stops just that
# one (the window stays open so you can re-run), and air auto-rebuilds the
# backend on .go saves without dropping the other.
#
#   ./dev.ps1          normal: on-host callers are trusted, so no sign-in
#   ./dev.ps1 -Auth    exercise authentication: requires the login password
#
# Open http://localhost:5173 — Vite proxies /api + /ws to the backend on :9180.
#
# -Auth exists because Vite proxies to the backend over loopback, and loopback callers are
# exempt by default — so the login gate is invisible in normal dev. This switch turns that
# exemption off for the backend window, which is the only way to see what a remote visitor
# sees. Set a password first with:  go run . --set-password
param([switch]$Auth)

$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot

$air = Join-Path (go env GOPATH) 'bin\air.exe'
if (-not (Test-Path $air)) {
  Write-Host "air not found — installing..." -ForegroundColor Yellow
  go install github.com/air-verse/air@latest
}

# Backend (air) — hot-reloads on .go changes; recovers on its own.
$backend = if ($Auth) {
  "Set-Location '$root'; `$env:SB_UI_TRUST_LOOPBACK='false'; & '$air'"
} else {
  "Set-Location '$root'; & '$air'"
}
Start-Process powershell -ArgumentList '-NoExit', '-Command', $backend
# Frontend (Vite) — HMR.
Start-Process powershell -ArgumentList '-NoExit', '-Command', "Set-Location '$root\frontend'; npm run dev"

Write-Host ""
Write-Host "Started two windows:" -ForegroundColor Cyan
Write-Host "  - air  (Go backend, hot-reload) -> :9180"
if ($Auth) {
  Write-Host "  - auth ENFORCED (loopback not trusted) — you will be asked to sign in" -ForegroundColor Yellow
}
Write-Host "  - Vite (frontend, HMR)          -> http://localhost:5173"
Write-Host ""
Write-Host "Open http://localhost:5173" -ForegroundColor Green
Write-Host "Editing a .go file rebuilds the backend (~5s) automatically — no restart needed." -ForegroundColor DarkGray
Write-Host "To stop: Ctrl-C (or close) each window." -ForegroundColor DarkGray
