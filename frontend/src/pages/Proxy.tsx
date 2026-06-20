/**
 * Proxy — tsdproxy (Tailscale) as a second proxy beside Traefik. Installed as a
 * host systemd service (survives Docker restarts). This page manages the host /
 * non-docker services exposed on the tailnet via the list provider (containerized
 * apps are exposed per-app via inventory labels instead).
 */
import { useEffect, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  useProxyStatus, useProxyInstall, useProxyRekey, useProxyTest, useProxyRestart,
  useProxyLists, useProxyAddList, useProxyDelList, useProxySelf, useProxySetSelf,
  useProxyDash, useProxySetDash, useProxyOpts, useProxySetOpts,
  useProxyApps, useProxyAppSet,
  type TsAuth, type ProxySelf, type ProxyOpts, type AppTSState,
} from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Switch } from '@/components/ui/switch'
import { Download, Plus, Trash2, Loader2, Check, KeyRound, ShieldCheck, RotateCw, SlidersHorizontal } from 'lucide-react'
import { cn } from '@/lib/cn'

/**
 * AuthSetup — shared Tailscale credential form (auth key vs OAuth client) with a
 * real "Test" against the Tailscale API. Used by both Install and Re-authenticate.
 */
function AuthSetup({ submitLabel, submitIcon, pending, error, success, onSubmit }: {
  submitLabel: string
  submitIcon: ReactNode
  pending: boolean
  error?: string
  success?: ReactNode
  onSubmit: (p: TsAuth) => void
}) {
  const test = useProxyTest()
  const [mode, setMode] = useState<'oauth' | 'authkey'>('oauth')
  const [authKey, setAuthKey] = useState('')
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [tags, setTags] = useState('tag:tsdproxy')

  const payload = (): TsAuth => mode === 'oauth'
    ? { mode, client_id: clientId.trim(), client_secret: clientSecret.trim(), tags: tags.trim() || 'tag:tsdproxy' }
    : { mode, auth_key: authKey.trim() }
  const ready = mode === 'oauth' ? !!clientId.trim() && !!clientSecret.trim() : !!authKey.trim()
  const r = test.data

  const Tab = ({ m, label }: { m: 'oauth' | 'authkey'; label: string }) => (
    <button
      onClick={() => { setMode(m); test.reset() }}
      className={cn('px-2.5 py-1 rounded-md text-xs border', mode === m
        ? 'border-primary bg-primary/10 text-foreground'
        : 'border-border text-muted-foreground hover:text-foreground')}
    >{label}</button>
  )

  return (
    <div className="space-y-2.5">
      <div className="flex gap-1.5">
        <Tab m="oauth" label="OAuth client (recommended)" />
        <Tab m="authkey" label="Auth key" />
      </div>

      {mode === 'oauth' ? (
        <>
          <p className="text-[11px] text-muted-foreground">OAuth client never expires. Create one in Tailscale admin → <span className="font-mono">Settings → OAuth clients</span> with the <span className="font-mono">auth_keys</span> write scope and a tag (e.g. <span className="font-mono">tag:tsdproxy</span>), and make sure that tag is in your ACL <span className="font-mono">tagOwners</span>.</p>
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <Label className="text-[10px] text-muted-foreground">Client ID</Label>
              <Input className="h-8 font-mono" value={clientId} onChange={(e) => setClientId(e.target.value)} placeholder="k123ABC…"
                autoComplete="off" name="tsdproxy-client-id" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
            </div>
            <div className="space-y-1">
              <Label className="text-[10px] text-muted-foreground">Client secret</Label>
              <Input className="h-8 font-mono" type="password" value={clientSecret} onChange={(e) => setClientSecret(e.target.value)} placeholder="tskey-client-…"
                autoComplete="new-password" name="tsdproxy-client-secret" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
            </div>
          </div>
          <div className="space-y-1">
            <Label className="text-[10px] text-muted-foreground">Tags (comma-separated)</Label>
            <Input className="h-8 font-mono" value={tags} onChange={(e) => setTags(e.target.value)} placeholder="tag:tsdproxy" />
          </div>
        </>
      ) : (
        <>
          <p className="text-[11px] text-muted-foreground">Auth key expires (≤90 days). Use a <strong>reusable, non-ephemeral</strong> key (Tailscale admin → <span className="font-mono">Settings → Keys</span>) — tsdproxy registers one node per service.</p>
          <div className="space-y-1">
            <Label className="text-[10px] text-muted-foreground">Auth key</Label>
            <Input className="h-8 font-mono" type="password" value={authKey} onChange={(e) => setAuthKey(e.target.value)} placeholder="tskey-auth-…"
              autoComplete="new-password" name="tsdproxy-auth-key" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
          </div>
        </>
      )}

      <div className="flex items-center gap-2">
        <Button size="sm" className="gap-1.5" disabled={pending || !ready} onClick={() => onSubmit(payload())}>
          {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : submitIcon}{submitLabel}
        </Button>
        <Button size="sm" variant="outline" className="gap-1.5" disabled={test.isPending || !ready} onClick={() => test.mutate(payload())}>
          {test.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <ShieldCheck className="h-3.5 w-3.5" />}Test
        </Button>
      </div>

      {/* test result */}
      {test.isError && <p className="text-xs text-destructive break-all">{test.error.message}</p>}
      {r?.ok && (
        <div className="rounded-md border border-success/30 bg-success/5 px-2.5 py-1.5 text-[11px] space-y-0.5">
          <p className="text-success font-medium">
            ✓ {r.mode === 'oauth' ? 'OAuth client valid' : (r.looks_valid ? 'Key format OK' : 'Unexpected key format')}
            {r.scope ? ` · scope: ${r.scope}` : ''}
            {r.expires_in ? ` · token ${Math.round(r.expires_in / 60)}m` : ''}
          </p>
          {r.tailnet && <p className="text-foreground">Tailnet: <span className="font-mono">{r.tailnet}</span>{r.user ? ` · ${r.user}` : ''}{r.devices != null ? ` · ${r.devices} devices` : ''}</p>}
          {r.note && <p className="text-muted-foreground">{r.note}</p>}
        </div>
      )}

      {error && <p className="text-xs text-destructive break-all">{error}</p>}
      {success}
    </div>
  )
}

