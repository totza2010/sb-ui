/**
 * Install Types — customise what each Saltbox profile installs.
 * Profiles (saltbox/mediabox/feederbox) are role lists; media_server /
 * download_clients / download_indexers expand from *_enabled lists. All are
 * inventory variables — edits are saved to host_vars/localhost.yml. Installing
 * reuses the `sb install <profile>` job flow.
 */
import { useEffect, useState } from 'react'
import {
  useInstallTypes, useSaveInstallTypes, useInstallApp,
  type InstallTypes as ITypes,
} from '@/lib/api'
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { LogStream } from '@/components/LogStream'
import { useQueryClient } from '@tanstack/react-query'
import {
  Save, Loader2, Check, X, Plus, RotateCcw, Download, Server, ArrowDownToLine,
} from 'lucide-react'
import { cn } from '@/lib/cn'

const ENABLED_LABELS: Record<string, string> = {
  media_servers_enabled: 'Media servers',
  download_clients_enabled: 'Download clients',
  download_indexers_enabled: 'Download indexers',
}

function sameList(a: string[], b: string[]) {
  return a.length === b.length && a.every((x, i) => x === b[i])
}

export function InstallTypes() {
  const { data, isLoading } = useInstallTypes()
  const save = useSaveInstallTypes()
  const install = useInstallApp()
  const qc = useQueryClient()

  const [it, setIt] = useState<ITypes | null>(null)
  const [saved, setSaved] = useState(false)
  const [jobId, setJobId] = useState<string | null>(null)
  const [jobTitle, setJobTitle] = useState('')

  useEffect(() => { if (data) setIt(structuredClone(data)) }, [data])

  if (isLoading || !it) {
    return <div className="p-6 flex items-center gap-2 text-muted-foreground">
      <Loader2 className="h-5 w-5 animate-spin" /> Loading install types…</div>
  }

  const dirty = data ? JSON.stringify(data) !== JSON.stringify(it) : false

  function setProfileRoles(name: keyof ITypes['profiles'], roles: string[]) {
    setIt(prev => prev && ({ ...prev, profiles: { ...prev.profiles,
      [name]: { ...prev.profiles[name], roles } } }))
    setSaved(false)
  }
  function setEnabled(key: string, value: string[]) {
    setIt(prev => prev && ({ ...prev, enabled: { ...prev.enabled,
      [key]: { ...prev.enabled[key], value } } }))
    setSaved(false)
  }

  async function handleSave() {
    if (!it) return
    await save.mutateAsync(it)
    qc.invalidateQueries({ queryKey: ['install-types'] })
    qc.invalidateQueries({ queryKey: ['bundles'] })
    setSaved(true); setTimeout(() => setSaved(false), 2500)
  }

  function installProfile(name: string) {
    if (!confirm(`Run "sb install ${name}"? Installs every role in this profile (can take a while).`)) return
    install.mutate({ tag: name, action: 'install' }, {
      onSuccess: (d) => { setJobId(d.job_id); setJobTitle(`Install ${name}`); qc.invalidateQueries({ queryKey: ['jobs'] }) },
    })
  }

  return (
    <div className="p-6 space-y-5 max-w-4xl">
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-xl font-semibold text-foreground">Install types</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            Customise which roles each profile installs. Saved to the inventory; defaults shown when not overridden.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {saved && <span className="text-xs text-green-500 flex items-center gap-1"><Check className="h-3.5 w-3.5" />Saved</span>}
          <Button size="sm" onClick={handleSave} disabled={!dirty || save.isPending}>
            {save.isPending ? <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" /> : <Save className="h-3.5 w-3.5 mr-1.5" />}
            Save changes
          </Button>
        </div>
      </div>

      {/* Dynamic app selection */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="flex items-center gap-2 text-foreground">
            <Server className="h-4 w-4" /> Dynamic app selection
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-xs text-muted-foreground">
            The <code>media_server</code>, <code>download_clients</code> and <code>download_indexers</code> roles
            install whatever you tick here.
          </p>
          {Object.entries(it.enabled).map(([key, e]) => (
            <div key={key} className="space-y-1.5">
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium">{ENABLED_LABELS[key] ?? key}</span>
                {!sameList(e.value, e.default) && (
                  <button className="text-[11px] text-muted-foreground hover:text-foreground flex items-center gap-0.5"
                    onClick={() => setEnabled(key, [...e.default])}>
                    <RotateCcw className="h-3 w-3" />reset
                  </button>
                )}
              </div>
              <div className="flex flex-wrap gap-1.5">
                {e.options.map(opt => {
                  const on = e.value.includes(opt)
                  return (
                    <button key={opt}
                      onClick={() => setEnabled(key, on ? e.value.filter(v => v !== opt) : [...e.value, opt])}
                      className={cn('text-xs font-mono px-2 py-1 rounded border transition-colors',
                        on ? 'bg-primary/15 text-primary border-primary/30'
                           : 'bg-muted/40 text-muted-foreground border-transparent hover:border-border')}>
                      {on && <Check className="h-3 w-3 inline mr-1" />}{opt}
                    </button>
                  )
                })}
              </div>
            </div>
          ))}
        </CardContent>
      </Card>

      {/* Profiles */}
      {(['saltbox', 'mediabox', 'feederbox'] as const).map(name => {
        const p = it.profiles[name]
        const isDefault = sameList(p.roles, p.default)
        return (
          <Card key={name}>
            <CardHeader className="pb-2 flex-row items-center justify-between">
              <CardTitle className="text-foreground capitalize flex items-center gap-2">
                {name}
                {p.overridden && <span className="text-[10px] text-amber-500 bg-amber-500/10 border border-amber-500/30 px-1.5 py-0.5 rounded">customised</span>}
              </CardTitle>
              <div className="flex items-center gap-2">
                {!isDefault && (
                  <button className="text-[11px] text-muted-foreground hover:text-foreground flex items-center gap-0.5"
                    onClick={() => setProfileRoles(name, [...p.default])}>
                    <RotateCcw className="h-3 w-3" />reset to default
                  </button>
                )}
                <Button size="sm" variant="outline" className="h-7 text-xs" disabled={install.isPending}
                  onClick={() => installProfile(name)}>
                  <ArrowDownToLine className="h-3.5 w-3.5 mr-1.5" />Install
                </Button>
              </div>
            </CardHeader>
            <CardContent>
              <RoleList roles={p.roles} available={it.available_roles}
                onChange={r => setProfileRoles(name, r)} />
            </CardContent>
          </Card>
        )
      })}

      <p className="text-xs text-muted-foreground flex items-center gap-1.5">
        <Download className="h-3.5 w-3.5" />
        Save changes first, then Install to apply. Only Saltbox roles work here — not Sandbox community roles.
      </p>

      <Dialog open={!!jobId} onOpenChange={(o) => { if (!o) setJobId(null) }}>
        <DialogContent className="max-w-3xl">
          <DialogHeader><DialogTitle className="font-mono text-sm">{jobTitle}</DialogTitle></DialogHeader>
          <LogStream jobId={jobId} />
        </DialogContent>
      </Dialog>
    </div>
  )
}

function RoleList({ roles, available, onChange }: {
  roles: string[]; available: string[]; onChange: (r: string[]) => void
}) {
  const addable = available.filter(r => !roles.includes(r))
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-1.5">
        {roles.map(r => (
          <span key={r} className="text-xs font-mono px-2 py-1 rounded bg-muted/50 text-foreground flex items-center gap-1">
            {r}
            <button onClick={() => onChange(roles.filter(x => x !== r))}
              className="text-muted-foreground hover:text-red-500" aria-label={`remove ${r}`}>
              <X className="h-3 w-3" />
            </button>
          </span>
        ))}
        {roles.length === 0 && <span className="text-xs text-muted-foreground">No roles</span>}
      </div>
      {addable.length > 0 && (
        <div className="flex items-center gap-1.5">
          <Plus className="h-3.5 w-3.5 text-muted-foreground" />
          <select
            className="h-7 text-xs font-mono bg-background border border-border rounded-md px-2 max-w-56"
            value=""
            onChange={e => { if (e.target.value) onChange([...roles, e.target.value]) }}
          >
            <option value="">add role…</option>
            {addable.map(r => <option key={r} value={r}>{r}</option>)}
          </select>
        </div>
      )}
    </div>
  )
}
