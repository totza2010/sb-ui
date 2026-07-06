# Built-in Autoscan — Implementation Plan

Goal: replace the external, docker-paused **autoscan** role with a built-in autoscan
in sb-ui. Receive scan triggers (arr webhooks / manual / post-upload / optional file
watch), coalesce them, and drive **Plex partial scans** (Emby/Jellyfin later) using the
primitives that already exist in the codebase. Modeled on `saltbox/autoplow`.

---

## 1. What already exists — reuse, do NOT rebuild

| autoplow concept | already in sb-ui |
|---|---|
| Plex partial scan `GET /library/sections/{id}/refresh?path=` | `plexScan(cfg, sectionKey, path)` — `internal/api/plexclient.go:193` |
| path → section (longest-prefix match) | `plexSectionForPath(cfg, path)` — `plexclient.go:215` |
| list sections + Location roots | `plexSections(cfg)` — `plexclient.go:97` |
| whole-section rescan | `plexRefreshAll(cfg)` — `plexclient.go:207` |
| path rewrite (arr/local → Plex-visible) | `optionsConfig.PathMappings` + `mapArrPath(p)` — `options.go:49,57` |
| external autoscan container control | `autoscanHold(bool)` (docker pause/unpause) — `autoscan.go` |
| Plex config (URL/token) | `loadOptions().Plex` (`plexConfig`) — `options.go:16,48` |

**The scan engine is already ported.** Scanning one path = one line:

```go
cfg := loadOptions().Plex
if key, ok := plexSectionForPath(cfg, mapArrPath(path)); ok { _ = plexScan(cfg, key, mapArrPath(path)) }
```

What's missing is the **orchestration + intake + config + UI** around it. autoplow's
inotify/polling/processor/anchor/matcharr/plexautolang/throttle/SSE stack is large; we
port only the parts that matter and lean on existing primitives.

---

## 2. Core design (the service)

One in-process singleton, `autoscanService`:

- **Intake** `Enqueue(paths ...string)` — dedup + **debounce** per path (reset a timer,
  e.g. 5s) so rapid duplicate triggers (arr rename events, multi-file drops) collapse
  into a single scan.
- **Due handler** `scanOne(path)` — `mapArrPath` rewrite → `plexSectionForPath` →
  `plexScan`. Optional pre-checks (file exists / min-age "anchor", like autoplow) to
  avoid scanning half-written files.
- **Concurrency** — small worker limit (`sem chan struct{}`, default 1–2) + one retry on
  transient Plex errors.
- **Observability** — in-memory ring buffer of recent scans (path, section, ok/err, ts)
  for the UI; queue depth counter.
- **Config-gated** — no-op unless enabled.

Injectable seam for tests: `var autoscanScanFn = liveScan` (override in `_test.go`),
same pattern as `qbitPauseFn` etc. in the uploader.

---

## 3. Data model — `optionsConfig.Autoscan` (options.json)

```go
type autoscanConfig struct {
    Enabled      bool     `json:"enabled"`
    DelaySec     int      `json:"delay_sec"`      // debounce, default 5
    MinAgeSec    int      `json:"min_age_sec"`    // skip files younger than this, default 0
    Concurrency  int      `json:"concurrency"`    // default 1
    OnUpload     bool     `json:"on_upload"`      // Phase 3: scan moved paths after an upload
    WebhookToken string   `json:"webhook_token"`  // shared secret for arr webhooks
    WatchEnabled bool     `json:"watch_enabled"`  // Phase 4
    WatchPaths   []string `json:"watch_paths"`    // Phase 4
    // Rewrites reuse the existing top-level PathMappings (mapArrPath).
    // Targets: Plex only for now (loadOptions().Plex); add Emby/Jellyfin later.
}
```

Add `Autoscan autoscanConfig json:"autoscan"` to `optionsConfig` (`options.go:47`).

---

## 4. Phases

### Phase 1 — Core scan service  *(no external behavior change yet)*
- **NEW** `internal/api/autoscan_scan.go`: `autoscanService` (pending map + timers, ring
  log, sem), `Enqueue`, `scanOne`, package singleton `autoscanSvc()`.
- **NEW** `internal/api/autoscan_scan_test.go`: debounce coalescing, rewrite applied,
  section match, disabled = no-op (injected `autoscanScanFn`).
- ~`options.go`: add `autoscanConfig` + field.

