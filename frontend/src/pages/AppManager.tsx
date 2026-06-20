import { useEffect, useRef, useState } from 'react'
import {
  useApps, useInstallApp, usePullImage, useRemoveApp, useSaltboxVersion,
  useUpdateStatus, useCheckUpdates, useSaltboxUpdate, useApplyPatches, useBundles,
  useContainerAction, useServiceAction, useCategories, useUpdateMeta,
  useCustomSets, useSaveCustomSet, useDeleteCustomSet, useInstallSet,
  type AppInfo, type Bundle, type InstanceInfo, type CustomSet,
} from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { LogStream } from '@/components/LogStream'
import { useJobSocket } from '@/hooks/useJobSocket'
import {
  Download, RefreshCw, Trash2, Search, ArrowDownToLine,
  GitCommit, AlertTriangle, CheckCircle2, Loader2, ArrowUpCircle, Settings2,
  ChevronDown, ChevronRight, Layers, Play, Square, Plus, Pencil, X, Package,
} from 'lucide-react'
import { RoleConfigModal } from '@/components/RoleConfigModal'
import { AppDetail } from '@/components/AppDetail'
import { ListRow } from '@/components/ListRow'
import { useQueryClient } from '@tanstack/react-query'
import { cn } from '@/lib/cn'
import { InstallTypes } from '@/pages/InstallTypes'
import { CustomRoles } from '@/components/CustomRoles'

type Filter = 'all' | 'saltbox' | 'sandbox' | 'outdated'
type Mode = 'installed' | 'add' | 'install-types' | 'roles'

function statusBadge(app: AppInfo) {
  if (!app.installed) return null
  if (app.kind === 'service') {
    const isActive = app.container_status === 'active'
    return (
      <Badge className={cn('text-xs border', isActive
        ? 'bg-purple-500/15 text-purple-400 border-purple-500/30'
        : 'bg-muted/50 text-muted-foreground border-border'
      )}>
        {isActive ? 'service' : 'inactive'}
      </Badge>
    )
  }
  return (
    <Badge variant={app.container_status === 'running' ? 'success' : 'warning'} className="text-xs">
      {app.container_status ?? 'installed'}
    </Badge>
  )
}

function imageLabel(image: string): { name: string; tag: string } {
  const lastSlash = image.lastIndexOf('/')
  const nameTag = lastSlash >= 0 ? image.slice(lastSlash + 1) : image
  const colonIdx = nameTag.lastIndexOf(':')
  if (colonIdx < 0) return { name: nameTag, tag: 'latest' }
  return { name: nameTag.slice(0, colonIdx), tag: nameTag.slice(colonIdx + 1) }
}

function UpdateDot({ outdated }: { outdated: boolean | null | undefined }) {
  if (outdated === true)
    return <span className="h-2 w-2 rounded-full bg-orange-400 shrink-0" title="Update available" />
  if (outdated === false)
    return <span className="h-2 w-2 rounded-full bg-green-500 shrink-0" title="Up to date" />
  return null
}

type InstanceAction = (name: string, action: 'start' | 'stop' | 'restart', kind: 'container' | 'service') => void

function ContainerRow({ c, attached, onInstanceAction }: {
  c: InstanceInfo
  attached?: boolean
  onInstanceAction: InstanceAction
}) {
  const running = c.container_status === 'running' || c.container_status === 'active'
  const isService = c.kind === 'service'
  const canAct = c.installed && (c.kind === 'container' || isService)
  const target = isService ? (c.unit || c.name) : c.name
  const kind: 'container' | 'service' = isService ? 'service' : 'container'
  return (
    <div className="flex items-center justify-between gap-2">
      <div className="flex items-center gap-1.5 min-w-0">
        {attached && <span className="text-muted-foreground/50 text-xs shrink-0">↳</span>}
        <span className={cn('h-1.5 w-1.5 rounded-full shrink-0',
          running ? 'bg-green-500' : c.installed ? 'bg-muted-foreground/50' : 'bg-border')} />
        <span className={cn('text-xs font-mono truncate', attached && 'text-muted-foreground')}>{c.name}</span>
        {isService && <span className="text-[9px] uppercase text-purple-400/70 shrink-0">svc</span>}
      </div>
      {canAct ? (
        <div className="flex items-center gap-0.5 shrink-0">
          <button title={running ? 'Stop' : 'Start'}
            className="h-5 w-5 grid place-items-center text-muted-foreground hover:text-foreground"
            onClick={() => onInstanceAction(target, running ? 'stop' : 'start', kind)}>
            {running ? <Square className="h-3 w-3" /> : <Play className="h-3 w-3" />}
          </button>
          <button title="Restart"
            className="h-5 w-5 grid place-items-center text-muted-foreground hover:text-foreground"
            onClick={() => onInstanceAction(target, 'restart', kind)}>
            <RefreshCw className="h-3 w-3" />
          </button>
        </div>
      ) : !c.installed ? (
        <span className="text-[10px] text-muted-foreground shrink-0">not installed</span>
      ) : null}
    </div>
  )
}