// iconPreviewURL maps a tsdproxy icon ref ("si/tailscale", "mdi/...", "sh/...") to a
// CDN SVG so we can preview it. Empty for unknown/blank refs.
function iconPreviewURL(v: string): string {
  const s = v.trim()
  const i = s.indexOf('/')
  if (i < 0) return ''
  const lib = s.slice(0, i), name = s.slice(i + 1)
  if (!name) return ''
  switch (lib) {
    case 'si': return `https://cdn.jsdelivr.net/npm/simple-icons/icons/${name}.svg`
    case 'mdi': return `https://cdn.jsdelivr.net/npm/@mdi/svg/svg/${name}.svg`
    case 'sh': return `https://cdn.jsdelivr.net/gh/selfhst/icons/svg/${name}.svg`
    default: return ''
  }
}

/** IconPreview — small swatch that renders the entered icon (on white so monochrome icons show). */
function IconPreview({ value }: { value: string }) {
  const url = iconPreviewURL(value)
  const [ok, setOk] = useState(true)
  useEffect(() => setOk(true), [url])
  return (
    <span className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded border border-border bg-white/90" title={value || 'icon preview'}>
      {url && ok
        ? <img src={url} alt="" className="h-4 w-4 object-contain" onError={() => setOk(false)} />
        : <span className="text-[9px] text-muted-foreground">?</span>}
    </span>
  )
}

/**
 * ManagedExpose — one compact row: toggle + tailnet sub-URL for an sb-ui-managed
 * entry (sb-ui itself or the tsdproxy dashboard). Backend resolves the live target.
 */
