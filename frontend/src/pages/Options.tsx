/**
 * Options — central settings. Plex integration closes the cloudplow loop:
 * throttle uploads while people are streaming, and refresh Plex libraries after
 * an upload finishes (replacing a separate autoscan).
 */
import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useOptions, useSaveOptions, usePlexTest, type OptionsConfig } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Settings, Save, Plug, Loader2 } from 'lucide-react'

const EMPTY: OptionsConfig = { plex: { url: '', token: '', throttle: false, max_streams: 1, scan_after_upload: true } }

export function Options() {
  const qc = useQueryClient()
  const { data } = useOptions()
  const save = useSaveOptions()
  const test = usePlexTest()
  const [cfg, setCfg] = useState<OptionsConfig>(EMPTY)
  const [saved, setSaved] = useState(false)
  useEffect(() => { if (data) setCfg({ ...EMPTY, ...data, plex: { ...EMPTY.plex, ...data.plex } }) }, [data])

  const up = (patch: Partial<OptionsConfig['plex']>) => setCfg((c) => ({ ...c, plex: { ...c.plex, ...patch } }))
  const doSave = () => save.mutate(cfg, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['options'] }); setSaved(true); setTimeout(() => setSaved(false), 2500) } })

  return (
    <div className="p-6 space-y-5 max-w-3xl">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-foreground flex items-center gap-2"><Settings className="h-5 w-5" />Options</h1>
          <p className="text-sm text-muted-foreground mt-0.5">Plex integration for the uploader — throttle while streaming, scan after upload.</p>
        </div>
        <Button size="sm" className="gap-1.5 shrink-0" onClick={doSave} disabled={save.isPending}><Save className="h-3.5 w-3.5" />{saved ? 'Saved' : 'Save'}</Button>
      </div>

      <div className="space-y-4 rounded-lg border border-border bg-card p-4">
        <h2 className="text-sm font-semibold text-foreground">Plex / Jellyfin</h2>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <div className="space-y-1 sm:col-span-2">
            <Label className="text-[11px]">Server URL</Label>
            <Input className="h-8 font-mono" value={cfg.plex.url} onChange={(e) => up({ url: e.target.value })} placeholder="http://localhost:32400" />
          </div>
          <div className="space-y-1">
            <Label className="text-[11px]">X-Plex-Token</Label>
            <Input className="h-8 font-mono" type="password" value={cfg.plex.token} onChange={(e) => up({ token: e.target.value })} placeholder="token" />
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" className="gap-1.5" disabled={test.isPending || !cfg.plex.url} onClick={() => test.mutate()}>
            {test.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plug className="h-3.5 w-3.5" />}Test connection
          </Button>
          {test.data && <span className="text-xs text-success">OK · {test.data.streams} stream(s) now · sections: {test.data.sections.join(', ') || '—'}</span>}
          {test.isError && <span className="text-xs text-destructive">{test.error.message}</span>}
        </div>

        <label className="flex items-start gap-2 text-sm text-foreground">
          <input type="checkbox" className="mt-0.5" checked={cfg.plex.throttle} onChange={(e) => up({ throttle: e.target.checked })} />
          <span>
            Throttle uploads while streaming
            <span className="block text-[11px] text-muted-foreground">Pause the uploader when active Plex streams reach the limit below.</span>
          </span>
        </label>
        {cfg.plex.throttle && (
          <div className="space-y-1 pl-6">
            <Label className="text-[11px]">Pause when streams ≥</Label>
            <Input type="number" min={1} className="h-8 w-24" value={cfg.plex.max_streams} onChange={(e) => up({ max_streams: Math.max(1, parseInt(e.target.value, 10) || 1) })} />
          </div>
        )}

        <label className="flex items-start gap-2 text-sm text-foreground">
          <input type="checkbox" className="mt-0.5" checked={cfg.plex.scan_after_upload} onChange={(e) => up({ scan_after_upload: e.target.checked })} />
          <span>
            Scan libraries after upload
            <span className="block text-[11px] text-muted-foreground">Refresh all Plex sections when an uploader run finishes (replaces autoscan).</span>
          </span>
        </label>
      </div>
    </div>
  )
}
