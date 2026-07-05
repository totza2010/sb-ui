/**
 * Integrations — live connectivity to every client library, dense enough to fit one
 * screen. Each instance shows version, latency, URL and item counts (series/movies/
 * episodes/indexers); Plex additionally breaks down each library's size.
 */
import { useState, useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useIntegrations, useOptions, useSaveOptions, useSeerrInstances, useSaveSeerrInstances, type ConnStatus, type IntegrationGroup, type OptionsConfig } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { Plug, Loader2, RefreshCw, CheckCircle2, XCircle, Star, CircleDashed, Film, Tv, Library as LibraryIcon, Settings2 } from 'lucide-react'

// Groups whose instances can be configured inline (per-instance connection).
const CONFIGURABLE = new Set(['plex', 'seerr', 'qbit'])
// Groups with overall (non-connection) settings, edited from the frame header.
// (Seerr default instance for requests. Plex upload behaviour moved to the Uploader.)
const GROUP_SETTINGS = new Set(['seerr'])

const n = (v: number) => v.toLocaleString()
const statsLine = (s?: { label: string; value: number }[]) => (s ?? []).map((x) => `${n(x.value)} ${x.label}`).join(' · ')

function StatusPill({ c, onConfigure }: { c: ConnStatus; onConfigure?: () => void }) {
  return (
    <div className={`rounded-md border px-2.5 py-1.5 ${c.ok ? 'border-success/30 bg-success/5' : 'border-destructive/30 bg-destructive/5'}`}>
      <div className="flex items-center gap-1.5">
        {c.ok ? <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-success" /> : <XCircle className="h-3.5 w-3.5 shrink-0 text-destructive" />}
        <span className="truncate text-xs font-medium text-foreground">{c.name}</span>
        {c.recommended && (
          <span className="flex items-center gap-0.5 rounded bg-[#e5a00d]/15 px-1 py-px text-[9px] font-semibold text-[#e5a00d]">
            <Star className="h-2.5 w-2.5 fill-current" />BEST
          </span>
        )}
        {c.primary && (
          <span className="rounded bg-primary/15 px-1 py-px text-[9px] font-semibold text-primary">DEFAULT</span>
        )}
        <span className="ml-auto shrink-0 text-[10px] tabular-nums text-muted-foreground">
          {c.version ? `v${c.version} · ` : ''}{c.latency_ms}ms
        </span>
        {onConfigure && (
          <button onClick={onConfigure} title={`Configure ${c.name}`} className="shrink-0 text-muted-foreground hover:text-foreground">
            <Settings2 className="h-3 w-3" />
          </button>
        )}
      </div>
      <p className="truncate font-mono text-[10px] text-muted-foreground" title={c.base_url}>{c.base_url || '—'}</p>
      {c.error && <p className="break-all text-[10px] text-destructive">{c.error}</p>}
      {!c.error && c.stats && c.stats.length > 0 && <p className="text-[10px] text-muted-foreground">{statsLine(c.stats)}</p>}
      {c.path_stats && c.path_stats.length > 0 && (
        <div className="mt-1 space-y-px border-t border-border/50 pt-1">
          {c.path_stats.map((ps) => (
            <div key={ps.path} className="flex items-baseline justify-between gap-2">
              <span className="truncate font-mono text-[10px] text-muted-foreground/80" title={ps.path}>{ps.path}</span>
              <span className="shrink-0 text-[10px] tabular-nums text-muted-foreground">{statsLine(ps.stats)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// groupTotals sums each instance stat label across the group (for the header).
function groupTotals(g: IntegrationGroup): string {
  const t = new Map<string, number>()
  for (const i of g.instances ?? []) for (const s of i.stats ?? []) t.set(s.label, (t.get(s.label) ?? 0) + s.value)
  return [...t].map(([l, v]) => `${n(v)} ${l}`).join(' · ')
}

function DialogShell({ title, children, onClose, footer }: { title: string; children: React.ReactNode; onClose: () => void; footer: React.ReactNode }) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <h2 className="text-base font-semibold text-foreground">{title}</h2>
        <div className="space-y-3">{children}{footer}</div>
      </DialogContent>
    </Dialog>
  )
}

function ConnFields({ url, setUrl, secret, setSecret, urlPh, secretLabel, secretPh }: {
  url: string; setUrl: (v: string) => void; secret: string; setSecret: (v: string) => void; urlPh: string; secretLabel: string; secretPh: string
}) {
  return (
    <>
      <label className="block space-y-1">
        <span className="text-xs font-medium text-muted-foreground">URL</span>
        <Input className="h-9 font-mono" value={url} onChange={(e) => setUrl(e.target.value)} placeholder={urlPh}
          autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
      </label>
      <label className="block space-y-1">
        <span className="text-xs font-medium text-muted-foreground">{secretLabel}</span>
        <Input className="h-9 font-mono" type="password" value={secret} onChange={(e) => setSecret(e.target.value)} placeholder={secretPh}
          autoComplete="new-password" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
      </label>
    </>
  )
}

function SaveFooter({ pending, error, onSave, onClose }: { pending: boolean; error?: string; onSave: () => void; onClose: () => void }) {
  return (
    <>
      {error && <p className="break-all text-[11px] text-destructive">{error}</p>}
      <div className="flex justify-end gap-2 pt-1">
        <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
        <Button size="sm" className="gap-1.5" disabled={pending} onClick={onSave}>
          {pending ? <Loader2 className="h-4 w-4 animate-spin" /> : null}Save
        </Button>
      </div>
    </>
  )
}

// PlexConfigDialog edits Plex from the options blob — connection (URL+token) or the
// overall upload behaviour (throttle / streams / scan).
function PlexConfigDialog({ scope, onClose }: { scope: 'instance' | 'group'; onClose: () => void }) {
  const { data: opts } = useOptions()
  const update = useSaveOptions()
  const qc = useQueryClient()
  const [url, setUrl] = useState('')
  const [secret, setSecret] = useState('')
  const [throttle, setThrottle] = useState(false)
  const [maxStreams, setMaxStreams] = useState(1)
  const [scan, setScan] = useState(true)
  useEffect(() => {
    if (!opts) return
    setUrl(opts.plex.url); setSecret(opts.plex.token)
    setThrottle(opts.plex.throttle); setMaxStreams(opts.plex.max_streams); setScan(opts.plex.scan_after_upload)
  }, [opts])
  const save = () => {
    if (!opts) return
    const next: OptionsConfig = scope === 'group'
      ? { ...opts, plex: { ...opts.plex, throttle, max_streams: maxStreams, scan_after_upload: scan } }
      : { ...opts, plex: { ...opts.plex, url: url.trim(), token: secret.trim() } }
    update.mutate(next, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['options'] }); qc.invalidateQueries({ queryKey: ['integrations'] }); onClose() } })
  }
  return (
    <DialogShell title={`Plex — ${scope === 'group' ? 'overall settings' : 'connection'}`} onClose={onClose}
      footer={<SaveFooter pending={update.isPending} error={update.error?.message} onSave={save} onClose={onClose} />}>
      {scope === 'group' ? (
        <>
          <label className="flex items-center justify-between gap-2 text-sm">
            <span className="text-foreground">Throttle uploads while streaming</span>
            <input type="checkbox" checked={throttle} onChange={(e) => setThrottle(e.target.checked)} className="accent-primary" />
          </label>
          <label className="flex items-center justify-between gap-2 text-sm">
            <span className="text-foreground">Pause above N active streams</span>
            <Input type="number" min={1} className="h-8 w-24" value={maxStreams} onChange={(e) => setMaxStreams(Math.max(1, parseInt(e.target.value, 10) || 1))} />
          </label>
          <label className="flex items-center justify-between gap-2 text-sm">
            <span className="text-foreground">Scan library after upload</span>
            <input type="checkbox" checked={scan} onChange={(e) => setScan(e.target.checked)} className="accent-primary" />
          </label>
        </>
      ) : (
        <ConnFields url={url} setUrl={setUrl} secret={secret} setSecret={setSecret} urlPh="http://localhost:32400" secretLabel="Token" secretPh="X-Plex-Token" />
      )}
    </DialogShell>
  )
}

// SeerrConfigDialog edits one detected Seerr instance (by name) in the multi-instance
// list — set its URL + API key without touching the Settings page.
function SeerrConfigDialog({ name, onClose }: { name: string; onClose: () => void }) {
  const { data } = useSeerrInstances()
  const save = useSaveSeerrInstances()
  const qc = useQueryClient()
  const list = data?.instances ?? []
  const [url, setUrl] = useState('')
  const [secret, setSecret] = useState('')
  useEffect(() => {
    const inst = list.find((i) => i.name === name)
    setUrl(inst?.url ?? ''); setSecret(inst?.api_key ?? '')
  }, [data, name]) // eslint-disable-line react-hooks/exhaustive-deps
  const onSave = () => {
    const next = list.some((i) => i.name === name)
      ? list.map((i) => (i.name === name ? { ...i, url: url.trim(), api_key: secret.trim() } : i))
      : [...list, { name, url: url.trim(), api_key: secret.trim() }]
    save.mutate(next, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['seerr-instances'] }); qc.invalidateQueries({ queryKey: ['integrations'] }); onClose() } })
  }
  return (
    <DialogShell title={`${name} — connection`} onClose={onClose}
      footer={<SaveFooter pending={save.isPending} error={save.error?.message} onSave={onSave} onClose={onClose} />}>
      <p className="text-[11px] text-muted-foreground">The URL is auto-detected from the container — usually you only need to paste the API key.</p>
      <ConnFields url={url} setUrl={setUrl} secret={secret} setSecret={setSecret} urlPh="https://requests.example.com" secretLabel="API key" secretPh="X-Api-Key" />
    </DialogShell>
  )
}