function ManagedExpose({ title, hint, data, pending, error, onSubmit }: {
  title: string
  hint: string
  data?: ProxySelf
  pending: boolean
  error?: string
  onSubmit: (p: { enabled: boolean; name: string; label: string; icon: string; hidden: boolean }) => void
}) {
  const [name, setName] = useState('')
  const [label, setLabel] = useState('')
  const [icon, setIcon] = useState('')
  const [hidden, setHidden] = useState(false)
  useEffect(() => { if (data) { setName(data.name); setLabel(data.label ?? ''); setIcon(data.icon ?? ''); setHidden(data.hidden ?? false) } }, [data])
  const changed = name.trim() !== data?.name || label.trim() !== (data?.label ?? '') || icon.trim() !== (data?.icon ?? '') || hidden !== (data?.hidden ?? false)
  const submit = (enabled: boolean, h = hidden) => onSubmit({ enabled, name: (name || data?.name || '').trim(), label: label.trim(), icon: icon.trim(), hidden: h })
  return (
    <div className="py-2.5 space-y-1.5">
      <div className="flex items-center gap-2 flex-wrap">
        <Switch checked={data?.enabled ?? false} disabled={pending} onCheckedChange={(v) => submit(v)} />
        <span className="text-sm font-medium text-foreground w-40 shrink-0">{title}</span>
        <Input className="h-7 w-28" value={name} onChange={(e) => setName(e.target.value)} disabled={!data?.enabled} title="Sub-URL (tailnet host)" placeholder="sub-url" />
        <Input className="h-7 w-24" value={label} onChange={(e) => setLabel(e.target.value)} disabled={!data?.enabled} title="Dashboard label" placeholder="label" />
        <Input className="h-7 w-28 font-mono" value={icon} onChange={(e) => setIcon(e.target.value)} disabled={!data?.enabled} title="Dashboard icon (e.g. si/synology)" placeholder="icon" />
        <IconPreview value={icon} />
        <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground shrink-0 cursor-pointer" title="Show on the tsdproxy dashboard">
          <Switch checked={!hidden} disabled={!data?.enabled} onCheckedChange={(v) => setHidden(!v)} />show
        </label>
        <Button size="sm" variant="outline" className="h-7 gap-1" disabled={pending || !name.trim() || !changed} onClick={() => submit(data?.enabled ?? true)}><Check className="h-3 w-3" />Save</Button>
        {data?.enabled && <span className="text-[11px] text-muted-foreground font-mono truncate">{(name || data?.name)}.&lt;tailnet&gt;.ts.net</span>}
      </div>
      <p className="text-[10px] text-muted-foreground ml-6">{hint}</p>
      {error && <p className="text-xs text-destructive break-all ml-6">{error}</p>}
    </div>
  )
}

/**
 * AppExposeRow — per-app "Expose on Tailscale" via Docker labels. Toggling intent is
 * local; Apply writes the app's inventory labels and reinstalls it (heavy, explicit).
 */
function AppExposeRow({ a, onApplied }: { a: AppTSState; onApplied: () => void }) {
  const set = useProxyAppSet(a.tag)
  const [enabled, setEnabled] = useState(a.enabled)
  const [name, setName] = useState(a.name)
  const [port, setPort] = useState(a.port)
  const [label, setLabel] = useState(a.label)
  const [icon, setIcon] = useState(a.icon)
  const [hidden, setHidden] = useState(a.hidden)
  useEffect(() => { setEnabled(a.enabled); setName(a.name); setPort(a.port); setLabel(a.label); setIcon(a.icon); setHidden(a.hidden) }, [a])
  const multi = (a.instances?.length ?? 0) > 1
  const changed = enabled !== a.enabled || (!multi && name !== a.name) || port !== a.port || label !== a.label || icon !== a.icon || hidden !== a.hidden
  const apply = () => set.mutate({ enabled, name: name.trim(), port: port.trim(), label: label.trim(), icon: icon.trim(), hidden }, { onSuccess: onApplied })
  return (
    <div className="py-2.5 space-y-1.5">
      <div className="flex items-center gap-2 flex-wrap">
        <Switch checked={enabled} disabled={set.isPending} onCheckedChange={setEnabled} />
        <span className="text-sm font-medium text-foreground w-36 shrink-0 truncate">{a.app}{multi && <span className="ml-1 text-[10px] text-muted-foreground">×{a.instances!.length}</span>}</span>
        {enabled && <>
          {!multi && <Input className="h-7 w-28" value={name} onChange={(e) => setName(e.target.value)} title="Tailnet host" placeholder="name" />}
          <Input className="h-7 w-16 font-mono" value={port} onChange={(e) => setPort(e.target.value)} title="Container port" placeholder={a.default_port || 'port'} />
          {!multi && <Input className="h-7 w-24" value={label} onChange={(e) => setLabel(e.target.value)} title="Dashboard label" placeholder="label" />}
          <Input className="h-7 w-28 font-mono" value={icon} onChange={(e) => setIcon(e.target.value)} title="Dashboard icon" placeholder="icon" />
          <IconPreview value={icon} />
          <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground shrink-0 cursor-pointer" title="Show on dashboard"><Switch checked={!hidden} onCheckedChange={(v) => setHidden(!v)} />show</label>
        </>}
        <Button size="sm" variant="outline" className="h-7 gap-1" disabled={set.isPending || !changed} onClick={apply}>
          {set.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : <Check className="h-3 w-3" />}Apply
        </Button>
        {enabled && !multi && <span className="text-[11px] text-muted-foreground font-mono truncate">{name}.&lt;tailnet&gt;.ts.net</span>}
      </div>
      {enabled && multi && <p className="text-[10px] text-muted-foreground ml-6">Per-instance hosts: {a.instances!.map((n) => <span key={n} className="font-mono">{n} </span>)}</p>}
      {set.isError && <p className="text-xs text-destructive break-all ml-6">{set.error.message}</p>}
      {set.isSuccess && <p className="text-[11px] text-success ml-6">Reinstalling {a.app} to apply… (see Logs)</p>}
    </div>
  )
}

