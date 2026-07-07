/**
 * Autoscan — built-in Plex partial-scan service (replaces the external autoscan
 * container). Configure the debounce, post-upload scanning, and the arr webhook URL;
 * watch recent scans live. Backend: /api/autoscan/* (see docs/autoscan-plan.md).
 */
import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useAutoscanConfig, useSaveAutoscanConfig, useAutoscanStatus, useAutoscanTrigger, useAutoscanClear, useAutoscanPause, useAutoscanSelfTest, useAutoscanConnCheck, useAutoscanWire, type AutoscanConfig, type ScanStatus, type InboundHook, type SelfTestResult, type ConnLink, type WireResult } from '@/lib/api'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { cn } from '@/lib/cn'
import { ScanLine, Save, Copy, Check, RefreshCw, Play, Pause, Loader2, Webhook, Zap, Trash2, Clock, CheckCircle2, XCircle, MinusCircle, Filter, ChevronDown, ChevronRight, Plus, X, FolderInput, SlidersHorizontal } from 'lucide-react'
import { PathPicker } from '@/components/PathPicker'

const EMPTY: AutoscanConfig = { enabled: false, delay_sec: 5, on_upload: false, webhook_token: '' }

// Top-level page (sidebar → Autoscan).
export function Autoscan() {
  return <div className="mx-auto max-w-6xl p-6"><AutoscanPanel /></div>
}