// SeerrDefaultDialog (group-level) chooses which configured instance handles Discover
// requests. Only fully-configured instances (URL + API key) are eligible.
function SeerrDefaultDialog({ onClose }: { onClose: () => void }) {
  const { data } = useSeerrInstances()
  const save = useSaveSeerrInstances()
  const qc = useQueryClient()
  const list = data?.instances ?? []
  const ready = list.filter((i) => i.url && i.api_key)
  const [def, setDef] = useState('')
  useEffect(() => { setDef(list.find((i) => i.default)?.name ?? ready[0]?.name ?? '') }, [data]) // eslint-disable-line react-hooks/exhaustive-deps
  const onSave = () => {
    const next = list.map((i) => ({ ...i, default: i.name === def }))
    save.mutate(next, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['seerr-instances'] }); qc.invalidateQueries({ queryKey: ['integrations'] }); onClose() } })
  }
  return (
    <DialogShell title="Seerr — default for requests" onClose={onClose}
      footer={<SaveFooter pending={save.isPending} error={save.error?.message} onSave={onSave} onClose={onClose} />}>
      {ready.length === 0 ? (
        <p className="text-sm text-muted-foreground">Configure an instance's URL + API key first (click the gear on its card).</p>
      ) : (
        <div className="space-y-1">
          <p className="text-xs text-muted-foreground">Discover requests are sent to the selected instance.</p>
          {ready.map((i) => (
            <label key={i.name} className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm hover:bg-accent">
              <input type="radio" name="seerr-default" checked={def === i.name} onChange={() => setDef(i.name)} className="accent-primary" />
              <span className="text-foreground">{i.name}</span>
              <span className="truncate font-mono text-[11px] text-muted-foreground">{i.url}</span>
            </label>
          ))}
        </div>
      )}
    </DialogShell>
  )
}