/**
 * AdvancedForm — structured editor for the server-level tsdproxy.yaml settings.
 * Saving rewrites the config (credentials preserved server-side) and restarts.
 */
function AdvancedForm({ opts, pending, error, success, onSubmit }: {
  opts?: ProxyOpts
  pending: boolean
  error?: string
  success?: ReactNode
  onSubmit: (o: ProxyOpts) => void
}) {
  const [o, setO] = useState<ProxyOpts>({
    log_level: 'info', log_json: false, dash_port: 8080, access_log: false, admin_localhost: true,
    control_url: '', prevent_duplicates: false, max_cert_concurrency: 2,
    target_hostname: 'host.docker.internal', try_internal_net: true,
    health_check: true, health_interval: 30, health_failures: 3, health_cooldown: 0, auto_restart: true,
  })
  useEffect(() => { if (opts) setO(opts) }, [opts])
  const set = (p: Partial<ProxyOpts>) => setO((s) => ({ ...s, ...p }))

  const num = (label: string, key: keyof ProxyOpts, hint?: string) => (
    <div className="space-y-1">
      <Label className="text-[10px] text-muted-foreground">{label}{hint && <span className="opacity-60"> · {hint}</span>}</Label>
      <Input className="h-8 font-mono" type="number" value={o[key] as number} onChange={(e) => set({ [key]: Number(e.target.value) } as Partial<ProxyOpts>)} />
    </div>
  )
  const toggle = (key: keyof ProxyOpts, label: string, hint?: string) => (
    <div className="flex items-start justify-between gap-3 rounded-md border border-border px-3 py-2">
      <span className="text-sm text-foreground">{label}{hint && <span className="block text-[11px] text-muted-foreground">{hint}</span>}</span>
      <Switch checked={o[key] as boolean} onCheckedChange={(v) => set({ [key]: v } as Partial<ProxyOpts>)} className="mt-0.5" />
    </div>
  )
  const section = (title: string) => <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground pt-2">{title}</h3>

  return (
    <div className="space-y-3 rounded-lg border border-border bg-card p-4">
      <h2 className="text-sm font-semibold text-foreground flex items-center gap-1.5"><SlidersHorizontal className="h-3.5 w-3.5" />tsdproxy.yaml settings</h2>
      <p className="text-[11px] text-muted-foreground">Full server-level config. Saving rewrites <span className="font-mono">tsdproxy.yaml</span> (credentials kept) and restarts. Auth (OAuth/key) lives in the Authentication tab.</p>

      {section('Server')}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div className="space-y-1">
          <Label className="text-[10px] text-muted-foreground">Log level</Label>
          <select className="h-8 w-full rounded-md border border-input bg-transparent px-2 text-sm text-foreground" value={o.log_level} onChange={(e) => set({ log_level: e.target.value })}>
            {['trace', 'debug', 'info', 'warn', 'error', 'fatal', 'panic'].map((l) => <option key={l} value={l}>{l}</option>)}
          </select>
        </div>
        {num('Dashboard port', 'dash_port', 'http')}
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
        {toggle('access_log', 'Proxy access log', 'log every request — verbose')}
        {toggle('log_json', 'JSON logs', 'structured log output')}
        {toggle('admin_localhost', 'Allow localhost admin', 'needed for the dashboard — keep on')}
      </div>

      {section('Tailscale provider')}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div className="space-y-1">
          <Label className="text-[10px] text-muted-foreground">Control URL <span className="opacity-60">· Headscale; default for Tailscale</span></Label>
          <Input className="h-8 font-mono" value={o.control_url} onChange={(e) => set({ control_url: e.target.value })} placeholder="https://controlplane.tailscale.com" />
        </div>
        {num('Max cert concurrency', 'max_cert_concurrency')}
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
        {toggle('prevent_duplicates', 'Prevent duplicates', 'avoid duplicate tailnet nodes on re-register')}
      </div>

      {section('Docker provider')}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div className="space-y-1">
          <Label className="text-[10px] text-muted-foreground">Target hostname <span className="opacity-60">· how tsdproxy reaches containers</span></Label>
          <Input className="h-8 font-mono" value={o.target_hostname} onChange={(e) => set({ target_hostname: e.target.value })} placeholder="host.docker.internal" />
        </div>
        <div className="flex items-end">{toggle('try_internal_net', 'Try Docker internal network', 'use container IP — host-mode tsdproxy')}</div>
      </div>

      {section('Health check (docker + host services)')}
      <div className="grid grid-cols-3 gap-3">
        {num('Interval', 'health_interval', 's')}
        {num('Failures', 'health_failures')}
        {num('Cooldown', 'health_cooldown', 's')}
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
        {toggle('health_check', 'Health check enabled', 'probe backends; off = no probing')}
        {toggle('auto_restart', 'Auto-restart', 'restart a proxy when its backend recovers')}
      </div>

      <Button size="sm" className="gap-1.5 mt-2" disabled={pending} onClick={() => onSubmit({ ...o, control_url: o.control_url.trim(), target_hostname: o.target_hostname.trim() })}>
        {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Check className="h-3.5 w-3.5" />}Save & restart
      </Button>
      {error && <p className="text-xs text-destructive break-all">{error}</p>}
      {success}
    </div>
  )
}