export function AutoscanPanel() {
  const qc = useQueryClient()
  const { data } = useAutoscanConfig()
  const save = useSaveAutoscanConfig()
  const { data: status } = useAutoscanStatus()
  const trigger = useAutoscanTrigger()
  const clear = useAutoscanClear()
  const pause = useAutoscanPause()
  const togglePause = () => pause.mutate(!status?.paused, { onSuccess: () => qc.invalidateQueries({ queryKey: ['autoscan-status'] }) })

  const [cfg, setCfg] = useState<AutoscanConfig>(EMPTY)
  const [saved, setSaved] = useState(false)
  const [testPath, setTestPath] = useState('')
  const [filter, setFilter] = useState<'all' | ScanStatus>('all')
  const [openRows, setOpenRows] = useState<Record<number, boolean>>({})
  const [now, setNow] = useState(Date.now())
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t) }, [])

  useEffect(() => { if (data) setCfg({ ...EMPTY, ...data }) }, [data])

  const up = <K extends keyof AutoscanConfig>(k: K, v: AutoscanConfig[K]) => setCfg((c) => ({ ...c, [k]: v }))
  const persist = (next: AutoscanConfig) =>
    save.mutate(next, { onSuccess: (d) => { setCfg({ ...EMPTY, ...d }); qc.invalidateQueries({ queryKey: ['autoscan-config'] }) } })

  const doSave = () => persist(cfg)
  const regenToken = () => persist({ ...cfg, webhook_token: '' }) // backend mints a fresh one
  // Build webhook URLs from the backend's REAL listen port (arr must hit the port
  // directly, not the Traefik/Authelia origin). Remote = the host you reached the UI
  // on; Local = same host as sb-ui.
  const port = status?.port ?? '8000'
  const tok = cfg.webhook_token
  const remoteURL = tok ? `http://${window.location.hostname}:${port}/api/autoscan/webhook/${tok}` : ''
  const localURL = tok ? `http://localhost:${port}/api/autoscan/webhook/${tok}` : ''
  const remoteBase = `http://${window.location.hostname}:${port}/api/autoscan/webhook`
  const [copiedKey, setCopiedKey] = useState('')
  const copyText = (s: string, key = 's') => { if (!s) return; navigator.clipboard.writeText(s); setCopiedKey(key); setTimeout(() => setCopiedKey(''), 1500) }
  const runTest = () => { const p = testPath.trim(); if (p) trigger.mutate([p], { onSuccess: () => { setTestPath(''); qc.invalidateQueries({ queryKey: ['autoscan-status'] }) } }) }

  // Webhook self-test: round-trip a Test payload through our own endpoint (loopback) —
  // proves our endpoint + token work, without an *arr. Secondary check.
  const connCheck = useAutoscanConnCheck()
  const checkConns = () => connCheck.mutate(undefined, { onSuccess: () => qc.invalidateQueries({ queryKey: ['autoscan-status'] }) })
  const conns = status?.connections ?? []

  // Auto-wire the webhook into an *arr via its own API: find a URL it can reach us on,
  // save it, and run its test. Result is kept per-connection for inline display.
  const wire = useAutoscanWire()
  const [wireResults, setWireResults] = useState<Record<string, WireResult>>({})
  const [wiringKey, setWiringKey] = useState('')
  const wireConn = (key: string) => {
    setWiringKey(key)
    wire.mutate({ key, hostname: window.location.hostname, save: true }, {
      onSettled: () => setWiringKey(''),
      onSuccess: (r) => { setWireResults((m) => ({ ...m, [key]: r })); qc.invalidateQueries({ queryKey: ['autoscan-status'] }) },
      onError: (e) => setWireResults((m) => ({ ...m, [key]: { ok: false, error: e.message } })),
    })
  }

  const selfTest = useAutoscanSelfTest()
  const [testResult, setTestResult] = useState<SelfTestResult | null>(null)
  const runSelfTest = () => selfTest.mutate(undefined, {
    onSuccess: (r) => { setTestResult(r); qc.invalidateQueries({ queryKey: ['autoscan-status'] }) },
    onError: (e) => setTestResult({ ok: false, url: '', error: e.message }),
  })

  // "Listen for *arr" — arm a wait, then the user clicks Test in Sonarr/Radarr; we
  // capture the next real inbound webhook and show exactly how we replied. While armed
  // we poll status fast so the hit shows within ~1.5s.
  const LISTEN_MS = 90_000
  const [listening, setListening] = useState(false)
  const [captured, setCaptured] = useState<InboundHook | null>(null)
  const [listenTimedOut, setListenTimedOut] = useState(false)
  const baselineAt = useRef<string | null>(null)
  const listenStart = useRef(0)
  const startListen = () => {
    baselineAt.current = status?.last_inbound?.at ?? null
    listenStart.current = Date.now()
    setCaptured(null)
    setListenTimedOut(false)
    setListening(true)
  }
  const lastAt = status?.last_inbound?.at
  useEffect(() => { // a fresh inbound arrived while armed → capture it
    if (listening && status?.last_inbound && lastAt !== baselineAt.current) {
      setCaptured(status.last_inbound)
      setListening(false)
    }
  }, [lastAt]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { // poll fast + time out while armed
    if (!listening) return
    if (Date.now() - listenStart.current > LISTEN_MS) { setListening(false); setListenTimedOut(true); return }
    const t = setInterval(() => qc.invalidateQueries({ queryKey: ['autoscan-status'] }), 1500)
    return () => clearInterval(t)
  }, [listening, now]) // eslint-disable-line react-hooks/exhaustive-deps

  const counts = status?.counts ?? { pending: 0, scanning: 0, completed: 0, skipped: 0, failed: 0, ignored: 0 }
  const scans = status?.scans ?? []
  const rows = scans.filter((r) => filter === 'all' || r.status === filter || (filter === 'failed' && r.status === 'skipped'))

  return (
    <div className="space-y-5">
      {/* header */}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary"><ScanLine className="h-5 w-5" /></div>
          <div>
            <h2 className="text-lg font-semibold text-foreground">Autoscan</h2>
            <p className="mt-0.5 text-sm text-muted-foreground">Built-in Plex partial-scan service — point Sonarr/Radarr here (or scan after uploads) to replace the external autoscan container.</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <span className={cn('flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium',
            status?.paused ? 'bg-warning/15 text-warning' : cfg.enabled ? 'bg-success/15 text-success' : 'bg-muted text-muted-foreground')}>
            <span className={cn('h-2 w-2 rounded-full', status?.paused ? 'bg-warning' : cfg.enabled ? 'bg-success' : 'bg-muted-foreground/50')} />{status?.paused ? 'Paused' : cfg.enabled ? 'Active' : 'Disabled'}
          </span>
          <Button size="sm" variant="outline" className="gap-1.5" onClick={togglePause} disabled={pause.isPending} title="Held while an upload runs; toggle manually here">
            {status?.paused ? <><Play className="h-3.5 w-3.5" />Resume</> : <><Pause className="h-3.5 w-3.5" />Pause</>}
          </Button>
          <Button size="sm" className="gap-1.5" onClick={doSave} disabled={save.isPending}><Save className="h-3.5 w-3.5" />{saved ? 'Saved ✓' : 'Save'}</Button>
        </div>
      </div>

      {/* stat cards — always visible (the working view) */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatCard label="Pending" value={counts.pending} tone="pending" />
        <StatCard label="Scanning" value={counts.scanning} tone="scanning" />
        <StatCard label="Completed" value={counts.completed} tone="completed" />
        <StatCard label="Failed" value={counts.failed + counts.skipped} tone="failed" />
      </div>

      <Tabs defaultValue="activity" className="space-y-3">
        <TabsList>
          <TabsTrigger value="activity" className="gap-1.5"><ScanLine className="h-3.5 w-3.5" />Activity</TabsTrigger>
          <TabsTrigger value="settings" className="gap-1.5"><SlidersHorizontal className="h-3.5 w-3.5" />Settings{!cfg.enabled && <span className="ml-0.5 rounded bg-warning/15 px-1 text-[10px] text-warning">off</span>}</TabsTrigger>
          <TabsTrigger value="webhook" className="gap-1.5"><Webhook className="h-3.5 w-3.5" />Webhook</TabsTrigger>
        </TabsList>

        {/* ── Activity ─────────────────────────────────────────────── */}
        <TabsContent value="activity">
          <Card className="space-y-3 rounded-xl border-border/70 p-4 shadow-sm">
            {/* toolbar: filter + test-a-path + clear */}
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="inline-flex gap-0.5 rounded-lg border border-border bg-muted p-0.5">
                {(['all', 'pending', 'scanning', 'completed', 'failed', 'ignored'] as const).map((f) => (
                  <button key={f} onClick={() => setFilter(f)}
                    className={cn('rounded-md px-2.5 py-1 text-xs font-medium capitalize transition-colors', filter === f ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')}>{f}</button>
                ))}
              </div>
              <div className="flex items-center gap-1.5">
                {!!status?.queued && <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{status.queued} queued</span>}
                <Input className="h-8 w-64 font-mono text-xs" value={testPath} onChange={(e) => setTestPath(e.target.value)} placeholder="/mnt/unionfs/Media/… — scan now" onKeyDown={(e) => e.key === 'Enter' && runTest()} />
                <Button size="sm" variant="outline" className="gap-1.5" onClick={runTest} disabled={trigger.isPending || !testPath.trim()}>
                  {trigger.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}Scan
                </Button>
                <Button size="icon" variant="ghost" className="h-8 w-8" title="Clear history" disabled={rows.length === 0} onClick={() => clear.mutate(undefined, { onSuccess: () => qc.invalidateQueries({ queryKey: ['autoscan-status'] }) })}><Trash2 className="h-3.5 w-3.5 text-destructive" /></Button>
              </div>
            </div>

            {/* table */}
            <div className="overflow-hidden rounded-md border border-border">
              <div className="flex items-center gap-3 border-b border-border bg-secondary/30 px-3 py-1.5 text-[10px] uppercase tracking-wide text-muted-foreground">
                <span className="w-3.5 shrink-0" />
                <span className="min-w-0 flex-1">Mapped path</span>
                <span className="w-32 shrink-0">Trigger</span>
                <span className="w-36 shrink-0">Status</span>
                <span className="w-32 shrink-0 text-right">Created</span>
              </div>
              <div className="max-h-[52vh] divide-y divide-border overflow-y-auto">
                {rows.length === 0 && <div className="px-4 py-10 text-center text-xs text-muted-foreground">No scans found. Trigger one below, wire an *arr webhook (Settings tab), or enable scan-after-upload.</div>}
                {rows.map((r) => {
                  const hits = r.hits ?? []
                  const open = !!openRows[r.id]
                  return (
                    <div key={r.id}>
                      <div className={cn('flex items-center gap-3 px-3 py-1.5 text-sm', hits.length > 0 && 'cursor-pointer hover:bg-muted/40')} onClick={() => hits.length > 0 && setOpenRows((o) => ({ ...o, [r.id]: !o[r.id] }))}>
                        <span className="w-3.5 shrink-0 text-muted-foreground">{hits.length > 0 && (open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />)}</span>
                        <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-foreground" title={r.error || r.path}>{r.path}{r.section && <span className="text-muted-foreground/70"> §{r.section}</span>}</span>
                        <span className="flex w-32 shrink-0 items-center gap-1.5">
                          <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] capitalize text-muted-foreground">{r.source || '—'}</span>
                          {r.event && <span className="truncate text-[10px] text-muted-foreground/70">{r.event}</span>}
                          {hits.length > 1 && <span className="shrink-0 rounded-full bg-primary/15 px-1.5 text-[10px] font-medium text-primary">×{hits.length}</span>}
                        </span>
                        <span className="flex w-36 shrink-0 items-center gap-1.5">
                          <StatusPill status={r.status} error={r.error} />
                          {r.status === 'pending' && (status?.paused
                            ? <span className="text-[10px] font-medium text-warning">held</span>
                            : r.fire_at && <span className="text-[10px] tabular-nums text-muted-foreground">in {Math.max(0, Math.ceil((new Date(r.fire_at).getTime() - now) / 1000))}s</span>)}
                          {r.status === 'scanning' && r.started_at && <span className="text-[10px] tabular-nums text-muted-foreground">{Math.max(0, Math.floor((now - new Date(r.started_at).getTime()) / 1000))}s</span>}
                        </span>
                        <span className="w-32 shrink-0 text-right text-[11px] tabular-nums text-muted-foreground">{new Date(r.created_at).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</span>
                      </div>
                      {open && hits.length > 0 && (
                        <div className="space-y-1 border-t border-border/50 bg-secondary/20 px-3 py-2 pl-8">
                          <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Webhook events ({hits.length})</p>
                          {hits.map((h, i) => (
                            <div key={i} className="flex items-center gap-2 text-[11px]">
                              <span className="w-16 shrink-0 tabular-nums text-muted-foreground/70">{new Date(h.time).toLocaleTimeString()}</span>
                              <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] capitalize text-muted-foreground">{h.source}{h.event ? ` · ${h.event}` : ''}</span>
                              <span className="min-w-0 flex-1 truncate font-mono text-foreground/80" title={h.path}>{h.path}</span>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
            </div>
          </Card>
        </TabsContent>

        {/* ── Settings ─────────────────────────────────────────────── */}
        <TabsContent value="settings" className="space-y-4">
          <div className="grid gap-4 lg:grid-cols-2">
            {/* general */}
            <Card className="space-y-4 rounded-xl border-border/70 p-4 shadow-sm">
              <label className="flex items-center justify-between gap-3">
                <span className="text-sm font-medium text-foreground">Enable autoscan
                  <span className="mt-0.5 block text-[11px] font-normal text-muted-foreground">Master switch — arr webhooks &amp; scan-after-upload do nothing while off (the manual “Scan now” box still works for testing).</span>
                </span>
                <Switch checked={cfg.enabled} onCheckedChange={(v) => up('enabled', v)} />
              </label>

              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1">
                  <Label className="text-[11px]">Debounce (seconds)</Label>
                  <Input type="number" min={1} className="h-8" value={cfg.delay_sec} onChange={(e) => up('delay_sec', Math.max(1, parseInt(e.target.value, 10) || 5))} />
                  <p className="text-[10px] text-muted-foreground">Wait this long, coalescing rapid events for the same folder into one scan.</p>
                </div>
                <div className="space-y-1">
                  <Label className="text-[11px]">Scan gap (seconds)</Label>
                  <Input type="number" min={0} className="h-8" value={cfg.scan_gap_sec ?? 3} onChange={(e) => up('scan_gap_sec', Math.max(0, parseInt(e.target.value, 10) || 0))} />
                  <p className="text-[10px] text-muted-foreground">Min spacing between scans — a released/queued backlog drains one at a time, not all at once. 0 = no limit.</p>
                </div>
                <div className="space-y-1">
                  <Label className="flex items-center gap-1 text-[11px]"><Zap className="h-3 w-3" />Scan after upload</Label>
                  <div className="flex h-8 items-center">
                    <Switch checked={cfg.on_upload} onCheckedChange={(v) => up('on_upload', v)} />
                  </div>
                  <p className="text-[10px] text-muted-foreground">When the Uploader moves files, scan the moved paths (needs a path mapping to the Plex side).</p>
                </div>
              </div>

              <div className="space-y-2 border-t border-border/60 pt-3">
                <label className="flex items-center justify-between gap-3">
                  <span className="text-sm text-foreground">Wait for Plex to finish
                    <span className="mt-0.5 block text-[11px] font-normal text-muted-foreground">Poll Plex so a scan shows <span className="text-foreground">Completed</span> only once Plex has actually finished (not just when triggered).</span>
                  </span>
                  <Switch checked={!!cfg.wait_completion} onCheckedChange={(v) => up('wait_completion', v)} />
                </label>
                {cfg.wait_completion && (
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-1">
                      <Label className="text-[11px]">Idle (seconds)</Label>
                      <Input type="number" min={10} className="h-8" value={cfg.idle_sec || 30} onChange={(e) => up('idle_sec', Math.max(10, parseInt(e.target.value, 10) || 30))} />
                      <p className="text-[10px] text-muted-foreground">No scan activity for this long = done.</p>
                    </div>
                    <div className="space-y-1">
                      <Label className="text-[11px]">Timeout (seconds)</Label>
                      <Input type="number" min={30} className="h-8" value={cfg.timeout_sec || 300} onChange={(e) => up('timeout_sec', Math.max(30, parseInt(e.target.value, 10) || 300))} />
                      <p className="text-[10px] text-muted-foreground">Give up waiting after this.</p>
                    </div>
                  </div>
                )}
              </div>

              <div className="space-y-1 border-t border-border/60 pt-3">
                <Label className="text-[11px]">Anchor files <span className="text-muted-foreground/70">(mount guard)</span></Label>
                <PathList value={cfg.anchors ?? []} onChange={(v) => up('anchors', v)} multi disks={['/mnt/remote', '/mnt/unionfs', '/mnt/local']} placeholder="/mnt/remote/&lt;remote&gt;/mounted.bin" />
                <p className="text-[10px] text-muted-foreground">Absolute files that must <span className="text-foreground">all</span> exist before scanning — add one per merged remote (browse <span className="font-mono">/mnt/remote/&lt;remote&gt;</span>). If any is missing that mount is treated as down and the scan is held, so Plex won't trash the library when an rclone mount drops.</p>
              </div>

              <label className="flex items-center justify-between gap-3 border-t border-border/60 pt-3">
                <span className="text-sm text-foreground">Log skipped webhooks
                  <span className="mt-0.5 block text-[11px] font-normal text-muted-foreground">Debug — also record events we don't scan (Grab, series-level rename, …) in the history, to see exactly what each *arr sends.</span>
                </span>
                <Switch checked={!!cfg.log_skipped} onCheckedChange={(v) => up('log_skipped', v)} />
              </label>
            </Card>

            {/* filters — moved to the right column of Settings */}
            <Card className="space-y-3 rounded-xl border-border/70 p-4 shadow-sm">
              <p className="flex items-center gap-1.5 text-sm font-medium text-foreground"><Filter className="h-4 w-4 text-muted-foreground" />Filters <span className="font-normal text-muted-foreground">— skip events that don't need a Plex scan</span></p>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1 sm:col-span-2">
                  <Label className="text-[11px]">Exclude extensions</Label>
                  <ExtSelect value={cfg.exclude_exts ?? []} onChange={(v) => up('exclude_exts', v)} />
                  <p className="text-[10px] text-muted-foreground">Click to toggle; type to add a custom one. Changes to these files won't trigger a scan.</p>
                </div>
                <div className="space-y-1">
                  <Label className="text-[11px]">Exclude paths</Label>
                  <PathList value={cfg.exclude_paths ?? []} onChange={(v) => up('exclude_paths', v)} placeholder="type a path or pick…" />
                  <p className="text-[10px] text-muted-foreground">Prefix match on the incoming path.</p>
                </div>
                <div className="space-y-1">
                  <Label className="text-[11px]">Include paths <span className="text-muted-foreground/70">(blank = all)</span></Label>
                  <PathList value={cfg.include_paths ?? []} onChange={(v) => up('include_paths', v)} placeholder="type a path or pick…" />
                  <p className="text-[10px] text-muted-foreground">If set, only paths under one of these scan.</p>
                </div>
              </div>
            </Card>
          </div>
        </TabsContent>

        {/* ── Webhook ──────────────────────────────────────────────── */}
        <TabsContent value="webhook" className="space-y-4">
          <div className="grid gap-4 lg:grid-cols-2">
            {/* left: webhook URLs + auth + live connection test */}
            <Card className="space-y-3 rounded-xl border-border/70 p-4 shadow-sm">
          <p className="flex items-center gap-1.5 text-sm font-medium text-foreground"><Webhook className="h-4 w-4 text-muted-foreground" />Sonarr / Radarr webhook</p>
          <p className="text-[11px] text-muted-foreground">In each *arr: <span className="text-foreground">Settings → Connect → Webhook</span>, tick On Import / On Rename / On Upgrade, then paste a URL below. These hit sb-ui's port <span className="font-mono text-foreground">:{port}</span> directly (skips the Traefik/Authelia front).</p>

          <div className="space-y-2">
            {([['remote', 'Remote', `via ${window.location.hostname} — for *arr on another host`, remoteURL], ['local', 'Local', 'for *arr on the same host as sb-ui', localURL]] as const).map(([key, label, hint, url]) => (
              <div key={key} className="space-y-0.5">
                <p className="text-[10px] text-muted-foreground"><span className="font-medium text-foreground">{label}</span> — {hint}</p>
                <div className="flex gap-1.5">
                  <Input readOnly className="h-8 font-mono text-xs" value={url} placeholder="save to generate a URL" onFocus={(e) => e.currentTarget.select()} />
                  <Button size="icon" variant="outline" className="h-8 w-8 shrink-0" title="Copy" onClick={() => copyText(url, key)} disabled={!url}>{copiedKey === key ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}</Button>
                  {key === 'remote' && <Button size="icon" variant="outline" className="h-8 w-8 shrink-0" title="Regenerate token" onClick={regenToken} disabled={save.isPending}><RefreshCw className={cn('h-3.5 w-3.5', save.isPending && 'animate-spin')} /></Button>}
                </div>
              </div>
            ))}
          </div>

          <div className="space-y-1 rounded-md border border-border bg-secondary/20 p-2 text-[10px] text-muted-foreground">
            <p className="text-foreground">Authenticate any of these ways:</p>
            <p>• <span className="text-foreground">Paste a URL above</span> — token is in the path (simplest).</p>
            <p>• Base URL <span className="font-mono text-foreground">{remoteBase}</span> + token in the <span className="font-mono text-foreground">X-API-Key</span> header (or <span className="font-mono">?apikey=</span>).</p>
            <p>• <span className="text-foreground">Username/Password</span> in *arr: username = anything, <span className="text-foreground">password = the token</span>.</p>
            <p className="flex items-center gap-1.5 pt-0.5">Token: <span className="min-w-0 flex-1 truncate font-mono text-foreground">{cfg.webhook_token || '—'}</span>
              <button type="button" className="shrink-0 text-primary hover:underline" onClick={() => copyText(cfg.webhook_token || '', 'tok')}>{copiedKey === 'tok' ? 'copied' : 'copy'}</button>
            </p>
            <p className="text-muted-foreground/70">Port <span className="font-mono">:{port}</span> must be reachable from your *arr (open host firewall if needed). Regenerating the token invalidates old URLs.</p>
          </div>

          {/* connection — arm a wait for the *arr's Test, then capture how we replied */}
          <div className="space-y-2.5 rounded-md border border-border p-2.5">
            <div className="flex items-center justify-between gap-2">
              <p className="text-[11px] font-medium text-foreground">Connection</p>
              {listening
                ? <Button size="sm" variant="outline" className="h-7 gap-1.5 text-xs" onClick={() => setListening(false)}><X className="h-3.5 w-3.5" />Cancel</Button>
                : <Button size="sm" variant="outline" className="h-7 gap-1.5 text-xs" onClick={startListen} disabled={!cfg.webhook_token}><Webhook className="h-3.5 w-3.5" />Listen for *arr test</Button>}
            </div>

            {listening ? (
              <div className="rounded border border-primary/40 bg-primary/5 p-2.5 text-[10px]">
                <p className="flex items-center gap-1.5 font-medium text-primary"><Loader2 className="h-3.5 w-3.5 animate-spin" />Waiting for a webhook from your *arr…</p>
                <p className="mt-1 text-muted-foreground">Now open <span className="text-foreground">Sonarr / Radarr → Settings → Connect → your Webhook</span> and click <span className="text-foreground">Test</span>. The moment it hits us, the result shows here.</p>
                <p className="mt-1 text-muted-foreground/70">Listening for {Math.max(0, Math.ceil((LISTEN_MS - (now - listenStart.current)) / 1000))}s · make sure the URL points at <span className="font-mono">:{port}</span>.</p>
              </div>
            ) : captured ? (
              <CapturedInbound hook={captured} now={now} />
            ) : listenTimedOut ? (
              <div className="rounded border border-warning/40 bg-warning/5 p-2.5 text-[10px]">
                <p className="flex items-center gap-1.5 font-medium text-warning"><XCircle className="h-3.5 w-3.5" />No webhook arrived within 90s.</p>
                <p className="mt-1 text-muted-foreground">The *arr never reached us — check the URL/port in its Webhook connection, and that <span className="font-mono">:{port}</span> is reachable (firewall / DNS / different host). Use “Test endpoint” below to confirm sb-ui itself is listening.</p>
              </div>
            ) : (
              <InboundIndicator hook={status?.last_inbound} now={now} />
            )}

            {/* secondary: loopback self-test — confirms our own endpoint + token */}
            <div className="flex items-center justify-between gap-2 border-t border-border/60 pt-2">
              <p className="text-[10px] text-muted-foreground">Or verify sb-ui's own endpoint (no *arr needed):</p>
              <Button size="sm" variant="ghost" className="h-6 gap-1.5 px-2 text-[11px]" onClick={runSelfTest} disabled={selfTest.isPending || !cfg.webhook_token}>
                {selfTest.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Zap className="h-3.5 w-3.5" />}Test endpoint
              </Button>
            </div>
            {testResult && (
              <div className={cn('rounded border p-2 text-[10px]', testResult.ok ? 'border-success/40 bg-success/5' : 'border-destructive/40 bg-destructive/5')}>
                <p className={cn('flex items-center gap-1.5 font-medium', testResult.ok ? 'text-success' : 'text-destructive')}>
                  {testResult.ok ? <CheckCircle2 className="h-3.5 w-3.5" /> : <XCircle className="h-3.5 w-3.5" />}
                  {testResult.ok ? 'Endpoint reachable — auth OK' : 'Self-test failed'}
                  {testResult.status != null && <span className="font-normal text-muted-foreground">· HTTP {testResult.status}</span>}
                  {testResult.latency_ms != null && <span className="font-normal text-muted-foreground">· {testResult.latency_ms}ms</span>}
                </p>
                {testResult.error && <p className="mt-0.5 break-all text-muted-foreground">{testResult.error}</p>}
                <p className="mt-1 text-muted-foreground/70">This only proves sb-ui is listening on <span className="font-mono">:{port}</span>. If it passes but the *arr still can't connect, the problem is network reachability from the *arr (firewall / DNS / different host).</p>
              </div>
            )}
          </div>

            </Card>

            {/* right: known connections registry — inbound + API health, re-checked every 60s */}
            <Card className="space-y-3 rounded-xl border-border/70 p-4 shadow-sm">
              <div className="flex items-center justify-between gap-2">
                <p className="flex items-center gap-1.5 text-sm font-medium text-foreground"><Webhook className="h-4 w-4 text-muted-foreground" />Known connections <span className="font-normal text-muted-foreground">— inbound &amp; API health</span></p>
                <Button size="sm" variant="outline" className="h-7 gap-1.5 text-xs" onClick={checkConns} disabled={connCheck.isPending}>
                  {connCheck.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}Check now
                </Button>
              </div>
              {conns.length === 0 ? (
                <p className="rounded-md border border-dashed border-border px-4 py-6 text-center text-xs text-muted-foreground">No connections yet. Discovered *arr appear after the first health check; webhook senders appear once they hit the endpoint.</p>
              ) : (
                <div className="grid gap-2 sm:grid-cols-2">{conns.map((c) => <ConnRow key={c.key} c={c} now={now} onWire={wireConn} wiring={wiringKey === c.key} result={wireResults[c.key]} />)}</div>
              )}
              <p className="text-[10px] text-muted-foreground/70">Inbound = arr → sb-ui (webhooks). API = sb-ui → arr (every 60s). Healthy when both work; “dropped” = API no longer reachable.</p>
            </Card>
          </div>
        </TabsContent>
      </Tabs>
    </div>
  )
}

// ExtSelect — a compact dropdown multiselect for file extensions: closed it shows
// the current selection; open it lists the common set as checkable rows and lets you
// filter or add a custom extension.
const COMMON_EXTS = ['srt', 'sub', 'ass', 'ssa', 'idx', 'vtt', 'smi', 'nfo', 'txt', 'xml', 'jpg', 'jpeg', 'png', 'tbn', 'webp']
function ExtSelect({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false) }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])

  const norm = (e: string) => e.trim().toLowerCase().replace(/^\./, '')
  const sel = value.map(norm).filter(Boolean)
  const selSet = new Set(sel)
  const toggle = (e: string) => { const n = new Set(selSet); n.has(e) ? n.delete(e) : n.add(e); onChange([...n]) }
  const options = [...new Set([...COMMON_EXTS, ...sel])]
  const filtered = options.filter((o) => o.includes(norm(q)))
  const canAdd = !!norm(q) && !options.includes(norm(q))
  const addCustom = () => { const e = norm(q); if (e && !selSet.has(e)) onChange([...value, e]); setQ('') }

  return (
    <div ref={ref} className="relative">
      <button type="button" onClick={() => setOpen((o) => !o)}
        className="flex h-8 w-full items-center justify-between gap-2 rounded-md border border-input bg-background px-3 text-sm shadow-sm outline-none transition-[color,box-shadow] focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50">
        <span className={cn('truncate text-xs', sel.length ? 'text-foreground' : 'text-muted-foreground')}>{sel.length ? `${sel.length} selected — ${sel.join(', ')}` : 'none — click to choose'}</span>
        <ChevronDown className={cn('h-4 w-4 shrink-0 opacity-50 transition-transform', open && 'rotate-180')} />
      </button>
      {open && (
        <div className="absolute z-50 mt-1 w-full overflow-hidden rounded-md border border-border bg-popover shadow-md">
          <div className="border-b border-border p-1.5">
            <Input autoFocus className="h-7 font-mono text-xs" value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); if (canAdd) addCustom() } }} placeholder="filter or add…" />
          </div>
          <div className="max-h-52 overflow-y-auto p-1">
            {filtered.map((o) => (
              <button key={o} type="button" onClick={() => toggle(o)} className="flex w-full items-center gap-2 rounded-sm px-2 py-1 text-left text-xs hover:bg-accent">
                <span className={cn('grid h-3.5 w-3.5 shrink-0 place-items-center rounded-[3px] border', selSet.has(o) ? 'border-primary bg-primary text-primary-foreground' : 'border-input')}>{selSet.has(o) && <Check className="h-2.5 w-2.5" />}</span>
                <span className="font-mono">{o}</span>
              </button>
            ))}
            {canAdd && (
              <button type="button" onClick={addCustom} className="flex w-full items-center gap-1.5 rounded-sm px-2 py-1 text-left text-xs text-primary hover:bg-accent">
                <Plus className="h-3 w-3" />Add “{norm(q)}”
              </button>
            )}
            {filtered.length === 0 && !canAdd && <p className="px-2 py-1.5 text-xs text-muted-foreground">no matches</p>}
          </div>
        </div>
      )}
    </div>
  )
}

// PathList — multiple paths: removable chips + manual entry + a picker. Browses the
// given host mounts; `multi` picks files (e.g. anchor files), else a folder.
function PathList({ value, onChange, placeholder, disks, multi }: {
  value: string[]; onChange: (v: string[]) => void; placeholder?: string
  disks?: readonly string[]; multi?: boolean
}) {
  const [pick, setPick] = useState(false)
  const [manual, setManual] = useState('')
  const add = (p: string) => { p = p.trim(); if (p && !value.includes(p)) onChange([...value, p]) }
  return (
    <div className="space-y-1.5">
      {value.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {value.map((p) => (
            <span key={p} className="inline-flex max-w-full items-center gap-1 rounded-md border border-border bg-muted px-1.5 py-0.5 font-mono text-[11px] text-foreground">
              <span className="truncate">{p}</span>
              <button type="button" onClick={() => onChange(value.filter((x) => x !== p))} className="shrink-0 text-muted-foreground hover:text-destructive"><X className="h-3 w-3" /></button>
            </span>
          ))}
        </div>
      )}
      <div className="flex gap-1.5">
        <Input className="h-7 min-w-0 flex-1 font-mono text-xs" value={manual} onChange={(e) => setManual(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); add(manual); setManual('') } }} placeholder={placeholder} />
        <Button size="sm" variant="outline" className="h-7 shrink-0 gap-1" onClick={() => setPick(true)}><FolderInput className="h-3.5 w-3.5" />Pick</Button>
      </div>
      {pick && (
        <PathPicker mode={multi ? 'multi' : 'folder'} disks={disks ?? ['/mnt/local']} hideRclone
          onClose={() => setPick(false)}
          onPick={(items) => { items.forEach((it) => add(it.path)); setPick(false) }} />
      )}
    </div>
  )
}

