/**
 * Transfers — pure transfer management: launch rclone copy/move/sync jobs and
 * watch them. Browsing remotes lives on the Files page (rclone group).
 */
import { useEffect, useMemo, useState } from 'react'
import { useRcloneTransfer, useJobs, useRcloneRemotes, useRcloneProviders, useTransferStats, useTasks, useCreateTask, useUpdateTask, useDeleteTask, useRunTask, useQueueTask, useToggleTask, useStopTransfer, useQueue, useQueueAction, type TransferOpts, type FlagInfo, type TransferTask } from '@/lib/api'
import { useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { LogStream } from '@/components/LogStream'
import { PathPicker, type PickItem } from '@/components/PathPicker'
import { cn } from '@/lib/cn'
import { ArrowRightLeft, Rocket, FolderInput, FilePlus2, X, ChevronDown, ChevronRight, Plus, Save, Clock, Play, ListPlus, Pencil, Trash2, Square, Pause, ArrowUp, ArrowDown } from 'lucide-react'

const TRANSFER_OPS = new Set(['copy', 'move', 'sync'])
const statusVariant = { completed: 'success', running: 'default', failed: 'destructive', pending: 'secondary', stopped: 'secondary' } as const

// Mirror the server's flag building so the dialog can preview the rclone command.
function buildFlags(op: string, o: TransferOpts, dryRun: boolean): string[] {
  const f: string[] = []
  if (dryRun) f.push('--dry-run')
  if (o.transfers) f.push('--transfers', String(o.transfers))
  if (o.checkers) f.push('--checkers', String(o.checkers))
  if (o.tpslimit) f.push('--tpslimit', String(o.tpslimit))
  if (o.retries) f.push('--retries', String(o.retries))
  if (o.bwlimit) f.push('--bwlimit', o.bwlimit)
  if (o.ignore_existing) f.push('--ignore-existing')
  if (o.update) f.push('--update')
  if (o.create_empty_src_dirs) f.push('--create-empty-src-dirs')
  if (o.no_traverse) f.push('--no-traverse')
  if (o.one_file_system) f.push('--one-file-system')
  if (o.fast_list) f.push('--fast-list')
  if (o.compare) f.push(`--${o.compare}`)
  if (op === 'sync' && o.sync_delete) f.push(`--delete-${o.sync_delete}`)
  for (const p of o.include ?? []) if (p.trim()) f.push('--include', p.trim())
  for (const p of o.exclude ?? []) if (p.trim()) f.push('--exclude', p.trim())
  for (const e of o.extra ?? []) if (e.flag) { f.push(e.flag); if (e.value) f.push(e.value) }
  return f
}

export function Transfers() {
  const { data: jobs = [] } = useJobs()
  const transfers = useMemo(
    () => jobs.filter((j) => TRANSFER_OPS.has(j.action)).sort((a, b) => b.created_at.localeCompare(a.created_at)),
    [jobs],
  )

  const transfer = useRcloneTransfer()
  const [dlg, setDlg] = useState(false)
  const [op, setOp] = useState<'copy' | 'move' | 'sync'>('copy')
  const [items, setItems] = useState<PickItem[]>([])
  const [dst, setDst] = useState('')
  const [dryRun, setDryRun] = useState(false)
  const [picker, setPicker] = useState<'src' | 'dst' | null>(null)
  const [autoOpenId, setAutoOpenId] = useState<string | null>(null)
  const [opts, setOpts] = useState<TransferOpts>({})
  const [showSettings, setShowSettings] = useState(false)
  const [name, setName] = useState('')
  const [schedule, setSchedule] = useState('')
  const [runMode, setRunMode] = useState<'queue' | 'now'>('queue')
  const [editingId, setEditingId] = useState<string | null>(null)
  const setOpt = <K extends keyof TransferOpts>(k: K, v: TransferOpts[K]) => setOpts((o) => ({ ...o, [k]: v }))

  // Tasks (saved transfers) + actions
  const qc = useQueryClient()
  const { data: tasks = [] } = useTasks()
  const createTask = useCreateTask()
  const updateTask = useUpdateTask()
  const deleteTask = useDeleteTask()
  const runTask = useRunTask()
  const queueTask = useQueueTask()
  const toggleTask = useToggleTask()
  const invalidateTasks = () => qc.invalidateQueries({ queryKey: ['tasks'] })

  // Queue manager
  const { data: queue } = useQueue()
  const queueAction = useQueueAction()
  const invalidateQueue = () => qc.invalidateQueries({ queryKey: ['queue'] })
  const doQueue = (path: string) => queueAction.mutate(path, { onSuccess: invalidateQueue })

  // Available rclone flags: global + the backend(s) of the remotes in this transfer.
  const { data: conf } = useRcloneRemotes()
  const { data: providers } = useRcloneProviders()
  const { available, catalog, types } = useMemo(() => {
    const ts = new Set<string>()
    for (const ep of [...items.map((i) => i.path), dst]) {
      const c = ep.indexOf(':')
      if (c > 0 && !ep.startsWith('/')) {
        const t = conf?.remotes?.[ep.slice(0, c)]?.type
        if (t) ts.add(t)
      }
    }
    const list: FlagInfo[] = [...(providers?.global ?? [])]
    for (const t of ts) for (const fl of providers?.backends?.[t] ?? []) list.push(fl)
    const cat = new Map(list.map((f) => [f.flag, f]))
    return { available: list, catalog: cat, types: [...ts] }
  }, [items, dst, conf, providers])

  const [flagList, setFlagList] = useState(false)
  const [flagSearch, setFlagSearch] = useState('')
  const addExtraFlag = (flag: string) => setOpts((o) => (o.extra?.some((e) => e.flag === flag) ? o : { ...o, extra: [...(o.extra ?? []), { flag, value: '' }] }))
  const updExtra = (i: number, v: string) => setOpts((o) => { const e = [...(o.extra ?? [])]; e[i] = { ...e[i], value: v }; return { ...o, extra: e } })
  const rmExtra = (i: number) => setOpts((o) => ({ ...o, extra: (o.extra ?? []).filter((_, j) => j !== i) }))

  function reset() { setItems([]); setDst(''); setOp('copy'); setDryRun(false); setOpts({}); setName(''); setSchedule(''); setRunMode('queue'); setEditingId(null); setShowSettings(false); setFlagList(false); setFlagSearch('') }
  function openNew() { reset(); setDlg(true) }
  function openEdit(t: TransferTask) {
    setItems(t.items); setDst(t.dst); setOp(t.op); setDryRun(!!t.dry_run); setOpts(t.opts ?? {})
    setName(t.name); setSchedule(t.schedule ?? ''); setRunMode(t.run_mode === 'now' ? 'now' : 'queue'); setEditingId(t.id)
    setShowSettings(false); setFlagList(false); setFlagSearch(''); setDlg(true)
  }
  function start() {
    transfer.mutate({ op, items, dst, dry_run: dryRun, opts }, { onSuccess: (d) => { setDlg(false); setAutoOpenId(d.job_id) } })
  }
  function queueNow() {
    transfer.mutate({ op, items, dst, dry_run: dryRun, opts, queue: true }, { onSuccess: (d) => { setDlg(false); setAutoOpenId(d.job_id) } })
  }
  function saveTask() {
    const body = { name: name || undefined, op, items, dst, dry_run: dryRun, opts, schedule: schedule || undefined, run_mode: schedule ? runMode : undefined } as Parameters<typeof createTask.mutate>[0]
    const done = () => { setDlg(false); invalidateTasks() }
    if (editingId) updateTask.mutate({ id: editingId, ...body }, { onSuccess: done })
    else createTask.mutate(body, { onSuccess: done })
  }

  const cmdPreview = `rclone ${op} <source> ${dst || '<dest>'} --stats 1s ${buildFlags(op, opts, dryRun).join(' ')}`.replace(/\s+/g, ' ').trim()

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-foreground">Transfers</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            Run and watch rclone copy / move / sync jobs. Browse remotes on the Files page.
          </p>
        </div>
        <Button size="sm" className="gap-1.5 shrink-0" onClick={openNew}>
          <ArrowRightLeft className="h-3.5 w-3.5" />New transfer
        </Button>
      </div>

      {/* Saved tasks (run / queue / schedule) */}
      {tasks.length > 0 && (
        <div>
          <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">Tasks</p>
          <div className="border border-border rounded-lg divide-y divide-border overflow-hidden">
            {tasks.map((t) => (
              <div key={t.id} className="flex items-center gap-3 px-4 py-2.5">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className={cn('text-sm font-medium truncate', t.disabled ? 'text-muted-foreground line-through' : 'text-foreground')}>{t.name}</span>
                    <Badge variant="secondary" className="text-[9px] capitalize">{t.op}</Badge>
                    {t.schedule && <span className="flex items-center gap-1 text-[10px] text-muted-foreground"><Clock className="h-3 w-3" />{t.schedule}</span>}
                    {t.disabled && <Badge variant="secondary" className="text-[9px]">paused</Badge>}
                  </div>
                  <p className="font-mono text-[11px] text-muted-foreground truncate">
                    {t.items.length} item(s) → {t.dst}
                    {t.schedule && !t.disabled && t.next_run && <span className="ml-2 text-muted-foreground/70">· next {new Date(t.next_run).toLocaleString()}</span>}
                  </p>
                </div>
                <div className="flex items-center gap-1 shrink-0">
                  <Button size="sm" variant="outline" className="gap-1.5" onClick={() => runTask.mutate(t.id, { onSuccess: (d) => setAutoOpenId(d.job_id) })}><Play className="h-3.5 w-3.5" />Run</Button>
                  <Button size="sm" variant="outline" className="gap-1.5" onClick={() => queueTask.mutate(t.id, { onSuccess: (d) => setAutoOpenId(d.job_id) })}><ListPlus className="h-3.5 w-3.5" />Queue</Button>
                  {t.schedule && (
                    <Button size="icon" variant="ghost" className="h-8 w-8" title={t.disabled ? 'Resume schedule' : 'Pause schedule'} onClick={() => toggleTask.mutate(t.id, { onSuccess: invalidateTasks })}>
                      {t.disabled ? <Play className="h-3.5 w-3.5 text-success" /> : <Pause className="h-3.5 w-3.5 text-warning" />}
                    </Button>
                  )}
                  <Button size="icon" variant="ghost" className="h-8 w-8" onClick={() => openEdit(t)}><Pencil className="h-3.5 w-3.5" /></Button>
                  <Button size="icon" variant="ghost" className="h-8 w-8" onClick={() => deleteTask.mutate(t.id, { onSuccess: invalidateTasks })}><Trash2 className="h-3.5 w-3.5 text-destructive" /></Button>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Scheduler — tasks with a cron schedule (always shown) */}
      <div>
        <div className="flex items-center gap-2 mb-1.5">
          <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Scheduler</p>
          <span className="text-[11px] text-muted-foreground">{tasks.filter((t) => t.schedule && !t.disabled).length} active</span>
        </div>
        <div className="border border-border rounded-lg divide-y divide-border overflow-hidden">
          {!tasks.some((t) => t.schedule) && (
            <div className="px-4 py-6 text-center text-[11px] text-muted-foreground">No scheduled tasks. Add a Schedule (cron) when saving a task to run it automatically.</div>
          )}
          {tasks.filter((t) => t.schedule).map((t) => (
              <div key={t.id} className="flex items-center gap-3 px-4 py-2.5">
                <Clock className={cn('h-4 w-4 shrink-0', t.disabled ? 'text-muted-foreground/50' : 'text-primary')} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className={cn('text-sm font-medium truncate', t.disabled ? 'text-muted-foreground line-through' : 'text-foreground')}>{t.name}</span>
                    <code className="text-[10px] text-muted-foreground">{t.schedule}</code>
                    <Badge variant={t.disabled ? 'secondary' : 'success'} className="text-[9px]">{t.disabled ? 'paused' : 'active'}</Badge>
                  </div>
                  <p className="text-[11px] text-muted-foreground">
                    {t.disabled ? 'paused' : t.next_run ? `next run ${new Date(t.next_run).toLocaleString()}` : 'computing…'}
                  </p>
                </div>
                <div className="flex items-center gap-1 shrink-0">
                  <Button size="sm" variant="outline" className="gap-1.5" onClick={() => runTask.mutate(t.id, { onSuccess: (d) => setAutoOpenId(d.job_id) })}><Play className="h-3.5 w-3.5" />Run now</Button>
                  <Button size="sm" variant="outline" className="gap-1.5" onClick={() => toggleTask.mutate(t.id, { onSuccess: invalidateTasks })}>
                    {t.disabled ? <><Play className="h-3.5 w-3.5" />Resume</> : <><Pause className="h-3.5 w-3.5" />Pause</>}
                  </Button>
                </div>
              </div>
            ))}
          </div>
        </div>

      {/* Queue (pending, runs one at a time) — always shown so it can be paused */}
      {queue && (
        <div>
          <div className="flex items-center gap-2 mb-1.5">
            <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Queue</p>
            <Badge variant={queue.running ? 'default' : 'secondary'} className="text-[9px]">{queue.running ? 'running' : 'paused'}</Badge>
            <span className="text-[11px] text-muted-foreground">{queue.items.length} queued</span>
            <div className="ml-auto flex gap-1">
              {queue.running
                ? <Button size="sm" variant="outline" className="gap-1.5" onClick={() => doQueue('/stop')}><Pause className="h-3.5 w-3.5" />Pause</Button>
                : <Button size="sm" variant="outline" className="gap-1.5" onClick={() => doQueue('/start')}><Play className="h-3.5 w-3.5" />Start</Button>}
              <Button size="sm" variant="ghost" className="gap-1.5 text-destructive" onClick={() => doQueue('/purge')} disabled={queue.items.length === 0}><Trash2 className="h-3.5 w-3.5" />Purge</Button>
            </div>
          </div>
          <div className="border border-border rounded-lg divide-y divide-border overflow-hidden">
            {queue.current && (
              <div className="flex items-center gap-3 px-4 py-2 bg-primary/5">
                <span className="text-[11px] text-primary w-5 shrink-0">▶</span>
                <span className="font-mono text-xs text-foreground truncate flex-1 min-w-0">{queue.current.label}</span>
                <Badge variant="default" className="text-[9px]">running</Badge>
              </div>
            )}
            {!queue.current && queue.items.length === 0 && (
              <div className="px-4 py-6 text-center text-[11px] text-muted-foreground">Queue empty. Pause it, then “Queue” a task to line jobs up.</div>
            )}
            {queue.items.map((it, i) => (
              <div key={it.job_id} className="flex items-center gap-3 px-4 py-2">
                <span className="text-[11px] text-muted-foreground w-5 shrink-0">{i + 1}</span>
                <span className="font-mono text-xs text-foreground truncate flex-1 min-w-0">{it.label}</span>
                <Badge variant="secondary" className="text-[9px]">queued</Badge>
                <div className="flex items-center gap-0.5 shrink-0">
                  <Button size="icon" variant="ghost" className="h-7 w-7" disabled={i === 0} onClick={() => doQueue(`/${it.job_id}/up`)}><ArrowUp className="h-3.5 w-3.5" /></Button>
                  <Button size="icon" variant="ghost" className="h-7 w-7" disabled={i === queue.items.length - 1} onClick={() => doQueue(`/${it.job_id}/down`)}><ArrowDown className="h-3.5 w-3.5" /></Button>
                  <Button size="icon" variant="ghost" className="h-7 w-7" onClick={() => doQueue(`/${it.job_id}/remove`)}><X className="h-3.5 w-3.5 text-destructive" /></Button>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Recent / active transfers (incl. queued = pending) */}
      <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">Activity</p>
      <div className="border border-border rounded-lg divide-y divide-border overflow-hidden">
        {transfers.length === 0 && (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">
            No transfers yet. Click <span className="text-foreground font-medium">New transfer</span> to start one.
          </div>
        )}
        {transfers.map((j) => (
          <ActivityRow key={j.id} job={j} autoOpen={j.id === autoOpenId} />
        ))}
      </div>

      {/* New transfer dialog — hidden while the picker is open (avoid nested modals) */}
      <Dialog open={dlg && !picker} onOpenChange={(o) => { if (!o && !picker) setDlg(false) }}>
        <DialogContent className="max-w-xl max-h-[88vh] overflow-y-auto">
          <DialogHeader><DialogTitle>{editingId ? 'Edit task' : 'New transfer'}</DialogTitle></DialogHeader>
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label>Operation</Label>
              <div className="flex gap-2">
                {(['copy', 'move', 'sync'] as const).map((o) => (
                  <Button key={o} size="sm" variant={op === o ? 'default' : 'outline'} onClick={() => setOp(o)} className="capitalize">{o}</Button>
                ))}
              </div>
              {op === 'sync' && <p className="text-[11px] text-warning">sync makes dest identical to src — extra files in dest are deleted.</p>}
            </div>
            {/* Source — one or more files/folders. Same layout as destination. */}
            <div className="space-y-1.5">
              <Label>Source</Label>
              <div className="flex items-start gap-2">
                <div className="flex-1 min-w-0 min-h-9 rounded border border-border px-2 py-1.5 flex flex-wrap items-center gap-1">
                  {items.length === 0
                    ? <span className="text-xs text-muted-foreground">—</span>
                    : items.map((it, i) => (
                      <span key={i} className="flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 text-[11px] max-w-full">
                        {it.is_dir ? <FolderInput className="h-3 w-3 text-blue-400 shrink-0" /> : <FilePlus2 className="h-3 w-3 text-muted-foreground shrink-0" />}
                        <span className="font-mono truncate">{it.path}</span>
                        <button onClick={() => setItems((a) => a.filter((_, j) => j !== i))} className="text-muted-foreground hover:text-destructive shrink-0"><X className="h-3 w-3" /></button>
                      </span>
                    ))}
                </div>
                <Button size="sm" variant="outline" className="gap-1.5 shrink-0" onClick={() => setPicker('src')}>
                  <FolderInput className="h-3.5 w-3.5" />Choose…
                </Button>
              </div>
            </div>

            {/* Destination — a single folder. */}
            <div className="space-y-1.5">
              <Label>Destination folder</Label>
              <div className="flex items-start gap-2">
                <div className="flex-1 min-w-0 min-h-9 rounded border border-border px-2 py-1.5 flex items-center">
                  <span className={cn('text-xs font-mono truncate', !dst && 'text-muted-foreground')}>{dst || '—'}</span>
                </div>
                <Button size="sm" variant="outline" className="gap-1.5 shrink-0" onClick={() => setPicker('dst')}>
                  <FolderInput className="h-3.5 w-3.5" />Choose…
                </Button>
              </div>
            </div>

            {/* Settings — rclone flags */}
            <div>
              <button onClick={() => setShowSettings((s) => !s)} className="flex items-center gap-1 text-sm font-medium text-foreground">
                {showSettings ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}Settings
              </button>
              {showSettings && (
                <div className="mt-2 space-y-3 rounded-md border border-border p-3">
                  <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
                    <NumField label="Transfers" v={opts.transfers} on={(n) => setOpt('transfers', n)} ph="4" />
                    <NumField label="Checkers" v={opts.checkers} on={(n) => setOpt('checkers', n)} ph="8" />
                    <NumField label="tps limit" v={opts.tpslimit} on={(n) => setOpt('tpslimit', n)} ph="off" />
                    <NumField label="Retries" v={opts.retries} on={(n) => setOpt('retries', n)} ph="3" />
                    <div className="space-y-1 col-span-2 sm:col-span-1">
                      <Label className="text-[11px]">Bandwidth</Label>
                      <Input className="h-8" value={opts.bwlimit ?? ''} onChange={(e) => setOpt('bwlimit', e.target.value)} placeholder="e.g. 10M" />
                    </div>
                  </div>
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-1.5">
                    <Chk label="Skip existing (--ignore-existing)" v={!!opts.ignore_existing} on={(b) => setOpt('ignore_existing', b)} />
                    <Chk label="Skip newer on dest (--update)" v={!!opts.update} on={(b) => setOpt('update', b)} />
                    <Chk label="Create empty src dirs" v={!!opts.create_empty_src_dirs} on={(b) => setOpt('create_empty_src_dirs', b)} />
                    <Chk label="No traverse (small→large)" v={!!opts.no_traverse} on={(b) => setOpt('no_traverse', b)} />
                    <Chk label="Fast list (--fast-list)" v={!!opts.fast_list} on={(b) => setOpt('fast_list', b)} />
                  </div>
                  <div className="space-y-1">
                    <Label className="text-[11px]">Compare method</Label>
                    <div className="flex flex-wrap gap-1.5">
                      {([['', 'Size & mod-time'], ['checksum', 'Checksum'], ['size-only', 'Size only'], ['ignore-size', 'Ignore size']] as const).map(([v, lbl]) => (
                        <Button key={v || 'default'} size="sm" variant={(opts.compare ?? '') === v ? 'default' : 'outline'} onClick={() => setOpt('compare', v)}>{lbl}</Button>
                      ))}
                    </div>
                  </div>
                  {op === 'sync' && (
                    <div className="space-y-1">
                      <Label className="text-[11px]">Sync delete order</Label>
                      <div className="flex gap-1.5">
                        {(['during', 'after', 'before'] as const).map((d) => (
                          <Button key={d} size="sm" variant={opts.sync_delete === d ? 'default' : 'outline'} className="capitalize" onClick={() => setOpt('sync_delete', opts.sync_delete === d ? '' : d)}>{d}</Button>
                        ))}
                      </div>
                    </div>
                  )}
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                    <PatField label="Include (comma sep)" v={opts.include} on={(a) => setOpt('include', a)} ph="*.mkv, *.mp4" />
                    <PatField label="Exclude (comma sep)" v={opts.exclude} on={(a) => setOpt('exclude', a)} ph="*.nfo, *.txt" />
                  </div>

                  {/* Extra rclone flags — pick from rclone's own flag list (global +
                      the backend of the chosen remotes, e.g. teldrive) with descriptions. */}
                  <div className="space-y-1.5">
                    <Label className="text-[11px]">
                      Extra flags{types.length > 0 && <span className="text-muted-foreground/70"> · incl. {types.join(', ')}</span>}
                    </Label>

                    {(opts.extra ?? []).map((e, i) => {
                      const info = catalog.get(e.flag)
                      const isBool = info?.type === 'bool'
                      return (
                        <div key={i} className="rounded border border-border p-2 space-y-1">
                          <div className="flex items-center gap-2">
                            <span className="font-mono text-xs text-foreground">{e.flag}</span>
                            {info?.type && <Badge variant="secondary" className="text-[9px]">{info.type}</Badge>}
                            <button onClick={() => rmExtra(i)} className="ml-auto text-muted-foreground hover:text-destructive"><X className="h-3.5 w-3.5" /></button>
                          </div>
                          {info?.help && <p className="text-[11px] text-muted-foreground">{info.help}</p>}
                          {!isBool && <Input className="h-7" value={e.value} placeholder="value" onChange={(ev) => updExtra(i, ev.target.value)} />}
                        </div>
                      )
                    })}

                    {flagList ? (
                      <div className="rounded border border-border p-2 space-y-2">
                        <Input className="h-8" autoFocus placeholder="Search flags…" value={flagSearch} onChange={(e) => setFlagSearch(e.target.value)} />
                        <div className="max-h-56 overflow-auto space-y-0.5">
                          {available
                            .filter((f) => !flagSearch || f.flag.includes(flagSearch.toLowerCase()) || f.help.toLowerCase().includes(flagSearch.toLowerCase()))
                            .slice(0, 80)
                            .map((f) => (
                              <button key={f.flag} onClick={() => { addExtraFlag(f.flag); setFlagList(false); setFlagSearch('') }}
                                className="w-full text-left rounded px-2 py-1 hover:bg-accent">
                                <div className="flex items-center gap-2">
                                  <span className="font-mono text-xs text-foreground">{f.flag}</span>
                                  {f.type && <Badge variant="secondary" className="text-[9px]">{f.type}</Badge>}
                                </div>
                                {f.help && <p className="text-[11px] text-muted-foreground truncate">{f.help}</p>}
                              </button>
                            ))}
                          {available.length === 0 && <p className="text-[11px] text-muted-foreground px-2 py-1">Flag list unavailable.</p>}
                        </div>
                        <Button size="sm" variant="ghost" onClick={() => { setFlagList(false); setFlagSearch('') }}>Close</Button>
                      </div>
                    ) : (
                      <Button size="sm" variant="outline" className="gap-1.5" onClick={() => setFlagList(true)}><Plus className="h-3.5 w-3.5" />Add flag…</Button>
                    )}
                  </div>
                </div>
              )}
            </div>

            {/* Save as task (optional) + schedule */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
              <div className="space-y-1">
                <Label className="text-[11px]">Task name (to save)</Label>
                <Input className="h-8" value={name} onChange={(e) => setName(e.target.value)} placeholder="optional" />
              </div>
              <div className="space-y-1">
                <Label className="text-[11px]">Schedule</Label>
                <ScheduleField value={schedule} onChange={setSchedule} />
                {schedule && (
                  <div className="flex items-center gap-2 pt-1">
                    <span className="text-[11px] text-muted-foreground">When due:</span>
                    {(['queue', 'now'] as const).map((m) => (
                      <Button key={m} size="sm" variant={runMode === m ? 'default' : 'outline'} onClick={() => setRunMode(m)}>
                        {m === 'queue' ? 'Add to queue' : 'Run immediately'}
                      </Button>
                    ))}
                  </div>
                )}
              </div>
            </div>

            {/* Command preview */}
            <div className="rounded-md bg-[#0d1117] text-[#c9d1d9] p-2 text-[11px] font-mono break-all">{cmdPreview}</div>

            <label className="flex items-center gap-2 text-sm text-muted-foreground">
              <input type="checkbox" checked={dryRun} onChange={(e) => setDryRun(e.target.checked)} />
              Dry run (preview, no changes)
            </label>
            <div className="flex flex-wrap justify-end gap-2">
              <Button size="sm" variant="outline" onClick={() => setDlg(false)}>Cancel</Button>
              <Button size="sm" variant="outline" className="gap-1.5" onClick={saveTask} disabled={items.length === 0 || !dst || createTask.isPending || updateTask.isPending}>
                <Save className="h-3.5 w-3.5" />{editingId ? 'Update task' : 'Save task'}
              </Button>
              {!editingId && (
                <Button size="sm" variant="outline" className="gap-1.5" onClick={queueNow} disabled={transfer.isPending || items.length === 0 || !dst}>
                  <ListPlus className="h-3.5 w-3.5" />Queue
                </Button>
              )}
              <Button size="sm" className="gap-1.5" onClick={start} disabled={transfer.isPending || items.length === 0 || !dst}>
                <Rocket className="h-3.5 w-3.5" />Start
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      {/* Path picker (source = multi, destination = folder) */}
      {picker && (
        <PathPicker
          mode={picker === 'src' ? 'multi' : 'folder'}
          onClose={() => setPicker(null)}
          onPick={(picked) => {
            if (picker === 'src') setItems((a) => [...a, ...picked])
            else setDst(picked[0]?.path ?? '')
            setPicker(null)
          }}
        />
      )}

    </div>
  )
}

// ── one expandable activity row (RcloneBrowser-style inline job view) ──────────
function ActivityRow({ job, autoOpen }: { job: { id: string; tag: string; status: 'pending' | 'running' | 'completed' | 'failed' | 'stopped'; created_at: string }; autoOpen: boolean }) {
  const [open, setOpen] = useState(autoOpen)
  const [out, setOut] = useState(false)
  const stop = useStopTransfer()
  const active = job.status === 'running'
  useEffect(() => { if (autoOpen) setOpen(true) }, [autoOpen])
  return (
    <div>
      <button onClick={() => setOpen((o) => !o)} className="flex items-center gap-2 w-full px-4 py-2.5 text-left hover:bg-muted/40">
        {open ? <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" /> : <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />}
        <span className="font-mono text-xs text-foreground truncate flex-1 min-w-0">{job.tag}</span>
        {active && (
          <span role="button" tabIndex={0}
            onClick={(e) => { e.stopPropagation(); stop.mutate(job.id) }}
            className="flex items-center gap-1 text-[11px] text-destructive hover:underline shrink-0">
            <Square className="h-3 w-3" />Stop
          </span>
        )}
        <Badge variant={statusVariant[job.status]}>{job.status}</Badge>
        <span className="text-[11px] text-muted-foreground shrink-0 w-32 text-right">{new Date(job.created_at).toLocaleString()}</span>
      </button>
      {open && (
        <div className="px-4 pb-3 space-y-2">
          <TransferProgress jobId={job.id} running={job.status === 'running'} />
          <button onClick={() => setOut((s) => !s)} className="flex items-center gap-1 text-xs font-medium text-foreground">
            {out ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}Show output
          </button>
          {out && <LogStream jobId={job.id} />}
        </div>
      )}
    </div>
  )
}

// ── live progress bars ────────────────────────────────────────────────────────
function hBytes(n: number) {
  if (!n) return '0 B'
  const u = ['B', 'KiB', 'MiB', 'GiB', 'TiB']; let i = 0; let v = n
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`
}
const hSpeed = (n: number) => `${hBytes(n)}/s`
function hEta(s: number) {
  if (!s || s < 0 || !isFinite(s)) return '—'
  const m = Math.floor(s / 60), sec = Math.floor(s % 60)
  return m > 59 ? `${Math.floor(m / 60)}h${m % 60}m` : m ? `${m}m${sec}s` : `${sec}s`
}

// ── friendly schedule builder (emits a 5-field cron) ──────────────────────────
// Daily mode supports picking specific weekdays (Mon–Sun) like RcloneBrowser.
const DOW_SHORT = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
const pad2 = (s: string | number) => String(s).padStart(2, '0')
type Sched = { mode: 'off' | 'daily' | 'hourly' | 'custom'; hour: string; min: string; days: number[]; custom: string }

function parseCron(v: string): Sched {
  const def: Sched = { mode: 'off', hour: '3', min: '0', days: [0, 1, 2, 3, 4, 5, 6], custom: '' }
  const t = (v ?? '').trim()
  if (!t) return def
  const f = t.split(/\s+/)
  const num = (s: string) => /^\d+$/.test(s)
  if (f.length === 5) {
    const [m, h, dom, mon, d] = f
    if (dom === '*' && mon === '*') {
      if (h === '*' && num(m)) return { ...def, mode: 'hourly', min: m }
      if (num(h) && num(m)) {
        if (d === '*') return { ...def, mode: 'daily', hour: h, min: m, days: [0, 1, 2, 3, 4, 5, 6] }
        const parts = d.split(',')
        if (parts.every((p) => num(p) && +p <= 6)) return { ...def, mode: 'daily', hour: h, min: m, days: parts.map(Number) }
      }
    }
  }
  return { ...def, mode: 'custom', custom: t }
}

function buildCron(s: Sched): string {
  const m = s.min || '0', h = s.hour || '0'
  switch (s.mode) {
    case 'hourly': return `${m} * * * *`
    case 'daily': {
      const days = s.days.length === 0 ? [0, 1, 2, 3, 4, 5, 6] : s.days
      const dow = days.length === 7 ? '*' : [...days].sort((a, b) => a - b).join(',')
      return `${m} ${h} * * ${dow}`
    }
    case 'custom': return s.custom.trim()
    default: return ''
  }
}

function schedSummary(s: Sched): string {
  const at = `${pad2(s.hour)}:${pad2(s.min)}`
  switch (s.mode) {
    case 'hourly': return `Every hour at :${pad2(s.min)}`
    case 'daily': {
      const d = s.days
      if (d.length === 0 || d.length === 7) return `Every day at ${at}`
      return `${[...d].sort((a, b) => a - b).map((i) => DOW_SHORT[i]).join(', ')} at ${at}`
    }
    case 'custom': return s.custom ? `Cron: ${s.custom}` : 'Not scheduled'
    default: return 'Not scheduled'
  }
}

function ScheduleField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const [s, setS] = useState<Sched>(() => parseCron(value))
  useEffect(() => { setS(parseCron(value)) }, [value])
  const up = (patch: Partial<Sched>) => { const ns = { ...s, ...patch }; setS(ns); onChange(buildCron(ns)) }
  const time = `${pad2(s.hour)}:${pad2(s.min)}`
  const onTime = (t: string) => { const [h, m] = t.split(':'); up({ hour: h || '0', min: m || '0' }) }
  const toggleDay = (i: number) => up({ days: s.days.includes(i) ? s.days.filter((x) => x !== i) : [...s.days, i] })
  const everyday = s.days.length === 7
  const MODES: [Sched['mode'], string][] = [['off', 'Off'], ['daily', 'Daily'], ['hourly', 'Hourly'], ['custom', 'Custom']]
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-1.5">
        {MODES.map(([m, lbl]) => (
          <Button key={m} size="sm" variant={s.mode === m ? 'default' : 'outline'} onClick={() => up({ mode: m })}>{lbl}</Button>
        ))}
      </div>
      {s.mode === 'daily' && (
        <div className="space-y-2 rounded-md border border-border p-2">
          <label className="flex items-center gap-2 text-xs text-foreground">
            <input type="checkbox" checked={everyday} onChange={(e) => up({ days: e.target.checked ? [0, 1, 2, 3, 4, 5, 6] : [] })} />
            Everyday
          </label>
          <div className="flex flex-wrap gap-1">
            {DOW_SHORT.map((d, i) => (
              <button key={i} onClick={() => toggleDay(i)}
                className={cn('px-2 py-1 rounded text-[11px] border', s.days.includes(i) ? 'bg-primary text-primary-foreground border-primary' : 'border-border text-muted-foreground hover:text-foreground')}>
                {d}
              </button>
            ))}
          </div>
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground">at</span>
            <Input type="time" className="h-8 w-28" value={time} onChange={(e) => onTime(e.target.value)} />
          </div>
        </div>
      )}
      {s.mode === 'hourly' && (
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          at minute
          <Input type="number" min={0} max={59} className="h-8 w-20" value={s.min} onChange={(e) => up({ min: e.target.value })} />
        </div>
      )}
      {s.mode === 'custom' && (
        <Input className="h-8 font-mono" value={s.custom} onChange={(e) => up({ custom: e.target.value })} placeholder="0 3 * * *  (min hour dom mon dow)" />
      )}
      {s.mode !== 'off' && <p className="text-[11px] text-primary">{schedSummary(s)}</p>}
    </div>
  )
}

function Stat({ label, value, danger }: { label: string; value: string; danger?: boolean }) {
  return (
    <div className="rounded-md border border-border bg-secondary/30 px-2.5 py-1.5">
      <p className="text-[10px] uppercase tracking-wider text-muted-foreground/70">{label}</p>
      <p className={cn('text-sm font-semibold tabular-nums truncate', danger ? 'text-destructive' : 'text-foreground')}>{value}</p>
    </div>
  )
}

function TransferProgress({ jobId, running }: { jobId: string | null; running?: boolean }) {
  const { data } = useTransferStats(jobId, !!running)
  const hasStats = !!data && !!(data.totalBytes || data.transferring?.length || data.totalTransfers || data.totalChecks)
  const hasTiming = !!(data?.started_at || data?.finished_at)
  // Always show the info box (even with no data → dashes), per request.
  const d = data ?? ({} as NonNullable<typeof data>)
  const pct = d.totalBytes ? Math.min(100, Math.round((d.bytes / d.totalBytes) * 100)) : 0
  const dash = (s: string) => (hasStats ? s : '—')
  return (
    <div className="space-y-3 rounded-lg border border-border bg-card p-3">
      {(hasTiming || running) && (
        <div className="flex flex-wrap gap-x-4 gap-y-0.5 text-[11px] text-muted-foreground">
          {d.started_at && <span>Started {new Date(d.started_at).toLocaleString()}</span>}
          {running
            ? d.eta > 0 && <span>Est. finish {new Date(Date.now() + d.eta * 1000).toLocaleString()}</span>
            : d.finished_at && <span>Finished {new Date(d.finished_at).toLocaleString()}</span>}
        </div>
      )}
      {/* Summary grid (RcloneBrowser-style boxed stats) */}
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-2">
        <Stat label="Transferred" value={dash(`${hBytes(d.bytes)} / ${hBytes(d.totalBytes)} (${pct}%)`)} />
        <Stat label="Speed" value={dash(hSpeed(d.speed))} />
        <Stat label="Transfers" value={dash(`${d.transfers ?? 0} / ${d.totalTransfers ?? 0}`)} />
        <Stat label="ETA" value={dash(hEta(d.eta))} />
        <Stat label="Elapsed" value={dash(hEta(d.elapsedTime))} />
        <Stat label="Checks" value={dash(`${d.checks ?? 0} / ${d.totalChecks ?? 0}`)} />
        <Stat label="Errors" value={dash(String(d.errors ?? 0))} danger={(d.errors ?? 0) > 0} />
      </div>

      {/* Overall bar */}
      <div>
        <div className="flex justify-end text-[10px] text-muted-foreground mb-0.5">{hasStats ? `${pct}%` : (running ? 'starting…' : 'no stats recorded')}</div>
        <div className="w-full bg-secondary rounded-full h-2 overflow-hidden">
          <div className="h-2 rounded-full bg-primary transition-all duration-500" style={{ width: `${pct}%` }} />
        </div>
      </div>

      {/* Per-file bars */}
      {(d.transferring ?? []).length > 0 && (
        <div className="space-y-2 pt-1">
          {(d.transferring ?? []).map((f) => {
            const fp = Math.min(100, f.percentage)
            return (
              <div key={f.name} className="space-y-1">
                <div className="flex items-center justify-between text-[11px] gap-3">
                  <span className="font-mono text-foreground truncate">{f.name}</span>
                  <span className="text-muted-foreground shrink-0 tabular-nums">{fp}% · {hSpeed(f.speed)} · {hEta(f.eta)}</span>
                </div>
                <div className="w-full bg-secondary rounded-full h-1.5 overflow-hidden">
                  <div className="h-1.5 rounded-full bg-primary/70 transition-all duration-500" style={{ width: `${fp}%` }} />
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

function NumField({ label, v, on, ph }: { label: string; v?: number; on: (n: number | undefined) => void; ph?: string }) {
  return (
    <div className="space-y-1">
      <Label className="text-[11px]">{label}</Label>
      <Input className="h-8" type="number" min={0} value={v ?? ''} placeholder={ph}
        onChange={(e) => on(e.target.value === '' ? undefined : Math.max(0, parseInt(e.target.value, 10) || 0))} />
    </div>
  )
}

function Chk({ label, v, on }: { label: string; v: boolean; on: (b: boolean) => void }) {
  return (
    <label className="flex items-center gap-2 text-xs text-muted-foreground">
      <input type="checkbox" checked={v} onChange={(e) => on(e.target.checked)} />{label}
    </label>
  )
}

function PatField({ label, v, on, ph }: { label: string; v?: string[]; on: (a: string[]) => void; ph?: string }) {
  return (
    <div className="space-y-1">
      <Label className="text-[11px]">{label}</Label>
      <Input className="h-8" value={(v ?? []).join(', ')} placeholder={ph}
        onChange={(e) => on(e.target.value.split(',').map((s) => s.trim()).filter(Boolean))} />
    </div>
  )
}
