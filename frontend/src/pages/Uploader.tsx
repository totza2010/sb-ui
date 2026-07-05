/**
 * Uploader — cloudplow++ : watch a local staging folder and, once it grows past a
 * threshold, move it up to cloud remotes, rotating across them with per-remote
 * daily caps + cooldowns to dodge quotas / bans.
 */
import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useUploader, useSaveUploader, useUploaderStatus, useUploaderRun, useUploaderSimulate, useUploaderCalibration, useUploaderTestBlock, useRcloneRemotes, type UploaderConfig, type UploaderRemote } from '@/lib/api'
import { PathPicker } from '@/components/PathPicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Card } from '@/components/ui/card'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { cn } from '@/lib/cn'
import { Save, Play, FolderInput, CloudUpload, FlaskConical, Loader2, ArrowRight, Pause, Ban, Clock, HardDrive, Server, SlidersHorizontal, Gauge, Route, Film, ScanLine, Magnet, Download, ArrowRightLeft, Zap, ChevronDown, ChevronRight, Settings2 } from 'lucide-react'
import { TransferOptions } from '@/components/TransferOptions'
import { Progress } from '@/components/ui/progress'
import { Checkbox } from '@/components/ui/checkbox'
import { Slider } from '@/components/ui/slider'
import { TransfersPanel, TransfersActivity } from '@/pages/Transfers'

const fmtDur = (min: number) => {
  if (min < 60) return `${min}m`
  const h = Math.floor(min / 60), m = min % 60
  if (h < 24) return m ? `${h}h ${m}m` : `${h}h`
  const d = Math.floor(h / 24), hh = h % 24
  return hh ? `${d}d ${hh}h` : `${d}d`
}
const fmtWhen = (iso: string) => new Date(iso).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
const fmtUntil = (a: string, b: string) => fmtDur(Math.round((new Date(b).getTime() - new Date(a).getTime()) / 60000))

// rough size→bytes for progress bars (rclone-style suffixes; bare number = GB elsewhere)
const parseSize = (s?: string): number => {
  if (!s) return 0
  const m = /^\s*([\d.]+)\s*([KMGTP]?)/i.exec(s)
  if (!m) return 0
  const n = parseFloat(m[1]); if (!isFinite(n)) return 0
  return n * Math.pow(1024, { '': 0, K: 1, M: 2, G: 3, T: 4, P: 5 }[m[2].toUpperCase()] ?? 0)
}

const EMPTY_PAUSE = { arr_disable: false, plex_kill_transcode: false, autoscan_hold: false, qbit: { enabled: false, action: 'pause' as const, dl_kbps: 0, up_kbps: 0 } }
const EMPTY: UploaderConfig = {
  enabled: false, source: '', threshold: '500G', strategy: 'lru',
  balance: { enabled: false, max_streak: 3, no_repeat: true }, pause: EMPTY_PAUSE, interval_minutes: 15,
  allowed_from: '', allowed_until: '', min_age: '15m', delete_empty_src: false,
  opts: { exclude: ['**partial~', '**_HIDDEN~', '.unionfs*/**', '**.fuse_hidden**'] }, remotes: [],
}
const emptyRemote: UploaderRemote = { name: '', dest: '', cap: '', cap_files: 0, gap_min: 0, bwlimit: '', tpslimit: 0 }
// Design-time tooling (Simulate dry-run, Test block) is shown only in dev; a production
// build (npm run build → embedded binary) ships just the working controls.
const isDev = import.meta.env.DEV

// TimeRange — "from – until" as two Origin <Input type="time"> fields. Native time
// inputs keep the browser clock picker (click to open) and full keyboard entry.
function TimeRange({ from, until, onFrom, onUntil }: { from: string; until: string; onFrom: (v: string) => void; onUntil: (v: string) => void }) {
  return (
    <div className="flex items-center gap-1.5">
      <Input type="time" aria-label="From" className="min-w-0 flex-1 px-2" value={from} onChange={(e) => onFrom(e.target.value)} />
      <span className="shrink-0 text-xs text-muted-foreground">–</span>
      <Input type="time" aria-label="Until" className="min-w-0 flex-1 px-2" value={until} onChange={(e) => onUntil(e.target.value)} />
    </div>
  )
}