const STAT_TONE: Record<string, string> = {
  pending: 'text-warning', scanning: 'text-primary', completed: 'text-success', failed: 'text-destructive',
}
// INBOUND_META colours the result of the last webhook the endpoint received.
const INBOUND_META: Record<string, { cls: string; Icon: typeof Clock; label: string }> = {
  accepted: { cls: 'text-success', Icon: CheckCircle2, label: 'accepted' },
  test: { cls: 'text-success', Icon: CheckCircle2, label: 'test OK' },
  ignored: { cls: 'text-muted-foreground', Icon: MinusCircle, label: 'ignored' },
  disabled: { cls: 'text-warning', Icon: MinusCircle, label: 'autoscan off' },
  unauthorized: { cls: 'text-destructive', Icon: XCircle, label: 'auth failed' },
}
function fmtAgo(iso: string, now: number): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000))
  if (s < 60) return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}
function InboundIndicator({ hook, now }: { hook?: InboundHook | null; now: number }) {
  if (!hook) return (
    <p className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
      <Webhook className="h-3.5 w-3.5" />No webhook received yet — nothing has hit this endpoint since the last restart.
    </p>
  )
  const m = INBOUND_META[hook.result] ?? { cls: 'text-muted-foreground', Icon: Webhook, label: hook.result }
  return (
    <div className="text-[10px]">
      <p className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
        <span className="text-muted-foreground">Last inbound:</span>
        <span className={cn('inline-flex items-center gap-1 font-medium capitalize', m.cls)}><m.Icon className="h-3.5 w-3.5" />{m.label}</span>
        <span className="font-medium text-foreground capitalize">{hook.source}</span>
        {hook.event && <span className="text-muted-foreground">{hook.event}</span>}
        <span className="text-muted-foreground/70">· {fmtAgo(hook.at, now)}</span>
        {hook.remote && <span className="font-mono text-muted-foreground/70">· {hook.remote}</span>}
      </p>
      {hook.detail && <p className="mt-0.5 text-muted-foreground">{hook.detail}</p>}
      {hook.result === 'unauthorized' && <p className="mt-0.5 text-destructive">The *arr reached us but the token/password was wrong — copy the token again or re-paste the URL.</p>}
    </div>
  )
}

