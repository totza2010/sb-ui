/**
 * AppDetail — rich view for an installed app: status, logs, /opt folder, and
 * job history. Configuration (variables + role files) opens the RoleConfigModal.
 */
import { useEffect, useMemo, useState } from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import {
  X, RefreshCw, Play, Square, Trash2, ArrowDownToLine, Settings2,
  Folder, File as FileIcon, Loader2, Activity, ScrollText, FolderOpen, History, ChevronRight,
  HardDrive, Database, Package, Save, ArrowLeft,
} from 'lucide-react'
import { Dialog, DialogOverlay, DialogPortal } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { parse as yamlParse, stringify as yamlStringify } from 'yaml'
import {
  useAppLogs, useAppAppdata, useJobs, useRcloneStatus, useRcloneLogs, useFsList, useImageInfo,
  useFsFile, useSaveFsFile,
  type AppInfo, type InstanceInfo,
} from '@/lib/api'
import { YamlNode } from '@/components/YamlForm'
import { cn } from '@/lib/cn'

function safeStringify(obj: unknown, fallback: string): string {
  try { return yamlStringify(obj) } catch { return fallback }
}

function timeAgo(ts?: number | null): string {
  if (!ts) return 'never'
  const s = Math.max(0, Date.now() / 1000 - ts)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}

function InfoRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <>
      <span className="text-muted-foreground">{label}</span>
      <span className={cn('truncate', mono && 'font-mono')} title={value}>{value}</span>
    </>
  )
}

function ImageInfoCard({ name, image }: { name: string; image: string }) {
  const { data } = useImageInfo(name, image)
  return (
    <div className="border border-border rounded-md p-3 space-y-2">
      <div className="flex items-center gap-2">
        <Package className="h-4 w-4 text-muted-foreground" />
        <span className="text-sm font-medium">Image</span>
        {data?.outdated === true && <span className="text-[10px] px-1.5 py-0.5 rounded bg-orange-500/15 text-orange-400 border border-orange-500/30">update available</span>}
        {data?.outdated === false && <span className="text-[10px] px-1.5 py-0.5 rounded bg-green-500/15 text-green-500 border border-green-500/30">up to date</span>}
        <span className="text-[10px] text-muted-foreground ml-auto">checked {timeAgo(data?.checked_at)}</span>
      </div>
      <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
        <InfoRow label="Image" value={image} mono />
        <InfoRow label="Created" value={data?.created ? new Date(data.created).toLocaleString() : '—'} />
        <InfoRow label="Size" value={data?.size ? fmtSize(data.size) : '—'} />
        <InfoRow label="Platform" value={data?.os || data?.architecture ? `${data?.os ?? ''}/${data?.architecture ?? ''}` : '—'} />
        <InfoRow label="Digest" value={data?.digest ? data.digest.slice(0, 23) + '…' : '—'} mono />
      </div>
    </div>
  )
}

function StorageInfoCard() {
  const { data } = useRcloneStatus(true)
  return (
    <div className="border border-border rounded-md p-3 space-y-1.5">
      <div className="flex items-center gap-2">
        <HardDrive className="h-4 w-4 text-muted-foreground" />
        <span className="text-sm font-medium">Mounts manager</span>
      </div>
      <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
        <InfoRow label="Version" value={data?.version ?? '—'} mono />
        <InfoRow label="Active mounts" value={String(data?.mounts.length ?? 0)} />
        <InfoRow label="Remotes" value={data?.remotes.join(', ') || '—'} mono />
      </div>
      <p className="text-[11px] text-muted-foreground pt-0.5">Open the Mounts tab for per-mount control, logs and files.</p>
    </div>
  )
}

type Tab = 'overview' | 'mounts' | 'logs' | 'files' | 'history'
type InstanceAction = (name: string, action: 'start' | 'stop' | 'restart', kind: 'container' | 'service') => void

const STORAGE_TAGS = ['rclone', 'remote', 'unionfs', 'mounts', 'mergerfs']

