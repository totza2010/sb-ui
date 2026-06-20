import { useEffect, useRef, useState } from 'react'
import { useConfig, useSaveConfig, useInstallApp, useRcloneRemotes, useSaveRcloneRemotes, useMountTemplates } from '@/lib/api'
import type { RcloneRemotes } from '@/lib/api'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { LogStream } from '@/components/LogStream'
import {
  Save, Play, Plus, Trash2, ChevronDown, ChevronRight,
  HardDrive, Cloud, Settings2, User, Eye, EyeOff, Server, Shield,
  Link, FolderOpen, X, GripVertical,
} from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { cn } from '@/lib/cn'

// ── yes/no helpers ─────────────────────────────────────────────────────────────

const yes = (v: unknown) => v === true || v === 'yes'
const toYN = (b: boolean) => (b ? 'yes' : 'no')

// ── Reusable UI pieces ─────────────────────────────────────────────────────────

function Field({ label, hint, children, className }: {
  label: string; hint?: string; children: React.ReactNode; className?: string
}) {
  return (
    <div className={cn('space-y-1.5', className)}>
      <Label className="text-sm font-medium">{label}</Label>
      {hint && <p className="text-xs text-muted-foreground leading-snug">{hint}</p>}
      {children}
    </div>
  )
}

function YesNoToggle({ value, onChange, label }: {
  value: unknown; onChange: (v: string) => void; label: string
}) {
  const on = yes(value)
  return (
    <button
      type="button"
      onClick={() => onChange(toYN(!on))}
      className={cn(
        'flex items-center gap-2 text-sm px-3 py-1.5 rounded-md border transition-colors select-none',
        on ? 'bg-primary/10 border-primary/30 text-primary'
           : 'bg-muted/30 border-border text-muted-foreground hover:bg-muted/50',
      )}
    >
      <span className={cn('w-8 h-4 rounded-full relative transition-colors shrink-0', on ? 'bg-primary' : 'bg-muted')}>
        <span className={cn('absolute top-0.5 w-3 h-3 bg-white rounded-full shadow transition-all', on ? 'left-4' : 'left-0.5')} />
      </span>
      {label}
    </button>
  )
}

function Section({ title, icon: Icon, children, defaultOpen = true }: {
  title: string; icon: React.ElementType; children: React.ReactNode; defaultOpen?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="border border-border rounded-lg overflow-hidden">
      <button type="button" onClick={() => setOpen(!open)}
        className="w-full flex items-center gap-2 px-4 py-3 bg-muted/20 hover:bg-muted/40 text-left transition-colors"
      >
        <Icon className="h-4 w-4 text-muted-foreground shrink-0" />
        <span className="font-medium text-sm">{title}</span>
        {open ? <ChevronDown className="h-3.5 w-3.5 ml-auto text-muted-foreground" />
               : <ChevronRight className="h-3.5 w-3.5 ml-auto text-muted-foreground" />}
      </button>
      {open && <div className="p-4 space-y-4">{children}</div>}
    </div>
  )
}

function SaveBar({ onSave, onApply, saving, applying, saved, error, applyLabel = 'Save & Apply' }: {
  onSave: () => void; onApply: () => void; saving: boolean; applying: boolean
  saved: boolean; error?: string; applyLabel?: string
}) {
  return (
    <div className="sticky top-[42px] z-20 -mx-6 px-6 py-2 mb-4 bg-background/95 backdrop-blur border-b border-border flex items-center gap-2 flex-wrap">
      <Button size="sm" variant="outline" onClick={onSave} disabled={saving || applying}>
        <Save className="h-3.5 w-3.5 mr-1.5" />Save
      </Button>
      <Button size="sm" onClick={onApply} disabled={saving || applying}>
        <Play className="h-3.5 w-3.5 mr-1.5" />{applyLabel}
      </Button>
      {saved && <span className="text-xs text-green-600 font-medium">Saved ✓</span>}
      {error && <span className="text-xs text-destructive">{error}</span>}
    </div>
  )
}

// ── Rclone Remote Card ─────────────────────────────────────────────────────────

interface VfsCache { enabled: unknown; max_age: string; size: string }
interface RemoteSettings {
  enable_refresh: unknown; mount: unknown; template: string
  union: unknown; upload: unknown; upload_from: string; vfs_cache: VfsCache
}
interface RcloneRemote { remote: string; settings: RemoteSettings }

function defaultRemote(): RcloneRemote {
  return {
    remote: '',
    settings: {
      enable_refresh: 'yes', mount: 'yes', template: 'google',
      union: 'yes', upload: 'yes', upload_from: '/mnt/local/Media',
      vfs_cache: { enabled: 'no', max_age: '504h', size: '50G' },
    },
  }
}