// CapturedInbound is the big confirmation shown after "Listen for *arr" catches a hit —
// it's the real webhook from the *arr and exactly how we replied.
function CapturedInbound({ hook, now }: { hook: InboundHook; now: number }) {
  const ok = hook.result !== 'unauthorized'
  const m = INBOUND_META[hook.result] ?? { cls: 'text-muted-foreground', Icon: Webhook, label: hook.result }
  return (
    <div className={cn('rounded border p-2.5 text-[10px]', ok ? 'border-success/40 bg-success/5' : 'border-destructive/40 bg-destructive/5')}>
      <p className={cn('flex items-center gap-1.5 text-[11px] font-medium', ok ? 'text-success' : 'text-destructive')}>
        {ok ? <CheckCircle2 className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
        Received a webhook from <span className="capitalize">{hook.source}</span>{hook.event ? ` (${hook.event})` : ''}
      </p>
      <p className="mt-1 text-muted-foreground">
        We replied <span className={cn('font-mono font-medium', ok ? 'text-success' : 'text-destructive')}>HTTP {hook.code ?? (ok ? 200 : 403)}</span>
        <span className="capitalize"> · {m.label}</span>
        <span className="text-muted-foreground/70"> · {fmtAgo(hook.at, now)}</span>
        {hook.remote && <span className="font-mono text-muted-foreground/70"> · from {hook.remote}</span>}
      </p>
      {hook.detail && <p className="mt-0.5 text-muted-foreground">{hook.detail}</p>}
      {!ok && <p className="mt-1 text-destructive">The *arr reached us but the token/password was wrong — re-copy the token or re-paste the URL, then listen again.</p>}
      {ok && <p className="mt-1 text-muted-foreground/70">Connection confirmed — the *arr can reach sb-ui and we accept its webhooks.</p>}
    </div>
  )
}

// ConnRow renders one known *arr connection: its two health dimensions (API probe +
// last inbound webhook) plus the "why" note when something is wrong.
const HEALTH_META: Record<string, { cls: string; dot: string; Icon: typeof Clock; label: string }> = {
  ok: { cls: 'text-success', dot: 'bg-success', Icon: CheckCircle2, label: 'API reachable' },
  fail: { cls: 'text-destructive', dot: 'bg-destructive', Icon: XCircle, label: 'API unreachable' },
  unknown: { cls: 'text-muted-foreground', dot: 'bg-muted-foreground/50', Icon: MinusCircle, label: 'not checked' },
}
function ConnRow({ c, now, onWire, wiring, result }: { c: ConnLink; now: number; onWire: (key: string) => void; wiring: boolean; result?: WireResult }) {
  const h = HEALTH_META[c.health] ?? HEALTH_META.unknown
  const authFail = c.last_result === 'unauthorized'
  return (
    <div className="rounded-lg border border-border p-2.5">
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <span className={cn('h-2 w-2 shrink-0 rounded-full', h.dot)} />
          <span className="truncate text-sm font-medium capitalize text-foreground">{c.instance || c.source}</span>
          <span className="shrink-0 rounded bg-secondary px-1.5 py-0.5 text-[10px] font-medium capitalize text-muted-foreground">{c.source}</span>
          {!c.matched && <span className="shrink-0 rounded bg-warning/15 px-1.5 py-0.5 text-[10px] font-medium text-warning">unknown sender</span>}
        </div>
        <span className={cn('flex shrink-0 items-center gap-1 text-[11px] font-medium', h.cls)}><h.Icon className="h-3.5 w-3.5" />{h.label}</span>
      </div>
      <div className="mt-1.5 space-y-0.5 pl-4 text-[10px]">
        <p className="text-muted-foreground">
          <span className="text-foreground">API</span> (sb-ui → arr): {c.health === 'ok' ? <span className="text-success">{c.health_note || 'ok'}</span> : c.health === 'fail' ? <span className="text-destructive">{c.health_note || 'unreachable'}</span> : <span>not checked yet</span>}
          {c.health_at && <span className="text-muted-foreground/60"> · {fmtAgo(c.health_at, now)}</span>}
        </p>
        <p className="text-muted-foreground">
          <span className="text-foreground">Inbound</span> (arr → sb-ui): {c.last_seen
            ? <>{authFail ? <span className="text-destructive">rejected (token)</span> : <span className="text-success">{c.last_result || 'received'}</span>} · {fmtAgo(c.last_seen, now)} · {c.hits}×</>
            : <span className="text-muted-foreground/60">no webhook yet</span>}
        </p>
      </div>
      {(c.probe_url || c.remote) && (
        <p className="mt-1 truncate pl-4 font-mono text-[10px] text-muted-foreground/60">{c.probe_url || (c.remote && `from ${c.remote}`)}</p>
      )}

      {/* auto-wire: only for arrs whose API we can reach (matched) */}
      {c.matched && c.health === 'ok' && (
        <div className="mt-2 border-t border-border/60 pt-2">
          <div className="flex items-center justify-between gap-2">
            <p className="text-[10px] text-muted-foreground">Set up its webhook via the arr API</p>
            <Button size="sm" variant="outline" className="h-6 gap-1.5 px-2 text-[11px]" onClick={() => onWire(c.key)} disabled={wiring}>
              {wiring ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Zap className="h-3.5 w-3.5" />}Wire &amp; test
            </Button>
          </div>
          {result && <WireOutcome r={result} />}
        </div>
      )}
    </div>
  )
}

// WireOutcome shows the result of auto-wiring: the URL that worked (and whether it was
// saved), or every candidate that failed with the *arr's own reason.
function WireOutcome({ r }: { r: WireResult }) {
  if (r.working) return (
    <div className="mt-1.5 rounded border border-success/40 bg-success/5 p-2 text-[10px]">
      <p className="flex items-center gap-1.5 font-medium text-success"><CheckCircle2 className="h-3.5 w-3.5" />Webhook reachable{r.saved ? ' — saved to the arr' : ''}</p>
      <p className="mt-0.5 break-all font-mono text-muted-foreground">{r.working}</p>
      {r.save_error && <p className="mt-0.5 text-destructive">Saved test passed but write failed: {r.save_error}</p>}
    </div>
  )
  return (
    <div className="mt-1.5 rounded border border-destructive/40 bg-destructive/5 p-2 text-[10px]">
      <p className="flex items-center gap-1.5 font-medium text-destructive"><XCircle className="h-3.5 w-3.5" />No reachable URL found</p>
      {r.error && <p className="mt-0.5 text-muted-foreground">{r.error}</p>}
      {r.candidates?.map((cand) => (
        <p key={cand.url} className="mt-0.5 break-all text-muted-foreground/80"><span className="font-mono">{cand.url}</span> — <span className="text-destructive">{cand.error || 'failed'}</span></p>
      ))}
    </div>
  )
}

function StatCard({ label, value, tone }: { label: string; value: number; tone: string }) {
  return (
    <div className="rounded-lg border border-border bg-card px-3 py-2.5">
      <p className="text-[11px] font-medium text-muted-foreground">{label}</p>
      <p className={cn('mt-0.5 text-2xl font-bold tabular-nums', STAT_TONE[tone])}>{value}</p>
    </div>
  )
}

const STATUS_META: Record<ScanStatus, { cls: string; Icon: typeof Clock }> = {
  pending: { cls: 'bg-warning/15 text-warning', Icon: Clock },
  scanning: { cls: 'bg-primary/15 text-primary', Icon: Loader2 },
  completed: { cls: 'bg-success/15 text-success', Icon: CheckCircle2 },
  skipped: { cls: 'bg-muted text-muted-foreground', Icon: MinusCircle },
  ignored: { cls: 'bg-muted text-muted-foreground/70', Icon: MinusCircle },
  failed: { cls: 'bg-destructive/15 text-destructive', Icon: XCircle },
}
function StatusPill({ status, error }: { status: ScanStatus; error?: string }) {
  const { cls, Icon } = STATUS_META[status]
  return (
    <span title={error} className={cn('inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium capitalize', cls)}>
      <Icon className={cn('h-3 w-3', status === 'scanning' && 'animate-spin')} />{status}
    </span>
  )
}