// QbitConfigDialog edits the qBittorrent WebUI connection (URL + user + pass) in the
// shared options — the uploader's block module uses the same connection.
function QbitConfigDialog({ detectedUrl, onClose }: { detectedUrl?: string; onClose: () => void }) {
  const { data: opts } = useOptions()
  const update = useSaveOptions()
  const qc = useQueryClient()
  const [url, setUrl] = useState('')
  const [user, setUser] = useState('')
  const [pass, setPass] = useState('')
  useEffect(() => {
    if (!opts) return
    setUrl(opts.qbit?.url || detectedUrl || ''); setUser(opts.qbit?.user ?? ''); setPass(opts.qbit?.pass ?? '')
  }, [opts, detectedUrl])
  const save = () => {
    if (!opts) return
    update.mutate({ ...opts, qbit: { url: url.trim(), user: user.trim(), pass } }, {
      onSuccess: () => { qc.invalidateQueries({ queryKey: ['options'] }); qc.invalidateQueries({ queryKey: ['integrations'] }); onClose() },
    })
  }
  return (
    <DialogShell title="qBittorrent — connection" onClose={onClose}
      footer={<SaveFooter pending={update.isPending} error={update.error?.message} onSave={save} onClose={onClose} />}>
      <p className="text-[11px] text-muted-foreground">URL auto-detects from the container — usually you only need the WebUI login. Used by the Uploader to pause/throttle while uploading.</p>
      <label className="block space-y-1">
        <span className="text-xs font-medium text-muted-foreground">URL</span>
        <Input className="h-9 font-mono" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="http://localhost:8080" autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
      </label>
      <div className="grid grid-cols-2 gap-2">
        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Username</span>
          <Input className="h-9" value={user} onChange={(e) => setUser(e.target.value)} placeholder="admin" autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
        </label>
        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Password</span>
          <Input className="h-9" type="password" value={pass} onChange={(e) => setPass(e.target.value)} placeholder="password" autoComplete="new-password" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
        </label>
      </div>
    </DialogShell>
  )
}

