/**
 * Uploader — cloudplow++ : watch a local staging folder and, once it grows past a
 * threshold, move it up to cloud remotes, rotating across them with per-remote
 * daily caps + cooldowns to dodge quotas / bans.
 *
 * Layout is master–detail: a left rail lists the config sections (each carrying a live
 * one-line summary so the whole configuration reads at a glance), the pane beside it edits
 * the selected one, and a full-width live strip along the bottom always shows the plan —
 * where the next uploads go and when they finish — expandable to the full monitor. This
 * keeps a dense, desktop-first screen instead of a long vertical stack of cards.
 */
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useUploader, useSaveUploader, useUploaderStatus, useUploaderRun, useUploaderPlan, useUploaderSimulate, useUploaderCalibration, useUploaderTestBlock, useUploaderSelfTest, useGenerateSequence, useResetCaps, useRcloneRemotes, useJobs, useTransferStats, type UploaderConfig, type UploaderRemote, type StCheck } from '@/lib/api'
import { PathPicker } from '@/components/PathPicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Card } from '@/components/ui/card'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { cn } from '@/lib/cn'
import { Save, Play, FolderInput, CloudUpload, FlaskConical, Loader2, ArrowRight, Pause, Ban, Clock, HardDrive, Server, SlidersHorizontal, Gauge, Route, Film, ScanLine, Magnet, Download, ArrowRightLeft, Zap, ChevronDown, ChevronRight, ChevronLeft, ChevronUp, Settings2, Minus, Plus, X, ShieldCheck, CheckCircle2, AlertTriangle, XCircle, MinusCircle, RotateCcw } from 'lucide-react'
import { TransferOptions } from '@/components/TransferOptions'
import { UnitInput, SIZE_UNITS, DUR_UNITS } from '@/components/UnitInput'
import { Progress } from '@/components/ui/progress'
import { Checkbox } from '@/components/ui/checkbox'
import { TransfersPanel, TransfersActivity } from '@/pages/Transfers'

const fmtDur = (min: number) => {
  if (min < 60) return `${min}m`
  const h = Math.floor(min / 60), m = min % 60
  if (h < 24) return m ? `${h}h ${m}m` : `${h}h`
  const d = Math.floor(h / 24), hh = h % 24
  return hh ? `${d}d ${hh}h` : `${d}d`
}
const fmtWhen = (iso: string) => new Date(iso).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
const fmtEta = (sec: number): string => {
  if (sec <= 0) return '—'
  if (sec < 60) return `${sec}s`
  if (sec < 3600) return `${Math.round(sec / 60)}m`
  const h = Math.floor(sec / 3600), m = Math.round((sec % 3600) / 60)
  return m ? `${h}h ${m}m` : `${h}h`
}
// fmtAbs — the wall-clock time `sec` from NOW, so the plan reads "if uploaded now, done by …".
const fmtAbs = (sec: number): string => new Date(Date.now() + sec * 1000).toLocaleString([], { weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
const fmtBytes = (b: number): string => {
  if (!b || b <= 0) return '0B'
  const u = ['B', 'K', 'M', 'G', 'T', 'P']; let i = 0, n = b
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }
  return `${n >= 100 || i === 0 ? Math.round(n) : n.toFixed(1)}${u[i]}`
}
// fmtAgo — compact "time since" for the history (e.g. "3h ago").
const fmtAgo = (iso: string): string => {
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000))
  return s < 60 ? 'just now' : `${fmtEta(s)} ago`
}
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

// stripCommon returns a labeller that drops the prefix shared by every name (so
// tgdrive_main_03 shows as "main_03") to keep the sequence chips compact.
function stripCommon(names: string[]): (n: string) => string {
  const uniq = [...new Set(names)]
  if (uniq.length < 2) return (n) => n
  let p = uniq[0]
  for (const n of uniq) { while (p && !n.startsWith(p)) p = p.slice(0, -1); if (!p) break }
  return (n) => (p && n.length > p.length ? n.slice(p.length) : n)
}

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

// SectionShell — a section's header + body, so every pane in the detail column reads the
// same way (title, one-line intent, then the fields).
function SectionShell({ icon, title, hint, children }: { icon: ReactNode; title: string; hint?: string; children: ReactNode }) {
  return (
    <div className="space-y-3">
      <div>
        <p className="flex items-center gap-1.5 text-sm font-semibold text-foreground">{icon}{title}</p>
        {hint && <p className="mt-0.5 text-[11px] text-muted-foreground">{hint}</p>}
      </div>
      {children}
    </div>
  )
}

// SelfTestList — renders the diagnostic checks with a status glyph each. Shared by the
// full-page panel and every section's inline "Verify".
const ST_GLYPH = {
  ok: <CheckCircle2 className="h-3.5 w-3.5 text-success" />,
  warn: <AlertTriangle className="h-3.5 w-3.5 text-amber-500" />,
  fail: <XCircle className="h-3.5 w-3.5 text-destructive" />,
  skip: <MinusCircle className="h-3.5 w-3.5 text-muted-foreground/50" />,
}
function SelfTestList({ checks }: { checks: StCheck[] }) {
  if (checks.length === 0) return <p className="px-1 text-[11px] text-muted-foreground">No checks ran.</p>
  return (
    <div className="divide-y divide-border/60 overflow-hidden rounded-md border border-border">
      {checks.map((c, i) => (
        <div key={i} className="flex items-start gap-2 px-3 py-1.5 text-xs">
          <span className="mt-0.5 shrink-0">{ST_GLYPH[c.status]}</span>
          <span className="w-36 shrink-0 truncate font-medium text-foreground" title={c.name}>{c.name}</span>
          <span className="min-w-0 flex-1 text-muted-foreground">{c.detail}</span>
        </div>
      ))}
    </div>
  )
}

// CommandList — the exact rclone command per destination the self-test resolved, so you
// can see precisely what will run (same builder as a real upload). Kept mono + scrollable.
function CommandList({ commands }: { commands: { remote: string; cmd: string }[] }) {
  if (commands.length === 0) return null
  return (
    <div className="space-y-1.5">
      <p className="text-[11px] font-medium text-muted-foreground">rclone commands <span className="text-muted-foreground/60">— exactly what each destination will run, incl. the per-remote daily-cap <span className="font-mono">--max-transfer</span> (from the remaining allowance right now)</span></p>
      <div className="space-y-1.5">
        {commands.map((c, i) => (
          <div key={i} className="overflow-hidden rounded-md border border-border">
            <div className="border-b border-border bg-secondary/30 px-2.5 py-1 font-mono text-[11px] text-foreground">{c.remote}</div>
            <pre className="overflow-x-auto px-2.5 py-1.5 font-mono text-[10px] leading-relaxed text-muted-foreground">{c.cmd}</pre>
          </div>
        ))}
      </div>
    </div>
  )
}

