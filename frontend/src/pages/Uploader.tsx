/**
 * Uploader — cloudplow++ : watch a local staging folder and, once it grows past a
 * threshold, move it up to cloud remotes, rotating across them with per-remote
 * daily caps + cooldowns to dodge quotas / bans.
 */
import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useUploader, useSaveUploader, useUploaderStatus, useUploaderRun, type UploaderConfig, type UploaderRemote } from '@/lib/api'
import { PathPicker } from '@/components/PathPicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/cn'
import { Plus, Trash2, Save, Play, FolderInput, CloudUpload } from 'lucide-react'

const EMPTY: UploaderConfig = {
  enabled: false, source: '', threshold: '500G', strategy: 'lru', interval_minutes: 15,
  allowed_from: '', allowed_until: '', min_age: '15m', delete_empty_src: false,
  excludes: ['**partial~', '**_HIDDEN~', '.unionfs*/**', '**.fuse_hidden**'], remotes: [],
}
const emptyRemote: UploaderRemote = { name: '', dest: '', cap: '', gap_min: 0, bwlimit: '', tpslimit: 0 }

export function Uploader() {
  const qc = useQueryClient()
  const { data } = useUploader()
  const save = useSaveUploader()
  const run = useUploaderRun()
  const { data: status } = useUploaderStatus()
  const [cfg, setCfg] = useState<UploaderConfig>(EMPTY)
  const [picker, setPicker] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => { if (data) setCfg({ ...EMPTY, ...data, remotes: data.remotes ?? [] }) }, [data])

  const up = <K extends keyof UploaderConfig>(k: K, v: UploaderConfig[K]) => setCfg((c) => ({ ...c, [k]: v }))
  const upRemote = (i: number, patch: Partial<UploaderRemote>) => setCfg((c) => { const r = [...c.remotes]; r[i] = { ...r[i], ...patch }; return { ...c, remotes: r } })
  const addRemote = () => setCfg((c) => ({ ...c, remotes: [...c.remotes, { ...emptyRemote }] }))
  const rmRemote = (i: number) => setCfg((c) => ({ ...c, remotes: c.remotes.filter((_, j) => j !== i) }))

  function doSave() {
    save.mutate(cfg, { onSuccess: () => { qc.invalidateQueries({ queryKey: ['uploader'] }); setSaved(true); setTimeout(() => setSaved(false), 2500) } })
  }

  const STRATS: [UploaderConfig['strategy'], string][] = [['lru', 'Least-recently-used'], ['round_robin', 'Round-robin'], ['most_free', 'Most quota free']]

  return (
    <div className="p-6 space-y-5 max-w-4xl">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-foreground flex items-center gap-2"><CloudUpload className="h-5 w-5" />Uploader</h1>
          <p className="text-sm text-muted-foreground mt-0.5">Auto-move a local folder to cloud once it fills, spread across remotes (quota/ban-aware).</p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <Button size="sm" variant="outline" className="gap-1.5" onClick={() => run.mutate()}><Play className="h-3.5 w-3.5" />Check now</Button>
          <Button size="sm" className="gap-1.5" onClick={doSave} disabled={save.isPending}><Save className="h-3.5 w-3.5" />{saved ? 'Saved' : 'Save'}</Button>
        </div>
      </div>

      {/* live status */}
      {status && (
        <div className="rounded-lg border border-border bg-card p-3 space-y-2 text-sm">
          <div className="flex flex-wrap items-center gap-x-6 gap-y-1">
            <span className="flex items-center gap-1.5"><span className={cn('h-2 w-2 rounded-full', status.enabled ? 'bg-success' : 'bg-muted-foreground/40')} />{status.enabled ? 'Active' : 'Disabled'}</span>
            <span className="text-muted-foreground">Source size: <span className="text-foreground font-medium">{status.last_size}</span> / threshold {status.threshold || '—'}</span>
            <span className="text-muted-foreground">Last check: {status.last_check ? new Date(status.last_check).toLocaleString() : 'never'}</span>
            {status.message && <span className="text-muted-foreground/80 italic">{status.message}</span>}
          </div>
          {status.remotes.length > 0 && (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2 pt-1">
              {status.remotes.map((r) => (
                <div key={r.name} className="rounded-md border border-border bg-secondary/30 px-2.5 py-1.5">
                  <p className="text-xs font-medium text-foreground truncate">{r.name}</p>
                  <p className="text-[11px] text-muted-foreground">today {r.used_today}{r.cap && ` / ${r.cap}`}</p>
                  <p className="text-[10px] text-muted-foreground/70">last {r.last_upload ? new Date(r.last_upload).toLocaleString() : '—'}</p>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* config */}
      <div className="space-y-4 rounded-lg border border-border bg-card p-4">
        <label className="flex items-center gap-2 text-sm text-foreground">
          <input type="checkbox" checked={cfg.enabled} onChange={(e) => up('enabled', e.target.checked)} />
          Enable auto-upload
        </label>

        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <div className="space-y-1 sm:col-span-2">
            <Label className="text-[11px]">Source folder (local)</Label>
            <div className="flex gap-2">
              <Input className="h-8 font-mono" value={cfg.source} onChange={(e) => up('source', e.target.value)} placeholder="/mnt/local/Media" />
              <Button size="sm" variant="outline" className="gap-1.5 shrink-0" onClick={() => setPicker(true)}><FolderInput className="h-3.5 w-3.5" />Pick</Button>
            </div>
          </div>
          <div className="space-y-1">
            <Label className="text-[11px]">Upload when size ≥</Label>
            <Input className="h-8" value={cfg.threshold} onChange={(e) => up('threshold', e.target.value)} placeholder="500G" />
          </div>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div className="space-y-1">
            <Label className="text-[11px]">Strategy (which remote next)</Label>
            <div className="flex flex-wrap gap-1.5">
              {STRATS.map(([s, lbl]) => (
                <Button key={s} size="sm" variant={cfg.strategy === s ? 'default' : 'outline'} onClick={() => up('strategy', s)}>{lbl}</Button>
              ))}
            </div>
          </div>
          <div className="space-y-1">
            <Label className="text-[11px]">Check every (minutes)</Label>
            <Input type="number" min={1} className="h-8 w-28" value={cfg.interval_minutes} onChange={(e) => up('interval_minutes', Math.max(1, parseInt(e.target.value, 10) || 15))} />
          </div>
        </div>

        {/* Safety / window options (cloudplow-style) */}
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <div className="space-y-1">
            <Label className="text-[11px]">Upload window (off-peak, optional)</Label>
            <div className="flex items-center gap-1.5">
              <Input type="time" className="h-8 w-28" value={cfg.allowed_from ?? ''} onChange={(e) => up('allowed_from', e.target.value)} />
              <span className="text-xs text-muted-foreground">–</span>
              <Input type="time" className="h-8 w-28" value={cfg.allowed_until ?? ''} onChange={(e) => up('allowed_until', e.target.value)} />
            </div>
          </div>
          <div className="space-y-1">
            <Label className="text-[11px]">Min file age (skip in-progress)</Label>
            <Input className="h-8 w-28" value={cfg.min_age ?? ''} onChange={(e) => up('min_age', e.target.value)} placeholder="15m" />
          </div>
          <label className="flex items-center gap-2 text-xs text-muted-foreground self-end pb-1.5">
            <input type="checkbox" checked={!!cfg.delete_empty_src} onChange={(e) => up('delete_empty_src', e.target.checked)} />
            Delete empty source dirs
          </label>
        </div>

        <div className="space-y-1">
          <Label className="text-[11px]">Exclude patterns (one per line)</Label>
          <textarea
            className="w-full rounded-md border border-border bg-background px-2 py-1.5 text-xs font-mono h-20"
            value={(cfg.excludes ?? []).join('\n')}
            onChange={(e) => up('excludes', e.target.value.split('\n').map((s) => s.trim()).filter(Boolean))}
            placeholder="**partial~&#10;.unionfs*/**"
          />
        </div>

        {/* remotes */}
        <div className="space-y-2">
          <Label className="text-[11px]">Destination remotes (rotated)</Label>
          <div className="space-y-2">
            {cfg.remotes.map((r, i) => (
              <div key={i} className="grid grid-cols-2 sm:grid-cols-12 gap-2 items-center">
                <Input className="h-8 sm:col-span-3" value={r.name} onChange={(e) => upRemote(i, { name: e.target.value })} placeholder="remote name" />
                <Input className="h-8 sm:col-span-2" value={r.dest} onChange={(e) => upRemote(i, { dest: e.target.value })} placeholder="dest path" />
                <Input className="h-8 sm:col-span-2" value={r.cap} onChange={(e) => upRemote(i, { cap: e.target.value })} placeholder="cap/day (700G)" />
                <Input type="number" className="h-8 sm:col-span-2" value={r.gap_min} onChange={(e) => upRemote(i, { gap_min: Math.max(0, parseInt(e.target.value, 10) || 0) })} placeholder="gap min" />
                <Input className="h-8 sm:col-span-1" value={r.bwlimit} onChange={(e) => upRemote(i, { bwlimit: e.target.value })} placeholder="bw" />
                <Input type="number" className="h-8 sm:col-span-1" value={r.tpslimit} onChange={(e) => upRemote(i, { tpslimit: Math.max(0, parseInt(e.target.value, 10) || 0) })} placeholder="tps" />
                <Button size="icon" variant="ghost" className="h-8 w-8" onClick={() => rmRemote(i)}><Trash2 className="h-3.5 w-3.5 text-destructive" /></Button>
              </div>
            ))}
          </div>
          <Button size="sm" variant="outline" className="gap-1.5" onClick={addRemote}><Plus className="h-3.5 w-3.5" />Add remote</Button>
          <p className="text-[11px] text-muted-foreground">cap/day empty = unlimited (e.g. teldrive); set for Google Drive (700G). gap = min minutes between using the same remote. bw = bandwidth (40M); tps = teldrive rate limit.</p>
        </div>
      </div>

      {picker && (
        <PathPicker mode="folder" onClose={() => setPicker(false)} onPick={(p) => { if (p[0]) up('source', p[0].path); setPicker(false) }} />
      )}
    </div>
  )
}
