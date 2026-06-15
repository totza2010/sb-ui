#!/usr/bin/env bash
# Build sb-ui: compile the React frontend, embed it, produce a single binary.
#
# Usage: ./build.sh [goos] [goarch]   (default: linux amd64 — the Saltbox target)
# Env:
#   VERSION   version string baked into the binary (default: git describe or "dev")
#   OUT       output path (default: ./sb-ui[.exe])
#   SKIP_WEB  if set, reuse the existing web/ (don't rebuild the frontend)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
FRONTEND="$ROOT/frontend"
GOOS="${1:-linux}"
GOARCH="${2:-amd64}"
VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}"

EXT=""
[[ "$GOOS" == "windows" ]] && EXT=".exe"
OUT="${OUT:-$ROOT/sb-ui$EXT}"

if [[ -z "${SKIP_WEB:-}" ]]; then
  echo "==> Building frontend ($FRONTEND)"
  ( cd "$FRONTEND" && npm ci && npm run build )

  echo "==> Copying dist -> web/"
  rm -rf "$ROOT/web"
  mkdir -p "$ROOT/web"
  cp -r "$FRONTEND/dist/." "$ROOT/web/"
fi

echo "==> Building sb-ui $VERSION ($GOOS/$GOARCH) -> $OUT"
( cd "$ROOT" && GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 \
    go build -trimpath \
      -ldflags="-s -w -X sb-ui/internal/buildinfo.Version=$VERSION" \
      -o "$OUT" . )

echo "==> Done: $OUT ($GOOS/$GOARCH, $VERSION)"