// SequenceEditor — the rotation as one explicit, hand-editable list of remotes (repeats
// allowed). The generators (Even / Weights / By fill / By free quota) just (re)write the
// list; it stays fully editable afterwards. Eligibility (cap/gap/ban) still skips a step at
// runtime — this only decides the intended order.
function SequenceEditor({ cfg, setSeq, selected }: { cfg: UploaderConfig; setSeq: (s: string[]) => void; selected: string[] }) {
  const gen = useGenerateSequence()
  const seq = cfg.sequence ?? []
  const label = stripCommon(selected.length ? [...selected, ...seq] : seq)
  const [weightMode, setWeightMode] = useState(false)
  const [weights, setWeights] = useState<Record<string, number>>({})

  const runGen = (mode: string, w?: Record<string, number>) =>
    gen.mutate({ mode, weights: w, config: cfg }, { onSuccess: (d) => setSeq(d.sequence) })
  const move = (i: number, dir: -1 | 1) => {
    const j = i + dir
    if (j < 0 || j >= seq.length) return
    const next = [...seq]
    ;[next[i], next[j]] = [next[j], next[i]]
    setSeq(next)
  }
  const missing = selected.filter((n) => !seq.includes(n))
  const GENBTN = 'h-7 gap-1 text-[11px]'

  return (
    <div className="space-y-2 rounded-md border border-border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground"><Gauge className="h-3.5 w-3.5" />Rotation sequence</span>
        <span className="text-[10px] text-muted-foreground">{seq.length} step{seq.length === 1 ? '' : 's'} · runs in this order, skipping any capped/cooling remote</span>
        <span className="flex-1" />
        {gen.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />}
        <Button size="sm" variant="outline" className={GENBTN} onClick={() => runGen('even')}>Even</Button>
        <Button size="sm" variant="outline" className={GENBTN} onClick={() => runGen('byfill')} title="Emptiest accounts first / more often (reads live fill)">By fill</Button>
        <Button size="sm" variant="outline" className={GENBTN} onClick={() => runGen('byfree')} title="Most daily quota remaining first">By free quota</Button>
        <Button size="sm" variant={weightMode ? 'default' : 'outline'} className={GENBTN} onClick={() => setWeightMode((v) => !v)}>Weights…</Button>
      </div>

      {weightMode && (
        <div className="space-y-1.5 rounded-md border border-border/60 bg-secondary/20 p-2">
          <p className="text-[10px] text-muted-foreground">How many slots each remote gets per cycle (blank = 1). Higher = used more often.</p>
          <div className="flex flex-wrap gap-2">
            {selected.map((n) => (
              <label key={n} className="flex items-center gap-1 text-[11px] text-foreground">
                <span className="font-mono">{label(n)}</span>
                <Input type="number" min={1} className="h-7 w-14" value={weights[n] ?? ''} placeholder="1"
                  onChange={(e) => setWeights((w) => ({ ...w, [n]: Math.max(1, parseInt(e.target.value, 10) || 1) }))} />
              </label>
            ))}
          </div>
          <Button size="sm" className="h-7 text-[11px]" disabled={selected.length === 0} onClick={() => { runGen('weights', weights); setWeightMode(false) }}>Generate from weights</Button>
        </div>
      )}

      {/* the sequence itself — chips in order, each movable / removable */}
      {seq.length === 0 ? (
        <p className="rounded-md border border-dashed border-border px-3 py-2 text-[11px] text-muted-foreground">Empty — generate above, or add remotes below. An empty sequence falls back to plain round-robin over the selected remotes.</p>
      ) : (
        <div className="flex flex-wrap items-center gap-1">
          {seq.map((n, i) => (
            <span key={i} className="group flex items-center gap-0.5 rounded-md border border-primary/40 bg-primary/5 py-0.5 pl-1.5 pr-0.5 font-mono text-[11px] text-foreground">
              <button type="button" title="move left" className="text-muted-foreground/50 hover:text-foreground disabled:opacity-30" disabled={i === 0} onClick={() => move(i, -1)}><ChevronLeft className="h-3 w-3" /></button>
              {label(n)}
              <button type="button" title="move right" className="text-muted-foreground/50 hover:text-foreground disabled:opacity-30" disabled={i === seq.length - 1} onClick={() => move(i, 1)}><ChevronRight className="h-3 w-3" /></button>
              <button type="button" title="remove" className="text-muted-foreground/50 hover:text-destructive" onClick={() => setSeq(seq.filter((_, k) => k !== i))}><X className="h-3 w-3" /></button>
            </span>
          ))}
        </div>
      )}

      {/* append a step */}
      {selected.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[10px] text-muted-foreground">add step:</span>
          {selected.map((n) => (
            <button key={n} type="button" onClick={() => setSeq([...seq, n])}
              className="flex items-center gap-0.5 rounded border border-border px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground hover:bg-accent/40 hover:text-foreground"><Plus className="h-2.5 w-2.5" />{label(n)}</button>
          ))}
        </div>
      )}

      {missing.length > 0 && (
        <p className="text-[10px] text-warning">Selected but not in the sequence (never uploaded to): <span className="font-mono">{missing.map(label).join(', ')}</span></p>
      )}
      {gen.isError && <p className="text-[10px] text-destructive">{gen.error.message}</p>}
    </div>
  )
}

type SectionKey = 'plan' | 'trigger' | 'dest' | 'opts' | 'pause' | 'sim'

// Off-peak window presets. Each fills the from–until pair; the time fields below stay
// editable for fine-tuning. Windows are start-only (a run that begins in-window finishes
// even past the end), so these describe when uploads may *begin*.
const WINDOW_PRESETS: { label: string; from: string; until: string }[] = [
  { label: 'Anytime', from: '', until: '' },
  { label: 'Morning · 06–12', from: '06:00', until: '12:00' },
  { label: 'Afternoon · 12–18', from: '12:00', until: '18:00' },
  { label: 'Evening · 18–24', from: '18:00', until: '00:00' },
  { label: 'Night · 00–06', from: '00:00', until: '06:00' },
  { label: 'Daytime · 06–18', from: '06:00', until: '18:00' },
  { label: 'Overnight · 22–06', from: '22:00', until: '06:00' },
]

