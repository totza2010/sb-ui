/**
 * Uploader — cloudplow++ : watch a local staging folder and, once it grows past a
 * threshold, move it up to cloud remotes, rotating across them with per-remote
 * daily caps + cooldowns to dodge quotas / bans.
 */
import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useUploader, useSaveUploader, useUploaderStatus, useUploaderRun, useUploaderSimulate, useTasks, type UploaderConfig, type UploaderRemote } from '@/lib/api'
import { PathPicker } from '@/components/PathPicker'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/cn'
import { Plus, Trash2, Save, Play, FolderInput, CloudUpload, FlaskConical, Loader2, ArrowRight, Pause, Ban, Clock } from 'lucide-react'

const fmtDur = (min: number) => {
  if (min < 60) return `${min}m`
  const h = Math.floor(min / 60), m = min % 60
  if (h < 24) return m ? `${h}h ${m}m` : `${h}h`
  const d = Math.floor(h / 24), hh = h % 24
  return hh ? `${d}d ${hh}h` : `${d}d`
}
const fmtWhen = (iso: string) => new Date(iso).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
const fmtUntil = (a: string, b: string) => fmtDur(Math.round((new Date(b).getTime() - new Date(a).getTime()) / 60000))

const EMPTY: UploaderConfig = {
  enabled: false, source: '', threshold: '500G', strategy: 'lru', interval_minutes: 15,
  allowed_from: '', allowed_until: '', min_age: '15m', delete_empty_src: false,
  excludes: ['**partial~', '**_HIDDEN~', '.unionfs*/**', '**.fuse_hidden**'], remotes: [],
}
const emptyRemote: UploaderRemote = { task_id: '', name: '', dest: '', cap: '', cap_files: 0, gap_min: 0, bwlimit: '', tpslimit: 0 }

