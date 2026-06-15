import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { Link2, Unlink } from 'lucide-react'
import { useMounts, useStorage, useContainers, type ContainerInfo, type RemoteInfo } from '@/lib/api'
import { cn } from '@/lib/cn'

// Live Saltbox media + storage pipeline. Every group uses the same shape: an
// outer category card with sub-cards ("Label ×count") listing each instance.
// Connection lines (measured SVG overlay) show who feeds whom.

type Sub = { label: string; app: string; re: RegExp }
const CATS: { key: string; title: string; subs: Sub[] }[] = [
  { key: 'indexers', title: 'Indexers', subs: [
    { label: 'Prowlarr', app: 'prowlarr', re: /^prowlarr/i },
    { label: 'Jackett', app: 'jackett', re: /^jackett/i },
    { label: 'NZBHydra', app: 'nzbhydra', re: /^nzbhydra/i },
  ] },
  { key: 'requesters', title: 'Requesters', subs: [
    { label: 'Jellyseerr', app: 'jellyseerr', re: /^jellyseerr/i },
    { label: 'Overseerr', app: 'overseerr', re: /^overseerr/i },
  ] },
  { key: 'downloaders', title: 'Downloaders', subs: [
    { label: 'Bittorrent', app: 'qbittorrent', re: /^(qbittorrent|deluge|transmission)/i },
    { label: 'Usenet', app: 'sabnzbd', re: /^(sabnzbd|nzbget)/i },
  ] },
  { key: 'importers', title: 'Importers', subs: [
    { label: 'TV', app: 'sonarr', re: /^sonarr/i },
    { label: 'Movies', app: 'radarr', re: /^radarr/i },
    { label: 'Music', app: 'lidarr', re: /^lidarr/i },
    { label: 'Books', app: 'readarr', re: /^readarr/i },
    { label: 'Adult', app: 'whisparr', re: /^whisparr/i },
    { label: 'Subtitles', app: 'bazarr', re: /^bazarr/i },
  ] },
  { key: 'scaners', title: 'Scaners', subs: [{ label: 'Autoscan', app: 'autoscan', re: /^autoscan/i }] },
  { key: 'media', title: 'Media Servers', subs: [
    // each media server is paired with its stats companion
    { label: 'Plex', app: 'plex', re: /^(plex|tautulli)/i },
    { label: 'Jellyfin', app: 'jellyfin', re: /^(jellyfin|jellystat)/i },
    { label: 'Emby', app: 'emby', re: /^(emby|embystat)/i },
  ] },
  { key: 'uploaders', title: 'Uploaders', subs: [
    { label: 'Rclone Browser', app: 'rclonebrowser', re: /^rclonebrowser/i },
    { label: 'Cloudplow', app: 'cloudplow', re: /^cloudplow/i },
  ] },
]

const EDGES: [string, string, string, boolean?][] = [
  ['requesters', 'importers', 'request'], ['indexers', 'importers', 'search'],
  ['importers', 'downloaders', 'grab'], ['downloaders', 'local', 'download'],
  ['importers', 'storage', 'import'], ['storage', 'media', 'read'],
  ['importers', 'scaners', 'webhook', true], ['scaners', 'media', 'scan', true],
  ['local', 'uploaders', 'move', true],
  ['uploaders', 'clounds', 'upload'], ['clounds', 'remote', 'rclone_vfs'],
]