// WindowPresets — quick-pick chips over the two time fields.
function WindowPresets({ from, until, onFrom, onUntil }: { from: string; until: string; onFrom: (v: string) => void; onUntil: (v: string) => void }) {
  const norm = (s: string) => s.slice(0, 5)
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-1.5">
        {WINDOW_PRESETS.map((p) => {
          const active = norm(from) === p.from && norm(until) === p.until
          return (
            <Button key={p.label} type="button" size="sm" variant={active ? 'default' : 'outline'} className="h-7 text-[11px]"
              onClick={() => { onFrom(p.from); onUntil(p.until) }}>{p.label}</Button>
          )
        })}
      </div>
      <div className="flex items-center gap-2">
        <span className="text-[10px] text-muted-foreground">custom</span>
        <TimeRange from={from} until={until} onFrom={onFrom} onUntil={onUntil} />
      </div>
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
  const plan = useUploaderPlan()
  const testBlock = useUploaderTestBlock()
  const selftest = useUploaderSelfTest()
  const { data: status } = useUploaderStatus()
  const { data: jobs = [] } = useJobs()
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
  const [section, setSection] = useState<SectionKey>('plan')
  const [stGroup, setStGroup] = useState<string | null>(null)  // which self-test run is showing
  // Run now / Check now kick off async work on the server and return immediately, so the
  // POST's isPending clears within ~100ms — long before the next status/jobs poll surfaces
  // the running job. `starting` bridges that gap with an instant indicator, cleared the
  // moment the real job appears (or on error / a safety timeout) so the button never lies.
  const [starting, setStarting] = useState(false)
  const activeUpload = jobs.find((j) => j.status === 'running' && j.tag.startsWith('uploader:'))

  useEffect(() => { if (data) setCfg({ ...EMPTY, ...data, remotes: data.remotes ?? [] }) }, [data])
  useEffect(() => { if (activeUpload || status?.checking) setStarting(false) }, [activeUpload, status?.checking])

  const up = <K extends keyof UploaderConfig>(k: K, v: UploaderConfig[K]) => setCfg((c) => ({ ...c, [k]: v }))
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

  // Refresh status + jobs a few times over the next ~3s so a freshly-started job shows in the
  // plan/Activity fast, instead of waiting for the normal 3–5s poll.
  function bumpPoll() {
    const hit = () => { qc.invalidateQueries({ queryKey: ['uploader-status'] }); qc.invalidateQueries({ queryKey: ['jobs'] }) }
    hit()
    for (const ms of [700, 1500, 2800]) setTimeout(hit, ms)
  }
  function runNow() {
    setStarting(true)
    run.mutate(undefined, { onSuccess: bumpPoll, onError: () => setStarting(false) })
    setTimeout(() => setStarting(false), 20000) // safety: never spin forever
  }
  // Self-test verifies the on-screen config (no Save needed). group='' = whole page; a
  // section key runs only that group so its Verify button is cheap.
  const runSelfTest = (group: string) => { setStGroup(group); selftest.mutate({ group: group || undefined, config: cfg }) }
  // Inline "Verify this section" block, reused by the Source / Destinations / Pause panes.
  const renderVerify = (group: string, label: string) => (
    <div className="space-y-2 border-t border-border/50 pt-3">
      <Button size="sm" variant="outline" className="h-7 gap-1.5" disabled={selftest.isPending && stGroup === group} onClick={() => runSelfTest(group)}>
        {selftest.isPending && stGroup === group ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <CheckCircle2 className="h-3.5 w-3.5" />}Verify {label}
      </Button>
      {stGroup === group && selftest.isError && <p className="text-[11px] text-destructive">{selftest.error.message}</p>}
      {stGroup === group && selftest.data && <SelfTestList checks={selftest.data.checks} />}
      {stGroup === group && selftest.data?.commands && <CommandList commands={selftest.data.commands} />}
    </div>
  )

  const pauseCount = [pause.plex_kill_transcode, pause.autoscan_hold, pause.qbit.enabled, pause.arr_disable].filter(Boolean).length
  const validDests = cfg.remotes.filter((r) => r.name).length
  const excludeCount = cfg.opts?.exclude?.length ?? 0

  // Rail — one row per section, each with a live summary so the whole config reads without
  // clicking through. Selection drives the detail pane on the right.
  const planSummary = status?.plan?.remotes?.length ? `${status.plan.remotes.length} step${status.plan.remotes.length === 1 ? '' : 's'} · ${status.plan.source_human}` : 'press Check now'
  const SECTIONS: { key: SectionKey; icon: ReactNode; label: string; summary: string; dev?: boolean }[] = [
    { key: 'plan', icon: <FlaskConical className="h-4 w-4" />, label: 'Upload plan', summary: planSummary },
    { key: 'trigger', icon: <HardDrive className="h-4 w-4" />, label: 'Trigger', summary: `${status?.last_size ?? '—'} / ${status?.threshold || cfg.threshold || '—'} · every ${cfg.interval_minutes}m` },
    { key: 'dest', icon: <Route className="h-4 w-4" />, label: 'Destinations', summary: validDests > 0 ? `${validDests} remote${validDests === 1 ? '' : 's'} · seq ${cfg.sequence?.length ?? 0}` : 'none selected' },
    { key: 'opts', icon: <SlidersHorizontal className="h-4 w-4" />, label: 'Transfer options', summary: `${cfg.op ?? 'move'} · ${excludeCount} exclude${excludeCount === 1 ? '' : 's'}${cfg.min_age ? ` · min-age ${cfg.min_age}` : ''}` },
    { key: 'pause', icon: <Pause className="h-4 w-4" />, label: 'Pause activity', summary: pauseCount > 0 ? `${pauseCount} enabled` : 'off' },
    ...(isDev ? [{ key: 'sim' as const, icon: <FlaskConical className="h-4 w-4" />, label: 'Simulate', summary: 'dry-run rotation' }] : []),
  ]

  return (
    <div className="space-y-3">
      {/* ── toolbar: status + master switch + actions ─────────────────────── */}
      <div className="flex flex-wrap items-center gap-2 rounded-xl border border-border/70 bg-card px-3 py-2 shadow-sm">
        <span className={cn('flex items-center gap-2 rounded-full px-2.5 py-1 text-xs font-medium',
          cfg.enabled ? 'bg-success/15 text-success' : 'bg-muted text-muted-foreground')}>
          <span className={cn('h-2 w-2 rounded-full', cfg.enabled ? 'bg-success' : 'bg-muted-foreground/50')} />
          {cfg.enabled ? 'Active' : 'Disabled'}
        </span>
        <label className="flex cursor-pointer items-center gap-1.5 text-xs text-foreground">
          <Switch checked={cfg.enabled} onCheckedChange={(v) => up('enabled', v)} />
          Enable auto-upload
        </label>
        {/* Dry-run: real rclone command with --dry-run — moves nothing, touches no state.
            Radix Switch is a <button>, so no <label> wrapper (would double-toggle). */}
        <div className={cn('flex items-center gap-1.5 rounded-full px-2 py-1 text-xs', cfg.dry_run ? 'bg-warning/15 text-warning' : 'text-muted-foreground')}
          title="Run the real rclone command with --dry-run: it logs exactly what would move but transfers nothing and doesn't touch caps or history.">
          <Switch checked={!!cfg.dry_run} onCheckedChange={(v) => up('dry_run', v)} />
          Dry-run
        </div>
        {status?.last_check && <span className="hidden text-[11px] text-muted-foreground sm:inline">checked {fmtAgo(status.last_check)}</span>}
        {status?.message && <span className="hidden truncate text-[11px] italic text-muted-foreground/80 lg:inline">{status.message}</span>}
        <span className="flex-1" />
        <Button size="sm" variant="outline" className="h-8 gap-1.5" onClick={() => runSelfTest('')} disabled={selftest.isPending && stGroup === ''}>
          {selftest.isPending && stGroup === '' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <ShieldCheck className="h-3.5 w-3.5" />}Self-test
        </Button>
        {/* Check now = build the plan only (no upload). Run now = execute one cycle now
            (real move, or --dry-run when dry-run mode is on). */}
        <Button size="sm" variant="outline" className="h-8 gap-1.5" onClick={() => plan.mutate(undefined, { onSuccess: () => qc.invalidateQueries({ queryKey: ['uploader-status'] }) })} disabled={plan.isPending || !!status?.checking}>
          {(plan.isPending || status?.checking) && !run.isPending ? <><Loader2 className="h-3.5 w-3.5 animate-spin" />Planning…</> : <><FlaskConical className="h-3.5 w-3.5" />Check now</>}
        </Button>
        <Button size="sm" variant={cfg.dry_run ? 'outline' : 'default'}
          className={cn('h-8 gap-1.5', cfg.dry_run && 'border-warning text-warning hover:text-warning')}
          onClick={runNow}
          disabled={run.isPending || starting || !!status?.checking}>
          {run.isPending || starting ? <><Loader2 className="h-3.5 w-3.5 animate-spin" />Starting…</> : <><CloudUpload className="h-3.5 w-3.5" />{cfg.dry_run ? 'Run dry-run' : 'Run now'}</>}
        </Button>
        <Button size="sm" className="h-8 gap-1.5" onClick={doSave} disabled={save.isPending}>
          <Save className="h-3.5 w-3.5" />{saved ? 'Saved ✓' : 'Save'}
        </Button>
      </div>

      {cfg.dry_run && (
        <div className="space-y-1 rounded-md bg-warning/10 px-3 py-2 text-[11px] text-warning">
          <p><span className="font-medium">Dry-run mode.</span> <span className="font-medium">Run now</span> executes the real rclone command with <span className="font-mono">--dry-run</span> — the Activity log shows exactly what would move, but nothing is uploaded, no daily caps are consumed, and the rotation position isn&rsquo;t advanced. Turn off to upload for real.</p>
          <p className="text-warning/80">Note: one <span className="font-medium">Run now</span> is a single cycle → one remote. And <span className="font-mono">--dry-run</span> ignores the daily cap (<span className="font-mono">--max-transfer</span> only counts real bytes), so it lists the <em>whole</em> source for that remote. A real run stops at the cap and the next cycle rotates on — the <span className="font-medium">Upload plan</span> below shows the true capped split.</p>
        </div>
      )}

      {/* ── full self-test results (whole page) ───────────────────────────── */}
      {stGroup === '' && (selftest.isPending || selftest.data || selftest.isError) && (
        <Card className="space-y-2 rounded-xl border-border/70 p-3 shadow-sm">
          <div className="flex items-center gap-2">
            <ShieldCheck className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-semibold text-foreground">Self-test</span>
            {selftest.data && (
              <span className="flex items-center gap-2 text-[11px]">
                <span className="text-success">{selftest.data.ok} ok</span>
                {selftest.data.warn > 0 && <span className="text-amber-500">{selftest.data.warn} warn</span>}
                {selftest.data.fail > 0 && <span className="text-destructive">{selftest.data.fail} fail</span>}
              </span>
            )}
            <span className="text-[11px] text-muted-foreground">verifies the on-screen config — nothing is uploaded or paused</span>
            <button onClick={() => setStGroup(null)} className="ml-auto text-muted-foreground hover:text-foreground"><ChevronUp className="h-4 w-4" /></button>
          </div>
          {selftest.isPending && <p className="flex items-center gap-2 px-1 text-xs text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Probing source, remotes, and pause targets…</p>}
          {selftest.isError && <p className="px-1 text-xs text-destructive">{selftest.error.message}</p>}
          {!selftest.isPending && selftest.data && <SelfTestList checks={selftest.data.checks} />}
          {!selftest.isPending && selftest.data?.commands && <CommandList commands={selftest.data.commands} />}
        </Card>
      )}

      {/* ── master–detail: section rail + editor pane ─────────────────────── */}
      <div className="flex flex-col gap-3 lg:flex-row lg:items-stretch">
        {/* rail */}
        <div className="flex shrink-0 gap-1.5 overflow-x-auto lg:w-56 lg:flex-col lg:overflow-visible">
          {SECTIONS.map((s) => {
            const active = section === s.key
            return (
              <button key={s.key} onClick={() => setSection(s.key)}
                className={cn('flex min-w-[9rem] items-center gap-2.5 rounded-lg border px-2.5 py-2 text-left transition-colors lg:min-w-0',
                  active ? 'border-primary/50 bg-primary/5' : 'border-border/70 bg-card hover:bg-accent/40')}>
                <span className={cn('shrink-0', active ? 'text-primary' : 'text-muted-foreground')}>{s.icon}</span>
                <span className="min-w-0 flex-1">
                  <span className={cn('block truncate text-xs font-medium', active ? 'text-foreground' : 'text-foreground/90')}>{s.label}</span>
                  <span className="block truncate text-[10px] text-muted-foreground">{s.summary}</span>
                </span>
              </button>
            )
          })}
        </div>

        {/* detail pane — the Upload plan monitor is the first, default tab (it's what you
            watch); the config sections sit behind it since they're set rarely. */}
        {section === 'plan' ? (
          <div className="min-w-0 flex-1 space-y-3">
            <UploadPlanCard starting={starting} />
            <div className="grid gap-3 2xl:grid-cols-2">
              <RemoteCapacityGrid capLabel={capLabel} capBytes={capBytes} />
              <RecentUploadsCard />
            </div>
          </div>
        ) : (
        <Card className="min-w-0 flex-1 rounded-xl border-border/70 p-4 shadow-sm">
          {section === 'trigger' && (
            <SectionShell icon={<HardDrive className="h-4 w-4 text-muted-foreground" />} title="Trigger"
              hint="Which folder is watched, how full it must get, and when uploads may start.">
              <div className="grid gap-4 xl:grid-cols-2">
                <div className="space-y-3">
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
                      <UnitInput value={cfg.threshold} onChange={(v) => up('threshold', v)} units={SIZE_UNITS} defaultUnit="G" placeholder="500" />
                    </div>
                    <div className="space-y-1.5">
                      <div className="flex items-center justify-between">
                        <Label className="text-[11px]">Check every</Label>
                        <span className="text-[11px] font-medium tabular-nums text-foreground">{cfg.interval_minutes} min</span>
                      </div>
                      <div className="flex items-center gap-2 py-1">
                        <Button type="button" size="icon" variant="outline" className="h-7 w-7 shrink-0" disabled={cfg.interval_minutes <= 1} onClick={() => up('interval_minutes', Math.max(1, cfg.interval_minutes - 1))}><Minus className="h-3.5 w-3.5" /></Button>
                        <input type="range" min={1} max={60} step={1} value={cfg.interval_minutes} onChange={(e) => up('interval_minutes', Number(e.target.value))} className="h-1.5 min-w-0 flex-1 cursor-pointer accent-primary" />
                        <Button type="button" size="icon" variant="outline" className="h-7 w-7 shrink-0" disabled={cfg.interval_minutes >= 60} onClick={() => up('interval_minutes', Math.min(60, cfg.interval_minutes + 1))}><Plus className="h-3.5 w-3.5" /></Button>
                      </div>
                    </div>
                  </div>
                  <div className="space-y-1">
                    <Label className="text-[11px]">Plan ETA speed <span className="text-muted-foreground/70">(blank = auto)</span></Label>
                    <UnitInput className="max-w-[10rem]" value={cfg.eta_speed ?? ''} onChange={(v) => up('eta_speed', v)} units={SIZE_UNITS} defaultUnit="M" placeholder="auto" />
                    <p className="text-[10px] text-muted-foreground">Assumed upload speed for the dry-run plan ETA. Blank uses each remote's calibrated average (auto-measured from real runs).</p>
                  </div>
                </div>
                <div className="space-y-1.5">
                  <Label className="text-[11px]">Upload window (off-peak)</Label>
                  <WindowPresets from={cfg.allowed_from ?? ''} until={cfg.allowed_until ?? ''} onFrom={(v) => up('allowed_from', v)} onUntil={(v) => up('allowed_until', v)} />
                  <p className="text-[10px] text-muted-foreground">Uploads may only <span className="text-foreground">start</span> inside this window (overnight ranges like 22–06 are fine). A run that begins in-window keeps going until it finishes — the window never interrupts it. Blank = anytime.</p>
                </div>
              </div>
              {renderVerify('config', 'trigger')}
            </SectionShell>
          )}

          {section === 'dest' && (
            <SectionShell icon={<Route className="h-4 w-4 text-muted-foreground" />} title="Destinations &amp; rotation"
              hint="Where uploads go and how they spread across remotes. Tick remotes to rotate the source across them (each used at most once); open a row to override the shared defaults.">
              <div className="space-y-3">
                <SequenceEditor cfg={cfg} setSeq={(s) => up('sequence', s)} selected={cfg.remotes.map((r) => r.name).filter(Boolean)} />

                <div className="flex items-center justify-between gap-2">
                  <span className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Destinations</span>
                  <span className="text-[11px] text-muted-foreground">{validDests} selected · shared defaults apply unless a row overrides</span>
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
                      <Label className="text-[10px] text-muted-foreground" title="Bytes uploaded per 24h before a remote is skipped. Blank = unlimited (teldrive). Google Drive: 700G.">Cap / day</Label>
                      <UnitInput className="w-28" value={cfg.cap ?? ''} onChange={(v) => up('cap', v)} units={SIZE_UNITS} defaultUnit="G" placeholder="∞" />
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
                  <p className="text-[10px] text-muted-foreground">Leave a row's field blank to inherit these. <span className="text-foreground">Cap</span>: daily upload cap (∞ = unlimited; Google Drive 700G). <span className="text-foreground">Files</span>: daily request cap (teldrive ban limit ~8000–10000). <span className="text-foreground">Gap</span>: minutes before reusing a remote.</p>
                </div>

                {/* remote checklist — tick to include in the rotation, expand to override defaults */}
                {remoteNames.length === 0 && cfg.remotes.length === 0 ? (
                  <div className="grid place-items-center rounded-md border border-dashed border-border py-6 text-xs text-muted-foreground">No rclone remotes found. Add one on the Files page first.</div>
                ) : (
                  <div className="grid grid-cols-1 items-start gap-1.5 md:grid-cols-2 2xl:grid-cols-3">
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
                                  <Label className="text-[10px] text-muted-foreground">Cap</Label>
                                  <UnitInput value={r!.cap} onChange={(v) => upRemote(idx, { cap: v })} units={SIZE_UNITS} defaultUnit="G" placeholder={cfg.cap || '∞'} />
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
              </div>
              {renderVerify('destinations', 'destinations')}
            </SectionShell>
          )}

          {section === 'opts' && (
            <SectionShell icon={<SlidersHorizontal className="h-4 w-4 text-muted-foreground" />} title="Transfer options"
              hint="How the upload runs and the rclone flags applied to every destination.">
              {/* Mode — the fundamental operation. Automation normally moves: the file
                  reappears via unionfs on the same path immediately, and the local copy is
                  freed. Copy keeps both (double the space). */}
              <div className="mb-3 flex flex-wrap items-center gap-3">
                <Label className="text-[11px]">Mode</Label>
                <div className="flex rounded-md border border-border p-0.5">
                  {([['move', 'Move'], ['copy', 'Copy']] as const).map(([m, lbl]) => (
                    <button key={m} type="button" onClick={() => up('op', m)}
                      className={cn('rounded px-3 py-1 text-xs font-medium', (cfg.op ?? 'move') === m ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground')}>{lbl}</button>
                  ))}
                </div>
                <p className="min-w-0 flex-1 text-[10px] text-muted-foreground">
                  {(cfg.op ?? 'move') === 'move'
                    ? 'Move (default for automation) — the source is freed after upload and the file appears immediately via unionfs on the same path, so nothing is duplicated.'
                    : 'Copy — keeps the local copy as well as the uploaded one (uses double the space). Use only if the source must stay in place.'}
                </p>
              </div>

              <TransferOptions
                opts={cfg.opts ?? {}} setOpts={setOpts} remoteTypes={destTypes}
                extraChecks={[{
                  label: 'Delete emptied source folders',
                  hint: 'After a move, remove directories left empty in the source (rclone --delete-empty-src-dirs). Keeps the staging tree tidy.',
                  v: !!cfg.delete_empty_src,
                  on: (b) => up('delete_empty_src', b),
                }]}
              />

              {/* Min file age lives here too (it's the rclone --min-age move flag) but is an
                  input rather than a checkbox, so it sits below the flag grid. */}
              <div className="mt-3 space-y-1 border-t border-border/50 pt-3">
                <Label className="text-[11px]">Min file age <span className="text-muted-foreground/70">(optional · --min-age)</span></Label>
                <UnitInput className="w-28" value={cfg.min_age ?? ''} onChange={(v) => up('min_age', v)} units={DUR_UNITS} defaultUnit="m" placeholder="off" />
                <p className="text-[10px] text-muted-foreground">Skip files modified within this age, so a file still being written isn't uploaded mid-copy. Blank = upload everything — fine when the source only ever holds finished files.</p>
              </div>
            </SectionShell>
          )}

          {section === 'pause' && (
            <SectionShell icon={<Pause className="h-4 w-4 text-muted-foreground" />} title="Pause other activity while uploading"
              hint="Upload is the priority — these free up resources / stop new files landing in the folder being moved.">
              {isDev && (
                <div className="flex items-center gap-1.5">
                  <Button size="sm" variant="outline" className="h-7 gap-1.5" disabled={testBlock.isPending} onClick={() => testBlock.mutate({ action: 'apply', pause })}>
                    {testBlock.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <FlaskConical className="h-3.5 w-3.5" />}Test block
                  </Button>
                  <Button size="sm" variant="ghost" className="h-7" disabled={testBlock.isPending} onClick={() => testBlock.mutate({ action: 'restore', pause })}>Restore</Button>
                </div>
              )}
              {isDev && testBlock.data && (
                <div className="rounded-md border border-border bg-secondary/30 px-2.5 py-1.5 text-[11px] text-muted-foreground">
                  <span className="font-medium capitalize text-foreground">{testBlock.data.action}ed</span> — qBit: <span className="text-foreground">{testBlock.data.qbit}</span> · arr: <span className="text-foreground">{testBlock.data.arr}</span> · Plex: <span className="text-foreground">{testBlock.data.plex}</span> · autoscan: <span className="text-foreground">{testBlock.data.autoscan}</span>
                  <span className="block text-[10px]">Check the services to confirm, then press Restore.</span>
                </div>
              )}
              {isDev && testBlock.isError && <p className="text-[11px] text-destructive">{testBlock.error.message}</p>}
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                <div className={cn('space-y-2 rounded-md border p-3', pause.plex_kill_transcode ? 'border-primary/40 bg-primary/5' : 'border-border')}>
                  <div className="flex items-center justify-between gap-2">
                    <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground"><Film className="h-3.5 w-3.5 text-muted-foreground" />Plex</span>
                    <Switch checked={pause.plex_kill_transcode} onCheckedChange={(v) => upPause({ plex_kill_transcode: v })} />
                  </div>
                  <p className="text-xs text-foreground">Stop transcodes while uploading</p>
                  <p className="text-[10px] text-muted-foreground">Terminates only transcoding sessions (frees CPU/disk for the upload); direct-play streams keep playing. Kicks recur through the run.</p>
                </div>

                <div className={cn('space-y-2 rounded-md border p-3', pause.autoscan_hold ? 'border-primary/40 bg-primary/5' : 'border-border')}>
                  <div className="flex items-center justify-between gap-2">
                    <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground"><ScanLine className="h-3.5 w-3.5 text-muted-foreground" />Autoscan</span>
                    <Switch checked={pause.autoscan_hold} onCheckedChange={(v) => upPause({ autoscan_hold: v })} />
                  </div>
                  <p className="text-xs text-foreground">Hold scans while uploading</p>
                  <p className="text-[10px] text-muted-foreground">Pauses the autoscan container during the run so it won't scan the folder being moved; unpaused after (queued scans then proceed).</p>
                </div>

                <div className={cn('space-y-2 rounded-md border p-3', pause.qbit.enabled ? 'border-primary/40 bg-primary/5' : 'border-border')}>
                  <div className="flex items-center justify-between gap-2">
                    <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground"><Magnet className="h-3.5 w-3.5 text-muted-foreground" />qBittorrent</span>
                    <Switch checked={pause.qbit.enabled} onCheckedChange={(v) => upQbit({ enabled: v })} />
                  </div>
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
                  <div className="flex items-center justify-between gap-2">
                    <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground"><Download className="h-3.5 w-3.5 text-muted-foreground" />Sonarr / Radarr</span>
                    <Switch checked={pause.arr_disable} onCheckedChange={(v) => upPause({ arr_disable: v })} />
                  </div>
                  <p className="text-xs text-foreground">Pause imports while uploading</p>
                  <p className="text-[10px] text-muted-foreground">Turns off Completed Download Handling (auto-import) in each *arr during the run, so no files are imported into the folder being moved. Downloading continues; re-enabled after.</p>
                </div>
              </div>
              {renderVerify('pause', 'pause targets')}
            </SectionShell>
          )}

          {isDev && section === 'sim' && (
            <SectionShell icon={<FlaskConical className="h-4 w-4 text-primary" />} title="Simulate rotation (dry-run)"
              hint="Drains a backlog across your current remotes (no need to Save) — caps, gaps, window & rate-limit pauses included. Nothing is uploaded.">
              {calib && calib.length > 0 && (
                <p className="-mt-1 text-[11px] text-muted-foreground">Measured: {calib.map((c) => `${c.remote} ~${c.avg_speed}/s${c.throttle_rate > 0 ? ` (${Math.round(c.throttle_rate * 100)}% throttled)` : ''}`).join(' · ')}</p>
              )}
              <div className="flex flex-wrap items-end gap-2">
                <div className="space-y-1">
                  <Label className="text-[10px] text-muted-foreground">Total to upload</Label>
                  <UnitInput className="w-32" value={simTotal} onChange={setSimTotal} units={SIZE_UNITS} defaultUnit="G" placeholder="3000" />
                </div>
                <div className="space-y-1">
                  <Label className="text-[10px] text-muted-foreground">Avg file size</Label>
                  <UnitInput className="w-28" value={simAvg} onChange={setSimAvg} units={SIZE_UNITS} defaultUnit="G" placeholder="5" />
                </div>
                <div className="space-y-1">
                  <Label className="text-[10px] text-muted-foreground">Speed / connection</Label>
                  <UnitInput className="w-28" value={simPerConn} onChange={setSimPerConn} units={SIZE_UNITS} defaultUnit="M" placeholder="5" />
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
            </SectionShell>
          )}
        </Card>
        )}
      </div>

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

// RemoteCapacityGrid — per-remote daily fill from the last run (used today vs cap, paused
// state). Lives in the expanded monitor.
function RemoteCapacityGrid({ capLabel, capBytes }: { capLabel: (c?: string) => string; capBytes: (c?: string) => number }) {
  const qc = useQueryClient()
  const { data: status } = useUploaderStatus()
  const reset = useResetCaps()
  const remotes = status?.remotes ?? []
  // Clearing a remote's rolling-window usage un-caps it (and lifts a flood bench) right
  // away — the way to re-test a rotation without waiting out the 24h window.
  const doReset = (remote?: string) =>
    reset.mutate(remote ? { remote } : undefined, {
      onSuccess: () => { qc.invalidateQueries({ queryKey: ['uploader-status'] }) },
    })
  return (
    <Card className="space-y-2 rounded-xl border-border/70 p-3 shadow-sm">
      <div className="flex items-center justify-between gap-2">
        <h2 className="flex items-center gap-1.5 text-sm font-semibold text-foreground"><Server className="h-4 w-4 text-muted-foreground" />Remote capacity <span className="font-normal text-muted-foreground">({remotes.length})</span></h2>
        {remotes.length > 0 && (
          <button onClick={() => doReset()} disabled={reset.isPending}
            title="Zero today's usage on every remote (also lifts flood pauses). Nothing is deleted — only the daily counters reset."
            className="flex items-center gap-1 text-[11px] font-medium text-muted-foreground hover:text-foreground disabled:opacity-50">
            {reset.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : <RotateCcw className="h-3 w-3" />}reset caps
          </button>
        )}
      </div>
      {remotes.length > 0 ? (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
          {remotes.map((r, i) => {
            const cb = capBytes(r.cap), ub = parseSize(r.used_today)
            const pct = cb > 0 ? (ub / cb) * 100 : 0
            return (
              <div key={r.name || i} className={cn('space-y-1.5 rounded-lg border px-2.5 py-2', r.paused_until ? 'border-amber-500/50 bg-amber-500/10' : 'border-border/70 bg-card')}>
                <div className="flex items-center justify-between gap-1">
                  <span className="truncate text-xs font-semibold text-foreground">{r.name}</span>
                  <div className="flex shrink-0 items-center gap-1">
                    {r.paused_until && <span className="rounded bg-amber-500/20 px-1 text-[9px] font-medium uppercase text-amber-600 dark:text-amber-400">paused</span>}
                    <button onClick={() => doReset(r.name)} disabled={reset.isPending} title={`Reset ${r.name}'s daily usage`}
                      className="text-muted-foreground/50 hover:text-foreground disabled:opacity-50"><RotateCcw className="h-3 w-3" /></button>
                  </div>
                </div>
                <Progress value={Math.min(100, pct)} className="h-1.5" />
                <p className="text-[10px] text-muted-foreground">{r.used_today} / {capLabel(r.cap)}{(r.cap_files ?? 0) > 0 && ` · ${r.files_today ?? 0}/${r.cap_files} files`}</p>
                <p className="truncate text-[9px] text-muted-foreground/60">{r.paused_until ? `until ${new Date(r.paused_until).toLocaleTimeString()}` : `last ${r.last_upload ? new Date(r.last_upload).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '—'}`}</p>
              </div>
            )
          })}
        </div>
      ) : (
        <div className="grid h-[76px] place-items-center rounded-lg border border-dashed border-border text-xs text-muted-foreground">No run yet — press “Check now”.</div>
      )}
    </Card>
  )
}

// UploadPlanCard — the rotation plan, made live: while a real upload runs, the step for
// that remote is marked running with a real ETA from the current transfer speed, and every
// following step's start time is recomputed from that live finish (using the measured
// speed) rather than the static dry-run estimate.
export function UploadPlanCard({ starting = false }: { starting?: boolean } = {}) {
  const { data: status } = useUploaderStatus()
  const { data: jobs = [] } = useJobs()
  const [showCmds, setShowCmds] = useState(false)
  const plan = status?.plan

  // The live uploader job (a real move, not a dry-run) and its running remote.
  const activeJob = jobs.find((j) => j.status === 'running' && j.tag.startsWith('uploader:') && !j.tag.includes('DRY-RUN'))
  const { data: stats } = useTransferStats(activeJob?.id ?? null, !!activeJob)
  const runRemote = activeJob?.tag.match(/→\s*([^:\s]+):/)?.[1] ?? ''

  const remotes = plan?.remotes ?? []
  // Recompute start/ETA per step from the measured speed once an upload is live.
  const live = useMemo(() => {
    const speed = stats?.speed && stats.speed > 0 ? stats.speed : 0
    const runIdx = runRemote ? remotes.findIndex((r) => r.remote === runRemote) : -1
    if (runIdx < 0 || speed <= 0) return null
    const transferred = stats?.bytes ?? 0
    let clock = 0
    const rows = remotes.map((r, i) => {
      if (i < runIdx) return { startSec: r.at_sec, etaSec: r.eta_sec, state: 'other' as const, pct: 100 }
      const waitBefore = i > runIdx ? Math.max(0, r.at_sec - (remotes[i - 1].at_sec + remotes[i - 1].eta_sec)) : 0
      const remaining = i === runIdx ? Math.max(0, r.bytes - transferred) : r.bytes
      const dur = remaining / speed
      const startSec = clock + waitBefore
      clock = startSec + dur
      return { startSec, etaSec: dur, state: i === runIdx ? ('running' as const) : ('next' as const), pct: i === runIdx && r.bytes > 0 ? Math.min(100, (transferred / r.bytes) * 100) : 0 }
    })
    return { rows, doneSec: clock, speed, transferred, runIdx }
  }, [runRemote, stats?.speed, stats?.bytes, remotes])

  // Instant feedback for Run now: the job hasn't shown up in the poll yet, but the click
  // registered — show it immediately rather than leaving the card looking idle.
  const startBanner = starting && !activeJob ? (
    <p className="flex items-center gap-2 rounded-md bg-primary/10 px-3 py-1.5 text-[11px] font-medium text-primary">
      <Loader2 className="h-3.5 w-3.5 animate-spin" />Starting upload — kicking off rclone &amp; measuring the source…
    </p>
  ) : null

  if (!plan) {
    if (starting && !activeJob) return (
      <Card className="flex items-center gap-2 rounded-xl border-border/70 p-4 text-sm text-muted-foreground shadow-sm">
        <Loader2 className="h-4 w-4 animate-spin" />Starting upload — kicking off rclone &amp; measuring the source…
      </Card>
    )
    if (!status?.checking) return (
      <Card className="grid place-items-center rounded-xl border-dashed border-border/70 p-4 text-xs text-muted-foreground shadow-sm">
        No plan yet — press “Check now”.
      </Card>
    )
    return (
      <Card className="flex items-center gap-2 rounded-xl border-border/70 p-4 text-sm text-muted-foreground shadow-sm">
        <Loader2 className="h-4 w-4 animate-spin" />Building upload plan — measuring the source &amp; simulating the rotation…
      </Card>
    )
  }
  const waits = plan.total_eta_sec > plan.transfer_sec + 60
  const startOf = (i: number) => (live ? live.rows[i].startSec : remotes[i].at_sec)
  const etaOf = (i: number) => (live ? live.rows[i].etaSec : remotes[i].eta_sec)
  const doneBySec = live ? live.doneSec : plan.total_eta_sec
  const runningRow = live ? remotes[live.runIdx] : null
  return (
    <Card className="space-y-3 rounded-xl border-border/70 p-3 shadow-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="flex items-center gap-1.5 text-sm font-semibold text-foreground"><FlaskConical className="h-4 w-4 text-muted-foreground" />Upload plan <span className="font-normal text-muted-foreground">— {live ? 'live' : `dry-run${!status?.enabled ? ' · off' : ''}`}</span>{(status?.checking || !!activeJob) && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />}</h2>
        <div className="flex items-center gap-2">
          {remotes.some((r) => r.cmd) && (
            <button onClick={() => setShowCmds((v) => !v)} className={cn('text-[11px] font-medium', showCmds ? 'text-primary' : 'text-muted-foreground hover:text-foreground')}>
              {showCmds ? 'hide' : 'show'} rclone
            </button>
          )}
          <span className={cn('rounded-full px-2 py-0.5 text-[11px] font-medium', plan.meets_threshold ? 'bg-success/15 text-success' : 'bg-warning/15 text-warning')}>
            {plan.source_human} / {plan.threshold_human} · {plan.meets_threshold ? 'over' : 'below'} auto-trigger
          </span>
        </div>
      </div>
      {startBanner}
      {status?.resume && (
        <p className="rounded-md bg-warning/10 px-3 py-1.5 text-[11px] text-warning">
          Resuming <span className="font-mono font-medium">{status.resume}</span> next — an upload was stopped mid-way; the next run finishes it there first (teldrive keeps the partial) before the rotation continues.
        </p>
      )}
      {remotes.length === 0 ? (
        <p className="rounded-md border border-dashed border-border px-4 py-4 text-center text-xs text-muted-foreground">{plan.files_total === 0 ? 'Source has no uploadable files right now (excludes / in-progress files skipped).' : (plan.leftover_why || 'No eligible remote right now (all capped or cooling down).')}{!plan.meets_threshold && ' Below the auto-trigger threshold — Run now uploads anyway.'}</p>
      ) : (
        <>
          {live && runningRow ? (
            <p className="flex flex-wrap items-center gap-1.5 rounded-md bg-success/10 px-3 py-1.5 text-[11px] text-foreground">
              <span className="flex items-center gap-1 font-medium text-success"><span className="h-1.5 w-1.5 animate-pulse rounded-full bg-success" />Running</span>
              <span className="font-mono">{runRemote}</span> — {fmtBytes(live.transferred)} / {runningRow.human} at <span className="font-medium">{fmtBytes(live.speed)}/s</span> · this step done in <span className="font-medium">{fmtEta(Math.round(etaOf(live.runIdx)))}</span> · whole plan done by <span className="font-medium">{fmtAbs(doneBySec)}</span>
            </p>
          ) : waits ? (
            <p className="text-[11px] text-muted-foreground">If uploaded now, done by <span className="font-medium text-foreground">{fmtAbs(plan.total_eta_sec)}</span> — {fmtEta(plan.transfer_sec)} of transfer plus waits for daily caps to reset.</p>
          ) : null}
          <div className="overflow-x-auto rounded-md border border-border">
            <div className="flex min-w-[34rem] items-center gap-2 border-b border-border bg-secondary/30 px-3 py-1.5 text-[10px] uppercase tracking-wide text-muted-foreground">
              <span className="w-5 shrink-0">#</span>
              <span className="w-40 shrink-0">Remote</span>
              <span className="w-20 shrink-0 text-right" title="Account fill the balancer saw when it picked this remote (levels the emptiest first)">Fill</span>
              <span className="w-20 shrink-0 text-right">Size</span>
              <span className="w-12 shrink-0 text-right">Files</span>
              <span className="min-w-0 flex-1">{live ? 'Starts' : 'Stops at'}</span>
              <span className="w-16 shrink-0 text-right">ETA</span>
            </div>
            {remotes.map((r, i, arr) => {
              const running = live?.rows[i].state === 'running'
              const wait = i > 0 ? Math.max(0, startOf(i) - (startOf(i - 1) + etaOf(i - 1))) : 0
              return (
                <div key={i} className="min-w-[34rem]">
                  {((i === 0 || r.round !== arr[i - 1].round) && r.round > 1 && wait > 60) && (
                    <div className="flex flex-wrap items-center gap-1.5 bg-warning/5 px-3 py-1 text-[10px] font-medium text-warning">
                      <Clock className="h-3 w-3" />Waits {fmtEta(Math.round(wait))} for a remote to free up (daily cap / gap cooldown) · starts {fmtAbs(startOf(i))}
                    </div>
                  )}
                  <div className={cn('flex items-center gap-2 border-b border-border/50 px-3 py-1.5 text-xs last:border-0', running && 'bg-success/5')}>
                    <span className="w-5 shrink-0 tabular-nums text-muted-foreground">{running ? <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-success" /> : i + 1}</span>
                    <span className="flex w-40 shrink-0 items-center gap-1 truncate font-mono text-[11px] text-foreground" title={r.dest}>{r.remote}{running && <span className="shrink-0 rounded bg-success/15 px-1 text-[9px] font-medium text-success">now</span>}</span>
                    <span className="w-20 shrink-0 text-right tabular-nums text-muted-foreground" title="account fill when the balancer picked this remote">{r.fill_human}</span>
                    <span className="w-20 shrink-0 text-right tabular-nums text-foreground">{r.human}</span>
                    <span className="w-12 shrink-0 text-right tabular-nums text-muted-foreground">{r.files}</span>
                    <span className="min-w-0 flex-1 truncate font-mono text-[10px] text-muted-foreground" title={r.stop_file}>{live && !running ? fmtAbs(startOf(i)) : r.capped ? `↳ ${r.stop_file}` : '(all remaining)'}</span>
                    <span className={cn('w-16 shrink-0 text-right tabular-nums', running ? 'font-medium text-foreground' : 'text-muted-foreground')}>{fmtEta(Math.round(etaOf(i)))}</span>
                  </div>
                  {running && (
                    <div className="px-3 pb-1.5 pt-0.5">
                      <div className="h-1 w-full overflow-hidden rounded-full bg-secondary">
                        <div className="h-full rounded-full bg-success transition-all" style={{ width: `${live!.rows[i].pct}%` }} />
                      </div>
                    </div>
                  )}
                </div>
              )
            })}
          </div>
          <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted-foreground">
            <span>{live ? 'Live' : 'Transfer'} <span className="font-medium text-foreground">{fmtEta(live ? Math.round(doneBySec) : plan.transfer_sec)}</span> · done by <span className="font-medium text-foreground">{fmtAbs(doneBySec)}</span> · {plan.files_total} files</span>
            {plan.leftover_bytes > 0 && <span className="text-warning">{plan.leftover_human} left — {plan.leftover_why}</span>}
          </div>
          {showCmds && (
            <CommandList commands={remotes.filter((r) => r.cmd).map((r, i) => ({ remote: `${i + 1}. ${r.remote}`, cmd: r.cmd! }))} />
          )}
        </>
      )}
    </Card>
  )
}

// RecentUploadsCard — the PAST sequence, the mirror of the plan's future: the real
// uploads the balancer already made (which remote, how much, the gap since the previous
// one, how long it took), newest first. Refreshes as new uploads land.
function RecentUploadsCard() {
  const { data: status } = useUploaderStatus()
  const history = status?.history ?? []
  const totalBytes = history.reduce((s, h) => s + h.bytes, 0)
  return (
    <Card className="space-y-3 rounded-xl border-border/70 p-3 shadow-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="flex items-center gap-1.5 text-sm font-semibold text-foreground"><Clock className="h-4 w-4 text-muted-foreground" />Recent uploads <span className="font-normal text-muted-foreground">— actual history</span></h2>
        {history.length > 0 && <span className="rounded-full bg-secondary/40 px-2 py-0.5 text-[11px] font-medium text-muted-foreground">{history.length} · {fmtBytes(totalBytes)}</span>}
      </div>
      {history.length === 0 ? (
        <p className="rounded-md border border-dashed border-border px-4 py-4 text-center text-xs text-muted-foreground">No uploads yet — real uploads will appear here (remote, size, gap, and duration) once auto-upload moves something. This is the actual past that feeds the plan above.</p>
      ) : (
      <div className="max-h-[38vh] overflow-y-auto rounded-md border border-border">
        <div className="sticky top-0 flex items-center gap-2 border-b border-border bg-secondary/30 px-3 py-1.5 text-[10px] uppercase tracking-wide text-muted-foreground">
          <span className="w-40 shrink-0">Remote</span>
          <span className="w-20 shrink-0 text-right">Size</span>
          <span className="w-12 shrink-0 text-right">Files</span>
          <span className="w-16 shrink-0 text-right" title="Gap since the previous upload">Gap</span>
          <span className="w-16 shrink-0 text-right" title="How long the upload took">Took</span>
          <span className="min-w-0 flex-1 text-right">When</span>
        </div>
        {history.map((h, i) => (
          <div key={i} className="flex items-center gap-2 border-b border-border/50 px-3 py-1.5 text-xs last:border-0">
            <span className="w-40 shrink-0 truncate font-mono text-[11px] text-foreground">{h.remote}</span>
            <span className="w-20 shrink-0 text-right tabular-nums text-foreground">{fmtBytes(h.bytes)}</span>
            <span className="w-12 shrink-0 text-right tabular-nums text-muted-foreground">{h.files}</span>
            <span className="w-16 shrink-0 text-right tabular-nums text-muted-foreground">{h.gap_sec > 0 ? fmtEta(h.gap_sec) : '—'}</span>
            <span className="w-16 shrink-0 text-right tabular-nums text-muted-foreground">{fmtEta(h.dur_sec)}</span>
            <span className="min-w-0 flex-1 truncate text-right tabular-nums text-muted-foreground" title={new Date(h.at).toLocaleString()}>{fmtAgo(h.at)}</span>
          </div>
        ))}
      </div>
      )}
    </Card>
  )
}

// ── Uploader hub ──────────────────────────────────────────────────────────────
// Central upload system: the automatic staging→cloud rotation (Auto-upload) and the
// manual/queued/scheduled rclone job manager (Transfers) merged behind one route.
export function Uploader() {
  const [mode, setMode] = useState('auto')
  // Newly-launched jobs auto-expand in the Transfers Activity list.
  const [autoOpenId, setAutoOpenId] = useState<string | null>(null)
  return (
    <div className="mx-auto max-w-[110rem] space-y-4 p-6">
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

      {/* mode switch — Origin UI tabs; forceMount keeps both panels mounted so in-flight
          edits / expanded jobs survive switching. Both modes take the full width (each has
          its own master–detail). */}
      <Tabs value={mode} onValueChange={setMode}>
        <TabsList>
          <TabsTrigger value="auto" className="gap-2"><Zap className="h-4 w-4" />Auto-upload</TabsTrigger>
          <TabsTrigger value="transfers" className="gap-2"><ArrowRightLeft className="h-4 w-4" />Transfers</TabsTrigger>
        </TabsList>
        <TabsContent value="auto" forceMount className="data-[state=inactive]:hidden"><AutoUploadPanel /></TabsContent>
        <TabsContent value="transfers" forceMount className="data-[state=inactive]:hidden"><TransfersPanel onJobStart={setAutoOpenId} /></TabsContent>
      </Tabs>

      {/* Shared Activity — every rclone job (automatic uploads AND manual transfers run the
          same move/copy engine), so it lives outside the mode tabs and stays visible under
          both. Collapsible so it doesn't crowd the configuration above. */}
      <ActivitySection autoOpenId={autoOpenId} />
    </div>
  )
}

// ActivitySection — the combined job list, shown under both modes. Defaults open when a
// job is running so an in-flight upload/transfer is visible without a click.
function ActivitySection({ autoOpenId }: { autoOpenId: string | null }) {
  const [open, setOpen] = useState(true)
  return (
    <div className="overflow-hidden rounded-xl border border-border/70 bg-card shadow-sm">
      <button onClick={() => setOpen((v) => !v)} className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-accent/30">
        <ArrowRightLeft className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="text-xs font-semibold text-foreground">Activity</span>
        <span className="text-[11px] text-muted-foreground">auto-uploads &amp; manual transfers</span>
        <span className="ml-auto">{open ? <ChevronUp className="h-4 w-4 text-muted-foreground" /> : <ChevronDown className="h-4 w-4 text-muted-foreground" />}</span>
      </button>
      {open && (
        <div className="border-t border-border/70 p-3">
          <TransfersActivity autoOpenId={autoOpenId} />
        </div>
      )}
    </div>
  )
}