export function ProxyPanel() {
  const qc = useQueryClient()
  const { data: status } = useProxyStatus()
  const install = useProxyInstall()
  const rekey = useProxyRekey()
  const { data: lists } = useProxyLists()
  const add = useProxyAddList()
  const del = useProxyDelList()
  const { data: self } = useProxySelf()
  const setSelf = useProxySetSelf()
  const { data: dash } = useProxyDash()
  const setDash = useProxySetDash()
  const { data: opts } = useProxyOpts()
  const setOpts = useProxySetOpts()
  const { data: appsData } = useProxyApps()
  const restart = useProxyRestart()
  const invApps = () => qc.invalidateQueries({ queryKey: ['proxy-apps'] })
  const [name, setName] = useState('')
  const [target, setTarget] = useState('')
  const [label, setLabel] = useState('')
  const [icon, setIcon] = useState('')
  const [hidden, setHidden] = useState(false)
  const entries = lists?.entries ?? []
  const invLists = () => qc.invalidateQueries({ queryKey: ['proxy-lists'] })
  const invSelf = () => qc.invalidateQueries({ queryKey: ['proxy-self'] })
  const invDash = () => qc.invalidateQueries({ queryKey: ['proxy-dash'] })

  const doInstall = (p: TsAuth) => install.mutate(p, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['proxy-status'] }); invSelf() } })
  const doRekey = (p: TsAuth) => rekey.mutate(p, { onSuccess: () => qc.invalidateQueries({ queryKey: ['proxy-status'] }) })
  const doAdd = () => add.mutate({ name: name.trim(), target: target.trim(), label: label.trim(), icon: icon.trim(), hidden }, { onSuccess: () => { setName(''); setTarget(''); setLabel(''); setIcon(''); setHidden(false); invLists() } })

  return (
    <div className="space-y-5">
      <p className="text-sm text-muted-foreground">Expose host services on your tailnet via tsdproxy — private HTTPS, no public exposure. Runs as a host service alongside Traefik.</p>

      {/* status */}
      <div className="flex items-center gap-3 rounded-lg border border-border bg-card px-3 py-2 text-sm">
        <span className={cn('h-2 w-2 rounded-full', status?.active ? 'bg-success' : status?.installed ? 'bg-amber-500' : 'bg-muted-foreground/40')} />
        <span className="text-foreground">{!status?.installed ? 'Not installed' : status?.active ? 'Running' : `Installed · ${status?.status || 'stopped'}`}</span>
        {status?.installed && (
          <Button
            size="sm" variant="outline" className="ml-auto h-7 gap-1.5"
            disabled={restart.isPending}
            onClick={() => restart.mutate(undefined, { onSuccess: () => qc.invalidateQueries({ queryKey: ['proxy-status'] }) })}
          >
            {restart.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RotateCw className="h-3.5 w-3.5" />}Restart
          </Button>
        )}
      </div>
      {restart.isError && <p className="-mt-3 text-xs text-destructive">{restart.error.message}</p>}

      {!status?.installed ? (
        <div className="space-y-3 rounded-lg border border-border bg-card p-4">
          <h2 className="text-sm font-semibold text-foreground">Install tsdproxy</h2>
          <p className="text-[11px] text-muted-foreground">Downloads the binary, writes a systemd unit (survives Docker restarts), and starts it.</p>
          <AuthSetup
            submitLabel="Install & start"
            submitIcon={<Download className="h-3.5 w-3.5" />}
            pending={install.isPending}
            error={install.isError ? install.error.message : undefined}
            onSubmit={doInstall}
          />
        </div>
      ) : (
       <Tabs defaultValue="services">
        <TabsList className="mb-1">
          <TabsTrigger value="services">Services</TabsTrigger>
          <TabsTrigger value="apps">Docker apps</TabsTrigger>
          <TabsTrigger value="auth">Authentication</TabsTrigger>
          <TabsTrigger value="advanced">Advanced</TabsTrigger>
        </TabsList>

        <TabsContent value="services" className="space-y-5">
        {/* built-in services (sb-ui + tsdproxy dashboard) — compact rows */}
        <div className="rounded-lg border border-border bg-card px-4 py-1 divide-y divide-border">
          <ManagedExpose
            title="sb-ui on Tailscale"
            hint="Management UI — auto-exposed; port resolved live (survives reinstalls)."
            data={self}
            pending={setSelf.isPending}
            error={setSelf.isError ? setSelf.error.message : undefined}
            onSubmit={(p) => setSelf.mutate({ ...p, name: p.name || 'sb-ui' }, { onSuccess: invSelf })}
          />
          <ManagedExpose
            title="tsdproxy Dashboard"
            hint="Built-in dashboard — all proxies & live status, authenticated by Tailscale identity."
            data={dash}
            pending={setDash.isPending}
            error={setDash.isError ? setDash.error.message : undefined}
            onSubmit={(p) => setDash.mutate({ ...p, name: p.name || 'dash' }, { onSuccess: invDash })}
          />
        </div>

        <div className="space-y-3 rounded-lg border border-border bg-card p-4">
          <h2 className="text-sm font-semibold text-foreground">Exposed host services</h2>
          <p className="text-[11px] text-muted-foreground">Each entry gets <span className="font-mono">name.&lt;tailnet&gt;.ts.net</span> over HTTPS (http auto-redirects). Use for non-docker services. Target must be reachable from the host — use <span className="font-mono">127.0.0.1:&lt;port&gt;</span>. Label/icon are optional dashboard card info (icon e.g. <span className="font-mono">si/synology</span>).</p>
          <div className="flex flex-wrap items-end gap-2">
            <div className="space-y-1">
              <Label className="text-[10px] text-muted-foreground">Name (tailnet host)</Label>
              <Input className="h-8 w-32" value={name} onChange={(e) => setName(e.target.value)} placeholder="nas" />
            </div>
            <div className="space-y-1 flex-1 min-w-[180px]">
              <Label className="text-[10px] text-muted-foreground">Target URL</Label>
              <Input className="h-8 font-mono" value={target} onChange={(e) => setTarget(e.target.value)} placeholder="http://127.0.0.1:9180" />
            </div>
            <div className="space-y-1">
              <Label className="text-[10px] text-muted-foreground">Label <span className="opacity-60">(opt)</span></Label>
              <Input className="h-8 w-28" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="NAS" />
            </div>
            <div className="space-y-1">
              <Label className="text-[10px] text-muted-foreground">Icon <span className="opacity-60">(opt)</span></Label>
              <div className="flex items-center gap-1.5">
                <Input className="h-8 w-28 font-mono" value={icon} onChange={(e) => setIcon(e.target.value)} placeholder="si/synology" />
                <IconPreview value={icon} />
              </div>
            </div>
            <label className="flex items-center gap-1.5 text-xs text-foreground pb-1.5 cursor-pointer" title="Show on the tsdproxy dashboard">
              <Switch checked={!hidden} onCheckedChange={(v) => setHidden(!v)} />show
            </label>
            <Button size="sm" className="gap-1.5" disabled={add.isPending || !name.trim() || !target.trim()} onClick={doAdd}><Plus className="h-3.5 w-3.5" />Add</Button>
          </div>
          <p className="text-[10px] text-muted-foreground">Browse icons: <a className="underline" href="https://simpleicons.org" target="_blank" rel="noreferrer">si/</a> · <a className="underline" href="https://pictogrammers.com/library/mdi/" target="_blank" rel="noreferrer">mdi/</a> · <a className="underline" href="https://selfh.st/icons/" target="_blank" rel="noreferrer">sh/</a> — reference as <span className="font-mono">library/name</span> (e.g. <span className="font-mono">si/tailscale</span>)</p>
          {add.isError && <p className="text-xs text-destructive break-all">{add.error.message}</p>}

          <div className="rounded-md border border-border divide-y divide-border">
            {entries.length === 0 && <div className="px-3 py-6 text-center text-xs text-muted-foreground">No host services exposed yet.</div>}
            {entries.map((e) => (
              <div key={e.name} className="flex items-center gap-3 px-3 py-2 text-sm">
                <span className="font-medium text-foreground w-32 shrink-0 truncate">{e.name}</span>
                <span className="flex-1 min-w-0 truncate font-mono text-[11px] text-muted-foreground">{e.target}</span>
                {e.label && <span className="text-[11px] text-muted-foreground shrink-0 truncate max-w-[100px]">{e.label}</span>}
                {e.icon && <span className="text-[10px] font-mono text-muted-foreground/70 shrink-0 truncate max-w-[90px]">{e.icon}</span>}
                <button onClick={() => del.mutate(e.name, { onSuccess: invLists })} className="text-muted-foreground hover:text-destructive shrink-0"><Trash2 className="h-3.5 w-3.5" /></button>
              </div>
            ))}
          </div>
        </div>
        </TabsContent>

        <TabsContent value="apps" className="space-y-5">
          <div className="rounded-lg border border-border bg-card px-4 py-1">
            <div className="py-2 space-y-0.5">
              <h2 className="text-sm font-semibold text-foreground">Expose Docker apps</h2>
              <p className="text-[11px] text-muted-foreground">Toggle an app and Apply — sb-ui writes tsdproxy Docker labels into the app's Saltbox inventory (<span className="font-mono">&lt;role&gt;_role_docker_labels_custom</span>) and <strong>reinstalls</strong> it so the container picks them up. Port auto-detects from the role; override if needed.</p>
            </div>
            <div className="divide-y divide-border">
              {(appsData?.apps ?? []).length === 0 && <div className="px-1 py-6 text-center text-xs text-muted-foreground">No installed Docker apps found.</div>}
              {(appsData?.apps ?? []).map((a) => <AppExposeRow key={a.tag} a={a} onApplied={invApps} />)}
            </div>
          </div>
        </TabsContent>

        <TabsContent value="auth" className="space-y-5">
          {/* re-authenticate — replace bad/expired credentials without re-installing */}
          <div className="space-y-2.5 rounded-lg border border-border bg-card p-4">
            <h2 className="text-sm font-semibold text-foreground flex items-center gap-1.5"><KeyRound className="h-3.5 w-3.5" />Re-authenticate</h2>
            <p className="text-[11px] text-muted-foreground">If nodes don't appear in Tailscale (e.g. <span className="font-mono">invalid key</span> in logs), enter fresh credentials — it rewrites the config, clears stale node state, and restarts. Test first to confirm they're valid.</p>
            <AuthSetup
              submitLabel="Update & restart"
              submitIcon={<KeyRound className="h-3.5 w-3.5" />}
              pending={rekey.isPending}
              error={rekey.isError ? rekey.error.message : undefined}
              success={rekey.isSuccess ? <p className="text-xs text-success">Updated — tsdproxy restarted. Check Tailscale admin in ~30s.</p> : undefined}
              onSubmit={doRekey}
            />
          </div>
        </TabsContent>

        <TabsContent value="advanced" className="space-y-5">
          <AdvancedForm
            opts={opts}
            pending={setOpts.isPending}
            error={setOpts.isError ? setOpts.error.message : undefined}
            success={setOpts.isSuccess ? <p className="text-xs text-success">Saved — tsdproxy.yaml updated & restarted.</p> : undefined}
            onSubmit={(o) => setOpts.mutate(o, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['proxy-opts'] }); qc.invalidateQueries({ queryKey: ['proxy-status'] }); invDash() } })}
          />
        </TabsContent>
       </Tabs>
      )}
    </div>
  )
}