export function Uploader() {
  const qc = useQueryClient()
  const { data } = useUploader()
  const save = useSaveUploader()
  const run = useUploaderRun()
  const { data: status } = useUploaderStatus()
  const { data: tasks } = useTasks()
  const sim = useUploaderSimulate()
  const taskName = (id?: string) => tasks?.find((t) => t.id === id)?.name
  const capLabel = (c?: string) => !c ? '∞' : /[a-zA-Z]$/.test(c) ? c : `${c}G` // bare number = GB
  const [simTotal, setSimTotal] = useState('3000G')
  const [simAvg, setSimAvg] = useState('5G')
  const [simPerConn, setSimPerConn] = useState('5M')
  const [simScenario, setSimScenario] = useState('')
  const [simFlood, setSimFlood] = useState('')
  const [cfg, setCfg] = useState<UploaderConfig>(EMPTY)
  const [picker, setPicker] = useState(false)
  const [addOpen, setAddOpen] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => { if (data) setCfg({ ...EMPTY, ...data, remotes: data.remotes ?? [] }) }, [data])

  const up = <K extends keyof UploaderConfig>(k: K, v: UploaderConfig[K]) => setCfg((c) => ({ ...c, [k]: v }))
  const upRemote = (i: number, patch: Partial<UploaderRemote>) => setCfg((c) => { const r = [...c.remotes]; r[i] = { ...r[i], ...patch }; return { ...c, remotes: r } })
  const addTask = (id: string) => setCfg((c) => ({ ...c, remotes: [...c.remotes, { ...emptyRemote, task_id: id, name: tasks?.find((t) => t.id === id)?.name ?? '' }] }))
  const rmRemote = (i: number) => setCfg((c) => ({ ...c, remotes: c.remotes.filter((_, j) => j !== i) }))

  function doSave() {
    save.mutate(cfg, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['uploader'] }); setSaved(true); setTimeout(() => setSaved(false), 2500) } })
  }

  const STRATS: [UploaderConfig['strategy'], string][] = [['lru', 'Least-recently-used'], ['round_robin', 'Round-robin'], ['most_free', 'Most quota free']]

  return (
    <div className="p-6 space-y-5">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-foreground flex items-center gap-2"><CloudUpload className="h-5 w-5" />Uploader</h1>
          <p className="text-sm text-muted-foreground mt-0.5">Auto-move a local folder to cloud once it fills, spread across remotes (quota/ban-aware).</p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <Button size="sm" variant="outline" className="gap-1.5" onClick={() => run.mutate()}><Play className="h-3.5 w-3.5" />Check now</Button>
          <Button size="sm" className="gap-1.5" onClick={doSave} disabled={save.isPending}><Save className="h-3.5 w-3.5" />{saved ? 'Saved' : 'Save'}</Button>
        </div>
      </div>

      {/* live status */}
      {status && (
        <div className="rounded-lg border border-border bg-card p-3 space-y-2 text-sm">
          <div className="flex flex-wrap items-center gap-x-6 gap-y-1">
            <span className="flex items-center gap-1.5"><span className={cn('h-2 w-2 rounded-full', status.enabled ? 'bg-success' : 'bg-muted-foreground/40')} />{status.enabled ? 'Active' : 'Disabled'}</span>
            <span className="text-muted-foreground">Source size: <span className="text-foreground font-medium">{status.last_size}</span> / threshold {status.threshold || '—'}</span>
            <span className="text-muted-foreground">Last check: {status.last_check ? new Date(status.last_check).toLocaleString() : 'never'}</span>
            {status.message && <span className="text-muted-foreground/80 italic">{status.message}</span>}
          </div>
          {status.remotes.length > 0 && (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2 pt-1">
              {status.remotes.map((r, i) => (
                <div key={r.task_id || r.name || i} className={cn('rounded-md border px-2.5 py-1.5', r.paused_until ? 'border-amber-500/50 bg-amber-500/10' : 'border-border bg-secondary/30')}>
                  <p className="text-xs font-medium text-foreground truncate flex items-center gap-1">
                    {r.task_id ? (taskName(r.task_id) ?? 'task') : r.name}
                    {r.task_id && <span className="text-[10px] text-primary">task</span>}
                    {r.paused_until && <span className="text-[10px] text-amber-600 dark:text-amber-400">paused</span>}
                  </p>
                  <p className="text-[11px] text-muted-foreground">today {r.used_today} / {capLabel(r.cap)}{(r.cap_files ?? 0) > 0 && ` · ${r.files_today ?? 0}/${r.cap_files} files`}</p>
                  <p className="text-[10px] text-muted-foreground/70">{r.paused_until ? `until ${new Date(r.paused_until).toLocaleTimeString()}` : `last ${r.last_upload ? new Date(r.last_upload).toLocaleString() : '—'}`}</p>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* config */}
      <div className="space-y-4 rounded-lg border border-border bg-card p-4">
        <label className="flex items-center gap-2 text-sm text-foreground">
          <input type="checkbox" checked={cfg.enabled} onChange={(e) => up('enabled', e.target.checked)} />
          Enable auto-upload
        </label>

        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <div className="space-y-1 sm:col-span-2">
            <Label className="text-[11px]">Source folder (local)</Label>
            <div className="flex gap-2">
              <Input className="h-8 font-mono" value={cfg.source} onChange={(e) => up('source', e.target.value)} placeholder="/mnt/local/Media" />
              <Button size="sm" variant="outline" className="gap-1.5 shrink-0" onClick={() => setPicker(true)}><FolderInput className="h-3.5 w-3.5" />Pick</Button>
            </div>
          </div>
          <div className="space-y-1">
            <Label className="text-[11px]">Upload when size ≥</Label>
            <Input className="h-8" value={cfg.threshold} onChange={(e) => up('threshold', e.target.value)} placeholder="500G" />
          </div>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div className="space-y-1">
            <Label className="text-[11px]">Strategy (which remote next)</Label>
            <div className="flex flex-wrap gap-1.5">
              {STRATS.map(([s, lbl]) => (
                <Button key={s} size="sm" variant={cfg.strategy === s ? 'default' : 'outline'} onClick={() => up('strategy', s)}>{lbl}</Button>
              ))}
            </div>
          </div>
          <div className="space-y-1">
            <Label className="text-[11px]">Check every (minutes)</Label>
            <Input type="number" min={1} className="h-8 w-28" value={cfg.interval_minutes} onChange={(e) => up('interval_minutes', Math.max(1, parseInt(e.target.value, 10) || 15))} />
          </div>
        </div>

        {/* Safety / window options (cloudplow-style) */}
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <div className="space-y-1">
            <Label className="text-[11px]">Upload window (off-peak, optional)</Label>
            <div className="flex items-center gap-1.5">
              <Input type="time" className="h-8 w-28" value={cfg.allowed_from ?? ''} onChange={(e) => up('allowed_from', e.target.value)} />
              <span className="text-xs text-muted-foreground">–</span>
              <Input type="time" className="h-8 w-28" value={cfg.allowed_until ?? ''} onChange={(e) => up('allowed_until', e.target.value)} />
            </div>
          </div>
          <div className="space-y-1">
            <Label className="text-[11px]">Min file age (skip in-progress)</Label>
            <Input className="h-8 w-28" value={cfg.min_age ?? ''} onChange={(e) => up('min_age', e.target.value)} placeholder="15m" />
          </div>
          <label className="flex items-center gap-2 text-xs text-muted-foreground self-end pb-1.5">
            <input type="checkbox" checked={!!cfg.delete_empty_src} onChange={(e) => up('delete_empty_src', e.target.checked)} />
            Delete empty source dirs
          </label>
        </div>

        <div className="space-y-1">
          <Label className="text-[11px]">Exclude patterns (one per line)</Label>
          <textarea
            className="w-full rounded-md border border-border bg-background px-2 py-1.5 text-xs font-mono h-20"
            value={(cfg.excludes ?? []).join('\n')}
            onChange={(e) => up('excludes', e.target.value.split('\n').map((s) => s.trim()).filter(Boolean))}
            placeholder="**partial~&#10;.unionfs*/**"
          />
        </div>

        {/* destinations — pick saved Transfer Tasks to rotate across; each carries its
            own op/dest/flags (made in Transfers). Uploader layers cap/gap governance. */}
        <div className="space-y-2">
          <Label className="text-[11px]">Destination tasks (rotated)</Label>
          {(tasks?.length ?? 0) === 0 && (
            <p className="text-[11px] text-muted-foreground">No transfer tasks yet — create move tasks in <span className="text-foreground">Transfers</span> (one per destination remote), then add them here.</p>
          )}
          <div className="space-y-2">
            {cfg.remotes.map((r, i) => {
              const task = tasks?.find((t) => t.id === r.task_id)
              return (
                <div key={i} className="flex flex-wrap items-end gap-2 rounded-md border border-border bg-secondary/20 p-2.5">
                  <div className="space-y-1 min-w-[260px] flex-1">
                    <Label className="text-[10px] text-muted-foreground">Task</Label>
                    <div className="h-8 flex items-center rounded-md border border-border bg-background px-2.5 text-sm font-medium text-foreground truncate">
                      {task ? task.name : <span className="text-destructive">task removed</span>}
                    </div>
                  </div>
                  <div className="space-y-1">
                    <Label className="text-[10px] text-muted-foreground">Cap GB / day</Label>
                    <Input className="h-8 w-28" value={r.cap} onChange={(e) => upRemote(i, { cap: e.target.value })} placeholder="700G / ∞" />
                  </div>
                  <div className="space-y-1">
                    <Label className="text-[10px] text-muted-foreground">Cap files / day</Label>
                    <Input type="number" className="h-8 w-24" value={r.cap_files ?? 0} onChange={(e) => upRemote(i, { cap_files: Math.max(0, parseInt(e.target.value, 10) || 0) })} placeholder="0 = ∞" />
                  </div>
                  <div className="space-y-1">
                    <Label className="text-[10px] text-muted-foreground">Gap (min)</Label>
                    <Input type="number" className="h-8 w-24" value={r.gap_min} onChange={(e) => upRemote(i, { gap_min: Math.max(0, parseInt(e.target.value, 10) || 0) })} placeholder="0" />
                  </div>
                  <Button size="icon" variant="ghost" className="h-8 w-8 mb-0.5" onClick={() => rmRemote(i)}><Trash2 className="h-3.5 w-3.5 text-destructive" /></Button>
                  {task && <p className="basis-full text-[11px] text-muted-foreground pl-0.5">{task.op} {task.items.length} item(s) → <span className="font-mono">{task.dst}</span> — flags & schedule managed in Transfers.</p>}
                </div>
              )
            })}
          </div>
          <Button size="sm" variant="outline" className="gap-1.5" onClick={() => setAddOpen(true)} disabled={(tasks?.length ?? 0) === 0}><Plus className="h-3.5 w-3.5" />Add task</Button>
          <p className="text-[11px] text-muted-foreground">Add one entry per destination remote (each = a saved move task). Cap/day empty = unlimited (e.g. teldrive); set 700G for Google Drive. Gap = min minutes before reusing the same task. The chosen <span className="text-foreground">Strategy</span> rotates across them.</p>
        </div>
      </div>

      {/* dry-run simulation — replays the rotation engine on the SAVED config with a
          throwaway ledger (no real uploads), so you can see how it behaves. */}
      <div className="space-y-3 rounded-lg border border-border bg-card p-4">
        <div className="flex items-center gap-2">
          <FlaskConical className="h-4 w-4 text-primary" />
          <h2 className="text-sm font-semibold text-foreground">Simulate rotation (dry-run)</h2>
        </div>
        <p className="text-[11px] text-muted-foreground -mt-1">Drains a backlog across your <span className="text-foreground">current</span> remotes (no need to Save) — caps, gaps, window & rate-limit pauses included. Upload speed = task <span className="font-mono">bwlimit</span> if set, else <span className="font-mono">transfers × upload_concurrency</span> (from task + rclone.conf) × the per-connection speed below. tpslimit is a ban guard, not a speed. Nothing is uploaded.</p>
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
                {cfg.remotes.filter((r) => r.name).map((r, i) => <option key={i} value={r.name}>{taskName(r.task_id) ?? r.name}</option>)}
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
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2">
              {sim.data.summary.map((r, i) => (
                <div key={i} className="rounded-md border border-border bg-secondary/30 px-2.5 py-1.5">
                  <p className="text-xs font-medium text-foreground truncate">{r.task_id ? (taskName(r.task_id) ?? r.name) : r.name}</p>
                  <p className="text-[11px] text-muted-foreground">{r.bytes} / {capLabel(r.cap)} · {r.files}{r.cap_files > 0 && ` / ${r.cap_files}`} files</p>
                </div>
              ))}
            </div>
            {/* timeline (moves + collapsed waits) */}
            <div className="rounded-md border border-border divide-y divide-border max-h-[42vh] overflow-y-auto">
              {sim.data.steps.map((s, i) => (
                <div key={i} className="flex items-center gap-3 px-3 py-1.5 text-sm">
                  <span className="w-24 shrink-0 text-[11px] text-muted-foreground/70 tabular-nums">{fmtWhen(s.at)}</span>
                  {s.kind === 'move' ? (
                    <span className="flex items-center gap-2 min-w-0 flex-1">
                      <span className={cn('h-2 w-2 rounded-full shrink-0', s.paused ? 'bg-amber-500' : 'bg-success')} />
                      <span className="font-medium text-foreground truncate">{taskName(s.task_id) ?? s.remote}</span>
                      <ArrowRight className="h-3 w-3 text-muted-foreground shrink-0" />
                      <span className="text-[11px] text-muted-foreground shrink-0">{s.bytes} · {s.files} files · {s.rate}{(s.took_min ?? 0) > 0 && ` ≈ ${fmtDur(s.took_min!)}`} · {s.remaining} left</span>
                      {s.paused && <span className="flex items-center gap-1 text-[11px] text-amber-600 dark:text-amber-400 shrink-0"><Pause className="h-3 w-3" />paused 60m</span>}
                    </span>
                  ) : (
                    <span className="flex items-center gap-2 min-w-0 flex-1 text-muted-foreground">
                      {s.kind === 'wait' ? <Clock className="h-3 w-3 shrink-0 text-amber-500" /> : <Ban className="h-3 w-3 shrink-0 text-destructive" />}
                      <span className="truncate text-[12px] italic">{s.note}{s.until && ` (≈${fmtUntil(s.at, s.until)})`}</span>
                    </span>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}
      </div>

      {picker && (
        <PathPicker mode="folder" onClose={() => setPicker(false)} onPick={(p) => { if (p[0]) up('source', p[0].path); setPicker(false) }} />
      )}

      {/* Add-task picker: list every saved task; click to add (added ones drop out so
          you can pick several without reopening). */}
      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader><DialogTitle>Add destination tasks</DialogTitle></DialogHeader>
          {(() => {
            const chosen = new Set(cfg.remotes.map((r) => r.task_id).filter(Boolean))
            const avail = (tasks ?? []).filter((t) => !chosen.has(t.id))
            if (avail.length === 0) return <p className="text-sm text-muted-foreground py-2">All tasks added. Create more in Transfers.</p>
            return (
              <div className="max-h-[60vh] overflow-y-auto space-y-1.5">
                {avail.map((t) => (
                  <button key={t.id} onClick={() => addTask(t.id)} className="flex w-full items-center justify-between gap-3 rounded-md border border-border bg-card px-3 py-2 text-left hover:bg-accent transition-colors">
                    <span className="min-w-0">
                      <span className="block text-sm font-medium text-foreground truncate">{t.name}</span>
                      <span className="block text-[11px] text-muted-foreground truncate">{t.op} {t.items.length} item(s) → <span className="font-mono">{t.dst}</span></span>
                    </span>
                    <Plus className="h-4 w-4 shrink-0 text-primary" />
                  </button>
                ))}
              </div>
            )
          })()}
          <div className="flex justify-end pt-1"><Button size="sm" onClick={() => setAddOpen(false)}>Done</Button></div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