function InstancePanel({ app, multiInstance, onInstanceAction }: {
  app: AppInfo
  multiInstance: boolean
  onInstanceAction: InstanceAction
}) {
  const instances = app.instances ?? []
  const companions = app.companions ?? []
  return (
    <div className="border-t border-border pt-2 mt-1 space-y-1">
      {multiInstance && (
        <p className="text-[10px] uppercase tracking-wider text-muted-foreground/70">
          {instances.length} instances
        </p>
      )}
      {instances.map(inst => (
        <ContainerRow key={inst.name} c={inst} onInstanceAction={onInstanceAction} />
      ))}
      {companions.length > 0 && (
        <>
          <p className="text-[10px] uppercase tracking-wider text-muted-foreground/70 pt-0.5">attached</p>
          {companions.map(c => (
            <ContainerRow key={c.name} c={c} attached onInstanceAction={onInstanceAction} />
          ))}
        </>
      )}
    </div>
  )
}

function AppCard({
  app, onAction, updateStatus, onConfigure, onInstanceAction, onOpenDetail,
}: {
  app: AppInfo
  onAction: (tag: string, action: string) => void
  updateStatus: Record<string, boolean | null>
  onConfigure: (app: AppInfo) => void
  onInstanceAction: InstanceAction
  onOpenDetail?: (app: AppInfo) => void
}) {
  const canRemove = app.installed && app.kind === 'container'
  const canPull   = app.installed && app.kind === 'container'
  const outdated  = app.image ? updateStatus[app.image] : undefined
  const insts = app.instances ?? []
  const comps = app.companions ?? []
  const multiInstance = insts.length > 1
  // Show the per-container panel for every installed app (consistent lifecycle
  // controls), and whenever there are extra instances or attached containers.
  const showPanel = app.installed || multiInstance || comps.length > 0

  return (
    <div className={cn(
      'border rounded-lg bg-card p-4 flex flex-col gap-3 transition-colors',
      outdated === true ? 'border-orange-500/30 hover:border-orange-500/50' : 'border-border hover:border-primary/30'
    )}>
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex items-center gap-1.5">
            {onOpenDetail ? (
              <button onClick={() => onOpenDetail(app)}
                className="font-medium text-sm text-foreground capitalize truncate hover:text-primary hover:underline text-left">
                {app.name}
              </button>
            ) : (
              <p className="font-medium text-sm text-foreground capitalize truncate">{app.name}</p>
            )}
            <UpdateDot outdated={outdated} />
          </div>
          <p className="text-xs text-muted-foreground font-mono mt-0.5 truncate">{app.tag}</p>
          {app.image && (() => {
            const { name, tag } = imageLabel(app.image)
            return (
              <p className="text-xs text-muted-foreground/70 mt-1 truncate" title={app.image}>
                {name}
                <span className={cn('ml-0.5 font-mono',
                  tag === 'latest' ? 'text-muted-foreground/50' : 'text-primary/70'
                )}>:{tag}</span>
                {outdated === true && (
                  <span className="ml-1.5 text-orange-400 font-medium">↑ update</span>
                )}
              </p>
            )
          })()}
        </div>
        <div className="flex items-center gap-1.5 shrink-0 flex-wrap justify-end">
          <Badge variant={app.repo === 'sandbox' ? 'secondary' : 'outline'} className="text-xs">
            {app.repo}
          </Badge>
          {statusBadge(app)}
          <Button size="sm" variant="ghost"
            className="h-6 w-6 p-0 text-muted-foreground hover:text-foreground"
            title="Configure inventory variables"
            onClick={() => onConfigure(app)}>
            <Settings2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      <div className="flex gap-1.5 flex-wrap">
        {!app.installed ? (
          <Button size="sm" className="flex-1 gap-1.5" onClick={() => onAction(app.tag, 'install')}>
            <Download className="h-3.5 w-3.5" />Install
          </Button>
        ) : (
          <>
            <Button size="sm" variant="outline" className="flex-1 gap-1.5" onClick={() => onAction(app.tag, 'reinstall')}>
              <RefreshCw className="h-3.5 w-3.5" />Reinstall
            </Button>
            {canPull && (
              <Button
                size="sm" variant="outline"
                className={cn('gap-1.5', outdated === true
                  ? 'border-orange-500/40 text-orange-400 hover:bg-orange-500/10'
                  : 'text-primary border-primary/30 hover:bg-primary/10'
                )}
                title={`Pull latest${app.image ? `: ${app.image}` : ''}`}
                onClick={() => onAction(app.tag, 'pull')}
              >
                <ArrowDownToLine className="h-3.5 w-3.5" />
              </Button>
            )}
            {canRemove && (
              <Button size="sm" variant="destructive" onClick={() => onAction(app.tag, 'remove')}>
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            )}
          </>
        )}
      </div>

      {showPanel && <InstancePanel app={app} multiInstance={multiInstance} onInstanceAction={onInstanceAction} />}
    </div>
  )
}

