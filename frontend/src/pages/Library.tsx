/**
 * Library — unified Sonarr/Radarr view, Prismarr-style poster grid. A title held
 * by several instances shows as ONE poster; the detail modal lists every
 * instance's copy and expands to its files (grouped by season).
 *
 * Perf: paginated grid (render a slice, lazy images), capped file lists — a large
 * library never blocks the main thread.
 */
import { useEffect, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useArrLibrary, useArrFiles, useArrCommand, type ArrItem, type ArrCopy, type ArrFile } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { Library as LibraryIcon, Loader2, ChevronRight, Tv, Film, Star, Check, Bookmark, RotateCw, Search, Pencil, Trash2 } from 'lucide-react'
import { cn } from '@/lib/cn'

const PAGE = 120
const MAX_FILES = 200

function fmtSize(n: number): string {
  if (!n) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0, v = n
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${u[i]}`
}

type Status = 'all' | 'continuing' | 'ended' | 'monitored' | 'unmonitored' | 'missing'
const STATUS_BTNS: { key: Status; label: string; active: string }[] = [
  { key: 'all', label: 'All', active: 'bg-secondary text-secondary-foreground border-secondary' },
  { key: 'continuing', label: 'Continuing', active: 'bg-success text-white border-success' },
  { key: 'ended', label: 'Ended', active: 'bg-secondary text-secondary-foreground border-secondary' },
  { key: 'monitored', label: 'Monitored', active: 'bg-primary text-primary-foreground border-primary' },
  { key: 'unmonitored', label: 'Unmonitored', active: 'bg-secondary text-secondary-foreground border-secondary' },
  { key: 'missing', label: 'Missing', active: 'bg-destructive text-white border-destructive' },
]

function itemComplete(i: ArrItem) { return i.copies.length > 0 && i.copies.every((c) => c.has_file) }

// ── poster card (status dot · monitored bookmark · overlay · hover info) ───────
function PosterCard({ item, onOpen }: { item: ArrItem; onOpen: () => void }) {
  const complete = itemComplete(item)
  const some = item.copies.some((c) => c.has_file)
  const sub = item.kind === 'sonarr'
    ? (item.seasons > 0 ? `${item.seasons} season${item.seasons === 1 ? '' : 's'}` : '')
    : (item.year ? String(item.year) : 'Movie')
  return (
    <button onClick={onOpen}
      style={{ contentVisibility: 'auto', containIntrinsicSize: 'auto 260px' }}
      className="group relative block aspect-[2/3] overflow-hidden rounded-lg border border-border bg-muted text-left transition hover:ring-2 hover:ring-primary">
      {item.poster
        ? <img src={item.poster} alt="" loading="lazy" decoding="async" className="absolute inset-0 h-full w-full object-cover" />
        : <div className="absolute inset-0 flex items-center justify-center text-muted-foreground">{item.kind === 'sonarr' ? <Tv className="h-8 w-8" /> : <Film className="h-8 w-8" />}</div>}

      {/* monitored bookmark (top-left) */}
      {item.monitored && <Bookmark className="absolute left-1.5 top-0 h-5 w-5 fill-primary text-primary drop-shadow" />}
      {/* multi-instance badge */}
      {item.copies.length > 1 && <span className="absolute left-1.5 top-6 rounded bg-black/65 px-1 py-0.5 text-[9px] font-semibold text-white">×{item.copies.length}</span>}
      {/* status dot (top-right) */}
      <span className={cn('absolute right-1.5 top-1.5 h-3 w-3 rounded-full ring-2 ring-black/40',
        complete ? 'bg-success' : some ? 'bg-amber-500' : 'bg-destructive')} />

      {/* bottom gradient + title */}
      <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/90 via-black/50 to-transparent p-2 pt-6">
        <p className="truncate text-[11px] font-semibold text-white" title={item.title}>{item.title}</p>
        <p className="flex items-center gap-1 text-[10px] text-white/70">
          {item.kind === 'sonarr' ? <Tv className="h-2.5 w-2.5" /> : <Film className="h-2.5 w-2.5" />}{sub}
        </p>
      </div>

      {/* hover info overlay */}
      <div className="absolute inset-0 flex flex-col justify-end gap-1 bg-gradient-to-t from-black/95 via-black/80 to-black/40 p-2 opacity-0 transition-opacity group-hover:opacity-100">
        <div className="flex flex-wrap gap-1">
          {item.year > 0 && <span className="rounded bg-white/15 px-1 py-0.5 text-[9px] text-white">{item.year}</span>}
          {item.kind === 'sonarr' && item.episodes > 0 && <span className="rounded bg-white/15 px-1 py-0.5 text-[9px] text-white">{item.episodes} eps</span>}
          {item.rating > 0 && <span className="rounded bg-white/15 px-1 py-0.5 text-[9px] text-white">★ {item.rating.toFixed(1)}</span>}
          {item.status && <span className="rounded bg-white/15 px-1 py-0.5 text-[9px] capitalize text-white">{item.status}</span>}
        </div>
        {item.overview && <p className="line-clamp-6 text-[10px] leading-snug text-white/85">{item.overview}</p>}
        <p className="truncate text-[11px] font-semibold text-white">{item.title}</p>
      </div>
    </button>
  )
}

// ── detail modal ──────────────────────────────────────────────────────────────
function groupBySeason(files: ArrFile[]): { season: number | null; files: ArrFile[] }[] {
  const map = new Map<number, ArrFile[]>()
  const noSeason: ArrFile[] = []
  for (const f of files) {
    if (f.season == null) { noSeason.push(f); continue }
    if (!map.has(f.season)) map.set(f.season, [])
    map.get(f.season)!.push(f)
  }
  const out: { season: number | null; files: ArrFile[] }[] = [...map.keys()].sort((a, b) => b - a).map((s) => ({ season: s, files: map.get(s)! }))
  if (noSeason.length) out.push({ season: null, files: noSeason })
  return out
}

// EpisodeRow — one episode/file row; clicking it reveals full file media details.
function EpisodeRow({ f, sonarr, run, busy }: {
  f: ArrFile; sonarr: boolean
  run: (action: string, extra: { episode_id?: number; file_id?: number; season?: number }, confirmMsg?: string) => void
  busy: string
}) {
  const [det, setDet] = useState(false)
  const m = f.media
  const kv: [string, string][] = []
  if (m?.resolution) kv.push(['Resolution', m.resolution])
  if (m?.video_codec) kv.push(['Video', m.video_codec + (m.dynamic_range ? ` · ${m.dynamic_range}` : '')])
  if (m?.audio_codec) kv.push(['Audio', m.audio_codec + (m.audio_channels ? ` ${m.audio_channels}` : '')])
  if (m?.audio_languages) kv.push(['Audio lang', m.audio_languages])
  if (m?.subtitles) kv.push(['Subtitles', m.subtitles])
  if (m?.runtime) kv.push(['Runtime', m.runtime])
  if (f.languages) kv.push(['Languages', f.languages])
  if (f.release_group) kv.push(['Release', f.release_group])
  if (f.date_added) kv.push(['Added', f.date_added.slice(0, 10)])

  return (
    <div className={cn(!f.has_file && 'opacity-60')}>
      <div className="flex items-center gap-2.5 px-3 py-1.5 text-[11px]">
        <button onClick={() => f.has_file && setDet((d) => !d)} className="flex flex-1 min-w-0 items-center gap-2.5 text-left" disabled={!f.has_file}>
          <span className={cn('h-1.5 w-1.5 rounded-full shrink-0', f.has_file ? 'bg-success' : 'bg-muted-foreground')} />
          {f.episode != null && <span className="font-mono text-muted-foreground shrink-0 w-7">E{String(f.episode).padStart(2, '0')}</span>}
          <span className="flex-1 min-w-0 truncate text-foreground" title={f.full_path || f.path || f.title}>{f.title || f.path || '—'}</span>
          {f.air_date && <span className="shrink-0 text-muted-foreground tabular-nums hidden md:inline">{f.air_date}</span>}
          {f.quality && <span className="shrink-0 rounded bg-accent px-1.5 py-0.5 text-muted-foreground">{f.quality}</span>}
          {f.has_file && <span className="shrink-0 tabular-nums text-muted-foreground w-16 text-right">{fmtSize(f.size)}</span>}
        </button>
        <span className="flex items-center gap-0.5 shrink-0">
          {sonarr && f.episode_id ? (
            <button title="Search episode" disabled={!!busy} onClick={() => run('episodeSearch', { episode_id: f.episode_id })}
              className="rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-40">
              {busy === `episodeSearch:${f.episode_id}` ? <Loader2 className="h-3 w-3 animate-spin" /> : <Search className="h-3 w-3" />}
            </button>
          ) : null}
          {f.has_file && f.file_id ? (
            <button title="Delete file" disabled={!!busy} onClick={() => run('deleteFile', { file_id: f.file_id }, 'Delete this file from disk?')}
              className="rounded p-0.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:opacity-40">
              {busy === `deleteFile:${f.file_id}` ? <Loader2 className="h-3 w-3 animate-spin" /> : <Trash2 className="h-3 w-3" />}
            </button>
          ) : null}
        </span>
      </div>
      {det && f.has_file && (
        <div className="bg-muted/40 px-3 pb-2 pt-1 text-[10px] space-y-1">
          {kv.length > 0 && (
            <div className="grid grid-cols-2 gap-x-4 gap-y-0.5 sm:grid-cols-3">
              {kv.map(([k, v]) => <div key={k} className="truncate"><span className="text-muted-foreground">{k}: </span><span className="text-foreground">{v}</span></div>)}
            </div>
          )}
          {f.full_path && <div className="truncate font-mono text-muted-foreground" title={f.full_path}><span className="not-italic">📁 </span>{f.full_path}</div>}
        </div>
      )}
    </div>
  )
}

// SeasonBlock — Prismarr-style collapsible season (progress bar + season search) →
// episode rows with per-episode search / delete.
function SeasonBlock({ kind, copy, season, files, defaultOpen, run, busy }: {
  kind: string; copy: ArrCopy; season: number | null; files: ArrFile[]; defaultOpen: boolean
  run: (action: string, extra: { episode_id?: number; file_id?: number; season?: number }, confirmMsg?: string) => void
  busy: string
}) {
  const [open, setOpen] = useState(defaultOpen)
  const have = files.filter((f) => f.has_file).length
  const size = files.reduce((s, f) => s + f.size, 0)
  const pct = files.length ? Math.round((have / files.length) * 100) : 0
  const sonarr = kind === 'sonarr'
  return (
    <div className="rounded-md border border-border overflow-hidden">
      <div className="flex w-full items-center gap-2 bg-muted/50 px-2.5 py-1.5">
        <button onClick={() => setOpen((o) => !o)} className="flex flex-1 min-w-0 items-center gap-2 text-left">
          <ChevronRight className={cn('h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform', open && 'rotate-90')} />
          <span className="text-xs font-semibold text-foreground shrink-0">{season != null ? `Season ${season}` : 'Files'}</span>
          <div className="flex-1 h-1.5 rounded-full bg-border overflow-hidden max-w-[180px]">
            <div className={cn('h-full', pct === 100 ? 'bg-success' : 'bg-amber-500')} style={{ width: `${pct}%` }} />
          </div>
          <span className="text-[11px] text-muted-foreground shrink-0 tabular-nums">{have}/{files.length}</span>
          <span className="flex-1" />
          <span className="text-[11px] text-muted-foreground shrink-0 tabular-nums">{fmtSize(size)}</span>
        </button>
        {sonarr && season != null && (
          <button title="Search season" disabled={!!busy} onClick={() => run('seasonSearch', { season })}
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-40 shrink-0">
            {busy === `seasonSearch:${season}` ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Search className="h-3.5 w-3.5" />}
          </button>
        )}
      </div>
      {open && (
        <div className="divide-y divide-border/60">
          {files.map((f, i) => <EpisodeRow key={i} f={f} sonarr={sonarr} run={run} busy={busy} />)}
        </div>
      )}
    </div>
  )
}

function CopyFiles({ kind, copy }: { kind: string; copy: ArrCopy }) {
  const qc = useQueryClient()
  const cmd = useArrCommand()
  const [busy, setBusy] = useState('')
  const { data, isLoading, isError } = useArrFiles(kind, copy.instance, copy.item_id, true)
  const run = (action: string, extra: { episode_id?: number; file_id?: number; season?: number }, confirmMsg?: string) => {
    if (confirmMsg && !confirm(confirmMsg)) return
    const tag = `${action}:${extra.episode_id ?? extra.file_id ?? extra.season ?? ''}`
    setBusy(tag)
    cmd.mutate({ kind, instance: copy.instance, id: copy.item_id, action, ...extra }, {
      onSuccess: () => { if (action === 'deleteFile') qc.invalidateQueries({ queryKey: ['arr-files', kind, copy.instance, copy.item_id] }) },
      onSettled: () => setBusy(''),
    })
  }
  if (isLoading) return <div className="px-3 py-2 text-xs text-muted-foreground flex items-center gap-1.5"><Loader2 className="h-3 w-3 animate-spin" />Loading…</div>
  if (isError) return <div className="px-3 py-2 text-xs text-destructive">Failed to load.</div>
  const files = (data?.files ?? []).slice(0, MAX_FILES)
  if (files.length === 0) return <div className="px-3 py-2 text-xs text-muted-foreground">No files.</div>
  const groups = groupBySeason(files)
  return (
    <div className="space-y-1.5 p-2">
      {cmd.isError && <p className="text-[10px] text-destructive break-all">{cmd.error.message}</p>}
      {groups.map((g, idx) => <SeasonBlock key={g.season ?? -1} kind={kind} copy={copy} season={g.season} files={g.files} defaultOpen={idx === 0} run={run} busy={busy} />)}
    </div>
  )
}

function CopyRow({ kind, copy }: { kind: string; copy: ArrCopy }) {
  const [open, setOpen] = useState(false)
  const qc = useQueryClient()
  const cmd = useArrCommand()
  const [busy, setBusy] = useState('')
  const [done, setDone] = useState('')

  const run = (action: string, label: string, confirmMsg?: string) => {
    if (confirmMsg && !confirm(confirmMsg)) return
    setBusy(action); setDone('')
    cmd.mutate({ kind, instance: copy.instance, id: copy.item_id, action }, {
      onSuccess: () => {
        setDone(label)
        setTimeout(() => setDone(''), 2500)
        if (action === 'rename') qc.invalidateQueries({ queryKey: ['arr-files', kind, copy.instance, copy.item_id] })
        if (action === 'monitor' || action === 'unmonitor') qc.invalidateQueries({ queryKey: ['arr-library'] })
      },
      onSettled: () => setBusy(''),
    })
  }
  const Act = ({ action, title, icon, confirmMsg }: { action: string; title: string; icon: React.ReactNode; confirmMsg?: string }) => (
    <button
      title={title}
      onClick={(e) => { e.stopPropagation(); run(action, title, confirmMsg) }}
      disabled={!!busy}
      className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-40"
    >{busy === action ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : icon}</button>
  )

  return (
    <div className="rounded-md border border-border">
      <div className="flex w-full items-center gap-2 px-2.5 py-1.5">
        <button onClick={() => setOpen((o) => !o)} className="flex flex-1 min-w-0 items-center gap-2 text-left">
          <ChevronRight className={cn('h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform', open && 'rotate-90')} />
          <span className={cn('h-2 w-2 rounded-full shrink-0', copy.has_file ? 'bg-success' : 'bg-destructive')} />
          <span className="font-medium text-foreground text-xs w-28 shrink-0 truncate">{copy.instance}</span>
          {copy.profile && <span className="rounded bg-primary/10 text-foreground px-1.5 py-0.5 text-[10px] shrink-0">{copy.profile}</span>}
          <span className="text-[11px] text-muted-foreground shrink-0">{copy.files} file{copy.files === 1 ? '' : 's'}</span>
          <span className="flex-1" />
          <span className="text-[11px] tabular-nums text-muted-foreground shrink-0">{fmtSize(copy.size)}</span>
        </button>
        {/* working action buttons (per instance) */}
        <div className="flex items-center gap-0.5 shrink-0 border-l border-border pl-1.5">
          <Act action="refresh" title="Refresh & scan" icon={<RotateCw className="h-3.5 w-3.5" />} />
          <Act action="search" title="Search" icon={<Search className="h-3.5 w-3.5" />} />
          <Act action="rename" title="Rename files" icon={<Pencil className="h-3.5 w-3.5" />} confirmMsg={`Rename files for ${copy.instance}? This moves files on disk.`} />
          <Act action="monitor" title="Monitor" icon={<Bookmark className="h-3.5 w-3.5" />} />
        </div>
      </div>
      {done && <p className="px-3 pb-1 text-[10px] text-success">{done} triggered</p>}
      {cmd.isError && <p className="px-3 pb-1 text-[10px] text-destructive break-all">{cmd.error.message}</p>}
      {open && <div className="border-t border-border bg-muted/30"><CopyFiles kind={kind} copy={copy} /></div>}
    </div>
  )
}

function DetailModal({ item, onClose }: { item: ArrItem | null; onClose: () => void }) {
  const total = item?.copies.reduce((s, c) => s + c.size, 0) ?? 0
  return (
    <Dialog open={!!item} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="max-w-3xl max-h-[85vh] overflow-y-auto">
        {item && (
          <div className="flex flex-col sm:flex-row gap-4">
            <div className="sm:w-44 shrink-0">
              {item.poster
                ? <img src={item.poster} alt="" className="w-full rounded-lg object-cover" />
                : <div className="aspect-[2/3] rounded-lg bg-muted flex items-center justify-center text-muted-foreground">{item.kind === 'sonarr' ? <Tv className="h-10 w-10" /> : <Film className="h-10 w-10" />}</div>}
            </div>
            <div className="flex-1 min-w-0 space-y-3">
              <div>
                <h2 className="text-lg font-semibold text-foreground">{item.title} {item.year > 0 && <span className="font-normal text-muted-foreground">({item.year})</span>}</h2>
                <div className="mt-1 flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
                  {item.kind === 'sonarr' && item.seasons > 0 && <span>{item.seasons} season{item.seasons === 1 ? '' : 's'}</span>}
                  {item.runtime > 0 && <span>· {item.runtime} min</span>}
                  {item.network && <span>· {item.network}</span>}
                  {item.rating > 0 && <span className="flex items-center gap-0.5">· <Star className="h-3 w-3 fill-amber-400 text-amber-400" />{item.rating.toFixed(1)}</span>}
                  {item.status && <span className="rounded bg-accent px-1.5 py-0.5 capitalize">{item.status}</span>}
                  {item.monitored && <span className="rounded bg-primary/10 px-1.5 py-0.5 text-foreground flex items-center gap-0.5"><Check className="h-3 w-3" />Monitored</span>}
                </div>
                {item.genres && item.genres.length > 0 && (
                  <div className="mt-1.5 flex flex-wrap gap-1">{item.genres.slice(0, 6).map((g) => <span key={g} className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{g}</span>)}</div>
                )}
              </div>
              {item.overview && <p className="text-xs text-muted-foreground leading-relaxed">{item.overview}</p>}
              <div>
                <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground mb-1.5">
                  {item.copies.length} cop{item.copies.length === 1 ? 'y' : 'ies'} · {fmtSize(total)}
                </h3>
                <div className="space-y-1.5">
                  {item.copies.map((c) => <CopyRow key={c.instance + c.item_id} kind={item.kind} copy={c} />)}
                </div>
              </div>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

// ── toolbar + grid ──────────────────────────────────────────────────────────
type Sort = 'title' | 'title_desc' | 'year' | 'year_desc' | 'size'

type MediaType = 'all' | 'sonarr' | 'radarr'

function ItemGrid({ items, hasSonarr, hasRadarr }: { items: ArrItem[]; hasSonarr: boolean; hasRadarr: boolean }) {
  const [type, setType] = useState<MediaType>('all')
  const [q, setQ] = useState('')
  const [status, setStatus] = useState<Status>('all')
  const [genre, setGenre] = useState('')
  const [network, setNetwork] = useState('')
  const [sort, setSort] = useState<Sort>('title')
  const [visible, setVisible] = useState(PAGE)
  const [sel, setSel] = useState<ArrItem | null>(null)

  const pool = useMemo(() => items.filter((i) => type === 'all' || i.kind === type), [items, type])
  const genres = useMemo(() => [...new Set(pool.flatMap((i) => i.genres ?? []))].sort(), [pool])
  const networks = useMemo(() => [...new Set(pool.map((i) => i.network).filter(Boolean))].sort(), [pool])
  const typeBtns: { key: MediaType; label: string; show: boolean }[] = [
    { key: 'all', label: 'All', show: hasSonarr && hasRadarr },
    { key: 'sonarr', label: 'Series', show: hasSonarr },
    { key: 'radarr', label: 'Movies', show: hasRadarr },
  ]

  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase()
    const out = pool.filter((i) => {
      if (t && !i.title.toLowerCase().includes(t)) return false
      if (genre && !(i.genres ?? []).includes(genre)) return false
      if (network && i.network !== network) return false
      switch (status) {
        case 'continuing': if ((i.status || '').toLowerCase() !== 'continuing') return false; break
        case 'ended': if ((i.status || '').toLowerCase() !== 'ended') return false; break
        case 'monitored': if (!i.monitored) return false; break
        case 'unmonitored': if (i.monitored) return false; break
        case 'missing': if (itemComplete(i)) return false; break
      }
      return true
    })
    out.sort((a, b) => {
      switch (sort) {
        case 'title_desc': return b.title.toLowerCase().localeCompare(a.title.toLowerCase())
        case 'year': return (a.year || 0) - (b.year || 0)
        case 'year_desc': return (b.year || 0) - (a.year || 0)
        case 'size': return b.copies.reduce((s, c) => s + c.size, 0) - a.copies.reduce((s, c) => s + c.size, 0)
        default: return a.title.toLowerCase().localeCompare(b.title.toLowerCase())
      }
    })
    return out
  }, [pool, q, genre, network, status, sort])

  useEffect(() => { setVisible(PAGE) }, [q, genre, network, status, sort, type])
  const shown = filtered.slice(0, visible)
  const selCls = 'h-8 rounded-md border border-input bg-transparent px-2 text-xs text-foreground'

  return (
    <div className="space-y-3">
      {/* toolbar */}
      <div className="space-y-2 rounded-lg border border-border bg-card p-3">
        <div className="flex flex-wrap items-center gap-2">
          <Input className="h-8 w-48" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search…" />
          {/* media type */}
          <div className="flex gap-1 rounded-md bg-muted p-0.5">
            {typeBtns.filter((b) => b.show).map((b) => (
              <button key={b.key} onClick={() => setType(b.key)}
                className={cn('rounded px-2.5 py-1 text-xs font-medium transition', type === b.key ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')}>
                {b.label}
              </button>
            ))}
          </div>
          <div className="flex flex-wrap gap-1">
            {STATUS_BTNS.map((b) => (
              <button key={b.key} onClick={() => setStatus(b.key)}
                className={cn('rounded-md border px-2.5 py-1 text-xs font-medium transition', status === b.key ? b.active : 'border-border text-muted-foreground hover:text-foreground')}>
                {b.label}
              </button>
            ))}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <select className={selCls} value={genre} onChange={(e) => setGenre(e.target.value)}>
            <option value="">All genres</option>
            {genres.map((g) => <option key={g} value={g}>{g}</option>)}
          </select>
          {networks.length > 0 && (
            <select className={selCls} value={network} onChange={(e) => setNetwork(e.target.value)}>
              <option value="">All networks</option>
              {networks.map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          )}
          <select className={selCls} value={sort} onChange={(e) => setSort(e.target.value as Sort)}>
            <option value="title">Title A→Z</option>
            <option value="title_desc">Title Z→A</option>
            <option value="year_desc">Year (newest)</option>
            <option value="year">Year (oldest)</option>
            <option value="size">Size</option>
          </select>
        </div>
      </div>

      {/* count */}
      <p className="text-[11px] text-muted-foreground">
        {filtered.length === 0 ? 'No titles' : `1–${shown.length} of ${filtered.length}`}
      </p>

      {/* poster grid */}
      <div className="grid grid-cols-3 sm:grid-cols-5 md:grid-cols-6 lg:grid-cols-8 xl:grid-cols-10 gap-3">
        {shown.map((i) => <PosterCard key={i.kind + i.key} item={i} onOpen={() => setSel(i)} />)}
      </div>

      {visible < filtered.length && (
        <div className="flex justify-center pt-1">
          <Button variant="outline" size="sm" onClick={() => setVisible((v) => v + PAGE)}>Load more ({filtered.length - visible})</Button>
        </div>
      )}

      <DetailModal item={sel} onClose={() => setSel(null)} />
    </div>
  )
}

export function Library() {
  const { data, isLoading, isError, error } = useArrLibrary()
  const items = data?.items ?? []
  const hasSonarr = (data?.instances ?? []).some((i) => i.kind === 'sonarr')
  const hasRadarr = (data?.instances ?? []).some((i) => i.kind === 'radarr')

  return (
    <div className="p-6 space-y-5">
      <div>
        <h1 className="text-xl font-semibold text-foreground flex items-center gap-2"><LibraryIcon className="h-5 w-5" />Library</h1>
        <p className="text-sm text-muted-foreground mt-0.5">Every Sonarr/Radarr instance merged — a title held by multiple instances shows once; open it to see each copy's files.</p>
      </div>

      {isLoading && <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Querying instances…</div>}
      {isError && <p className="text-sm text-destructive">{(error as Error)?.message || 'Failed to load library.'}</p>}

      {!isLoading && !isError && <ItemGrid items={items} hasSonarr={hasSonarr} hasRadarr={hasRadarr} />}
    </div>
  )
}