function RemoteCard({ remote, index, onChange, onRemove, availableRemotes = [], mountTemplates = [] }: {
  remote: RcloneRemote; index: number
  onChange: (r: RcloneRemote) => void; onRemove: () => void
  availableRemotes?: string[]
  mountTemplates?: string[]
}) {
  const [open, setOpen] = useState(index === 0)
  const s = remote.settings

  // Split "remoteName:subPath" → { base, sub }
  const colonIdx = remote.remote.indexOf(':')
  const baseName = colonIdx >= 0 ? remote.remote.slice(0, colonIdx) : remote.remote
  const subPath  = colonIdx >= 0 ? remote.remote.slice(colonIdx + 1) : ''

  function setRemoteName(base: string, sub: string) {
    onChange({ ...remote, remote: sub ? `${base}:${sub}` : base })
  }

  function set(patch: Partial<RemoteSettings>) {
    onChange({ ...remote, settings: { ...s, ...patch } })
  }
  function setVfs(patch: Partial<VfsCache>) {
    set({ vfs_cache: { ...s.vfs_cache, ...patch } })
  }

  // Short display label for a template value
  function tplLabel(t: string) {
    return t.startsWith('/opt/mount-templates/')
      ? t.replace('/opt/mount-templates/', '').replace(/\.j2$/, '')
      : t
  }

  // Include current value in dropdown even if not in rclone.conf yet
  const selectOptions = availableRemotes.includes(baseName) || !baseName
    ? availableRemotes
    : [baseName, ...availableRemotes]

  return (
    <div className="border border-border rounded-lg overflow-hidden">
      {/* Card header */}
      <div className="flex items-center gap-2 px-3 py-2.5 bg-muted/10">
        <button type="button" onClick={() => setOpen(!open)}
          className="flex items-center gap-2 flex-1 text-left min-w-0"
        >
          {open ? <ChevronDown className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                : <ChevronRight className="h-3.5 w-3.5 text-muted-foreground shrink-0" />}
          <Cloud className="h-3.5 w-3.5 text-primary/70 shrink-0" />
          <span className="font-mono text-sm font-medium truncate">
            {remote.remote || <span className="text-muted-foreground italic">unnamed</span>}
          </span>
          {s.template && (
            <span className="text-xs text-muted-foreground bg-muted/50 px-1.5 py-0.5 rounded shrink-0 font-mono">
              {tplLabel(s.template)}
            </span>
          )}
        </button>
        <Button size="sm" variant="ghost" className="h-7 w-7 p-0 text-destructive/60 hover:text-destructive shrink-0"
          onClick={onRemove}>
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>

      {open && (
        <div className="p-4 space-y-4 border-t border-border/50">
          <div className="grid grid-cols-2 gap-4">
            {/* Remote name — dropdown + optional :subpath below */}
            <Field label="Remote name" hint="เลือกจาก rclone.conf">
              <div className="space-y-1.5">
                {selectOptions.length > 0 ? (
                  <select
                    value={baseName}
                    onChange={e => setRemoteName(e.target.value, subPath)}
                    className="w-full h-9 rounded-md border border-input bg-background px-3 text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring"
                  >
                    {!baseName && <option value="">— select remote —</option>}
                    {selectOptions.map(r => <option key={r} value={r}>{r}</option>)}
                  </select>
                ) : (
                  <Input value={baseName}
                    onChange={e => setRemoteName(e.target.value, subPath)}
                    placeholder="google" className="font-mono text-sm" />
                )}
                <Input value={subPath}
                  onChange={e => setRemoteName(baseName, e.target.value)}
                  placeholder=":subfolder (optional)"
                  className="font-mono text-xs h-7 text-muted-foreground" />
              </div>
            </Field>

            {/* Mount template */}
            <Field label="Mount template"
              hint={s.template ? tplLabel(s.template) : 'เลือก template'}>
              <select value={s.template} onChange={e => set({ template: e.target.value })}
                className="w-full h-9 rounded-md border border-input bg-background px-3 text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring">
                {s.template && !mountTemplates.includes(s.template) && (
                  <option value={s.template}>{tplLabel(s.template)}</option>
                )}
                {mountTemplates.length > 0
                  ? mountTemplates.map(t => <option key={t} value={t}>{tplLabel(t)}</option>)
                  : ['google', 'dropbox', 'sftp', 'onedrive', 'teldrive', 'custom'].map(t =>
                      <option key={t} value={t}>{t}</option>)
                }
              </select>
            </Field>
          </div>

          <Field label="Upload from path"
            hint="Local path Cloudplow uses as the source for uploads">
            <Input value={s.upload_from} onChange={e => set({ upload_from: e.target.value })}
              placeholder="/mnt/local/Media" className="font-mono text-sm" />
          </Field>

          <div className="flex flex-wrap gap-2">
            <YesNoToggle value={s.mount}           onChange={v => set({ mount: v })}           label="Mount" />
            <YesNoToggle value={s.union}           onChange={v => set({ union: v })}           label="Include in unionfs" />
            <YesNoToggle value={s.upload}          onChange={v => set({ upload: v })}          label="Upload (Cloudplow)" />
            <YesNoToggle value={s.enable_refresh}  onChange={v => set({ enable_refresh: v })}  label="Enable refresh" />
          </div>

          <div className="border border-border/50 rounded-md p-3 space-y-3 bg-muted/5">
            <div className="flex items-center gap-3">
              <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">VFS Cache</span>
              <YesNoToggle value={s.vfs_cache.enabled} onChange={v => setVfs({ enabled: v })} label="Enabled" />
            </div>
            {yes(s.vfs_cache.enabled) && (
              <div className="grid grid-cols-2 gap-3">
                <Field label="Max age" hint="e.g. 504h">
                  <Input value={s.vfs_cache.max_age}
                    onChange={e => setVfs({ max_age: e.target.value })}
                    placeholder="504h" className="font-mono text-sm" />
                </Field>
                <Field label="Max size" hint="Actual usage may exceed this">
                  <Input value={s.vfs_cache.size}
                    onChange={e => setVfs({ size: e.target.value })}
                    placeholder="50G" className="font-mono text-sm" />
                </Field>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

// ── settings.yml ───────────────────────────────────────────────────────────────

interface SettingsYml extends Record<string, unknown> {
  authelia?: { master?: unknown; subdomain?: string }
  downloads?: string
  transcodes?: string
  shell?: string
  rclone?: { enabled?: unknown; version?: string; remotes?: RcloneRemote[] }
}

function SettingsForm() {
  const { data, isLoading } = useConfig('settings')
  const { data: rcloneData } = useRcloneRemotes()
  const { data: templatesData } = useMountTemplates()
  const save = useSaveConfig('settings')
  const install = useInstallApp()
  const qc = useQueryClient()
  const [form, setForm] = useState<SettingsYml>({})
  const [jobId, setJobId] = useState<string | null>(null)

  useEffect(() => { if (data?.data) setForm(data.data as SettingsYml) }, [data])

  if (isLoading) return <p className="text-sm text-muted-foreground p-4">Loading…</p>

  const availableRemotes = Object.keys(rcloneData?.remotes ?? {})
  const mountTemplates = templatesData?.templates ?? []
  const rc = form.rclone ?? {}
  const remotes: RcloneRemote[] = (rc.remotes as RcloneRemote[]) ?? []
  function setRc(patch: object) { setForm(f => ({ ...f, rclone: { ...f.rclone, ...patch } })) }
  function updateRemote(i: number, r: RcloneRemote) {
    const next = [...remotes]; next[i] = r; setRc({ remotes: next })
  }

  function doSave(cb?: () => void) {
    save.mutate(form, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['config', 'settings'] }); cb?.() } })
  }

  return (
    <div className="space-y-4">
      <SaveBar
        onSave={() => doSave()}
        onApply={() => doSave(() => install.mutate({ tag: 'settings' }, { onSuccess: d => setJobId(d.job_id) }))}
        saving={save.isPending} applying={install.isPending}
        saved={save.isSuccess} error={save.error ? String(save.error) : undefined}
      />

      {/* Paths */}
      <Section title="Paths" icon={HardDrive}>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Downloads" hint="Directory for Docker downloads volume">
            <Input value={form.downloads ?? ''} className="font-mono text-sm"
              placeholder="/mnt/unionfs/downloads"
              onChange={e => setForm(f => ({ ...f, downloads: e.target.value }))} />
          </Field>
          <Field label="Transcodes" hint="Directory for temporary transcode files">
            <Input value={form.transcodes ?? ''} className="font-mono text-sm"
              placeholder="/mnt/local/transcodes"
              onChange={e => setForm(f => ({ ...f, transcodes: e.target.value }))} />
          </Field>
        </div>
        <Field label="Shell" hint="System shell" className="w-40">
          <select value={String(form.shell ?? 'bash')}
            onChange={e => setForm(f => ({ ...f, shell: e.target.value }))}
            className="w-full h-9 rounded-md border border-input bg-background px-3 text-sm">
            <option value="bash">bash</option>
            <option value="zsh">zsh</option>
          </select>
        </Field>
      </Section>

      {/* Authelia */}
      <Section title="Authelia" icon={Shield} defaultOpen={false}>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Subdomain" hint="URL where Authelia login page is accessible">
            <Input value={form.authelia?.subdomain ?? ''}
              placeholder="login"
              onChange={e => setForm(f => ({ ...f, authelia: { ...f.authelia, subdomain: e.target.value } }))} />
          </Field>
          <Field label="Master instance"
            hint="Enable if this server hosts the primary Authelia instance">
            <YesNoToggle
              value={form.authelia?.master ?? 'yes'}
              onChange={v => setForm(f => ({ ...f, authelia: { ...f.authelia, master: v } }))}
              label="This is the master Authelia instance" />
          </Field>
        </div>
      </Section>

      {/* Rclone */}
      <Section title="Rclone" icon={Cloud}>
        <div className="flex flex-wrap items-end gap-4">
          <Field label="Version" hint='"latest", "beta", or specific e.g. "1.65"' className="w-40">
            <Input value={String(rc.version ?? 'latest')} className="font-mono text-sm"
              onChange={e => setRc({ version: e.target.value })} />
          </Field>
          <YesNoToggle value={rc.enabled ?? 'yes'}
            onChange={v => setRc({ enabled: v })} label="Enabled" />
        </div>

        <div className="grid grid-cols-1 xl:grid-cols-2 gap-2">
          {remotes.map((r, i) => (
            <RemoteCard key={i} index={i} remote={r}
              onChange={r => updateRemote(i, r)}
              onRemove={() => setRc({ remotes: remotes.filter((_, j) => j !== i) })}
              availableRemotes={availableRemotes}
              mountTemplates={mountTemplates} />
          ))}
        </div>
        <Button size="sm" variant="outline" className="gap-1.5"
          onClick={() => setRc({ remotes: [...remotes, defaultRemote()] })}>
          <Plus className="h-3.5 w-3.5" />Add remote
        </Button>
      </Section>

      <Dialog open={!!jobId} onOpenChange={o => { if (!o) setJobId(null) }}>
        <DialogContent className="max-w-3xl">
          <DialogHeader><DialogTitle>Applying settings</DialogTitle></DialogHeader>
          <LogStream jobId={jobId} />
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ── accounts.yml ───────────────────────────────────────────────────────────────

interface AccountsYml extends Record<string, unknown> {
  apprise?: string
  cloudflare?: { api?: string; email?: string }
  dockerhub?: { token?: string; user?: string }
  user?: { domain?: string; email?: string; name?: string; pass?: string; ssh_key?: string }
}

function AccountsForm() {
  const { data, isLoading } = useConfig('accounts')
  const save = useSaveConfig('accounts')
  const install = useInstallApp()
  const qc = useQueryClient()
  const [form, setForm] = useState<AccountsYml>({})
  const [jobId, setJobId] = useState<string | null>(null)
  const [show, setShow] = useState({ pass: false, cfApi: false, dhToken: false })

  useEffect(() => { if (data?.data) setForm(data.data as AccountsYml) }, [data])

  if (isLoading) return <p className="text-sm text-muted-foreground p-4">Loading…</p>

  const u = form.user ?? {}
  const cf = form.cloudflare ?? {}
  const dh = form.dockerhub ?? {}

  function setUser(p: object) { setForm(f => ({ ...f, user: { ...f.user, ...p } })) }
  function setCf(p: object) { setForm(f => ({ ...f, cloudflare: { ...f.cloudflare, ...p } })) }
  function setDh(p: object) { setForm(f => ({ ...f, dockerhub: { ...f.dockerhub, ...p } })) }

  function doSave(cb?: () => void) {
    save.mutate(form, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['config', 'accounts'] }); cb?.() } })
  }

  function RevealInput({ value, onChange, placeholder, showKey }: {
    value: string; onChange: (v: string) => void; placeholder?: string
    showKey: keyof typeof show
  }) {
    return (
      <div className="relative">
        <Input type={show[showKey] ? 'text' : 'password'} value={value}
          onChange={e => onChange(e.target.value)} placeholder={placeholder}
          className="pr-9 font-mono text-sm" />
        <button type="button" onClick={() => setShow(s => ({ ...s, [showKey]: !s[showKey] }))}
          className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground">
          {show[showKey] ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
        </button>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <SaveBar
        onSave={() => doSave()}
        onApply={() => doSave(() => install.mutate({ tag: 'user' }, { onSuccess: d => setJobId(d.job_id) }))}
        saving={save.isPending} applying={install.isPending}
        saved={save.isSuccess} error={save.error ? String(save.error) : undefined}
        applyLabel="Save & Apply (user)"
      />

      {/* User */}
      <Section title="User account" icon={User}>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Username" hint="Cannot be root">
            <Input value={u.name ?? ''} placeholder="seed"
              onChange={e => setUser({ name: e.target.value })} />
          </Field>
          <Field label="Password" hint="Min 12 characters, no special characters">
            <RevealInput value={u.pass ?? ''} onChange={v => setUser({ pass: v })}
              placeholder="password1234" showKey="pass" />
          </Field>
          <Field label="Domain" hint="Your server domain">
            <Input value={u.domain ?? ''} placeholder="domain.tld"
              onChange={e => setUser({ domain: e.target.value })} />
          </Field>
          <Field label="Email" hint="Used for Let's Encrypt SSL certificates">
            <Input type="email" value={u.email ?? ''} placeholder="your@email.com"
              onChange={e => setUser({ email: e.target.value })} />
          </Field>
        </div>
        <Field label="SSH public key"
          hint="Optional. SSH key or GitHub URL e.g. https://github.com/username.keys">
          <Input value={u.ssh_key ?? ''} className="font-mono text-xs"
            placeholder="ssh-ed25519 AAAA... or https://github.com/username.keys"
            onChange={e => setUser({ ssh_key: e.target.value })} />
        </Field>
        <Field label="Apprise notifications URL"
          hint='Optional. e.g. "discord://webhook-id/token". Leave blank to disable.'>
          <Input value={form.apprise ?? ''} className="font-mono text-xs"
            placeholder="discord://..."
            onChange={e => setForm(f => ({ ...f, apprise: e.target.value }))} />
        </Field>
      </Section>

      {/* Cloudflare */}
      <Section title="Cloudflare" icon={Cloud}>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Cloudflare account email">
            <Input type="email" value={cf.email ?? ''} placeholder="me@cloudflare.com"
              onChange={e => setCf({ email: e.target.value })} />
          </Field>
          <Field label="Global API key"
            hint='Found in Cloudflare → My Profile → API Tokens → "Global API Key"'>
            <RevealInput value={cf.api ?? ''} onChange={v => setCf({ api: v })}
              placeholder="••••••••••••••••••••••••••••••••••••••••" showKey="cfApi" />
          </Field>
        </div>
      </Section>

      {/* Docker Hub */}
      <Section title="Docker Hub" icon={Server} defaultOpen={false}>
        <p className="text-xs text-muted-foreground">
          Optional — increases image pull limit from 100 → 200 requests per 6 hours.
        </p>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Username">
            <Input value={dh.user ?? ''} onChange={e => setDh({ user: e.target.value })} />
          </Field>
          <Field label="Personal access token"
            hint='Generate in Docker Hub → Account Settings → Security'>
            <RevealInput value={dh.token ?? ''} onChange={v => setDh({ token: v })}
              placeholder="dckr_pat_..." showKey="dhToken" />
          </Field>
        </div>
      </Section>

      <Dialog open={!!jobId} onOpenChange={o => { if (!o) setJobId(null) }}>
        <DialogContent className="max-w-3xl">
          <DialogHeader><DialogTitle>Applying accounts</DialogTitle></DialogHeader>
          <LogStream jobId={jobId} />
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ── adv_settings.yml ───────────────────────────────────────────────────────────

interface AdvSettingsYml extends Record<string, unknown> {
  dns?: { ipv4?: unknown; ipv6?: unknown; proxied?: unknown }
  docker?: { json_driver?: unknown }
  gpu?: { intel?: unknown }
  mounts?: { ipv4_only?: unknown }
  system?: { timezone?: string }
  traefik?: {
    cert?: { http_validation?: unknown; zerossl?: unknown }
    error_pages?: unknown
    hsts?: unknown
    metrics?: unknown
    provider?: string
    subdomains?: { dash?: string; metrics?: string }
  }
}

function AdvSettingsForm() {
  const { data, isLoading } = useConfig('adv_settings')
  const save = useSaveConfig('adv_settings')
  const install = useInstallApp()
  const qc = useQueryClient()
  const [form, setForm] = useState<AdvSettingsYml>({})
  const [jobId, setJobId] = useState<string | null>(null)

  useEffect(() => { if (data?.data) setForm(data.data as AdvSettingsYml) }, [data])

  if (isLoading) return <p className="text-sm text-muted-foreground p-4">Loading…</p>

  const dns  = form.dns     ?? {}
  const tr   = form.traefik ?? {}
  const cert = tr.cert      ?? {}
  const sub  = tr.subdomains ?? {}

  function setDns(p: object) { setForm(f => ({ ...f, dns: { ...f.dns, ...p } })) }
  function setTr(p: object)  { setForm(f => ({ ...f, traefik: { ...f.traefik, ...p } })) }
  function setCert(p: object){ setTr({ cert: { ...cert, ...p } }) }
  function setSub(p: object) { setTr({ subdomains: { ...sub, ...p } }) }

  function doSave(cb?: () => void) {
    save.mutate(form, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['config', 'adv_settings'] }); cb?.() } })
  }

  return (
    <div className="space-y-4">
      <SaveBar
        onSave={() => doSave()}
        onApply={() => doSave(() => install.mutate({ tag: 'settings' }, { onSuccess: d => setJobId(d.job_id) }))}
        saving={save.isPending} applying={install.isPending}
        saved={save.isSuccess} error={save.error ? String(save.error) : undefined}
      />

      {/* DNS */}
      <Section title="DNS" icon={Cloud}>
        <p className="text-xs text-muted-foreground">
          Control which DNS record types Saltbox manages via Cloudflare.
        </p>
        <div className="flex flex-wrap gap-2">
          <YesNoToggle value={dns.ipv4}    onChange={v => setDns({ ipv4: v })}    label="IPv4 (A records)" />
          <YesNoToggle value={dns.ipv6}    onChange={v => setDns({ ipv6: v })}    label="IPv6 (AAAA records)" />
          <YesNoToggle value={dns.proxied} onChange={v => setDns({ proxied: v })} label="Cloudflare proxy" />
        </div>
      </Section>

      {/* System */}
      <Section title="System" icon={Settings2}>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Timezone"
            hint='"auto" for geolocation detection, or a tz database name e.g. "Asia/Bangkok"'>
            <Input value={form.system?.timezone ?? 'auto'}
              placeholder="auto"
              onChange={e => setForm(f => ({ ...f, system: { ...f.system, timezone: e.target.value } }))} />
          </Field>
        </div>
        <div className="flex flex-wrap gap-2">
          <YesNoToggle value={form.gpu?.intel}
            onChange={v => setForm(f => ({ ...f, gpu: { ...f.gpu, intel: v } }))}
            label="Intel GPU tasks" />
          <YesNoToggle value={form.docker?.json_driver}
            onChange={v => setForm(f => ({ ...f, docker: { ...f.docker, json_driver: v } }))}
            label="Docker json-file logging driver" />
          <YesNoToggle value={form.mounts?.ipv4_only}
            onChange={v => setForm(f => ({ ...f, mounts: { ...f.mounts, ipv4_only: v } }))}
            label="Rclone IPv4-only mounts" />
        </div>
      </Section>

      {/* Traefik */}
      <Section title="Traefik" icon={Shield}>
        <div className="grid grid-cols-2 gap-4">
          <Field label="DNS provider"
            hint="Provider for DNS-01 certificate validation. e.g. cloudflare, cloudns">
            <Input value={String(tr.provider ?? 'cloudflare')}
              onChange={e => setTr({ provider: e.target.value })} />
          </Field>
        </div>

        <div className="space-y-1">
          <Label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Subdomains</Label>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Dashboard subdomain">
              <Input value={String(sub.dash ?? 'dash')}
                onChange={e => setSub({ dash: e.target.value })} />
            </Field>
            <Field label="Metrics subdomain">
              <Input value={String(sub.metrics ?? 'metrics')}
                onChange={e => setSub({ metrics: e.target.value })} />
            </Field>
          </div>
        </div>

        <div className="space-y-1">
          <Label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Features</Label>
          <div className="flex flex-wrap gap-2">
            <YesNoToggle value={tr.hsts}        onChange={v => setTr({ hsts: v })}        label="HSTS" />
            <YesNoToggle value={tr.metrics}     onChange={v => setTr({ metrics: v })}     label="Prometheus metrics" />
            <YesNoToggle value={tr.error_pages} onChange={v => setTr({ error_pages: v })} label="Custom error pages" />
          </div>
        </div>

        <div className="space-y-1">
          <Label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Certificate</Label>
          <div className="flex flex-wrap gap-2">
            <YesNoToggle value={cert.zerossl}         onChange={v => setCert({ zerossl: v })}         label="Use ZeroSSL (instead of Let's Encrypt)" />
            <YesNoToggle value={cert.http_validation} onChange={v => setCert({ http_validation: v })} label="HTTP-01 validation" />
          </div>
        </div>
      </Section>

      <Dialog open={!!jobId} onOpenChange={o => { if (!o) setJobId(null) }}>
        <DialogContent className="max-w-3xl">
          <DialogHeader><DialogTitle>Applying adv_settings</DialogTitle></DialogHeader>
          <LogStream jobId={jobId} />
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ── rclone.conf ────────────────────────────────────────────────────────────────

const SENSITIVE = new Set(['access_token', 'token', 'client_secret', 'client_id',
  'password', 'pass', 'bearer_token', 'service_account_credentials'])

const RCLONE_TYPES = [
  'drive', 'onedrive', 's3', 'b2', 'sftp', 'ftp', 'dropbox', 'box',
  'mega', 'pcloud', 'azureblob', 'swift', 'webdav', 'http', 'union',
  'crypt', 'teldrive', 'local', 'alias', 'chunker', 'compress', 'other',
]

function RcloneRemoteCard({
  name, fields, linked,
  onChangeName, onChangeFields, onRemove,
}: {
  name: string
  fields: Record<string, string>
  linked: boolean
  onChangeName: (n: string) => void
  onChangeFields: (f: Record<string, string>) => void
  onRemove: () => void
}) {
  const [open, setOpen] = useState(false)
  const [revealed, setRevealed] = useState<Set<string>>(new Set())
  const [addKey, setAddKey] = useState('')
  const [editingName, setEditingName] = useState(false)
  const [draftName, setDraftName] = useState(name)
  const nameRef = useRef<HTMLInputElement>(null)

  const type = fields.type ?? ''

  function toggleReveal(k: string) {
    setRevealed(prev => {
      const next = new Set(prev)
      next.has(k) ? next.delete(k) : next.add(k)
      return next
    })
  }
  function setField(k: string, v: string) { onChangeFields({ ...fields, [k]: v }) }
  function removeField(k: string) {
    const next = { ...fields }; delete next[k]; onChangeFields(next)
  }
  function addField() {
    const k = addKey.trim()
    if (!k || k in fields) return
    onChangeFields({ ...fields, [k]: '' })
    setAddKey('')
  }

  return (
    <div className={cn(
      'rounded-lg border transition-colors overflow-hidden',
      linked ? 'border-primary/40' : 'border-border',
    )}>
      {/* Header */}
      <div className="flex items-center gap-2 px-3 py-2.5 bg-card">
        <button type="button" onClick={() => setOpen(o => !o)}
          className="flex items-center gap-2 flex-1 min-w-0 text-left">
          {open
            ? <ChevronDown className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
            : <ChevronRight className="h-3.5 w-3.5 text-muted-foreground shrink-0" />}

          {editingName ? (
            <input ref={nameRef} value={draftName}
              className="font-mono text-sm bg-background border border-primary rounded px-1.5 py-0.5 flex-1 min-w-0 outline-none"
              onChange={e => setDraftName(e.target.value)}
              onBlur={() => { onChangeName(draftName); setEditingName(false) }}
              onKeyDown={e => { if (e.key === 'Enter') { onChangeName(draftName); setEditingName(false) } }}
              onClick={e => e.stopPropagation()} />
          ) : (
            <span className="font-mono text-sm font-medium truncate"
              onDoubleClick={() => { setEditingName(true); setTimeout(() => nameRef.current?.select(), 10) }}>
              {name}
            </span>
          )}

          {type && (
            <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground shrink-0">
              {type}
            </span>
          )}
          {linked && (
            <span className="flex items-center gap-0.5 text-xs text-primary shrink-0">
              <Link className="h-3 w-3" />linked
            </span>
          )}
        </button>
        <button type="button" onClick={onRemove}
          className="p-1 rounded text-muted-foreground/50 hover:text-destructive hover:bg-destructive/10 transition-colors shrink-0">
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </div>

      {/* Body */}
      {open && (
        <div className="border-t border-border p-3 space-y-1.5 bg-background/40">
          {Object.entries(fields).map(([k, v]) => {
            const isSensitive = SENSITIVE.has(k)
            const isRevealed = revealed.has(k)
            return (
              <div key={k} className="flex items-center gap-2">
                <span className="w-40 shrink-0 text-xs font-mono text-muted-foreground truncate">{k}</span>
                <div className="relative flex-1 min-w-0">
                  <input
                    type={isSensitive && !isRevealed ? 'password' : 'text'}
                    value={v}
                    onChange={e => setField(k, e.target.value)}
                    className={cn(
                      'w-full h-7 px-2 rounded border border-input bg-background text-sm font-mono',
                      'focus:outline-none focus:ring-1 focus:ring-ring',
                      isSensitive ? 'pr-8' : '',
                    )} />
                  {isSensitive && (
                    <button type="button" onClick={() => toggleReveal(k)}
                      className="absolute right-1.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground">
                      {isRevealed ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                    </button>
                  )}
                </div>
                <button type="button" onClick={() => removeField(k)}
                  className="p-1 text-muted-foreground/40 hover:text-destructive transition-colors shrink-0">
                  <X className="h-3 w-3" />
                </button>
              </div>
            )
          })}

          {/* Add field */}
          <div className="flex items-center gap-2 pt-1">
            <input value={addKey} onChange={e => setAddKey(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && addField()}
              placeholder="new key…"
              className="w-40 shrink-0 h-7 px-2 rounded border border-dashed border-muted-foreground/30 bg-background text-xs font-mono focus:outline-none focus:border-primary" />
            <button type="button" onClick={addField}
              className="h-7 px-2 text-xs text-primary hover:bg-primary/10 rounded transition-colors">
              + Add field
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

function AddRemoteDialog({ open, onClose, onAdd }: {
  open: boolean
  onClose: () => void
  onAdd: (name: string, type: string) => void
}) {
  const [name, setName] = useState('')
  const [type, setType] = useState('drive')

  function submit() {
    if (!name.trim()) return
    onAdd(name.trim(), type)
    setName(''); setType('drive'); onClose()
  }

  return (
    <Dialog open={open} onOpenChange={o => { if (!o) onClose() }}>
      <DialogContent className="max-w-sm">
        <DialogHeader><DialogTitle>Add rclone remote</DialogTitle></DialogHeader>
        <div className="space-y-4 pt-2">
          <Field label="Remote name" hint="Must match name in rclone.conf (e.g. gdrive, mybox)">
            <Input value={name} onChange={e => setName(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && submit()}
              placeholder="gdrive" className="font-mono" autoFocus />
          </Field>
          <Field label="Type">
            <select value={type} onChange={e => setType(e.target.value)}
              className="w-full h-9 rounded-md border border-input bg-background px-3 text-sm">
              {RCLONE_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
            </select>
          </Field>
          <div className="flex gap-2 justify-end">
            <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
            <Button size="sm" onClick={submit} disabled={!name.trim()}>Add</Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function RcloneConfTab() {
  const { data, isLoading } = useRcloneRemotes()
  const { data: settingsData } = useConfig('settings')
  const save = useSaveRcloneRemotes()
  const qc = useQueryClient()

  const [remotes, setRemotes] = useState<RcloneRemotes>({})
  const [order, setOrder] = useState<string[]>([])
  const [addOpen, setAddOpen] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    if (data?.remotes) {
      setRemotes(data.remotes)
      setOrder(Object.keys(data.remotes))
    }
  }, [data])

  const linkedNames = new Set<string>(
    ((settingsData?.data as Record<string, unknown>)?.rclone as Record<string, unknown> | undefined)
      ?.remotes
      ? ((settingsData?.data as Record<string, unknown>).rclone as { remotes?: { remote?: string }[] })
          .remotes?.map(r => r.remote ?? '') ?? []
      : []
  )

  function handleChangeName(oldName: string, newName: string) {
    if (oldName === newName || !newName) return
    const next: RcloneRemotes = {}
    const nextOrder = order.map(k => k === oldName ? newName : k)
    for (const k of nextOrder) {
      next[k] = k === newName ? remotes[oldName] : remotes[k]
    }
    setRemotes(next); setOrder(nextOrder)
  }

  function handleAdd(name: string, type: string) {
    setRemotes(prev => ({ ...prev, [name]: { type } }))
    setOrder(prev => [...prev, name])
  }

  function handleRemove(name: string) {
    setRemotes(prev => { const n = { ...prev }; delete n[name]; return n })
    setOrder(prev => prev.filter(k => k !== name))
  }

  function handleSave() {
    // Rebuild in current order
    const ordered: RcloneRemotes = {}
    for (const k of order) if (remotes[k]) ordered[k] = remotes[k]
    save.mutate(ordered, {
      onSuccess: () => {
        qc.invalidateQueries({ queryKey: ['rclone-remotes'] })
        setSaved(true); setTimeout(() => setSaved(false), 2500)
      },
    })
  }

  if (isLoading) return <p className="text-sm text-muted-foreground p-4">Loading…</p>

  return (
    <div className="space-y-4">
      {/* Path info */}
      {data?.path && (
        <div className="flex items-center gap-2 text-xs text-muted-foreground bg-muted/30 rounded-md px-3 py-2">
          <FolderOpen className="h-3.5 w-3.5 shrink-0" />
          <span className="font-mono truncate">{data.path}</span>
        </div>
      )}

      {/* Sticky save bar */}
      <div className="sticky top-[42px] z-20 -mx-6 px-6 py-2 mb-4 bg-background/95 backdrop-blur border-b border-border flex items-center gap-2 flex-wrap">
        <Button size="sm" onClick={handleSave} disabled={save.isPending}>
          <Save className="h-3.5 w-3.5 mr-1.5" />Save rclone.conf
        </Button>
        <Button size="sm" variant="outline" onClick={() => setAddOpen(true)}>
          <Plus className="h-3.5 w-3.5 mr-1.5" />Add remote
        </Button>
        {saved && <span className="text-xs text-green-600">Saved ✓</span>}
        {save.error && <span className="text-xs text-destructive">{String(save.error)}</span>}
      </div>

      {/* Remotes */}
      {order.length === 0 ? (
        <div className="text-center py-12 text-muted-foreground text-sm border border-dashed rounded-lg">
          No remotes — click <strong>Add remote</strong> to create one.
        </div>
      ) : (
        <div className="grid grid-cols-1 xl:grid-cols-2 gap-2">
          {order.filter(k => remotes[k]).map(name => (
            <RcloneRemoteCard
              key={name}
              name={name}
              fields={remotes[name]}
              linked={linkedNames.has(name)}
              onChangeName={newName => handleChangeName(name, newName)}
              onChangeFields={f => setRemotes(prev => ({ ...prev, [name]: f }))}
              onRemove={() => handleRemove(name)}
            />
          ))}
        </div>
      )}

      <AddRemoteDialog open={addOpen} onClose={() => setAddOpen(false)} onAdd={handleAdd} />

      {linkedNames.size > 0 && (
        <p className="text-xs text-muted-foreground flex items-center gap-1">
          <Link className="h-3 w-3" />
          {[...linkedNames].join(', ')} {linkedNames.size === 1 ? 'is' : 'are'} linked in settings.yml
        </p>
      )}
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export function ConfigPanel() {
  return (
    <div>
      <Tabs defaultValue="settings">
        {/* Sticky tab switcher */}
        <div className="sticky top-0 z-30 -mx-6 px-6 py-2 bg-background/95 backdrop-blur border-b border-border mb-5">
          <TabsList>
            <TabsTrigger value="settings">settings.yml</TabsTrigger>
            <TabsTrigger value="accounts">accounts.yml</TabsTrigger>
            <TabsTrigger value="adv_settings">adv_settings.yml</TabsTrigger>
            <TabsTrigger value="rclone">rclone.conf</TabsTrigger>
          </TabsList>
        </div>
        <TabsContent value="settings"><SettingsForm /></TabsContent>
        <TabsContent value="accounts"><AccountsForm /></TabsContent>
        <TabsContent value="adv_settings"><AdvSettingsForm /></TabsContent>
        <TabsContent value="rclone"><RcloneConfTab /></TabsContent>
      </Tabs>
    </div>
  )
}