// AddRow — one available (not-installed) app, rendered via the shared ListRow.
function AddRow({ app, onAction, onConfigure }: {
  app: AppInfo
  onAction: (tag: string, action: string) => void
  onConfigure: (app: AppInfo) => void
}) {
  return (
    <ListRow
      icon={<Package />}
      title={<>
        <span className="text-sm font-medium text-foreground capitalize truncate">{app.name}</span>
        <Badge variant={app.repo === 'sandbox' ? 'secondary' : 'outline'} className="text-xs shrink-0">{app.repo}</Badge>
      </>}
      subtitle={app.tag}
      actions={<>
        <Button size="sm" variant="outline" title="Configure inventory variables" onClick={() => onConfigure(app)}>
          <Settings2 className="h-3.5 w-3.5 mr-1.5" />Configure
        </Button>
        <Button size="sm" className="gap-1.5" onClick={() => onAction(app.tag, 'install')}>
          <Download className="h-3.5 w-3.5" />Install
        </Button>
      </>}
    />
  )
}

function InstalledRow({ app, onOpen, onConfigure, onInstanceAction, outdated }: {
  app: AppInfo
  onOpen: (a: AppInfo) => void
  onConfigure: (a: AppInfo) => void
  onInstanceAction: InstanceAction
  outdated: boolean | null | undefined
}) {
  const insts = app.instances ?? []
  const comps = app.companions ?? []
  const primary = insts[0]
  const running = primary?.container_status === 'running' || primary?.container_status === 'active'
  const isService = primary?.kind === 'service'
  const statusText = isService ? (primary?.container_status ?? 'service')
    : running ? 'running' : app.on_demand ? 'on-demand' : app.installed ? 'stopped' : '—'
  const target = isService ? (primary?.unit || primary?.name || '') : (primary?.name || '')
  const canAct = primary && primary.installed && (primary.kind === 'container' || isService)

  return (
    <div className="flex items-center gap-3 px-4 py-2.5 hover:bg-muted/30">
      <button onClick={() => onOpen(app)} className="flex items-center gap-3 flex-1 min-w-0 text-left">
        <span className={cn('h-2 w-2 rounded-full shrink-0',
          running ? 'bg-green-500' : app.installed ? 'bg-muted-foreground/40' : 'bg-border')} />
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="font-medium text-sm capitalize truncate">{app.name}</span>
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-muted/60 text-muted-foreground shrink-0">{app.repo}</span>
            {outdated === true && <span className="text-[10px] text-orange-400 shrink-0">↑ update</span>}
          </div>
          <span className="text-xs font-mono text-muted-foreground truncate block">
            {app.tag}{app.image ? ` · ${imageLabel(app.image).name}:${imageLabel(app.image).tag}` : ''}
          </span>
        </div>
      </button>

      <div className="hidden sm:flex items-center gap-3 text-xs text-muted-foreground shrink-0">
        {insts.length > 1 && <span>{insts.length} inst</span>}
        {comps.length > 0 && <span>+{comps.length} attached</span>}
        <span className={cn('capitalize', running && 'text-green-500', isService && 'text-purple-400')}>{statusText}</span>
      </div>

      <div className="flex items-center gap-0.5 shrink-0">
        {canAct && (
          <>
            <button title={running ? 'Stop' : 'Start'} className="h-7 w-7 grid place-items-center text-muted-foreground hover:text-foreground"
              onClick={() => onInstanceAction(target, running ? 'stop' : 'start', isService ? 'service' : 'container')}>
              {running ? <Square className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
            </button>
            <button title="Restart" className="h-7 w-7 grid place-items-center text-muted-foreground hover:text-foreground"
              onClick={() => onInstanceAction(target, 'restart', isService ? 'service' : 'container')}>
              <RefreshCw className="h-3.5 w-3.5" />
            </button>
          </>
        )}
        <button title="Configure" className="h-7 w-7 grid place-items-center text-muted-foreground hover:text-foreground"
          onClick={() => onConfigure(app)}>
          <Settings2 className="h-3.5 w-3.5" />
        </button>
        <button title="Manage" className="h-7 w-7 grid place-items-center text-muted-foreground hover:text-foreground"
          onClick={() => onOpen(app)}>
          <ChevronRight className="h-4 w-4" />
        </button>
      </div>
    </div>
  )
}

function JobWatcher({ jobId, onComplete }: { jobId: string | null; onComplete: () => void }) {
  const { status } = useJobSocket(jobId)
  const firedRef = useRef(false)
  useEffect(() => {
    if (!firedRef.current && (status === 'completed' || status === 'failed')) {
      firedRef.current = true
      onComplete()
    }
  }, [status, onComplete])
  return null
}

const KIND_BADGE: Record<Bundle['kind'], { label: string; cls: string }> = {
  profile: { label: 'profile', cls: 'bg-blue-500/15 text-blue-400 border-blue-500/30' },
  bundle:  { label: 'bundle',  cls: 'bg-teal-500/15 text-teal-400 border-teal-500/30' },
  dynamic: { label: 'dynamic', cls: 'bg-amber-500/15 text-amber-400 border-amber-500/30' },
}

function BundleCard({ bundle, onInstall, busy }: {
  bundle: Bundle
  onInstall: (tag: string) => void
  busy: boolean
}) {
  const badge = KIND_BADGE[bundle.kind]
  return (
    <div className="border border-border rounded-lg p-3 flex flex-col gap-2 bg-card">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 min-w-0">
          <span className="font-medium text-sm truncate">{bundle.label}</span>
          <Badge className={cn('text-[10px] border shrink-0', badge.cls)}>{badge.label}</Badge>
        </div>
        <code className="text-[10px] text-muted-foreground font-mono shrink-0">sb install {bundle.tag}</code>
      </div>
      <p className="text-xs text-muted-foreground leading-snug">{bundle.description}</p>
      <div className="flex flex-wrap gap-1">
        {bundle.roles.map(r => (
          <span key={r} className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-muted/50 text-muted-foreground">{r}</span>
        ))}
        {bundle.pulls.map(p => (
          <span key={p.role}
            title={`pulled in dynamically via the ${p.via} role${p.conditional ? ' (conditional)' : ''}`}
            className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-500 border border-amber-500/30">
            +{p.role}
          </span>
        ))}
      </div>
      <Button size="sm" variant="outline" className="h-7 text-xs mt-auto"
        onClick={() => onInstall(bundle.tag)} disabled={busy}>
        <ArrowDownToLine className="h-3.5 w-3.5 mr-1.5" />Install set
      </Button>
    </div>
  )
}

function BundlesSection({ onInstall, busy }: { onInstall: (tag: string) => void; busy: boolean }) {
  const { data: bundles } = useBundles()
  const [open, setOpen] = useState(false)
  if (!bundles?.length) return null
  return (
    <div className="border border-border rounded-lg">
      <button
        className="flex items-center gap-2 w-full px-4 py-2.5 text-left"
        onClick={() => setOpen(o => !o)}
      >
        {open ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
        <Layers className="h-4 w-4 text-muted-foreground" />
        <span className="text-sm font-medium">Install / update sets</span>
        <span className="text-xs text-muted-foreground">— run sb install for a group of roles (also updates/reinstalls them)</span>
        <span className="ml-auto text-xs text-muted-foreground">{bundles.length}</span>
      </button>
      {open && (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3 p-4 pt-0">
          {bundles.map(b => (
            <BundleCard key={b.tag} bundle={b} onInstall={onInstall} busy={busy} />
          ))}
        </div>
      )}
    </div>
  )
}

function CustomSetsSection({ sets, busy, onRun, onEdit, onDelete, onCreate }: {
  sets: CustomSet[]
  busy: boolean
  onRun: (s: CustomSet) => void
  onEdit: (s: CustomSet) => void
  onDelete: (s: CustomSet) => void
  onCreate: () => void
}) {
  return (
    <div className="border border-border rounded-lg p-4 space-y-3">
      <div className="flex items-center gap-2">
        <Layers className="h-4 w-4 text-muted-foreground" />
        <span className="text-sm font-medium">My sets</span>
        <span className="text-xs text-muted-foreground">— install several apps at once (sb install a,b,…)</span>
        <Button size="sm" variant="outline" className="ml-auto h-7 text-xs gap-1" onClick={onCreate}>
          <Plus className="h-3.5 w-3.5" />Create set
        </Button>
      </div>
      {sets.length === 0 ? (
        <p className="text-xs text-muted-foreground">No custom sets yet. Create one to install a group of apps together.</p>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
          {sets.map(s => (
            <div key={s.id} className="border border-border rounded-md p-3 flex flex-col gap-2">
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium text-sm truncate">{s.name}</span>
                <div className="flex items-center gap-0.5 shrink-0">
                  <button title="Edit" className="h-6 w-6 grid place-items-center text-muted-foreground hover:text-foreground" onClick={() => onEdit(s)}>
                    <Pencil className="h-3.5 w-3.5" />
                  </button>
                  <button title="Delete" className="h-6 w-6 grid place-items-center text-muted-foreground hover:text-red-500" onClick={() => onDelete(s)}>
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
              </div>
              <code className="text-[10px] text-muted-foreground font-mono break-all">sb install {s.tags.join(',')}</code>
              <div className="flex flex-wrap gap-1">
                {s.tags.map(t => (
                  <span key={t} className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-muted/50 text-muted-foreground">{t}</span>
                ))}
              </div>
              <Button size="sm" variant="outline" className="h-7 text-xs mt-auto gap-1" disabled={busy} onClick={() => onRun(s)}>
                <ArrowDownToLine className="h-3.5 w-3.5" />Install set
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function CustomSetModal({ apps, initial, onClose, onSaved }: {
  apps: AppInfo[]
  initial: CustomSet | null
  onClose: () => void
  onSaved: () => void
}) {
  const save = useSaveCustomSet()
  const { data: bundles = [] } = useBundles()
  const [name, setName] = useState(initial?.name ?? '')
  const [tags, setTags] = useState<string[]>(initial?.tags ?? [])
  const [q, setQ] = useState('')
  const [freeform, setFreeform] = useState('')

  const ql = q.toLowerCase()
  const inc = (t: string) => !tags.includes(t) && t.toLowerCase().includes(ql)
  const groups = [
    { label: 'Sets', items: bundles.map(b => b.tag).filter(inc) },
    { label: 'Saltbox apps', items: apps.filter(a => a.repo === 'saltbox').map(a => a.tag).filter(inc).sort() },
    { label: 'Sandbox apps', items: apps.filter(a => a.repo === 'sandbox').map(a => a.tag).filter(inc).sort() },
  ].filter(g => g.items.length > 0)

  const toggle = (t: string) => setTags(p => p.includes(t) ? p.filter(x => x !== t) : [...p, t])
  function addFree() {
    freeform.split(',').map(s => s.trim()).filter(Boolean).forEach(t => {
      setTags(p => p.includes(t) ? p : [...p, t])
    })
    setFreeform('')
  }
  async function handleSave() {
    await save.mutateAsync({ id: initial?.id, name, tags })
    onSaved()
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader><DialogTitle>{initial ? 'Edit set' : 'Create set'}</DialogTitle></DialogHeader>
        <div className="space-y-3">
          <Input placeholder="Set name (e.g. My media stack)" value={name} onChange={e => setName(e.target.value)} />

          <div>
            <p className="text-xs text-muted-foreground mb-1">Selected ({tags.length})</p>
            <div className="flex flex-wrap gap-1 min-h-7">
              {tags.length === 0 && <span className="text-xs text-muted-foreground">Pick apps below.</span>}
              {tags.map(t => (
                <span key={t} className="text-xs font-mono px-1.5 py-0.5 rounded bg-primary/15 text-primary flex items-center gap-1">
                  {t}<button onClick={() => toggle(t)} className="hover:text-red-500"><X className="h-3 w-3" /></button>
                </span>
              ))}
            </div>
          </div>

          <Input placeholder="Search sets and apps to add…" value={q} onChange={e => setQ(e.target.value)} className="h-8" />
          <div className="max-h-52 overflow-y-auto border border-border rounded-md">
            {groups.length === 0 && <p className="px-3 py-2 text-xs text-muted-foreground">No matches.</p>}
            {groups.map(g => (
              <div key={g.label}>
                <p className="sticky top-0 bg-muted/60 backdrop-blur px-3 py-1 text-[10px] uppercase tracking-wider text-muted-foreground">
                  {g.label}
                </p>
                {g.items.map(t => (
                  <button key={t} onClick={() => toggle(t)}
                    className="flex items-center gap-2 w-full text-left px-3 py-1.5 text-sm font-mono hover:bg-muted/40 border-b border-border/40 last:border-0">
                    <Plus className="h-3 w-3 text-muted-foreground shrink-0" />{t}
                  </button>
                ))}
              </div>
            ))}
          </div>

          <div className="flex gap-2">
            <Input placeholder="Add tag manually (comma separated)" value={freeform}
              onChange={e => setFreeform(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && (e.preventDefault(), addFree())} className="h-8" />
            <Button size="sm" variant="outline" onClick={addFree}>Add</Button>
          </div>

          <div className="flex justify-end gap-2 pt-1">
            <Button size="sm" variant="outline" onClick={onClose}>Cancel</Button>
            <Button size="sm" onClick={handleSave} disabled={!name.trim() || tags.length === 0 || save.isPending}>
              {save.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : 'Save set'}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

export function AppManager() {
  const { data: apps, isLoading } = useApps()
  const { data: cats } = useCategories()
  const { data: updMeta } = useUpdateMeta()
  const { data: version, refetch: refetchVersion } = useSaltboxVersion()
  const { data: updateStatus = {}, refetch: refetchUpdates } = useUpdateStatus()
  const install   = useInstallApp()
  const pullImg   = usePullImage()
  const removeApp = useRemoveApp()
  const checkUpd  = useCheckUpdates()
  const saltboxUpd = useSaltboxUpdate()
  const applyPatches = useApplyPatches()
  const containerAction = useContainerAction()
  const serviceAction = useServiceAction()
  const { data: customSets = [] } = useCustomSets()
  const deleteSet = useDeleteCustomSet()
  const installSet = useInstallSet()
  const qc = useQueryClient()
  const [setModal, setSetModal] = useState<{ open: boolean; editing: CustomSet | null }>({ open: false, editing: null })

  function handleRunSet(s: CustomSet) {
    if (!confirm(`Run "sb install ${s.tags.join(',')}"?`)) return
    installSet.mutate(s.tags, { onSuccess: d => startJob(d.job_id, s.name, 'install-set') })
  }
  function handleDeleteSet(s: CustomSet) {
    if (!confirm(`Delete set "${s.name}"?`)) return
    deleteSet.mutate(s.id, { onSuccess: () => qc.invalidateQueries({ queryKey: ['custom-sets'] }) })
  }

  function handleInstanceAction(
    name: string, action: 'start' | 'stop' | 'restart', kind: 'container' | 'service',
  ) {
    const mut = kind === 'service' ? serviceAction : containerAction
    mut.mutate({ name, action }, {
      onSuccess: () => setTimeout(() => qc.invalidateQueries({ queryKey: ['apps'] }), 800),
    })
  }

  const [mode, setMode]           = useState<Mode>('installed')
  const [search, setSearch]       = useState('')
  const [filter, setFilter]       = useState<Filter>('all')
  const [activeJobId, setActiveJobId] = useState<string | null>(null)
  const [activeTag, setActiveTag] = useState('')
  const [activeAction, setActiveAction] = useState('')
  const [configuringApp, setConfiguringApp] = useState<AppInfo | null>(null)
  const [detailApp, setDetailApp] = useState<AppInfo | null>(null)

  // Build image→outdated map from updateStatus
  const imageOutdated = updateStatus  // Record<image, bool|null>

  const matchSearch = (a: AppInfo) =>
    a.name.toLowerCase().includes(search.toLowerCase()) ||
    a.tag.toLowerCase().includes(search.toLowerCase())

  const installedApps = (apps ?? []).filter(a => a.installed && matchSearch(a))

  // Group installed apps by category, ordered per the backend's category order.
  const catOrder = cats?.order ?? ['other']
  const catLabels = cats?.labels ?? {}
  const groupedInstalled = catOrder
    .map(c => [c, installedApps.filter(a => (a.category ?? 'other') === c)] as const)
    .filter(([, list]) => list.length > 0)
  // Any app whose category isn't in the known order → bucket under 'other'
  const known = new Set(catOrder)
  const orphan = installedApps.filter(a => !known.has(a.category ?? 'other'))
  if (orphan.length) groupedInstalled.push(['other', orphan] as const)
  const addApps = (apps ?? []).filter((a) => {
    if (a.installed || !matchSearch(a)) return false
    if (filter === 'saltbox') return a.repo === 'saltbox'
    if (filter === 'sandbox') return a.repo === 'sandbox'
    return true
  })
  const installedCount = (apps ?? []).filter(a => a.installed).length
  const availableCount = (apps ?? []).filter(a => !a.installed).length

  const outdatedCount = (apps ?? []).filter(a => a.image && imageOutdated[a.image] === true).length

  // Keep the open detail view in sync with refreshed app data
  const liveDetailApp = detailApp ? (apps ?? []).find(a => a.tag === detailApp.tag) ?? detailApp : null

  function startJob(jobId: string, tag: string, action: string) {
    setActiveJobId(jobId)
    setActiveTag(tag)
    setActiveAction(action)
    qc.invalidateQueries({ queryKey: ['apps'] })
  }

  function handleAction(tag: string, action: string) {
    if (action === 'remove') {
      if (!confirm(`Remove ${tag}? This stops and removes its container(s).`)) return
      const purge = confirm(
        `Also DELETE all appdata in /opt for ${tag}?\n\n` +
        `This is IRREVERSIBLE — back up first if unsure.\n` +
        `OK = delete data · Cancel = keep data (container only)`,
      )
      removeApp.mutate({ tag, purge }, { onSuccess: d => startJob(d.job_id, tag, 'remove') })
      return
    }
    if (action === 'pull') {
      pullImg.mutate(tag, { onSuccess: d => startJob(d.job_id, tag, 'pull') })
    } else {
      install.mutate({ tag, action }, { onSuccess: d => startJob(d.job_id, tag, action) })
    }
  }

  function handleInstallBundle(tag: string) {
    if (!confirm(`Run "sb install ${tag}"? This installs/updates every role in the set.`)) return
    install.mutate({ tag, action: 'install' }, { onSuccess: d => startJob(d.job_id, tag, 'install') })
  }

  function handleCheckUpdates() {
    checkUpd.mutate(undefined, {
      onSuccess: (d) => {
        setActiveJobId(d.job_id)
        setActiveTag('Image update check')
        setActiveAction('check-updates')
        // Refetch update-status when job finishes (poll via LogStream completion)
      },
    })
  }

  function handleSaltboxUpdate() {
    saltboxUpd.mutate(undefined, {
      onSuccess: (d) => {
        startJob(d.job_id, 'saltbox', 'update')
        refetchVersion()
      },
    })
  }

  function handleApplyPatches() {
    applyPatches.mutate(undefined, {
      onSuccess: (d) => startJob(d.job_id, 'saltbox', 'apply-patches'),
    })
  }

  const filterBtns: { key: Filter; label: string }[] = [
    { key: 'all',       label: 'All' },
    { key: 'saltbox',   label: 'Saltbox' },
    { key: 'sandbox',   label: 'Sandbox' },
  ]

  const dialogTitle =
    activeAction === 'pull'          ? `Pull & update: ${activeTag}` :
    activeAction === 'remove'        ? `Remove: ${activeTag}` :
    activeAction === 'update'        ? 'Saltbox update' :
    activeAction === 'check-updates' ? 'Image update check' :
    activeTag

  return (
    <div className="p-6 space-y-5">
      {/* Header */}
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-xl font-semibold text-foreground">App Manager</h1>
          {version && (
            <p className="text-xs text-muted-foreground mt-0.5 flex items-center gap-1.5">
              <GitCommit className="h-3 w-3" />
              Saltbox {version.tag ?? version.sha}
              {version.date && <span className="opacity-60">{version.date.slice(0, 10)}</span>}
            </p>
          )}
        </div>
        <div className="flex gap-2 flex-wrap">
          <div className="flex flex-col items-end">
            <Button
              size="sm" variant="outline"
              onClick={handleCheckUpdates}
              disabled={checkUpd.isPending}
            >
              {checkUpd.isPending
                ? <><Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />Checking…</>
                : <><CheckCircle2 className="h-3.5 w-3.5 mr-1.5" />Check image updates</>}
            </Button>
            {updMeta?.last_checked && (
              <span className="text-[10px] text-muted-foreground mt-0.5">
                checked {(() => {
                  const s = Math.max(0, Date.now() / 1000 - updMeta.last_checked)
                  return s < 60 ? 'just now' : s < 3600 ? `${Math.floor(s / 60)}m ago`
                    : s < 86400 ? `${Math.floor(s / 3600)}h ago` : `${Math.floor(s / 86400)}d ago`
                })()}
              </span>
            )}
          </div>
          <Button
            size="sm"
            variant={version && version.behind > 0 ? 'default' : 'outline'}
            className={version && version.behind > 0 ? 'bg-orange-500 hover:bg-orange-600 text-white' : ''}
            onClick={handleSaltboxUpdate}
            disabled={saltboxUpd.isPending || applyPatches.isPending}
          >
            {saltboxUpd.isPending
              ? <><Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />Updating…</>
              : <><ArrowUpCircle className="h-3.5 w-3.5 mr-1.5" />
                {version && version.behind > 0
                  ? `Update Saltbox (${version.behind} behind)`
                  : 'Update Saltbox'}</>}
          </Button>
          <Button
            size="sm" variant="outline"
            onClick={handleApplyPatches}
            disabled={applyPatches.isPending || saltboxUpd.isPending}
            title="Re-apply stored file patches without running sb update"
          >
            {applyPatches.isPending
              ? <><Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />Applying…</>
              : 'Apply patches'}
          </Button>
        </div>
      </div>

      {/* Saltbox update available banner (commits preview) */}
      {version && version.behind > 0 && version.commits.length > 0 && (
        <div className="flex items-center gap-3 rounded-lg border border-orange-500/30 bg-orange-500/5 px-4 py-3">
          <AlertTriangle className="h-4 w-4 text-orange-400 shrink-0" />
          <div className="text-sm text-orange-400 min-w-0">
            <span className="font-medium">{version.behind} commit{version.behind !== 1 ? 's' : ''} behind</span>
            <p className="text-xs mt-0.5 opacity-60 font-mono truncate">{version.commits[0]}</p>
          </div>
        </div>
      )}

      {/* Mode toggle: installed apps vs add-app catalog */}
      <div className="flex items-center gap-2 border-b border-border">
        {([['installed', `Installed (${installedCount})`], ['add', `Add app (${availableCount})`], ['install-types', 'Install types'], ['roles', 'Custom roles']] as const).map(([m, label]) => (
          <button key={m} onClick={() => setMode(m)}
            className={cn('px-3 py-2 text-sm border-b-2 -mb-px transition-colors',
              mode === m ? 'border-primary text-foreground font-medium' : 'border-transparent text-muted-foreground hover:text-foreground')}>
            {m === 'add' && <Plus className="h-3.5 w-3.5 inline mr-1" />}{label}
          </button>
        ))}
      </div>

      {/* Search + (add-mode) repo filters */}
      {(mode === 'installed' || mode === 'add') && (
        <div className="flex gap-3 flex-wrap">
          <div className="relative flex-1 min-w-52">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
            <Input className="pl-8" placeholder="Search apps…"
              value={search} onChange={(e) => setSearch(e.target.value)} />
          </div>
          {mode === 'add' && (
            <div className="flex gap-1.5 flex-wrap">
              {filterBtns.map(({ key, label }) => (
                <Button key={key} size="sm" variant={filter === key ? 'default' : 'outline'}
                  onClick={() => setFilter(key)}>{label}</Button>
              ))}
            </div>
          )}
        </div>
      )}

      {mode === 'install-types' && <InstallTypes embedded />}
      {mode === 'roles' && <CustomRoles />}

      {isLoading && <p className="text-muted-foreground text-sm">Loading apps…</p>}

      {/* Modals */}
      {setModal.open && (
        <CustomSetModal apps={apps ?? []} initial={setModal.editing}
          onClose={() => setSetModal({ open: false, editing: null })}
          onSaved={() => { setSetModal({ open: false, editing: null }); qc.invalidateQueries({ queryKey: ['custom-sets'] }) }} />
      )}
      <RoleConfigModal app={configuringApp} onClose={() => setConfiguringApp(null)} />
      <AppDetail app={liveDetailApp} onClose={() => setDetailApp(null)}
        onAction={handleAction} onInstanceAction={handleInstanceAction}
        onConfigure={(a) => { setDetailApp(null); setConfiguringApp(a) }}
        onOpenJob={(id) => { setActiveJobId(id); setActiveTag(id); setActiveAction('log') }} />

      {mode === 'installed' && installedApps.length === 0 && !isLoading && (
        <div className="text-center py-16 text-muted-foreground text-sm">
          No installed apps yet. Switch to <button className="text-primary underline" onClick={() => setMode('add')}>Add app</button> to install one.
        </div>
      )}

      {mode === 'installed' ? (
        <div className="space-y-5">
          {/* Custom user sets: install several apps at once (sb install a,b,…) */}
          <CustomSetsSection sets={customSets} busy={installSet.isPending}
            onRun={handleRunSet} onEdit={(s) => setSetModal({ open: true, editing: s })}
            onDelete={handleDeleteSet} onCreate={() => setSetModal({ open: true, editing: null })} />

          {/* Install sets = the same `sb install <tag>` commands used to update/reinstall
              the installed system (core, mounts, profiles…) */}
          <BundlesSection busy={install.isPending} onInstall={handleInstallBundle} />
          {groupedInstalled.map(([cat, list]) => (
            <section key={cat}>
              <div className="flex items-center gap-2 mb-1.5">
                <h2 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                  {catLabels[cat] ?? cat}
                </h2>
                <span className="text-xs text-muted-foreground/60">{list.length}</span>
                <div className="flex-1 h-px bg-border" />
              </div>
              <div className="border border-border rounded-lg divide-y divide-border overflow-hidden">
                {list.map((app) => (
                  <InstalledRow key={app.tag} app={app}
                    onOpen={setDetailApp} onConfigure={setConfiguringApp}
                    onInstanceAction={handleInstanceAction}
                    outdated={app.image ? imageOutdated[app.image] : undefined} />
                ))}
              </div>
            </section>
          ))}
        </div>
      ) : mode === 'add' ? (
        <div className="space-y-2">
          {addApps.map((app) => (
            <AddRow key={app.tag} app={app} onAction={handleAction} onConfigure={setConfiguringApp} />
          ))}
        </div>
      ) : null}

      {/* Auto-refetch update-status when a check-updates job finishes */}
      {activeAction === 'check-updates' && (
        <JobWatcher
          jobId={activeJobId}
          onComplete={() => { refetchUpdates(); refetchVersion(); qc.invalidateQueries({ queryKey: ['image-info'] }) }}
        />
      )}

      <Dialog
        open={!!activeJobId}
        onOpenChange={(o) => {
          if (!o) {
            setActiveJobId(null)
            refetchUpdates()
            // pull/reinstall re-check the image server-side after the playbook; by the
            // time the user closes the log it's done — refresh the detail badge too.
            qc.invalidateQueries({ queryKey: ['image-info'] })
          }
        }}
      >
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle className="font-mono text-sm">{dialogTitle}</DialogTitle>
          </DialogHeader>
          <LogStream jobId={activeJobId} />
        </DialogContent>
      </Dialog>
    </div>
  )
}
