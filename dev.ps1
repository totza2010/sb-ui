# Dev hot-reload — Go backend (air) + Vite frontend (HMR) together.
#
#   ./dev.ps1
#
# Opens the app at http://localhost:5173. Vite serves the React app with HMR and
# proxies /api + /ws to the Go backend on :8000. `air` rebuilds + restarts the
# Go backend (~1s) on any .go change. The backend reads .env for the SSH/local
# connection just like the built binary.
$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot

$air = Join-Path (go env GOPATH) 'bin\air.exe'
if (-not (Test-Path $air)) {
  Write-Host "air not found — installing..." -ForegroundColor Yellow
  go install github.com/air-verse/air@latest
}

Write-Host "==> Vite (frontend, HMR) in a new window..." -ForegroundColor Cyan
$vite = Start-Process -PassThru -FilePath 'powershell' `
  -ArgumentList '-NoExit', '-Command', "Set-Location '$root\frontend'; npm run dev"

try {
  Write-Host "==> air (Go backend, hot-reload) on :8000 — open http://localhost:5173" -ForegroundColor Cyan
  & $air
} finally {
  Write-Host "`n==> stopping Vite..." -ForegroundColor Cyan
  Stop-Process -Id $vite.Id -Force -ErrorAction SilentlyContinue
}
