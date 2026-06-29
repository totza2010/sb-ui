/**
 * Options — central settings. Plex integration closes the cloudplow loop:
 * throttle uploads while people are streaming, and refresh Plex libraries after
 * an upload finishes (replacing a separate autoscan).
 */
import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useOptions, useSaveOptions, usePlexTest, usePathmapSuggest, type OptionsConfig, type PathMapping } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Save, Plug, Loader2, Plus, Trash2, Wand2, ArrowRight } from 'lucide-react'

const EMPTY: OptionsConfig = { plex: { url: '', token: '', throttle: false, max_streams: 1, scan_after_upload: true }, path_mappings: [], seerr: { url: '', api_key: '' } }

export function OptionsPanel() {
  const qc = useQueryClient()
  const { data } = useOptions()
  const save = useSaveOptions()
  const test = usePlexTest()
  const [cfg, setCfg] = useState<OptionsConfig>(EMPTY)
  const [saved, setSaved] = useState(false)
  useEffect(() => { if (data) setCfg({ ...EMPTY, ...data, plex: { ...EMPTY.plex, ...data.plex }, seerr: { ...EMPTY.seerr!, ...data.seerr }, tmdb: { api_key: '', ...data.tmdb } }) }, [data])

  const up = (patch: Partial<OptionsConfig['plex']>) => setCfg((c) => ({ ...c, plex: { ...c.plex, ...patch } }))
  const upSeerr = (patch: Partial<NonNullable<OptionsConfig['seerr']>>) => setCfg((c) => ({ ...c, seerr: { ...(c.seerr ?? { url: '', api_key: '' }), ...patch } }))
  const upTmdb = (patch: Partial<NonNullable<OptionsConfig['tmdb']>>) => setCfg((c) => ({ ...c, tmdb: { ...(c.tmdb ?? { api_key: '' }), ...patch } }))
  const doSave = () => save.mutate(cfg, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['options'] }); setSaved(true); setTimeout(() => setSaved(false), 2500) } })

  // path mappings (arr → Plex)
  const maps = cfg.path_mappings ?? []
  const setMaps = (m: PathMapping[]) => setCfg((c) => ({ ...c, path_mappings: m }))
  const addMap = () => setMaps([...maps, { from: '', to: '' }])
  const upMap = (i: number, patch: Partial<PathMapping>) => setMaps(maps.map((m, j) => (j === i ? { ...m, ...patch } : m)))
  const delMap = (i: number) => setMaps(maps.filter((_, j) => j !== i))

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
          <Button size="sm" variant="outline" className="gap-1.5" disabled={test.isPending || !cfg.plex.url} onClick={() => test.mutate({ url: cfg.plex.url, token: cfg.plex.token })}>
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

      <PathMappings maps={maps} addMap={addMap} upMap={upMap} delMap={delMap} />

      <div className="space-y-4 rounded-lg border border-border bg-card p-4">
        <h2 className="text-sm font-semibold text-foreground">Discover &amp; Requests</h2>
        <p className="text-[11px] text-muted-foreground">TMDb provides all Discover metadata; Jellyseerr/Overseerr handles requesting the files.</p>
        <div className="space-y-1">
          <Label className="text-[11px]">TMDb API key</Label>
          <Input className="h-8 font-mono" type="password" value={cfg.tmdb?.api_key ?? ''} onChange={(e) => upTmdb({ api_key: e.target.value })} placeholder="themoviedb.org v3 API key"
            autoComplete="new-password" name="tmdb-key" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
          <p className="text-[10px] text-muted-foreground">Free key from themoviedb.org → Settings → API (same key Jellyseerr uses).</p>
        </div>
        <div className="h-px bg-border" />
        <p className="text-[11px] font-medium text-foreground">Jellyseerr / Overseerr (requests)</p>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div className="space-y-1 sm:col-span-2">
            <Label className="text-[11px]">Server URL</Label>
            <Input className="h-8 font-mono" value={cfg.seerr?.url ?? ''} onChange={(e) => upSeerr({ url: e.target.value })} placeholder="https://requests.example.com"
              autoComplete="off" name="seerr-url" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
          </div>
          <div className="space-y-1">
            <Label className="text-[11px]">API Key</Label>
            <Input className="h-8 font-mono" type="password" value={cfg.seerr?.api_key ?? ''} onChange={(e) => upSeerr({ api_key: e.target.value })} placeholder="api key"
              autoComplete="new-password" name="seerr-key" data-1p-ignore="true" data-lpignore="true" data-form-type="other" />
          </div>
        </div>
        <p className="text-[11px] text-muted-foreground">Connection status shows on the Integrations page.</p>
      </div>
    </div>
  )
}

function PathMappings({ maps, addMap, upMap, delMap }: {
  maps: PathMapping[]
  addMap: () => void
  upMap: (i: number, patch: Partial<PathMapping>) => void
  delMap: (i: number) => void
}) {
  const [open, setOpen] = useState(false)
  const { data: sug } = usePathmapSuggest()
  return (
    <div className="space-y-3 rounded-lg border border-border bg-card p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold text-foreground">Path mappings (arr → Plex)</h2>
        <Button size="sm" variant="outline" className="gap-1.5 h-7" onClick={() => setOpen((o) => !o)}><Wand2 className="h-3.5 w-3.5" />Suggest</Button>
      </div>
      <p className="text-[11px] text-muted-foreground">When arr and Plex use different library roots (e.g. <span className="font-mono">/Media/TV-UHD</span> vs <span className="font-mono">/Media/tvuhd</span>), map the prefix so Plex availability + targeted refresh resolve the right path. Leave empty if roots already match.</p>

      {open && (
        <div className="grid grid-cols-2 gap-3 rounded-md border border-border bg-muted/30 p-2 text-[11px]">
          <div>
            <p className="font-semibold text-muted-foreground mb-1">arr roots</p>
            {(sug?.arr_roots ?? []).map((p) => <p key={p} className="font-mono truncate text-foreground" title={p}>{p}</p>)}
          </div>
          <div>
            <p className="font-semibold text-muted-foreground mb-1">Plex roots</p>
            {(sug?.plex_roots ?? []).map((p) => <p key={p} className="font-mono truncate text-foreground" title={p}>{p}</p>)}
          </div>
        </div>
      )}

      <div className="space-y-2">
        {maps.map((m, i) => (
          <div key={i} className="flex items-center gap-2">
            <Input className="h-8 font-mono flex-1" value={m.from} onChange={(e) => upMap(i, { from: e.target.value })} placeholder="/mnt/unionfs/Media/TV-UHD" />
            <ArrowRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <Input className="h-8 font-mono flex-1" value={m.to} onChange={(e) => upMap(i, { to: e.target.value })} placeholder="/mnt/unionfs/Media/tvuhd" />
            <button onClick={() => delMap(i)} className="text-muted-foreground hover:text-destructive shrink-0"><Trash2 className="h-3.5 w-3.5" /></button>
          </div>
        ))}
        <Button size="sm" variant="outline" className="gap-1.5 h-7" onClick={addMap}><Plus className="h-3.5 w-3.5" />Add mapping</Button>
      </div>
    </div>
  )
}
