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
import { Switch } from '@/components/ui/switch'
import { Save, Plug, Loader2 } from 'lucide-react'

const EMPTY: OptionsConfig = { plex: { url: '', token: '', throttle: false, max_streams: 1, scan_after_upload: true } }

export function OptionsPanel() {
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
    <div className="space-y-5">
      <div className="flex items-start justify-between gap-4">
        <p className="text-sm text-muted-foreground">Plex integration for the uploader — throttle while streaming, scan after upload.</p>
        <Button size="sm" className="gap-1.5 shrink-0" onClick={doSave} disabled={save.isPending}><Save className="h-3.5 w-3.5" />{saved ? 'Saved' : 'Save'}</Button>
      </div>

      <div className="space-y-4 rounded-lg border border-border bg-card p-4">
        <h2 className="text-sm font-semibold text-foreground">Plex / Jellyfin</h2>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <div className="space-y-1 sm:col-span-2">
            <Label className="text-[11px]">Server URL</Label>
            <Input className="h-8 font-mono" value={cfg.plex.url} onChange={(e) => up({ url: e.target.value })} placeholder="http://localhost:32400"
              autoComplete="off" name="plex-url" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
          </div>
          <div className="space-y-1">
            <Label className="text-[11px]">X-Plex-Token</Label>
            <Input className="h-8 font-mono" type="password" value={cfg.plex.token} onChange={(e) => up({ token: e.target.value })} placeholder="token"
              autoComplete="new-password" name="plex-token" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" className="gap-1.5" disabled={test.isPending || !cfg.plex.url} onClick={() => test.mutate()}>
            {test.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plug className="h-3.5 w-3.5" />}Test connection
          </Button>
          {test.data && <span className="text-xs text-success">OK · {test.data.streams} stream(s) now · sections: {test.data.sections.join(', ') || '—'}</span>}
          {test.isError && <span className="text-xs text-destructive">{test.error.message}</span>}
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
          <div className="flex items-start justify-between gap-3 rounded-md border border-border px-3 py-2">
            <span className="text-sm text-foreground">Throttle uploads while streaming
              <span className="block text-[11px] text-muted-foreground">Pause the uploader when active Plex streams reach the limit.</span>
            </span>
            <Switch checked={cfg.plex.throttle} onCheckedChange={(v) => up({ throttle: v })} className="mt-0.5" />
          </div>
          <div className="flex items-start justify-between gap-3 rounded-md border border-border px-3 py-2">
            <span className="text-sm text-foreground">Scan libraries after upload
              <span className="block text-[11px] text-muted-foreground">Refresh all Plex sections when an uploader run finishes (replaces autoscan).</span>
            </span>
            <Switch checked={cfg.plex.scan_after_upload} onCheckedChange={(v) => up({ scan_after_upload: v })} className="mt-0.5" />
          </div>
        </div>
        {cfg.plex.throttle && (
          <div className="space-y-1">
            <Label className="text-[11px]">Pause when streams ≥</Label>
            <Input type="number" min={1} className="h-8 w-24" value={cfg.plex.max_streams} onChange={(e) => up({ max_streams: Math.max(1, parseInt(e.target.value, 10) || 1) })} />
          </div>
        )}
      </div>
    </div>
  )
}