const BASE_TABS: { key: Tab; label: string; icon: typeof Activity }[] = [
  { key: 'overview', label: 'Overview', icon: Activity },
  { key: 'logs', label: 'Logs', icon: ScrollText },
  { key: 'files', label: 'Files', icon: FolderOpen },
  { key: 'history', label: 'History', icon: History },
]

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(0)} KB`
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`
  return `${(n / 1024 ** 3).toFixed(1)} GB`
}

function StatusRow({ c, attached, onInstanceAction }: {
  c: InstanceInfo; attached?: boolean; onInstanceAction: InstanceAction
}) {
  const running = c.container_status === 'running' || c.container_status === 'active'
  const isService = c.kind === 'service'
  const canAct = c.installed && (c.kind === 'container' || isService)
  const target = isService ? (c.unit || c.name) : c.name
  const kind: 'container' | 'service' = isService ? 'service' : 'container'
  return (
    <div className="flex items-center justify-between gap-2 px-3 py-2 rounded-md border border-border">
      <div className="flex items-center gap-2 min-w-0">
        {attached && <span className="text-muted-foreground/50 text-xs">↳</span>}
        <span className={cn('h-2 w-2 rounded-full shrink-0',
          running ? 'bg-green-500' : c.installed ? 'bg-muted-foreground/50' : 'bg-border')} />
        <span className="text-sm font-mono truncate">{c.name}</span>
        {isService && <span className="text-[9px] uppercase text-purple-400/80">svc</span>}
        <span className="text-xs text-muted-foreground">{c.container_status ?? (c.installed ? 'installed' : '—')}</span>
      </div>
      {canAct && (
        <div className="flex items-center gap-1 shrink-0">
          <Button size="sm" variant="ghost" className="h-7 px-2"
            onClick={() => onInstanceAction(target, running ? 'stop' : 'start', kind)}>
            {running ? <Square className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
          </Button>
          <Button size="sm" variant="ghost" className="h-7 px-2"
            onClick={() => onInstanceAction(target, 'restart', kind)}>
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>
      )}
    </div>
  )
}

function LogsTab({ instances }: { instances: InstanceInfo[] }) {
  const containerNames = instances.filter(i => i.kind === 'container' && i.installed).map(i => i.name)
  const [sel, setSel] = useState(containerNames[0] ?? null)
  const { data, isFetching } = useAppLogs(sel)
  if (containerNames.length === 0)
    return <div className="text-sm text-muted-foreground py-12 text-center">No container logs available.</div>
  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="flex items-center gap-2 px-1 pb-2 shrink-0">
        {containerNames.length > 1 && (
          <select className="h-7 text-xs font-mono bg-background border border-border rounded-md px-2"
            value={sel ?? ''} onChange={e => setSel(e.target.value)}>
            {containerNames.map(n => <option key={n} value={n}>{n}</option>)}
          </select>
        )}
        {isFetching && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />}
        <span className="text-xs text-muted-foreground ml-auto">auto-refresh 5s · last 200 lines</span>
      </div>
      <pre className="flex-1 overflow-auto bg-background border border-border rounded-md p-3 text-[11px] font-mono whitespace-pre-wrap break-all leading-relaxed">
        {data?.logs?.trim() || 'No output.'}
      </pre>
    </div>
  )
}