function GroupCard({ g, onConfigureGroup, onConfigureInstance }: { g: IntegrationGroup; onConfigureGroup?: () => void; onConfigureInstance?: (c: ConnStatus) => void }) {
  const instances = g.instances ?? []
  const okCount = instances.filter((i) => i.ok).length
  const totals = groupTotals(g)
  return (
    <div className="space-y-2 rounded-lg border border-border bg-card p-3">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
        <h2 className="text-sm font-semibold text-foreground">{g.label}</h2>
        {onConfigureGroup && (
          <button onClick={onConfigureGroup} title={`${g.label} overall settings`} className="text-muted-foreground hover:text-foreground">
            <Settings2 className="h-3.5 w-3.5" />
          </button>
        )}
        {g.used ? (
          <span className="rounded bg-primary/10 px-1.5 py-px text-[10px] font-medium text-primary">in use</span>
        ) : (
          <span className="flex items-center gap-0.5 rounded bg-muted px-1.5 py-px text-[10px] font-medium text-muted-foreground">
            <CircleDashed className="h-2.5 w-2.5" />probe
          </span>
        )}
        {g.configured && instances.length > 0 && (
          <span className={`text-[11px] ${okCount === instances.length ? 'text-success' : okCount > 0 ? 'text-[#e5a00d]' : 'text-destructive'}`}>
            {okCount}/{instances.length} OK
          </span>
        )}
        {totals && <span className="text-[11px] text-muted-foreground">· {totals}</span>}
        <span className="ml-auto hidden max-w-[45%] truncate font-mono text-[10px] text-muted-foreground sm:inline" title={g.library}>{g.library}</span>
      </div>

      {instances.length > 0 && (
        <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
          {instances.map((c, i) => <StatusPill key={`${c.name}-${i}`} c={c} onConfigure={onConfigureInstance ? () => onConfigureInstance(c) : undefined} />)}
        </div>
      )}

      {g.note && !totals && <p className="text-[11px] text-muted-foreground">{g.note}</p>}

      {g.libraries && g.libraries.length > 0 && (
        <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-3">
          {g.libraries.map((l) => {
            const Icon = l.type === 'movie' ? Film : l.type === 'show' ? Tv : LibraryIcon
            return (
              <div key={l.title} className="rounded border border-border bg-muted/30 px-2 py-1">
                <div className="flex items-center gap-1">
                  <Icon className="h-3 w-3 shrink-0 text-muted-foreground" />
                  <span className="truncate text-[11px] font-medium text-foreground" title={l.title}>{l.title}</span>
                </div>
                <p className="text-sm font-semibold tabular-nums text-foreground">{n(l.count)}</p>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

export function Integrations() {
  const qc = useQueryClient()
  const { data, isLoading, isFetching, isError, error } = useIntegrations()
  const groups = data?.groups ?? []
  const [config, setConfig] = useState<{ key: string; scope: 'instance' | 'group'; name?: string } | null>(null)

  return (
    <div className="w-full space-y-3 p-4">
      <div className="flex items-center justify-between gap-4">
        <h1 className="flex items-center gap-2 text-base font-semibold text-foreground"><Plug className="h-4 w-4" />Integrations</h1>
        <Button size="sm" variant="outline" className="h-7 shrink-0 gap-1.5" disabled={isFetching}
          onClick={() => qc.invalidateQueries({ queryKey: ['integrations'] })}>
          {isFetching ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}Recheck
        </Button>
      </div>

      {isLoading && <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Probing connections…</div>}
      {isError && <p className="text-sm text-destructive">{(error as Error)?.message}</p>}

      <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
        {groups.map((g) => (
          <GroupCard key={g.key} g={g}
            onConfigureGroup={GROUP_SETTINGS.has(g.key) ? () => setConfig({ key: g.key, scope: 'group' }) : undefined}
            onConfigureInstance={CONFIGURABLE.has(g.key) ? (c) => setConfig({ key: g.key, scope: 'instance', name: c.name }) : undefined} />
        ))}
      </div>

      {config?.key === 'plex' && <PlexConfigDialog scope={config.scope} onClose={() => setConfig(null)} />}
      {config?.key === 'seerr' && config.scope === 'group' && <SeerrDefaultDialog onClose={() => setConfig(null)} />}
      {config?.key === 'seerr' && config.scope === 'instance' && <SeerrConfigDialog name={config.name ?? ''} onClose={() => setConfig(null)} />}
      {config?.key === 'qbit' && <QbitConfigDialog detectedUrl={groups.find((g) => g.key === 'qbit')?.instances?.[0]?.base_url} onClose={() => setConfig(null)} />}
    </div>
  )
}