### Phase 2 — Trigger intake (endpoints)
- **NEW** `internal/api/autoscan_http.go`:
  - `POST /api/autoscan/trigger` `{paths:[...]}` — manual / generic (session auth).
  - `POST /api/autoscan/webhook/{token}` — parse **Sonarr/Radarr webhook** JSON
    (`eventType`: Download/Rename/Test; pull file/folder paths) → `Enqueue`. Token from
    config; optionally also accept autoscan's legacy `/triggers/*` shape so existing arr
    configs work unchanged.
  - `GET /api/autoscan/status` — enabled, queue depth, recent log.
  - `GET/PUT /api/autoscan/config` — read/save `autoscanConfig`.
- ~`api.go`: register routes.

### Phase 3 — Post-upload scan  *(fills the known gap)*
- ~`internal/api/uploader.go`: after the successful move in `uploaderCheck` (~line 727,
  right after `restoreUploadPause`), if `Autoscan.Enabled && Autoscan.OnUpload`, compute
  the moved item paths on the **Plex-visible (union) side** and `Enqueue`.
- Replaces the need to `docker pause` autoscan for uploads: uploader simply scans the
  moved paths itself afterward.

### Phase 4 — File watcher  *(heaviest; optional/last)*
- **NEW** `internal/api/autoscan_watch.go`: `fsnotify` recursive watch over
  `WatchPaths`, debounced via the same `Enqueue`; polling fallback for network mounts.
- ~`go.mod`: `github.com/fsnotify/fsnotify`.

### Phase 5 — UI
- Autoscan card (Integrations page, or its own section): enable; debounce / min-age /
  concurrency; **webhook URL + token** (copyable, paste into Sonarr/Radarr Connect);
  post-upload toggle; watch paths; **live recent-scan log** + a "scan a path now" test box.
- ~`frontend/src/lib/api.ts`: `useAutoscanStatus`, `useAutoscanConfig`/save,
  `useAutoscanTrigger`.
- ~`frontend/src/pages/Integrations.tsx` (or **NEW** `Autoscan.tsx`).

---

## 5. Relationship to the existing external autoscan
- The uploader's **"hold autoscan" (docker pause)** stays valid while the external
  container is in use. Once built-in autoscan is adopted, the uploader stops pausing and
  instead **enqueues moved paths after upload** (Phase 3) — no docker pause needed.
- Keep `autoscanHold` for back-compat; a config flag ("use built-in autoscan") switches
  the uploader between the two strategies. Eventually the external role can be dropped.

---

## 6. Endpoints (summary)

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/autoscan/trigger` | manual scan of given paths |
| POST | `/api/autoscan/webhook/{token}` | Sonarr/Radarr (+ legacy autoscan) intake |
| GET  | `/api/autoscan/status` | enabled, queue depth, recent scans |
| GET/PUT | `/api/autoscan/config` | read/save autoscanConfig |

## 7. Files (summary)

| File | Change |
|---|---|
| `internal/api/autoscan_scan.go` | NEW — service (queue/debounce/scan/log) |
| `internal/api/autoscan_scan_test.go` | NEW — unit tests |
| `internal/api/autoscan_http.go` | NEW — trigger/webhook/status/config handlers |
| `internal/api/autoscan_watch.go` | NEW (Phase 4) — fsnotify watcher |
| `internal/api/options.go` | +`autoscanConfig` + field |
| `internal/api/api.go` | + routes |
| `internal/api/uploader.go` | + post-upload enqueue (Phase 3) |
| `frontend/src/pages/Integrations.tsx` / new `Autoscan.tsx` | UI |
| `frontend/src/lib/api.ts` | hooks |
| `go.mod` | + fsnotify (Phase 4 only) |

---

## 8. Open decisions (confirm before coding)
1. **Multi-target**: Plex only first, or wire Emby/Jellyfin now? (recommend Plex first)
2. **Webhook compatibility**: also emulate autoscan's `/triggers/{sonarr,radarr}` URL
   shape so existing arr Connect configs point to sb-ui unchanged? (recommend yes)
3. **Watcher**: ship `fsnotify` (Phase 4), or rely on arr-webhook + post-upload only?
   (recommend defer watcher — webhooks cover most cases with far less complexity)
4. **Auth on webhook**: path token (simple, arr-friendly) vs header secret.

Recommended build order: **Phase 1 → 2 → 3** (a working webhook/post-upload autoscan that
replaces the container), then **5 (UI)**, then **4 (watcher)** only if needed.
