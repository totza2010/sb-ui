/**
 * Autoscan — built-in Plex partial-scan service (replaces the external autoscan
 * container). Configure the debounce, post-upload scanning, and the arr webhook URL;
 * watch recent scans live. Backend: /api/autoscan/* (see docs/autoscan-plan.md).
 */
import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useAutoscanConfig, useSaveAutoscanConfig, useAutoscanStatus, useAutoscanTrigger, useAutoscanClear, type AutoscanConfig, type ScanStatus } from '@/lib/api'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { cn } from '@/lib/cn'
import { ScanLine, Save, Copy, Check, RefreshCw, Play, Loader2, Webhook, Zap, Trash2, Clock, CheckCircle2, XCircle, MinusCircle, Filter, ChevronDown, ChevronRight, Plus, X, FolderInput, SlidersHorizontal } from 'lucide-react'
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

  const [cfg, setCfg] = useState<AutoscanConfig>(EMPTY)
  const [saved, setSaved] = useState(false)
  const [testPath, setTestPath] = useState('')
  const [filter, setFilter] = useState<'all' | ScanStatus>('all')
  const [openRows, setOpenRows] = useState<Record<number, boolean>>({})

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
          <span className={cn('flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium', cfg.enabled ? 'bg-success/15 text-success' : 'bg-muted text-muted-foreground')}>
            <span className={cn('h-2 w-2 rounded-full', cfg.enabled ? 'bg-success' : 'bg-muted-foreground/50')} />{cfg.enabled ? 'Active' : 'Disabled'}
          </span>
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
                <span className="w-28 shrink-0">Status</span>
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
                        <span className="w-28 shrink-0"><StatusPill status={r.status} error={r.error} /></span>
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
                  <Label className="flex items-center gap-1 text-[11px]"><Zap className="h-3 w-3" />Scan after upload</Label>
                  <div className="flex h-8 items-center">
                    <Switch checked={cfg.on_upload} onCheckedChange={(v) => up('on_upload', v)} />
                  </div>
                  <p className="text-[10px] text-muted-foreground">When the Uploader moves files, scan the moved paths (needs a path mapping to the Plex side).</p>
                </div>
              </div>

              <label className="flex items-center justify-between gap-3 border-t border-border/60 pt-3">
                <span className="text-sm text-foreground">Log skipped webhooks
                  <span className="mt-0.5 block text-[11px] font-normal text-muted-foreground">Debug — also record events we don't scan (Grab, series-level rename, …) in the history, to see exactly what each *arr sends.</span>
                </span>
                <Switch checked={!!cfg.log_skipped} onCheckedChange={(v) => up('log_skipped', v)} />
              </label>
            </Card>

            {/* webhook */}
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
        </Card>
      </div>

      {/* filters */}
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

// PathList — multiple filter paths: removable chips + manual entry + a folder picker
// (browses the local/merged mounts, returns an absolute path).
function PathList({ value, onChange, placeholder }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
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
        <PathPicker mode="folder" disks={['/mnt/local']} hideRclone
          onClose={() => setPick(false)}
          onPick={(items) => { if (items[0]) add(items[0].path); setPick(false) }} />
      )}
    </div>
  )
}

const STAT_TONE: Record<string, string> = {
  pending: 'text-warning', scanning: 'text-primary', completed: 'text-success', failed: 'text-destructive',
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