// AutoUploadPanel — the automatic staging→cloud rotation (cloudplow++). Rendered as
// the "Auto-upload" tab of the central Uploader hub below.
function AutoUploadPanel() {
  const qc = useQueryClient()
  const { data } = useUploader()
  const save = useSaveUploader()
  const run = useUploaderRun()
  const testBlock = useUploaderTestBlock()
  const { data: status } = useUploaderStatus()
  const { data: rcRemotes } = useRcloneRemotes()
  const remoteNames = Object.keys(rcRemotes?.remotes ?? {})
  const sim = useUploaderSimulate()
  const { data: calib } = useUploaderCalibration()
  const capLabel = (c?: string) => !c ? '∞' : /[a-zA-Z]$/.test(c) ? c : `${c}G` // bare number = GB
  const capBytes = (c?: string) => !c ? 0 : parseSize(/[a-zA-Z]$/.test(c) ? c : `${c}G`)
  const [simTotal, setSimTotal] = useState('3000G')
  const [simAvg, setSimAvg] = useState('5G')
  const [simPerConn, setSimPerConn] = useState('5M')
  const [simScenario, setSimScenario] = useState('')
  const [simFlood, setSimFlood] = useState('')
  const [cfg, setCfg] = useState<UploaderConfig>(EMPTY)
  const [picker, setPicker] = useState(false)
  const [saved, setSaved] = useState(false)
  const [openDest, setOpenDest] = useState<Record<string, boolean>>({})
  const [subPick, setSubPick] = useState<{ target: 'shared' } | { target: 'remote'; idx: number } | null>(null)

  useEffect(() => { if (data) setCfg({ ...EMPTY, ...data, remotes: data.remotes ?? [] }) }, [data])

  const up = <K extends keyof UploaderConfig>(k: K, v: UploaderConfig[K]) => setCfg((c) => ({ ...c, [k]: v }))
  const bal = cfg.balance ?? { enabled: false, max_streak: 3, no_repeat: true }
  const upBal = (patch: Partial<NonNullable<UploaderConfig['balance']>>) => setCfg((c) => ({ ...c, balance: { ...(c.balance ?? { enabled: false, max_streak: 3, no_repeat: true }), ...patch } }))
  const pause = cfg.pause ?? EMPTY_PAUSE
  const upPause = (patch: Partial<NonNullable<UploaderConfig['pause']>>) => setCfg((c) => ({ ...c, pause: { ...EMPTY_PAUSE, ...(c.pause ?? {}), ...patch } }))
  const upQbit = (patch: Partial<NonNullable<UploaderConfig['pause']>['qbit']>) => setCfg((c) => ({ ...c, pause: { ...EMPTY_PAUSE, ...(c.pause ?? {}), qbit: { ...EMPTY_PAUSE.qbit, ...(c.pause?.qbit ?? {}), ...patch } } }))
  const upRemote = (i: number, patch: Partial<UploaderRemote>) => setCfg((c) => { const r = [...c.remotes]; r[i] = { ...r[i], ...patch }; return { ...c, remotes: r } })
  // toggle a remote in/out of the rotation (each remote is used at most once).
  const toggleDest = (name: string) => setCfg((c) => {
    const i = c.remotes.findIndex((r) => r.name === name)
    return i >= 0
      ? { ...c, remotes: c.remotes.filter((_, j) => j !== i) }
      : { ...c, remotes: [...c.remotes, { ...emptyRemote, name }] }
  })
  const setOpts = (upd: (o: NonNullable<UploaderConfig['opts']>) => NonNullable<UploaderConfig['opts']>) => setCfg((c) => ({ ...c, opts: upd(c.opts ?? {}) }))
  const destTypes = [...new Set(cfg.remotes.map((r) => rcRemotes?.remotes?.[r.name]?.type).filter((t): t is string => !!t))]

  function doSave() {
    save.mutate(cfg, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['uploader'] }); setSaved(true); setTimeout(() => setSaved(false), 2500) } })
  }

  const STRATS: [UploaderConfig['strategy'], string][] = [['lru', 'Least-recently-used'], ['round_robin', 'Round-robin'], ['most_free', 'Most quota free']]
  const pauseCount = [pause.plex_kill_transcode, pause.autoscan_hold, pause.qbit.enabled, pause.arr_disable].filter(Boolean).length
  const srcPct = status ? (parseSize(status.threshold) > 0 ? (parseSize(status.last_size) / parseSize(status.threshold)) * 100 : 0) : 0
  const validDests = cfg.remotes.filter((r) => r.name).length

  return (
    <div className="space-y-5">
      {/* ── panel toolbar ──────────────────────────────────────────────── */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">Auto-move a local folder to the cloud once it fills — spread across remotes, quota/ban-aware.</p>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" className="gap-1.5" onClick={() => run.mutate()} disabled={run.isPending}>
            {run.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}Check now
          </Button>
          <Button size="sm" className="gap-1.5" onClick={doSave} disabled={save.isPending}>
            <Save className="h-3.5 w-3.5" />{saved ? 'Saved ✓' : 'Save'}
          </Button>
        </div>
      </div>

      {/* ── status hero ────────────────────────────────────────────────── */}
      <Card className="overflow-hidden rounded-2xl border-border/70 shadow-md">
        <div className="flex flex-wrap items-center justify-between gap-4 border-b border-border px-4 py-3">
          <div className="flex items-center gap-3">
            <span className={cn('flex items-center gap-2 rounded-full px-2.5 py-1 text-xs font-medium',
              cfg.enabled ? 'bg-success/15 text-success' : 'bg-muted text-muted-foreground')}>
              <span className={cn('h-2 w-2 rounded-full', cfg.enabled ? 'bg-success' : 'bg-muted-foreground/50')} />
              {cfg.enabled ? 'Active' : 'Disabled'}
            </span>
            {status?.last_check && <span className="text-xs text-muted-foreground">Last check {new Date(status.last_check).toLocaleString()}</span>}
            {status?.message && <span className="text-xs italic text-muted-foreground/80">{status.message}</span>}
          </div>
          <label className="flex cursor-pointer items-center gap-2 text-sm text-foreground">
            Enable auto-upload
            <Switch checked={cfg.enabled} onCheckedChange={(v) => up('enabled', v)} />
          </label>
        </div>

        <div className="grid gap-4 p-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
          {/* source fill */}
          <div className="space-y-2 rounded-xl border border-border bg-muted/40 p-3.5">
            <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
              <HardDrive className="h-3.5 w-3.5" />Source fill
            </div>
            <div className="flex items-baseline gap-1.5">
              <span className="text-3xl font-bold tracking-tight text-foreground">{status?.last_size ?? '—'}</span>
              <span className="text-sm text-muted-foreground">/ {status?.threshold || cfg.threshold || '—'}</span>
            </div>
            <Progress value={Math.min(100, srcPct)} className="h-1.5" />
            <p className="truncate font-mono text-[11px] text-muted-foreground/70" title={cfg.source}>{cfg.source || 'no source folder set'}</p>
          </div>

          {/* remote capacity cards */}
          <div className="space-y-2">
            <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
              <Server className="h-3.5 w-3.5" />Remotes <span className="text-muted-foreground/60">({status?.remotes?.length ?? validDests})</span>
            </div>
            {status && status.remotes.length > 0 ? (
              <div className="grid grid-cols-2 gap-2 xl:grid-cols-3">
                {status.remotes.map((r, i) => {
                  const cb = capBytes(r.cap), ub = parseSize(r.used_today)
                  const pct = cb > 0 ? (ub / cb) * 100 : 0
                  return (
                    <div key={r.name || i} className={cn('space-y-1.5 rounded-xl border px-2.5 py-2 transition-all hover:-translate-y-0.5 hover:shadow-md', r.paused_until ? 'border-amber-500/50 bg-amber-500/10' : 'border-border/70 bg-card')}>
                      <div className="flex items-center justify-between gap-1">
                        <span className="truncate text-xs font-semibold text-foreground">{r.name}</span>
                        {r.paused_until && <span className="shrink-0 rounded bg-amber-500/20 px-1 text-[9px] font-medium uppercase text-amber-600 dark:text-amber-400">paused</span>}
                      </div>
                      <Progress value={Math.min(100, pct)} className="h-1.5" />
                      <p className="text-[10px] text-muted-foreground">{r.used_today} / {capLabel(r.cap)}{(r.cap_files ?? 0) > 0 && ` · ${r.files_today ?? 0}/${r.cap_files} files`}</p>
                      <p className="truncate text-[9px] text-muted-foreground/60">{r.paused_until ? `until ${new Date(r.paused_until).toLocaleTimeString()}` : `last ${r.last_upload ? new Date(r.last_upload).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '—'}`}</p>
                    </div>
                  )
                })}
              </div>
            ) : (
              <div className="grid h-[76px] place-items-center rounded-lg border border-dashed border-border text-xs text-muted-foreground">
                {validDests > 0 ? 'No run yet — press “Check now”.' : 'Add destination remotes below.'}
              </div>
            )}
          </div>
        </div>
      </Card>

      {/* ── config tabs ────────────────────────────────────────────────── */}
      <Tabs defaultValue="trigger" className="space-y-4">
        <TabsList className="h-auto flex-wrap gap-1 rounded-xl border border-border/70 bg-card p-1 shadow-sm">
          <TabsTrigger value="trigger" className="gap-1.5"><Gauge className="h-3.5 w-3.5" />Trigger &amp; rotation</TabsTrigger>
          <TabsTrigger value="dest" className="gap-1.5"><Route className="h-3.5 w-3.5" />Destinations{validDests > 0 && <span className="ml-0.5 rounded bg-primary/15 px-1 text-[10px] text-primary">{validDests}</span>}</TabsTrigger>
          <TabsTrigger value="opts" className="gap-1.5"><SlidersHorizontal className="h-3.5 w-3.5" />Transfer options</TabsTrigger>
          <TabsTrigger value="pause" className="gap-1.5"><Pause className="h-3.5 w-3.5" />Pause activity{pauseCount > 0 && <span className="ml-0.5 rounded bg-primary/15 px-1 text-[10px] text-primary">{pauseCount}</span>}</TabsTrigger>
          {isDev && <TabsTrigger value="sim" className="gap-1.5"><FlaskConical className="h-3.5 w-3.5" />Simulate</TabsTrigger>}
        </TabsList>

        {/* ── Trigger & rotation ─────────────────────────────────────── */}
        <TabsContent value="trigger">
          <div className="grid gap-4 md:grid-cols-2">
            <Card className="space-y-3 rounded-xl border-border/70 p-4 shadow-sm">
              <p className="flex items-center gap-1.5 text-sm font-medium text-foreground"><HardDrive className="h-4 w-4 text-muted-foreground" />Source &amp; trigger</p>
              <div className="space-y-1">
                <Label className="text-[11px]">Source folder (local)</Label>
                <div className="flex gap-2">
                  <Input className="h-8 font-mono" value={cfg.source} onChange={(e) => up('source', e.target.value)} placeholder="/mnt/local/Media" />
                  <Button size="sm" variant="outline" className="shrink-0 gap-1.5" onClick={() => setPicker(true)}><FolderInput className="h-3.5 w-3.5" />Pick</Button>
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1">
                  <Label className="text-[11px]">Upload when size ≥</Label>
                  <Input className="h-8" value={cfg.threshold} onChange={(e) => up('threshold', e.target.value)} placeholder="500G" />
                </div>
                <div className="space-y-1.5">
                  <div className="flex items-center justify-between">
                    <Label className="text-[11px]">Check every</Label>
                    <span className="text-[11px] font-medium tabular-nums text-foreground">{cfg.interval_minutes} min</span>
                  </div>
                  <Slider min={1} max={60} step={1} value={[cfg.interval_minutes]} onValueChange={([v]) => up('interval_minutes', Math.max(1, v))} className="py-1.5" />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1">
                  <Label className="text-[11px]">Upload window (off-peak)</Label>
                  <TimeRange from={cfg.allowed_from ?? ''} until={cfg.allowed_until ?? ''} onFrom={(v) => up('allowed_from', v)} onUntil={(v) => up('allowed_until', v)} />
                </div>
                <div className="space-y-1">
                  <Label className="text-[11px]">Min file age</Label>
                  <div className="flex items-center gap-3">
                    <Input className="h-8 w-20" value={cfg.min_age ?? ''} onChange={(e) => up('min_age', e.target.value)} placeholder="15m" />
                    <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                      <Checkbox checked={!!cfg.delete_empty_src} onCheckedChange={(v) => up('delete_empty_src', v === true)} />
                      Delete empty
                    </label>
                  </div>
                </div>
              </div>
            </Card>

            <Card className="space-y-3 rounded-xl border-border/70 p-4 shadow-sm">
              <p className="flex items-center gap-1.5 text-sm font-medium text-foreground"><Route className="h-4 w-4 text-muted-foreground" />Rotation strategy</p>
              <div className="space-y-1">
                <Label className="text-[11px]">Which remote goes next</Label>
                <div className="flex flex-wrap gap-1.5">
                  {STRATS.map(([s, lbl]) => (
                    <Button key={s} size="sm" variant={cfg.strategy === s ? 'default' : 'outline'} disabled={bal.enabled} onClick={() => up('strategy', s)}>{lbl}</Button>
                  ))}
                </div>
                {bal.enabled && <p className="text-[10px] text-muted-foreground">Overridden by capacity balancing below.</p>}
              </div>
              <div className={cn('space-y-2 rounded-md border p-3', bal.enabled ? 'border-primary/40 bg-primary/5' : 'border-border')}>
                <label className="flex items-start justify-between gap-3">
                  <span className="text-sm text-foreground">Capacity balancing
                    <span className="mt-0.5 block text-[11px] text-muted-foreground">Fill the emptiest account first (level them up), never the same remote twice in a row, and slot in a periodic upload to a fuller account so no remote gets hammered.</span>
                  </span>
                  <Switch checked={bal.enabled} onCheckedChange={(v) => upBal({ enabled: v })} className="mt-0.5" />
                </label>
                {bal.enabled && (
                  <div className="space-y-2 pt-1">
                    <div className="space-y-1.5">
                      <div className="flex items-center justify-between">
                        <Label className="text-[11px]">Uploads before a relief pick</Label>
                        <span className="text-[11px] font-medium tabular-nums text-foreground">{bal.max_streak}</span>
                      </div>
                      <Slider min={1} max={10} step={1} value={[bal.max_streak]} onValueChange={([v]) => upBal({ max_streak: Math.max(1, v) })} className="py-1.5" />
                      <p className="text-[10px] text-muted-foreground">~1 in {bal.max_streak + 1} uploads goes to a fuller account</p>
                    </div>
                    <label className="flex items-center gap-2 text-xs text-muted-foreground">
                      <Checkbox checked={bal.no_repeat} onCheckedChange={(v) => upBal({ no_repeat: v === true })} />
                      Never the same remote twice in a row
                    </label>
                  </div>
                )}
              </div>
            </Card>
          </div>
        </TabsContent>

        {/* ── Destinations ───────────────────────────────────────────── */}
        <TabsContent value="dest">
          <Card className="space-y-3 rounded-xl border-border/70 p-4 shadow-sm">
            <div className="flex items-center justify-between gap-2">
              <p className="flex items-center gap-1.5 text-sm font-medium text-foreground"><Route className="h-4 w-4 text-muted-foreground" />Destination remotes <span className="font-normal text-muted-foreground">(rotated)</span></p>
              <span className="text-[11px] text-muted-foreground">{cfg.remotes.filter((r) => r.name).length} selected</span>
            </div>

            {/* shared defaults — apply to every destination unless a row overrides them */}
            <div className="space-y-1.5 rounded-md border border-border bg-secondary/20 p-2.5">
              <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Shared defaults</p>
              <div className="grid grid-cols-[1fr_auto_auto_auto] gap-1.5">
                <div className="space-y-0.5">
                  <Label className="text-[10px] text-muted-foreground">Subpath</Label>
                  <div className="flex gap-1">
                    <Input className="h-8 font-mono text-xs" value={cfg.subpath ?? ''} onChange={(e) => up('subpath', e.target.value)} placeholder="blank = root · e.g. Media/TV" />
                    <Button size="icon" variant="outline" className="h-8 w-8 shrink-0" title="Pick from merged folder" onClick={() => setSubPick({ target: 'shared' })}><FolderInput className="h-3.5 w-3.5" /></Button>
                  </div>
                </div>
                <div className="space-y-0.5">
                  <Label className="text-[10px] text-muted-foreground" title="Bytes uploaded per 24h before a remote is skipped. Blank = unlimited (teldrive). Google Drive: 700.">Cap GB/day</Label>
                  <Input className="h-8 w-20 text-center" value={cfg.cap ?? ''} onChange={(e) => up('cap', e.target.value)} placeholder="∞" />
                </div>
                <div className="space-y-0.5">
                  <Label className="text-[10px] text-muted-foreground" title="Files (API requests) per 24h before skip. 0 = unlimited. teldrive ban dimension — try 8000–10000.">Files/day</Label>
                  <Input type="number" className="h-8 w-20 text-center" value={cfg.cap_files || ''} onChange={(e) => up('cap_files', Math.max(0, parseInt(e.target.value, 10) || 0))} placeholder="∞" />
                </div>
                <div className="space-y-0.5">
                  <Label className="text-[10px] text-muted-foreground" title="Minimum minutes before reusing a remote (spreads request load). 0 = no wait.">Gap min</Label>
                  <Input type="number" className="h-8 w-16 text-center" value={cfg.gap_min || ''} onChange={(e) => up('gap_min', Math.max(0, parseInt(e.target.value, 10) || 0))} placeholder="0" />
                </div>
              </div>
              <p className="text-[10px] text-muted-foreground">Leave a row's field blank to inherit these. <span className="text-foreground">Cap GB</span>: daily upload cap (∞ = unlimited; Google Drive 700). <span className="text-foreground">Files</span>: daily request cap (teldrive ban limit ~8000–10000). <span className="text-foreground">Gap</span>: minutes before reusing a remote.</p>
            </div>

            {/* remote checklist — tick to include in the rotation, expand to override defaults */}
            {remoteNames.length === 0 && cfg.remotes.length === 0 ? (
              <div className="grid place-items-center rounded-md border border-dashed border-border py-6 text-xs text-muted-foreground">No rclone remotes found. Add one on the Files page first.</div>
            ) : (
              <div className="grid grid-cols-1 items-start gap-1.5 md:grid-cols-2">
                {[...remoteNames, ...cfg.remotes.map((r) => r.name).filter((n) => n && !remoteNames.includes(n))].map((name) => {
                  const idx = cfg.remotes.findIndex((r) => r.name === name)
                  const sel = idx >= 0
                  const r = sel ? cfg.remotes[idx] : null
                  const type = rcRemotes?.remotes?.[name]?.type
                  const eff = ((r?.dest) || cfg.subpath || '').replace(/^\//, '')
                  const overridden = sel && !!(r!.dest || r!.cap || r!.cap_files || r!.gap_min)
                  const open = !!openDest[name]
                  return (
                    <div key={name} className={cn('rounded-md border transition-colors', sel ? 'border-primary/40 bg-primary/5' : 'border-border')}>
                      <div className="flex items-center gap-2.5 px-2.5 py-2">
                        <Checkbox checked={sel} onCheckedChange={() => toggleDest(name)} />
                        <div className="flex min-w-0 flex-1 items-center gap-2">
                          <span className="shrink-0 text-sm font-medium text-foreground">{name}</span>
                          {type && <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{type}</span>}
                          {sel && <span className="truncate font-mono text-[11px] text-muted-foreground/80">→ {name}:{eff}</span>}
                        </div>
                        {sel && (
                          <button type="button" onClick={() => setOpenDest((o) => ({ ...o, [name]: !o[name] }))}
                            className={cn('flex shrink-0 items-center gap-1 rounded px-1.5 py-1 text-[11px] font-medium', overridden ? 'text-primary' : 'text-muted-foreground hover:text-foreground')}>
                            <Settings2 className="h-3.5 w-3.5" />{overridden ? 'custom' : 'defaults'}
                            {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                          </button>
                        )}
                      </div>
                      {sel && open && (
                        <div className="space-y-2 border-t border-border/60 px-2.5 py-2">
                          <div className="space-y-0.5">
                            <Label className="text-[10px] text-muted-foreground">Subpath override</Label>
                            <div className="flex gap-1">
                              <Input className="h-8 font-mono text-xs" value={r!.dest} onChange={(e) => upRemote(idx, { dest: e.target.value })} placeholder={cfg.subpath ? `= ${cfg.subpath}` : '= root (shared)'} />
                              <Button size="icon" variant="outline" className="h-8 w-8 shrink-0" title="Pick from merged folder" onClick={() => setSubPick({ target: 'remote', idx })}><FolderInput className="h-3.5 w-3.5" /></Button>
                            </div>
                          </div>
                          <div className="flex gap-2">
                            <div className="flex-1 space-y-0.5">
                              <Label className="text-[10px] text-muted-foreground">Cap GB</Label>
                              <Input className="h-8 text-center" value={r!.cap} onChange={(e) => upRemote(idx, { cap: e.target.value })} placeholder={cfg.cap || '∞'} />
                            </div>
                            <div className="flex-1 space-y-0.5">
                              <Label className="text-[10px] text-muted-foreground">Files</Label>
                              <Input type="number" className="h-8 text-center" value={r!.cap_files || ''} onChange={(e) => upRemote(idx, { cap_files: Math.max(0, parseInt(e.target.value, 10) || 0) })} placeholder={cfg.cap_files ? String(cfg.cap_files) : '∞'} />
                            </div>
                            <div className="flex-1 space-y-0.5">
                              <Label className="text-[10px] text-muted-foreground">Gap min</Label>
                              <Input type="number" className="h-8 text-center" value={r!.gap_min || ''} onChange={(e) => upRemote(idx, { gap_min: Math.max(0, parseInt(e.target.value, 10) || 0) })} placeholder={cfg.gap_min ? String(cfg.gap_min) : '0'} />
                            </div>
                          </div>
                          <p className="text-[10px] text-muted-foreground">Blank inherits the shared default above.</p>
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
            )}
            <p className="text-[11px] text-muted-foreground">Tick remotes to rotate the <span className="text-foreground">Source folder</span> across them (each used at most once). Open <span className="text-foreground">defaults</span> to override cap / files / gap / subpath per remote; the <span className="font-mono">→</span> line shows each real destination.</p>
          </Card>
        </TabsContent>

        {/* ── Transfer options ───────────────────────────────────────── */}
        <TabsContent value="opts">
          <Card className="space-y-2 rounded-xl border-border/70 p-4 shadow-sm">
            <p className="flex items-center gap-1.5 text-sm font-medium text-foreground"><SlidersHorizontal className="h-4 w-4 text-muted-foreground" />Transfer options <span className="font-normal text-muted-foreground">— rclone flags · include/exclude (applied to every destination)</span></p>
            <TransferOptions opts={cfg.opts ?? {}} setOpts={setOpts} remoteTypes={destTypes} />
          </Card>
        </TabsContent>

        {/* ── Pause activity ─────────────────────────────────────────── */}
        <TabsContent value="pause">
          <Card className="space-y-3 rounded-xl border-border/70 p-4 shadow-sm">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <p className="flex items-center gap-1.5 text-sm font-medium text-foreground"><Pause className="h-4 w-4 text-muted-foreground" />Pause other activity while uploading</p>
                <p className="mt-0.5 text-[11px] text-muted-foreground">Upload is the priority — these free up resources / stop new files landing in the folder being moved.</p>
              </div>
              {isDev && (
                <div className="flex items-center gap-1.5">
                  <Button size="sm" variant="outline" className="h-7 gap-1.5" disabled={testBlock.isPending} onClick={() => testBlock.mutate({ action: 'apply', pause })}>
                    {testBlock.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <FlaskConical className="h-3.5 w-3.5" />}Test block
                  </Button>
                  <Button size="sm" variant="ghost" className="h-7" disabled={testBlock.isPending} onClick={() => testBlock.mutate({ action: 'restore', pause })}>Restore</Button>
                </div>
              )}
            </div>
            {isDev && testBlock.data && (
              <div className="rounded-md border border-border bg-secondary/30 px-2.5 py-1.5 text-[11px] text-muted-foreground">
                <span className="font-medium capitalize text-foreground">{testBlock.data.action}ed</span> — qBit: <span className="text-foreground">{testBlock.data.qbit}</span> · arr: <span className="text-foreground">{testBlock.data.arr}</span> · Plex: <span className="text-foreground">{testBlock.data.plex}</span> · autoscan: <span className="text-foreground">{testBlock.data.autoscan}</span>
                <span className="block text-[10px]">Check the services to confirm, then press Restore.</span>
              </div>
            )}
            {isDev && testBlock.isError && <p className="text-[11px] text-destructive">{testBlock.error.message}</p>}
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
              <div className={cn('space-y-2 rounded-md border p-3', pause.plex_kill_transcode ? 'border-primary/40 bg-primary/5' : 'border-border')}>
                <label className="flex items-center justify-between gap-2">
                  <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground"><Film className="h-3.5 w-3.5 text-muted-foreground" />Plex</span>
                  <Switch checked={pause.plex_kill_transcode} onCheckedChange={(v) => upPause({ plex_kill_transcode: v })} />
                </label>
                <p className="text-xs text-foreground">Stop transcodes while uploading</p>
                <p className="text-[10px] text-muted-foreground">Terminates only transcoding sessions (frees CPU/disk for the upload); direct-play streams keep playing. Kicks recur through the run.</p>
              </div>

              <div className={cn('space-y-2 rounded-md border p-3', pause.autoscan_hold ? 'border-primary/40 bg-primary/5' : 'border-border')}>
                <label className="flex items-center justify-between gap-2">
                  <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground"><ScanLine className="h-3.5 w-3.5 text-muted-foreground" />Autoscan</span>
                  <Switch checked={pause.autoscan_hold} onCheckedChange={(v) => upPause({ autoscan_hold: v })} />
                </label>
                <p className="text-xs text-foreground">Hold scans while uploading</p>
                <p className="text-[10px] text-muted-foreground">Pauses the autoscan container during the run so it won't scan the folder being moved; unpaused after (queued scans then proceed).</p>
              </div>

              <div className={cn('space-y-2 rounded-md border p-3', pause.qbit.enabled ? 'border-primary/40 bg-primary/5' : 'border-border')}>
                <label className="flex items-center justify-between gap-2">
                  <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground"><Magnet className="h-3.5 w-3.5 text-muted-foreground" />qBittorrent</span>
                  <Switch checked={pause.qbit.enabled} onCheckedChange={(v) => upQbit({ enabled: v })} />
                </label>
                <p className="text-xs text-foreground">Slow down while uploading</p>
                {pause.qbit.enabled ? (
                  <div className="space-y-1.5">
                    <div className="flex rounded-md border border-border p-0.5">
                      {([['pause', 'Pause downloads'], ['throttle', 'Throttle']] as const).map(([a, lbl]) => (
                        <button key={a} onClick={() => upQbit({ action: a })} className={cn('flex-1 rounded px-2 py-0.5 text-[11px] font-medium', pause.qbit.action === a ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground')}>{lbl}</button>
                      ))}
                    </div>
                    {pause.qbit.action === 'throttle' && (
                      <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                        <Input type="number" min={0} className="h-7 w-16" value={pause.qbit.dl_kbps} onChange={(e) => upQbit({ dl_kbps: Math.max(0, parseInt(e.target.value, 10) || 0) })} />↓
                        <Input type="number" min={0} className="h-7 w-16" value={pause.qbit.up_kbps} onChange={(e) => upQbit({ up_kbps: Math.max(0, parseInt(e.target.value, 10) || 0) })} />↑ KB/s (0=∞)
                      </div>
                    )}
                    <p className="text-[10px] text-muted-foreground">{pause.qbit.action === 'pause' ? 'Pauses only downloading torrents (seeders keep seeding, ratio untouched) so nothing new completes/imports; resumes them after.' : 'Caps global up/down speeds during the run, restores after.'}</p>
                    <p className="text-[10px] text-muted-foreground/70">Connection (URL + login) is set on the <span className="font-medium text-foreground">Integrations</span> page.</p>
                  </div>
                ) : (
                  <p className="text-[10px] text-muted-foreground">Pause only downloading torrents, or throttle global speeds, during the run.</p>
                )}
              </div>

              <div className={cn('space-y-2 rounded-md border p-3', pause.arr_disable ? 'border-primary/40 bg-primary/5' : 'border-border')}>
                <label className="flex items-center justify-between gap-2">
                  <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground"><Download className="h-3.5 w-3.5 text-muted-foreground" />Sonarr / Radarr</span>
                  <Switch checked={pause.arr_disable} onCheckedChange={(v) => upPause({ arr_disable: v })} />
                </label>
                <p className="text-xs text-foreground">Pause imports while uploading</p>
                <p className="text-[10px] text-muted-foreground">Turns off Completed Download Handling (auto-import) in each *arr during the run, so no files are imported into the folder being moved. Downloading continues; re-enabled after.</p>
              </div>
            </div>
          </Card>
        </TabsContent>

        {/* ── Simulate (dev only) ────────────────────────────────────── */}
        {isDev && (
          <TabsContent value="sim">
            <Card className="space-y-3 rounded-xl border-border/70 p-4 shadow-sm">
              <div className="flex items-center gap-2">
                <FlaskConical className="h-4 w-4 text-primary" />
                <h2 className="text-sm font-semibold text-foreground">Simulate rotation (dry-run)</h2>
              </div>
              <p className="-mt-1 text-[11px] text-muted-foreground">Drains a backlog across your <span className="text-foreground">current</span> remotes (no need to Save) — caps, gaps, window &amp; rate-limit pauses included. Upload speed = task <span className="font-mono">bwlimit</span>, else <span className="font-mono">measured</span> throughput from past runs, else <span className="font-mono">transfers × upload_concurrency</span> × the per-connection speed below. Nothing is uploaded.</p>
              {calib && calib.length > 0 && (
                <p className="-mt-1 text-[11px] text-muted-foreground">Measured: {calib.map((c) => `${c.remote} ~${c.avg_speed}/s${c.throttle_rate > 0 ? ` (${Math.round(c.throttle_rate * 100)}% throttled)` : ''}`).join(' · ')}</p>
              )}
              <div className="flex flex-wrap items-end gap-2">
                <div className="space-y-1">
                  <Label className="text-[10px] text-muted-foreground">Total to upload</Label>
                  <Input className="h-8 w-28" value={simTotal} onChange={(e) => setSimTotal(e.target.value)} placeholder="3000G" />
                </div>
                <div className="space-y-1">
                  <Label className="text-[10px] text-muted-foreground">Avg file size</Label>
                  <Input className="h-8 w-24" value={simAvg} onChange={(e) => setSimAvg(e.target.value)} placeholder="5G" />
                </div>
                <div className="space-y-1">
                  <Label className="text-[10px] text-muted-foreground">Speed / connection</Label>
                  <Input className="h-8 w-24" value={simPerConn} onChange={(e) => setSimPerConn(e.target.value)} placeholder="5M" />
                </div>
                <div className="space-y-1">
                  <Label className="text-[10px] text-muted-foreground">Event scenario</Label>
                  <select className="h-8 rounded-md border border-border bg-background px-2 text-sm" value={simScenario} onChange={(e) => setSimScenario(e.target.value)}>
                    <option value="">Happy path (no incidents)</option>
                    <option value="flood">Rate-limit one remote</option>
                    <option value="offline">One remote offline</option>
                    <option value="flaky">All remotes flaky (intermittent)</option>
                  </select>
                </div>
                {(simScenario === 'flood' || simScenario === 'offline') && (
                  <div className="space-y-1">
                    <Label className="text-[10px] text-muted-foreground">{simScenario === 'offline' ? 'Which remote is down' : 'Which remote rate-limits'}</Label>
                    <select className="h-8 rounded-md border border-border bg-background px-2 text-sm" value={simFlood} onChange={(e) => setSimFlood(e.target.value)}>
                      <option value="">select…</option>
                      {cfg.remotes.filter((r) => r.name).map((r, i) => <option key={i} value={r.name}>{r.name}</option>)}
                    </select>
                  </div>
                )}
                <Button size="sm" className="gap-1.5" disabled={sim.isPending || cfg.remotes.length === 0} onClick={() => sim.mutate({ total: simTotal, avg_file: simAvg, per_conn: simPerConn, scenario: simScenario, flood_remote: simFlood, config: cfg })}>
                  {sim.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}Run simulation
                </Button>
              </div>

              {sim.data && (
                <div className="space-y-3 pt-1">
                  {/* headline result */}
                  <div className={cn('rounded-md border px-3 py-2 text-sm', sim.data.done ? 'border-success/40 bg-success/10' : 'border-amber-500/40 bg-amber-500/10')}>
                    {sim.data.done
                      ? <>Uploaded <span className="font-medium text-foreground">{sim.data.moved}</span> across {sim.data.summary.length} remotes in <span className="font-medium text-foreground">{fmtDur(sim.data.elapsed_min)}</span>.</>
                      : <>Stuck after <span className="font-medium text-foreground">{sim.data.moved}</span> / {sim.data.total} — remotes can't drain the rest (raise caps or add remotes).</>}
                  </div>
                  {/* per-remote summary */}
                  <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
                    {sim.data.summary.map((r, i) => (
                      <div key={i} className="rounded-md border border-border bg-secondary/30 px-2.5 py-1.5">
                        <p className="truncate text-xs font-medium text-foreground">{r.name}</p>
                        <p className="text-[11px] text-muted-foreground">{r.bytes} / {capLabel(r.cap)} · {r.files}{r.cap_files > 0 && ` / ${r.cap_files}`} files</p>
                      </div>
                    ))}
                  </div>
                  {/* timeline (moves + collapsed waits) */}
                  <div className="max-h-[42vh] divide-y divide-border overflow-y-auto rounded-md border border-border">
                    {sim.data.steps.map((s, i) => (
                      <div key={i} className="flex items-center gap-3 px-3 py-1.5 text-sm">
                        <span className="w-24 shrink-0 text-[11px] tabular-nums text-muted-foreground/70">{fmtWhen(s.at)}</span>
                        {s.kind === 'move' ? (
                          <span className="flex min-w-0 flex-1 items-center gap-2">
                            <span className={cn('h-2 w-2 shrink-0 rounded-full', s.paused ? 'bg-amber-500' : 'bg-success')} />
                            <span className="truncate font-medium text-foreground">{s.remote}</span>
                            <ArrowRight className="h-3 w-3 shrink-0 text-muted-foreground" />
                            <span className="shrink-0 text-[11px] text-muted-foreground">{s.bytes} · {s.files} files · {s.rate}{(s.took_min ?? 0) > 0 && ` ≈ ${fmtDur(s.took_min!)}`} · {s.remaining} left</span>
                            {s.paused && <span className="flex shrink-0 items-center gap-1 text-[11px] text-amber-600 dark:text-amber-400"><Pause className="h-3 w-3" />paused 60m</span>}
                          </span>
                        ) : (
                          <span className="flex min-w-0 flex-1 items-center gap-2 text-muted-foreground">
                            {s.kind === 'wait' ? <Clock className="h-3 w-3 shrink-0 text-amber-500" /> : <Ban className="h-3 w-3 shrink-0 text-destructive" />}
                            <span className="truncate text-[12px] italic">{s.note}{s.until && ` (≈${fmtUntil(s.at, s.until)})`}</span>
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </Card>
          </TabsContent>
        )}
      </Tabs>

      {picker && (
        <PathPicker mode="folder" disks={['/mnt/local', '/mnt/unionfs']} hideRclone onClose={() => setPicker(false)} onPick={(p) => { if (p[0]) up('source', p[0].path); setPicker(false) }} />
      )}
      {subPick && (
        <PathPicker mode="folder" disks={['/mnt/unionfs']} hideRclone relative
          onClose={() => setSubPick(null)}
          onPick={(p) => {
            const v = p[0]?.path ?? ''
            if (subPick.target === 'shared') up('subpath', v)
            else upRemote(subPick.idx, { dest: v })
            setSubPick(null)
          }} />
      )}
    </div>
  )
}

// ── Uploader hub ──────────────────────────────────────────────────────────────
// Central upload system: the automatic staging→cloud rotation (Auto-upload) and the
// manual/queued/scheduled rclone job manager (Transfers) merged behind one route.
export function Uploader() {
  const [mode, setMode] = useState('auto')
  // Newly-launched jobs auto-expand in the shared Activity list below, regardless of mode.
  const [autoOpenId, setAutoOpenId] = useState<string | null>(null)
  return (
    <div className="mx-auto max-w-[100rem] space-y-5 p-6">
      {/* header */}
      <div className="flex items-start gap-3">
        <div className="grid h-11 w-11 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
          <CloudUpload className="h-5 w-5" />
        </div>
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-foreground">Uploads</h1>
          <p className="mt-0.5 text-sm text-muted-foreground">Central upload hub — automatic folder rotation and manual/scheduled transfers in one place.</p>
        </div>
      </div>

      {/* main content (left) + Activity (right sidebar) */}
      <div className="flex flex-col gap-5 xl:flex-row xl:items-start">
        {/* mode switch — Origin UI tabs; forceMount keeps both panels mounted so
            in-flight edits / expanded jobs survive switching. */}
        <Tabs value={mode} onValueChange={setMode} className="min-w-0 flex-1">
          <TabsList>
            <TabsTrigger value="auto" className="gap-2"><Zap className="h-4 w-4" />Auto-upload</TabsTrigger>
            <TabsTrigger value="transfers" className="gap-2"><ArrowRightLeft className="h-4 w-4" />Transfers</TabsTrigger>
          </TabsList>
          <TabsContent value="auto" forceMount className="data-[state=inactive]:hidden"><AutoUploadPanel /></TabsContent>
          <TabsContent value="transfers" forceMount className="data-[state=inactive]:hidden"><TransfersPanel onJobStart={setAutoOpenId} /></TabsContent>
        </Tabs>

        {/* shared Activity — auto-upload + manual transfer jobs, always visible */}
        <aside className="w-full shrink-0 xl:sticky xl:top-6 xl:max-h-[calc(100vh-3rem)] xl:w-[34rem] xl:overflow-y-auto 2xl:w-[42rem]">
          <TransfersActivity autoOpenId={autoOpenId} />
        </aside>
      </div>
    </div>
  )
}
