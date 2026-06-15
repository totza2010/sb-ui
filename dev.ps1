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
  Write-Host "    (Ctrl-C or close this window to stop everything)" -ForegroundColor DarkGray
  # Watchdog: if air exits on its own (crash), relaunch it so you don't have to
  # re-run dev.ps1. Ctrl-C terminates the script (-> finally), not the loop.
  while ($true) {
    $start = Get-Date
    & $air
    if (((Get-Date) - $start).TotalSeconds -lt 3) {
      Write-Host "air exited immediately — stopping. Fix the issue, then re-run dev.ps1." -ForegroundColor Yellow
      break
    }
    Write-Host "`n==> air exited — restarting..." -ForegroundColor Yellow
    Start-Sleep 1
  }
} finally {
  Write-Host "`n==> stopping Vite..." -ForegroundColor Cyan
  Stop-Process -Id $vite.Id -Force -ErrorAction SilentlyContinue
}