type Rect = { x: number; y: number; w: number; h: number }
const base = (t: string) => t.replace(/^\/mnt\/remote\//, '').replace(/^\/mnt\//, '')
const pct = (s?: string) => { const n = parseInt((s ?? '').replace('%', ''), 10); return isNaN(n) ? 0 : n }
const barCls = (p: number) => (p > 90 ? 'bg-destructive' : p > 75 ? 'bg-warning' : 'bg-primary')
const cap = (s: string) => (s ? s[0].toUpperCase() + s.slice(1) : s)
function prettyName(name: string, app: string) {
  if (name.toLowerCase().startsWith(app)) {
    const suf = name.slice(app.length).replace(/^[-_]/, '')
    const f = !suf ? '' : suf.length <= 3 ? suf.toUpperCase() : cap(suf)
    return cap(app) + (f ? ' ' + f : '')
  }
  return cap(name)
}

function Bar({ p }: { p: number }) {
  return <div className="w-full bg-secondary rounded-full h-1.5"><div className={cn('h-1.5 rounded-full', barCls(p))} style={{ width: `${Math.min(p, 100)}%` }} /></div>
}

function connect(a: Rect, b: Rect) {
  const ac = { x: a.x + a.w / 2, y: a.y + a.h / 2 }, bc = { x: b.x + b.w / 2, y: b.y + b.h / 2 }
  const dx = bc.x - ac.x, dy = bc.y - ac.y
  if (Math.abs(dx) >= Math.abs(dy)) {
    const p1 = { x: dx > 0 ? a.x + a.w : a.x, y: ac.y }, p2 = { x: dx > 0 ? b.x : b.x + b.w, y: bc.y }
    const mx = (p1.x + p2.x) / 2
    return { d: `M${p1.x} ${p1.y} C ${mx} ${p1.y}, ${mx} ${p2.y}, ${p2.x} ${p2.y}`, lx: mx, ly: (p1.y + p2.y) / 2 }
  }
  const p1 = { x: ac.x, y: dy > 0 ? a.y + a.h : a.y }, p2 = { x: bc.x, y: dy > 0 ? b.y : b.y + b.h }
  const my = (p1.y + p2.y) / 2
  return { d: `M${p1.x} ${p1.y} C ${p1.x} ${my}, ${p2.x} ${my}, ${p2.x} ${p2.y}`, lx: (p1.x + p2.x) / 2, ly: my }
}

const Dot = ({ ok, muted }: { ok?: boolean; muted?: boolean }) =>
  <span className={cn('h-1.5 w-1.5 rounded-full shrink-0', muted ? 'bg-muted-foreground/50' : ok ? 'bg-success' : 'bg-destructive')} />

export function StorageFlow() {
  const { data: storage } = useStorage()
  const { data: mounts } = useMounts()
  const { data: containers } = useContainers()

  const remotes = storage?.remotes ?? []
  const local = storage?.local
  const list = mounts ?? []
  const remoteMounts = list.filter((m) => m.target.startsWith('/mnt/remote'))
  const union = list.find((m) => m.kind === 'mergerfs' || m.target.includes('unionfs'))
  const cs = containers ?? []

  // group cloud remotes by backend type → Clounds card
  const byType: Record<string, RemoteInfo[]> = {}
  for (const r of remotes) (byType[r.type || 'other'] ??= []).push(r)

  const containerRef = useRef<HTMLDivElement>(null)
  const nodes = useRef<Record<string, HTMLElement | null>>({})
  const [rects, setRects] = useState<Record<string, Rect>>({})
  const reg = (id: string) => (el: HTMLElement | null) => { nodes.current[id] = el }

  useLayoutEffect(() => {
    const measure = () => {
      const c = containerRef.current
      if (!c) return
      const cb = c.getBoundingClientRect()
      const next: Record<string, Rect> = {}
      for (const [id, el] of Object.entries(nodes.current)) {
        if (!el) continue
        const r = el.getBoundingClientRect()
        next[id] = { x: r.left - cb.left, y: r.top - cb.top, w: r.width, h: r.height }
      }
      setRects(next)
    }
    measure()
    requestAnimationFrame(measure) // catch sub-card layout after paint
    const ro = new ResizeObserver(measure)
    if (containerRef.current) ro.observe(containerRef.current)
    window.addEventListener('resize', measure)
    return () => { ro.disconnect(); window.removeEventListener('resize', measure) }
  }, [containers, mounts, storage])

  const SubCard = ({ label, count, children }: { label: string; count: number; children: ReactNode }) => (
    <div className="rounded-md border border-border bg-secondary/50 p-1.5">
      <div className="text-[11px] font-medium text-foreground mb-1">{label} <span className="text-muted-foreground">×{count}</span></div>
      <div className="space-y-0.5">{children}</div>
    </div>
  )

  const CategoryCard = ({ cat }: { cat: typeof CATS[number] }) => {
    const subs = cat.subs.map((s) => ({ s, inst: cs.filter((c) => s.re.test(c.name)) })).filter((x) => x.inst.length)
    if (!subs.length) return null
    return (
      <div ref={reg(cat.key)} className="rounded-lg border-2 border-border bg-card p-2 space-y-2 min-w-[160px]">
        <div className="text-xs font-semibold text-foreground">{cat.title}</div>
        {subs.map(({ s, inst }) => (
          <SubCard key={s.label} label={s.label} count={inst.length}>
            {inst.map((c: ContainerInfo) => (
              <div key={c.id} className="flex items-center gap-1.5" title={c.running ? 'running' : c.status}>
                <Dot ok={c.running} /><span className="text-[11px] text-foreground">{prettyName(c.name, s.app)}</span>
              </div>
            ))}
          </SubCard>
        ))}
      </div>
    )
  }

  return (
    <div ref={containerRef} className="relative inline-block min-w-full">
      <svg className="absolute inset-0 z-10 w-full h-full pointer-events-none overflow-visible">
        <defs>
          <marker id="sf-a" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
            <path d="M0 0L10 5L0 10z" fill="var(--color-primary)" />
          </marker>
        </defs>
        {EDGES.map(([from, to, label, ctrl], i) => {
          const a = rects[from], b = rects[to]
          if (!a || !b) return null
          const { d, lx, ly } = connect(a, b)
          return (
            <g key={i}>
              <path d={d} fill="none" stroke="var(--color-primary)" strokeWidth={1.75} strokeOpacity={0.85}
                strokeDasharray={ctrl ? '5 3' : undefined} markerEnd="url(#sf-a)" />
              <text x={lx} y={ly - 3} textAnchor="middle" className="fill-foreground" fontSize={9}
                style={{ paintOrder: 'stroke' }} stroke="var(--color-background)" strokeWidth={3}>{label}</text>
            </g>
          )
        })}
      </svg>

      <div className="relative flex flex-nowrap items-start gap-x-14 w-max">
        {/* col A — sources */}
        <div className="flex flex-col gap-3"><CategoryCard cat={CATS[0]} /><CategoryCard cat={CATS[1]} /></div>
        {/* col B — acquire (extra gap between Downloaders and Importers) */}
        <div className="flex flex-col gap-10"><CategoryCard cat={CATS[2]} /><CategoryCard cat={CATS[3]} /></div>

        {/* col C — storage + scaners below */}
        <div className="flex flex-col gap-6">
          <div ref={reg('storage')} className="rounded-lg border-2 border-primary/60 bg-card p-2 space-y-2 min-w-[250px]">
            <div className="flex items-center justify-between text-[11px]">
              <span className="font-semibold text-foreground">unionfs · mergerfs</span>
              <span className="text-muted-foreground">{union ? `${union.used}/${union.size} (${union.use_pct})` : '—'}</span>
            </div>
            {union && <Bar p={pct(union.use_pct)} />}
            <div ref={reg('local')} className="rounded-md border border-border bg-secondary/50 p-1.5">
              <div className="flex items-center gap-1.5 text-[11px]">
                <Dot ok={local?.ok} /><span className="font-medium text-foreground">Local · 8TB</span>
                <span className="ml-auto text-muted-foreground">{local ? `${local.used}/${local.size} (${local.use_pct})` : '—'}</span>
              </div>
              {local?.use_pct && <div className="mt-1.5"><Bar p={pct(local.use_pct)} /></div>}
            </div>
            <div ref={reg('remote')} className="rounded-md border border-border bg-secondary/50 p-1.5">
              <div className="text-[11px] font-medium text-foreground mb-1">Remote mounts <span className="text-muted-foreground">×{remoteMounts.length}</span></div>
              <div className="space-y-1.5">
                {remoteMounts.map((m) => (
                  <div key={m.target}>
                    <div className="flex items-center gap-1.5">
                      <Dot ok={m.ok} /><span className="font-mono text-[11px] text-foreground">{base(m.target)}</span>
                      <span className="ml-auto text-[10px] text-muted-foreground">{m.used}/{m.size} ({m.use_pct})</span>
                    </div>
                    {m.use_pct && <div className="mt-1"><Bar p={pct(m.use_pct)} /></div>}
                  </div>
                ))}
              </div>
            </div>
          </div>
          <CategoryCard cat={CATS[4]} />
        </div>

        {/* col D — uploaders, then media servers pushed below the clounds line */}
        <div className="flex flex-col"><CategoryCard cat={CATS[6]} /><div className="mt-40"><CategoryCard cat={CATS[5]} /></div></div>

        {/* col E — clounds */}
        {remotes.length > 0 && (
          <div ref={reg('clounds')} className="rounded-lg border-2 border-border bg-card p-2 space-y-2 min-w-[180px]">
            <div className="text-xs font-semibold text-foreground">Clounds</div>
            {Object.entries(byType).map(([type, rs]) => (
              <SubCard key={type} label={type} count={rs.length}>
                {rs.map((r) => {
                  const mnt = remoteMounts.find((m) => base(m.target) === r.name)
                  return (
                    <div key={r.name} className="flex items-center gap-1.5"
                      title={mnt ? `mounted at ${mnt.target}` : 'not mounted'}>
                      <Dot ok={r.ok} />
                      <span className="font-mono text-[11px] text-foreground">{r.name}</span>
                      {mnt
                        ? <Link2 className="h-3 w-3 text-success shrink-0" />
                        : <Unlink className="h-3 w-3 text-muted-foreground/40 shrink-0" />}
                      {r.used && <span className="ml-auto text-[10px] text-muted-foreground">{r.used}</span>}
                    </div>
                  )
                })}
              </SubCard>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