function FileEditor({ path, onBack }: { path: string; onBack: () => void }) {
  const { data, isLoading } = useFsFile(path)
  const write = useSaveFsFile()
  const isYaml = /\.ya?ml$/i.test(path)
  const [draft, setDraft] = useState('')
  const [obj, setObj] = useState<unknown>(null)
  const [mode, setMode] = useState<'form' | 'raw'>('raw')
  const [saved, setSaved] = useState(false)
  const writable = data?.writable ?? false

  useEffect(() => {
    if (!data) return
    setDraft(data.content)
    if (isYaml) {
      try { setObj(yamlParse(data.content)); setMode('form') }
      catch { setObj(null); setMode('raw') }
    }
  }, [data, isYaml])

  // Current serialized content depends on the active editor
  const serialized = mode === 'form' && obj !== null ? safeStringify(obj, draft) : draft
  const dirty = data ? serialized !== data.content : false

  function switchMode(next: 'form' | 'raw') {
    if (next === 'form') {
      try { setObj(yamlParse(draft)); setMode('form') }
      catch { /* keep raw if unparseable */ }
    } else {
      if (obj !== null) setDraft(safeStringify(obj, draft))
      setMode('raw')
    }
  }

  async function save() {
    await write.mutateAsync({ path, content: serialized })
    if (mode === 'form') setDraft(serialized)
    setSaved(true); setTimeout(() => setSaved(false), 2000)
  }

  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="flex items-center gap-2 px-1 pb-2 text-xs shrink-0">
        <button onClick={onBack} className="text-muted-foreground hover:text-foreground flex items-center gap-1">
          <ArrowLeft className="h-3.5 w-3.5" />back
        </button>
        <span className="font-mono text-muted-foreground truncate">{path}</span>
        {isYaml && obj !== null && (
          <div className="flex gap-0.5 bg-muted/50 rounded p-0.5 ml-1">
            {(['form', 'raw'] as const).map(m => (
              <button key={m} onClick={() => switchMode(m)}
                className={cn('px-2 py-0.5 rounded text-[11px] capitalize',
                  mode === m ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground')}>
                {m}
              </button>
            ))}
          </div>
        )}
        {saved && <span className="text-green-500">Saved</span>}
        {writable ? (
          <Button size="sm" className="h-6 text-xs ml-auto gap-1" onClick={save} disabled={!dirty || write.isPending}>
            {write.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : <Save className="h-3 w-3" />}Save
          </Button>
        ) : (
          <span className="ml-auto text-muted-foreground">read-only</span>
        )}
      </div>
      {isLoading ? (
        <div className="flex items-center justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>
      ) : mode === 'form' && obj !== null ? (
        <div className="flex-1 overflow-auto border border-border rounded-md p-3">
          <YamlNode value={obj} onChange={writable ? setObj : () => {}} />
        </div>
      ) : (
        <textarea className="flex-1 font-mono text-xs p-2 bg-background border border-border rounded-md resize-none outline-none"
          value={draft} onChange={e => setDraft(e.target.value)} readOnly={!writable} spellCheck={false} />
      )}
    </div>
  )
}

function FilesTab({ tag }: { tag: string }) {
  const { data: ad } = useAppAppdata(tag)
  const paths = ad?.paths ?? []
  const [instIdx, setInstIdx] = useState(0)
  const root = paths[instIdx]?.path ?? ''
  const [path, setPath] = useState('')       // subpath relative to root
  const [editFile, setEditFile] = useState<string | null>(null)
  const full = path ? `${root}/${path}` : root
  const { data, isLoading } = useFsList(root ? full : null)
  const crumbs = path ? path.split('/').filter(Boolean) : []

  if (editFile) return <FileEditor path={editFile} onBack={() => setEditFile(null)} />

  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="flex items-center gap-2 px-1 pb-2 text-xs shrink-0 flex-wrap">
        {paths.length > 1 && (
          <select className="h-7 font-mono bg-background border border-border rounded-md px-2"
            value={instIdx} onChange={e => { setInstIdx(Number(e.target.value)); setPath('') }}>
            {paths.map((p, i) => <option key={p.instance} value={i}>{p.instance}</option>)}
          </select>
        )}
        <button className="font-mono text-muted-foreground hover:text-foreground" onClick={() => setPath('')}>
          {root || '…'}
        </button>
        {crumbs.map((c, i) => (
          <span key={i} className="flex items-center gap-1">
            <ChevronRight className="h-3 w-3 text-muted-foreground/50" />
            <button className="font-mono text-muted-foreground hover:text-foreground"
              onClick={() => setPath(crumbs.slice(0, i + 1).join('/'))}>{c}</button>
          </span>
        ))}
      </div>
      <div className="flex-1 overflow-auto border border-border rounded-md">
        {isLoading ? (
          <div className="flex items-center justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>
        ) : !data?.exists ? (
          <div className="text-sm text-muted-foreground py-12 text-center">Folder not found: {full}</div>
        ) : data.entries.length === 0 ? (
          <div className="text-sm text-muted-foreground py-12 text-center">Empty folder.</div>
        ) : data.entries.map(e => (
          <div key={e.name}
            className="flex items-center justify-between gap-2 px-3 py-1.5 text-sm border-b border-border/50 last:border-0 cursor-pointer hover:bg-muted/40"
            onClick={() => e.type === 'dir'
              ? setPath(path ? `${path}/${e.name}` : e.name)
              : setEditFile(`${full}/${e.name}`)}>
            <div className="flex items-center gap-2 min-w-0">
              {e.type === 'dir' ? <Folder className="h-4 w-4 text-blue-400 shrink-0" /> : <FileIcon className="h-4 w-4 text-muted-foreground shrink-0" />}
              <span className="font-mono truncate">{e.name}</span>
            </div>
            <span className="text-xs text-muted-foreground shrink-0">{e.type === 'file' ? fmtSize(e.size) : ''}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function HistoryTab({ tag, onOpenJob }: { tag: string; onOpenJob: (id: string) => void }) {
  const { data: jobs = [] } = useJobs()
  const mine = useMemo(
    () => jobs.filter(j => j.tag === tag || j.tag === `sandbox-${tag}` || j.tag === `mod-${tag}`),
    [jobs, tag])
  if (mine.length === 0)
    return <div className="text-sm text-muted-foreground py-12 text-center">No job history for this app.</div>
  return (
    <div className="space-y-1">
      {mine.map(j => (
        <button key={j.id} onClick={() => onOpenJob(j.id)}
          className="flex items-center justify-between gap-2 w-full text-left px-3 py-2 rounded-md border border-border hover:bg-muted/40">
          <div className="flex items-center gap-2 min-w-0">
            <span className={cn('h-2 w-2 rounded-full shrink-0',
              j.status === 'completed' ? 'bg-green-500' : j.status === 'failed' ? 'bg-red-500'
              : j.status === 'running' ? 'bg-blue-500 animate-pulse' : 'bg-muted-foreground/50')} />
            <span className="text-sm capitalize">{j.action}</span>
            <span className="text-xs text-muted-foreground">{j.status}</span>
          </div>
          <span className="text-xs text-muted-foreground shrink-0">{new Date(j.created_at).toLocaleString()}</span>
        </button>
      ))}
    </div>
  )
}

function UnitLogs({ unit }: { unit: string }) {
  const { data, isFetching } = useRcloneLogs(unit)
  return (
    <pre className="mt-1 max-h-56 overflow-auto bg-background border border-border rounded-md p-2 text-[10px] font-mono whitespace-pre-wrap break-all leading-relaxed">
      {isFetching && !data ? 'Loading…' : (data?.logs?.trim() || 'No journal output.')}
    </pre>
  )
}

function FsBrowser({ root }: { root: string }) {
  const [path, setPath] = useState(root)
  const { data, isLoading } = useFsList(path)
  const rel = path.startsWith(root) ? path.slice(root.length).replace(/^\//, '') : ''
  const segs = rel ? rel.split('/') : []
  return (
    <div className="mt-1 border border-border rounded-md">
      <div className="flex items-center gap-1 px-2 py-1 text-xs flex-wrap border-b border-border/50">
        <button className="font-mono text-muted-foreground hover:text-foreground" onClick={() => setPath(root)}>{root}</button>
        {segs.map((seg, i) => (
          <span key={i} className="flex items-center gap-1">
            <ChevronRight className="h-3 w-3 text-muted-foreground/50" />
            <button className="font-mono text-muted-foreground hover:text-foreground"
              onClick={() => setPath(`${root}/${segs.slice(0, i + 1).join('/')}`)}>{seg}</button>
          </span>
        ))}
      </div>
      <div className="max-h-56 overflow-auto">
        {isLoading ? <div className="p-3 text-xs text-muted-foreground">Loading…</div>
          : !data?.exists ? <div className="p-3 text-xs text-muted-foreground">Not accessible.</div>
          : data.entries.length === 0 ? <div className="p-3 text-xs text-muted-foreground">Empty.</div>
          : data.entries.map(e => (
            <div key={e.name}
              className={cn('flex items-center justify-between gap-2 px-2 py-1 text-xs border-b border-border/30 last:border-0',
                e.type === 'dir' && 'cursor-pointer hover:bg-muted/40')}
              onClick={() => e.type === 'dir' && setPath(`${path}/${e.name}`)}>
              <div className="flex items-center gap-1.5 min-w-0">
                {e.type === 'dir' ? <Folder className="h-3.5 w-3.5 text-blue-400 shrink-0" /> : <FileIcon className="h-3.5 w-3.5 text-muted-foreground shrink-0" />}
                <span className="font-mono truncate">{e.name}</span>
              </div>
              <span className="text-muted-foreground shrink-0">{e.type === 'file' ? fmtSize(e.size) : ''}</span>
            </div>
          ))}
      </div>
    </div>
  )
}

function MountsTab({ enabled, onInstanceAction }: {
  enabled: boolean; onInstanceAction: InstanceAction
}) {
  const { data, isLoading } = useRcloneStatus(enabled)
  const [openLogs, setOpenLogs] = useState<string | null>(null)
  const [openFiles, setOpenFiles] = useState<string | null>(null)
  if (isLoading) return <div className="flex justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>
  if (!data) return null
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1 text-sm">
        <span className="text-muted-foreground">rclone</span>
        <span className="font-mono">{data.version ?? 'not found'}</span>
        <span className="text-muted-foreground">remotes</span>
        <div className="flex flex-wrap gap-1">
          {data.remotes.length ? data.remotes.map(r => (
            <span key={r} className="text-xs font-mono px-1.5 py-0.5 rounded bg-muted/50">{r}</span>
          )) : <span className="text-muted-foreground text-xs">none configured</span>}
        </div>
      </div>

      {/* Mount services with per-unit logs */}
      <div>
        <p className="text-xs uppercase tracking-wider text-muted-foreground/70 mb-1.5">Mount services</p>
        {data.units.length === 0 ? (
          <p className="text-sm text-muted-foreground">No mount services found.</p>
        ) : (
          <div className="border border-border rounded-md divide-y divide-border">
            {data.units.map(u => {
              const running = u.sub === 'running' || u.sub === 'mounted'
              const active = u.active === 'active'
              const showLogs = openLogs === u.unit
              return (
                <div key={u.unit} className="px-3 py-2">
                  <div className="flex items-center justify-between gap-2">
                    <div className="flex items-center gap-2 min-w-0">
                      <span className={cn('h-2 w-2 rounded-full shrink-0', running ? 'bg-green-500' : active ? 'bg-amber-400' : 'bg-muted-foreground/40')} />
                      <span className="text-sm font-mono truncate">{u.unit}</span>
                      <span className="text-xs text-muted-foreground">{u.active}/{u.sub}</span>
                    </div>
                    <div className="flex items-center gap-0.5 shrink-0">
                      <Button size="sm" variant="ghost" className="h-7 px-2 text-xs"
                        onClick={() => setOpenLogs(showLogs ? null : u.unit)}>
                        <ScrollText className="h-3.5 w-3.5" />
                      </Button>
                      <Button size="sm" variant="ghost" className="h-7 px-2"
                        onClick={() => onInstanceAction(u.unit, running ? 'stop' : 'start', 'service')}>
                        {running ? <Square className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
                      </Button>
                      <Button size="sm" variant="ghost" className="h-7 px-2"
                        onClick={() => onInstanceAction(u.unit, 'restart', 'service')}>
                        <RefreshCw className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </div>
                  {showLogs && <UnitLogs unit={u.unit} />}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* Refresh timers */}
      {data.timers.length > 0 && (
        <div>
          <p className="text-xs uppercase tracking-wider text-muted-foreground/70 mb-1.5">Refresh schedule</p>
          <div className="border border-border rounded-md divide-y divide-border">
            {data.timers.map(t => (
              <div key={t.unit} className="flex items-center justify-between gap-2 px-3 py-2">
                <div className="flex items-center gap-2 min-w-0">
                  <span className={cn('h-2 w-2 rounded-full shrink-0', t.sub === 'waiting' || t.active === 'active' ? 'bg-blue-500' : 'bg-muted-foreground/40')} />
                  <span className="text-sm font-mono truncate">{t.unit.replace('.timer', '')}</span>
                  <span className="text-xs text-muted-foreground">
                    {t.next ? `next ${new Date(t.next).toLocaleString()}` : t.active}
                  </span>
                </div>
                <Button size="sm" variant="ghost" className="h-7 px-2 text-xs gap-1"
                  title="Run refresh now"
                  onClick={() => onInstanceAction(t.activates, 'start', 'service')}>
                  <RefreshCw className="h-3.5 w-3.5" />Refresh now
                </Button>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Active mounts with size + file browse */}
      <div>
        <p className="text-xs uppercase tracking-wider text-muted-foreground/70 mb-1.5">Active mounts</p>
        {data.mounts.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nothing mounted right now.</p>
        ) : (
          <div className="space-y-1">
            {data.mounts.map(m => {
              const isUnion = m.fstype.includes('mergerfs')
              const showFiles = openFiles === m.target
              return (
                <div key={m.target} className="rounded-md border border-border">
                  <div className="flex items-center gap-2 px-3 py-2 text-sm">
                    {isUnion ? <Database className="h-4 w-4 text-purple-400 shrink-0" /> : <HardDrive className="h-4 w-4 text-blue-400 shrink-0" />}
                    <span className="font-mono truncate">{m.target}</span>
                    <ChevronRight className="h-3 w-3 text-muted-foreground/50 shrink-0" />
                    <span className="font-mono text-muted-foreground truncate">{m.source}</span>
                    {m.size && <span className="text-[10px] text-muted-foreground ml-auto shrink-0">{m.used ?? '?'}/{m.size}{m.use_pct ? ` · ${m.use_pct}` : ''}</span>}
                    <Button size="sm" variant="ghost" className={cn('h-7 px-2 text-xs', !m.size && 'ml-auto')}
                      onClick={() => setOpenFiles(showFiles ? null : m.target)}>
                      <FolderOpen className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                  {showFiles && <div className="px-3 pb-2"><FsBrowser root={m.target} /></div>}
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}

export function AppDetail({ app, onClose, onAction, onInstanceAction, onConfigure, onOpenJob }: {
  app: AppInfo | null
  onClose: () => void
  onAction: (tag: string, action: string) => void
  onInstanceAction: InstanceAction
  onConfigure: (app: AppInfo) => void
  onOpenJob: (id: string) => void
}) {
  const [tab, setTab] = useState<Tab>('overview')
  const open = !!app
  const instances = app?.instances ?? []
  const companions = app?.companions ?? []
  const bare = app ? app.tag.replace(/^(sandbox|mod)-/, '') : ''
  const isStorage = STORAGE_TAGS.includes(bare)
  const tabs = isStorage
    ? [BASE_TABS[0], { key: 'mounts' as Tab, label: 'Mounts', icon: HardDrive }, ...BASE_TABS.slice(1)]
    : BASE_TABS
  const activeTab: Tab = tabs.some(t => t.key === tab) ? tab : 'overview'

  return (
    <Dialog open={open} onOpenChange={o => !o && onClose()}>
      <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Content className="fixed right-0 top-0 h-full w-full max-w-2xl z-50 bg-card border-l border-border shadow-xl outline-none flex flex-col">
          {app && (
            <>
              <div className="flex items-center gap-3 px-5 py-4 border-b border-border shrink-0">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <h2 className="text-base font-semibold capitalize truncate">{app.name}</h2>
                    <span className="text-xs px-1.5 py-0.5 rounded bg-muted/60 text-muted-foreground">{app.repo}</span>
                  </div>
                  <p className="text-xs text-muted-foreground font-mono mt-0.5 truncate">{app.tag}{app.image ? ` · ${app.image}` : ''}</p>
                </div>
                <Button size="sm" variant="outline" className="gap-1.5" onClick={() => onConfigure(app)}>
                  <Settings2 className="h-3.5 w-3.5" />Configure
                </Button>
                <DialogPrimitive.Close asChild>
                  <Button size="sm" variant="ghost" className="h-8 w-8 p-0"><X className="h-4 w-4" /></Button>
                </DialogPrimitive.Close>
              </div>

              <div className="flex gap-1 px-4 pt-3 border-b border-border shrink-0">
                {tabs.map(({ key, label, icon: Icon }) => (
                  <button key={key} onClick={() => setTab(key)}
                    className={cn('flex items-center gap-1.5 px-3 py-2 text-sm rounded-t-md border-b-2 -mb-px transition-colors',
                      activeTab === key ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground')}>
                    <Icon className="h-3.5 w-3.5" />{label}
                  </button>
                ))}
              </div>

              <div className="flex-1 min-h-0 overflow-auto p-4">
                {activeTab === 'overview' && (
                  <div className="space-y-4">
                    <div className="flex flex-wrap gap-2">
                      <Button size="sm" variant="outline" className="gap-1.5" onClick={() => onAction(app.tag, 'reinstall')}>
                        <RefreshCw className="h-3.5 w-3.5" />Reinstall
                      </Button>
                      {app.kind === 'container' && (
                        <Button size="sm" variant="outline" className="gap-1.5 text-primary border-primary/30"
                          onClick={() => onAction(app.tag, 'pull')}>
                          <ArrowDownToLine className="h-3.5 w-3.5" />Pull latest
                        </Button>
                      )}
                      {app.kind === 'container' && (
                        <Button size="sm" variant="destructive" className="gap-1.5" onClick={() => onAction(app.tag, 'remove')}>
                          <Trash2 className="h-3.5 w-3.5" />Remove
                        </Button>
                      )}
                    </div>
                    {app.image
                      ? <ImageInfoCard name={bare} image={app.image} />
                      : isStorage ? <StorageInfoCard /> : null}
                    <div className="space-y-1.5">
                      <p className="text-xs uppercase tracking-wider text-muted-foreground/70">
                        {instances.length > 1 ? `${instances.length} instances` : 'Status'}
                      </p>
                      {instances.map(i => <StatusRow key={i.name} c={i} onInstanceAction={onInstanceAction} />)}
                      {companions.length > 0 && <>
                        <p className="text-xs uppercase tracking-wider text-muted-foreground/70 pt-1">Attached</p>
                        {companions.map(c => <StatusRow key={c.name} c={c} attached onInstanceAction={onInstanceAction} />)}
                      </>}
                    </div>
                  </div>
                )}
                {activeTab === 'mounts' && <MountsTab enabled={isStorage && tab === 'mounts'} onInstanceAction={onInstanceAction} />}
                {activeTab === 'logs' && <LogsTab instances={instances} />}
                {activeTab === 'files' && <FilesTab tag={app.tag} />}
                {activeTab === 'history' && <HistoryTab tag={bare} onOpenJob={onOpenJob} />}
              </div>
            </>
          )}
        </DialogPrimitive.Content>
      </DialogPortal>
    </Dialog>
  )
}
