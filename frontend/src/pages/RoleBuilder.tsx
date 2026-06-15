import { useState } from 'react'
import { usePreviewRole, useCommitRole, type RoleSpec } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { LogStream } from '@/components/LogStream'
import { Plus, Trash2, ChevronRight, ChevronLeft, Eye, Rocket } from 'lucide-react'

const EMPTY_SPEC: RoleSpec = {
  name: '', docker_image: '', docker_tag: 'latest',
  port: '', subdomain: '',
  volumes: [], env_vars: [],
  auth_mode: 'sso',
}

const STEPS = ['Basic', 'Network', 'Storage', 'Env Vars', 'Preview']

export function RoleBuilder() {
  const [step, setStep] = useState(0)
  const [spec, setSpec] = useState<RoleSpec>(EMPTY_SPEC)
  const preview = usePreviewRole()
  const commit = useCommitRole()
  const [jobId, setJobId] = useState<string | null>(null)

  function upd<K extends keyof RoleSpec>(key: K, val: RoleSpec[K]) {
    setSpec((s) => ({ ...s, [key]: val }))
  }

  function goPreview() {
    setStep(4)
    preview.mutate(spec)
  }

  function handleCommit() {
    commit.mutate(spec, { onSuccess: (d) => setJobId(d.job_id) })
  }

  return (
    <div className="p-6">
      <h1 className="text-xl font-semibold text-foreground mb-5">Role Builder</h1>

      {/* Step indicator */}
      <div className="flex gap-1 mb-6">
        {STEPS.map((s, i) => (
          <div key={s} className={`flex-1 h-1 rounded-full transition-colors ${i <= step ? 'bg-primary' : 'bg-border'}`} />
        ))}
      </div>
      <p className="text-sm font-medium text-foreground mb-4">Step {step + 1}: {STEPS[step]}</p>

      {/* Step 0: Basic */}
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

      {/* Step 1: Network */}
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

      {/* Step 2: Storage */}
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

      {/* Step 3: Env Vars */}
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

      {/* Step 4: Preview */}
      {step === 4 && (
        <div className="space-y-3">
          {preview.isPending && <p className="text-sm text-muted-foreground">Generating preview…</p>}
          {preview.data && (
            <div className="space-y-3">
              <div>
                <p className="text-xs font-medium text-muted-foreground mb-1">defaults/main.yml</p>
                <pre className="bg-background border border-border rounded-md p-3 text-xs overflow-auto max-h-64 text-foreground">{preview.data.defaults}</pre>
              </div>
              <div>
                <p className="text-xs font-medium text-muted-foreground mb-1">tasks/main.yml</p>
                <pre className="bg-background border border-border rounded-md p-3 text-xs overflow-auto max-h-40 text-foreground">{preview.data.tasks}</pre>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Navigation */}
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
            <Rocket className="h-4 w-4 mr-1.5" />Commit & Install
          </Button>
        )}
      </div>

      <Dialog open={!!jobId} onOpenChange={(o) => { if (!o) setJobId(null) }}>
        <DialogContent className="max-w-3xl">
          <DialogHeader><DialogTitle>Installing sandbox-{spec.name}</DialogTitle></DialogHeader>
          <LogStream jobId={jobId} />
        </DialogContent>
      </Dialog>
    </div>
  )
}
