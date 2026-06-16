import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  usePreviewRole, useCommitRole, useModRoles, useRemoveRole, useInstallApp,
  type RoleSpec, type ModRole, type AppInfo,
} from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { LogStream } from '@/components/LogStream'
import { RoleConfigModal } from '@/components/RoleConfigModal'
import { ListRow } from '@/components/ListRow'
import { cn } from '@/lib/cn'
import {
  Plus, Trash2, ChevronRight, ChevronLeft, Eye, Rocket, RefreshCw, Settings2,
  Package, AlertTriangle, Loader2,
} from 'lucide-react'

const EMPTY_SPEC: RoleSpec = {
  name: '', docker_image: '', docker_tag: 'latest',
  port: '', subdomain: '', volumes: [], env_vars: [], auth_mode: 'sso',
}

function titleCase(s: string) {
  return s.replace(/[-_]/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}

const asApp = (r: ModRole): AppInfo => ({
  tag: `mod-${r.name}`, name: titleCase(r.name), repo: 'mod',
  installed: r.registered, kind: 'container',
})

// Custom saltbox_mod roles — listed as cards (matching the App Manager catalog),
// with a built-in create wizard. Embedded as the "Custom roles" App Manager tab.
export function CustomRoles() {
  const qc = useQueryClient()
  const { data, isLoading } = useModRoles()
  const install = useInstallApp()
  const remove = useRemoveRole()
  const [configRole, setConfigRole] = useState<ModRole | null>(null)
  const [confirmRole, setConfirmRole] = useState<ModRole | null>(null)
  const [creating, setCreating] = useState(false)
  const [jobId, setJobId] = useState<string | null>(null)
  const [jobTitle, setJobTitle] = useState('')

  const roles = data?.roles ?? []

  function reinstall(r: ModRole) {
    setJobTitle(`Installing mod-${r.name}`)
    install.mutate({ tag: `mod-${r.name}`, action: 'reinstall' }, { onSuccess: (d) => setJobId(d.job_id) })
  }
  async function doRemove(r: ModRole) {
    await remove.mutateAsync({ role: r.name })
    setConfirmRole(null)
    qc.invalidateQueries({ queryKey: ['mod-roles'] })
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <p className="text-sm text-muted-foreground">
          Custom roles under <code className="text-xs">saltbox_mod</code> — they persist across{' '}
          <code className="text-xs">sb update</code>. Edit config &amp; files, reinstall, or remove.
          (sb-ui's own role is hidden.)
        </p>
        <Button size="sm" className="shrink-0 gap-1.5" onClick={() => setCreating(true)}>
          <Plus className="h-3.5 w-3.5" />Create role
        </Button>
      </div>

      {isLoading && <p className="text-sm text-muted-foreground">Loading roles…</p>}
      {!isLoading && roles.length === 0 && (
        <div className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
          No custom roles yet. Click <span className="text-foreground font-medium">Create role</span> to add one.
        </div>
      )}

      <div className="space-y-2">
        {roles.map((r) => (
          <ListRow
            key={r.name}
            icon={<Package />}
            title={<span className="text-sm font-medium text-foreground truncate">{titleCase(r.name)}</span>}
            subtitle={`mod-${r.name}`}
            trailing={r.registered
              ? <Badge variant="success">registered</Badge>
              : <Badge variant="secondary">unregistered</Badge>}
            actions={<>
              <Button size="sm" variant="outline" onClick={() => setConfigRole(r)}>
                <Settings2 className="h-3.5 w-3.5 mr-1.5" />Configure
              </Button>
              <Button size="sm" variant="outline" onClick={() => reinstall(r)} disabled={install.isPending}>
                <RefreshCw className="h-3.5 w-3.5 mr-1.5" />Reinstall
              </Button>
              <Button size="icon" variant="ghost" className="h-8 w-8" onClick={() => setConfirmRole(r)}>
                <Trash2 className="h-4 w-4 text-destructive" />
              </Button>
            </>}
          />
        ))}
      </div>

      {/* Configure (full inventory + file editor, repo=mod) */}
      <RoleConfigModal app={configRole ? asApp(configRole) : null} onClose={() => setConfigRole(null)} />

      {/* Create wizard */}
      <Dialog open={creating} onOpenChange={(o) => { if (!o) setCreating(false) }}>
        <DialogContent className="max-w-2xl">
          <DialogHeader><DialogTitle>Create custom role</DialogTitle></DialogHeader>
          <CreateWizard
            onJob={(id, name) => { setCreating(false); setJobTitle(`Installing mod-${name}`); setJobId(id) }}
          />
        </DialogContent>
      </Dialog>

      {/* Reinstall / create log */}
      <Dialog open={!!jobId} onOpenChange={(o) => { if (!o) { setJobId(null); qc.invalidateQueries({ queryKey: ['mod-roles'] }) } }}>
        <DialogContent className="max-w-3xl">
          <DialogHeader><DialogTitle>{jobTitle}</DialogTitle></DialogHeader>
          <LogStream jobId={jobId} />
        </DialogContent>
      </Dialog>

      {/* Remove confirm */}
      <Dialog open={!!confirmRole} onOpenChange={(o) => { if (!o) setConfirmRole(null) }}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertTriangle className="h-4 w-4 text-destructive" />Remove role
            </DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-foreground">
            This stops &amp; removes the <code className="text-xs">{confirmRole?.name}</code> container,
            deletes its role folder under saltbox_mod, and unregisters it. App data is not touched.
          </p>
          <div className="flex justify-end gap-2 mt-2">
            <Button size="sm" variant="outline" onClick={() => setConfirmRole(null)}>Cancel</Button>
            <Button size="sm" variant="destructive" onClick={() => confirmRole && doRemove(confirmRole)} disabled={remove.isPending}>
              {remove.isPending ? <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5 mr-1.5" />}
              Remove
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ── Create wizard ────────────────────────────────────────────────────────────

const STEPS = ['Basic', 'Network', 'Storage', 'Env Vars', 'Preview']

function CreateWizard({ onJob }: { onJob: (jobId: string, name: string) => void }) {
  const [step, setStep] = useState(0)
  const [spec, setSpec] = useState<RoleSpec>(EMPTY_SPEC)
  const preview = usePreviewRole()
  const commit = useCommitRole()

  function upd<K extends keyof RoleSpec>(key: K, val: RoleSpec[K]) {
    setSpec((s) => ({ ...s, [key]: val }))
  }
  function goPreview() { setStep(4); preview.mutate(spec) }
  function handleCommit() {
    commit.mutate(spec, { onSuccess: (d) => onJob(d.job_id, spec.name) })
  }

  return (
    <div>
      <p className="text-xs text-muted-foreground mb-4">
        Generated into <code>saltbox_mod</code> and installed as{' '}
        <code>mod-{spec.name || 'name'}</code>.
      </p>

      <div className="flex gap-1 mb-4">
        {STEPS.map((s, i) => (
          <div key={s} className={cn('flex-1 h-1 rounded-full transition-colors', i <= step ? 'bg-primary' : 'bg-border')} />
        ))}
      </div>
      <p className="text-sm font-medium text-foreground mb-4">Step {step + 1}: {STEPS[step]}</p>

      {step === 0 && (
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label>Role name (lowercase, no spaces)</Label>
            <Input placeholder="myapp" value={spec.name} onChange={(e) => upd('name', e.target.value.toLowerCase().replace(/\s/g, '-'))} />
          </div>
          <div className="space-y-1.5">
            <Label>Docker image</Label>
            <Input placeholder="linuxserver/myapp" value={spec.docker_image} onChange={(e) => upd('docker_image', e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label>Docker tag</Label>
            <Input placeholder="latest" value={spec.docker_tag} onChange={(e) => upd('docker_tag', e.target.value)} />
          </div>
        </div>
      )}

      {step === 1 && (
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label>Subdomain</Label>
            <Input placeholder={spec.name || 'myapp'} value={spec.subdomain} onChange={(e) => upd('subdomain', e.target.value)} />
            <p className="text-xs text-muted-foreground">Leave empty to use role name</p>
          </div>
          <div className="space-y-1.5">
            <Label>Container port</Label>
            <Input placeholder="8080" value={spec.port} onChange={(e) => upd('port', e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label>Auth mode</Label>
            <div className="flex gap-2">
              {(['sso', 'bypass', 'none'] as const).map((m) => (
                <Button key={m} size="sm" variant={spec.auth_mode === m ? 'default' : 'outline'} onClick={() => upd('auth_mode', m)}>
                  {m === 'sso' ? 'Authelia SSO' : m === 'bypass' ? 'Bypass auth' : 'No middleware'}
                </Button>
              ))}
            </div>
          </div>
        </div>
      )}

      {step === 2 && (
        <div className="space-y-3">
          {spec.volumes.map((v, i) => (
            <div key={i} className="flex gap-2 items-center">
              <Input placeholder="host path" value={v.host} onChange={(e) => {
                const vols = [...spec.volumes]; vols[i] = { ...v, host: e.target.value }; upd('volumes', vols)
              }} />
              <span className="text-muted-foreground shrink-0">→</span>
              <Input placeholder="container path" value={v.container} onChange={(e) => {
                const vols = [...spec.volumes]; vols[i] = { ...v, container: e.target.value }; upd('volumes', vols)
              }} />
              <Button size="icon" variant="ghost" onClick={() => upd('volumes', spec.volumes.filter((_, j) => j !== i))}>
                <Trash2 className="h-4 w-4 text-destructive" />
              </Button>
            </div>
          ))}
          <Button size="sm" variant="outline" onClick={() => upd('volumes', [...spec.volumes, { host: '', container: '' }])}>
            <Plus className="h-3.5 w-3.5 mr-1.5" />Add volume
          </Button>
        </div>
      )}

      {step === 3 && (
        <div className="space-y-3">
          {spec.env_vars.map((ev, i) => (
            <div key={i} className="flex gap-2 items-center">
              <Input placeholder="KEY" value={ev.key} onChange={(e) => {
                const envs = [...spec.env_vars]; envs[i] = { ...ev, key: e.target.value }; upd('env_vars', envs)
              }} />
              <span className="text-muted-foreground shrink-0">=</span>
              <Input placeholder="VALUE" value={ev.value} onChange={(e) => {
                const envs = [...spec.env_vars]; envs[i] = { ...ev, value: e.target.value }; upd('env_vars', envs)
              }} />
              <Button size="icon" variant="ghost" onClick={() => upd('env_vars', spec.env_vars.filter((_, j) => j !== i))}>
                <Trash2 className="h-4 w-4 text-destructive" />
              </Button>
            </div>
          ))}
          <Button size="sm" variant="outline" onClick={() => upd('env_vars', [...spec.env_vars, { key: '', value: '' }])}>
            <Plus className="h-3.5 w-3.5 mr-1.5" />Add variable
          </Button>
        </div>
      )}

      {step === 4 && (
        <div className="space-y-3">
          {preview.isPending && <p className="text-sm text-muted-foreground">Generating preview…</p>}
          {preview.data && (
            <div className="space-y-3">
              <div>
                <p className="text-xs font-medium text-muted-foreground mb-1">defaults/main.yml</p>
                <pre className="bg-background border border-border rounded-md p-3 text-xs overflow-auto max-h-56 text-foreground">{preview.data.defaults}</pre>
              </div>
              <div>
                <p className="text-xs font-medium text-muted-foreground mb-1">tasks/main.yml</p>
                <pre className="bg-background border border-border rounded-md p-3 text-xs overflow-auto max-h-36 text-foreground">{preview.data.tasks}</pre>
              </div>
            </div>
          )}
        </div>
      )}

      <div className="flex justify-between mt-6">
        <Button variant="outline" onClick={() => setStep((s) => s - 1)} disabled={step === 0}>
          <ChevronLeft className="h-4 w-4 mr-1" />Back
        </Button>
        {step < 3 && (
          <Button onClick={() => setStep((s) => s + 1)} disabled={step === 0 && !spec.name}>
            Next<ChevronRight className="h-4 w-4 ml-1" />
          </Button>
        )}
        {step === 3 && (
          <Button onClick={goPreview}><Eye className="h-4 w-4 mr-1.5" />Preview</Button>
        )}
        {step === 4 && (
          <Button onClick={handleCommit} disabled={commit.isPending}>
            <Rocket className="h-4 w-4 mr-1.5" />Commit &amp; Install
          </Button>
        )}
      </div>
    </div>
  )
}
