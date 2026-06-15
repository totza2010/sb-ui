# Dev hot-reload — launches the Go backend (air) and the Vite frontend (HMR),
# each in its OWN window so they're independent: Ctrl-C in either stops just that
# one (the window stays open so you can re-run), and air auto-rebuilds the
# backend on .go saves without dropping the other.
#
#   ./dev.ps1
#
# Open http://localhost:5173 — Vite proxies /api + /ws to the backend on :8000.
$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot

$air = Join-Path (go env GOPATH) 'bin\air.exe'
if (-not (Test-Path $air)) {
  Write-Host "air not found — installing..." -ForegroundColor Yellow
  go install github.com/air-verse/air@latest
}

# Backend (air) — hot-reloads on .go changes; recovers on its own.
Start-Process powershell -ArgumentList '-NoExit', '-Command', "Set-Location '$root'; & '$air'"
# Frontend (Vite) — HMR.
Start-Process powershell -ArgumentList '-NoExit', '-Command', "Set-Location '$root\frontend'; npm run dev"

Write-Host ""
Write-Host "Started two windows:" -ForegroundColor Cyan
Write-Host "  - air  (Go backend, hot-reload) -> :8000"
Write-Host "  - Vite (frontend, HMR)          -> http://localhost:5173"
Write-Host ""
Write-Host "Open http://localhost:5173" -ForegroundColor Green
Write-Host "Editing a .go file rebuilds the backend (~5s) automatically — no restart needed." -ForegroundColor DarkGray
Write-Host "To stop: Ctrl-C (or close) each window." -ForegroundColor DarkGray
