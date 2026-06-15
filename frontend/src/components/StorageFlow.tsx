import { useLayoutEffect, useRef, useState } from 'react'
import { useMounts, useStorage, useContainers, type ContainerInfo } from '@/lib/api'
import { cn } from '@/lib/cn'

// Live Saltbox media + storage pipeline. Apps are listed per category (header +
// one line per instance, like the mounts). Connection lines (SVG overlay,
// measured from the real card positions) show who feeds whom.

const CATS: { key: string; title: string; re: RegExp }[] = [
  { key: 'requests', title: 'Requests', re: /^(jellyseerr|overseerr|ombi)/i },
  { key: 'indexers', title: 'Indexers', re: /^(prowlarr|jackett|nzbhydra)/i },
  { key: 'arr', title: '*arr (PVR)', re: /^(sonarr|radarr|whisparr|lidarr|readarr|bazarr)/i },
  { key: 'downloaders', title: 'Downloaders', re: /^(qbittorrent|sabnzbd|nzbget|rdtclient|deluge)/i },
  { key: 'media', title: 'Media servers', re: /^(plex|emby|jellyfin)/i },
  { key: 'scan', title: 'Scan trigger', re: /^(autoscan)/i },
  { key: 'stats', title: 'Stats', re: /^(tautulli)/i },
]

// edges: [from, to, label, control?]
const EDGES: [string, string, string, boolean?][] = [
  ['requests', 'arr', 'request'],
  ['indexers', 'arr', 'search'],
  ['arr', 'downloaders', 'grab'],
  ['downloaders', 'storage', 'download'],
  ['arr', 'storage', 'import'],
  ['storage', 'media', 'read'],
  ['cloud', 'storage', 'rclone_vfs'],
  ['storage', 'rclonebrowser', 'move', true],
  ['rclonebrowser', 'cloud', 'upload', true],
  ['scan', 'media', 'scan', true],
  ['media', 'stats', 'stats', true],
]

type Rect = { x: number; y: number; w: number; h: number }
const base = (t: string) => t.replace(/^\/mnt\/remote\//, '').replace(/^\/mnt\//, '')
const pct = (s?: string) => { const n = parseInt((s ?? '').replace('%', ''), 10); return isNaN(n) ? 0 : n }
const barCls = (p: number) => (p > 90 ? 'bg-destructive' : p > 75 ? 'bg-warning' : 'bg-primary')

function Bar({ p }: { p: number }) {
  return <div className="w-full bg-secondary rounded-full h-1.5"><div className={cn('h-1.5 rounded-full', barCls(p))} style={{ width: `${Math.min(p, 100)}%` }} /></div>
}

// cubic path between two rects, anchored on the facing edges
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
  const rb = cs.find((c) => c.name === 'rclonebrowser')
  const appsOf = (key: string) => cs.filter((c) => CATS.find((x) => x.key === key)!.re.test(c.name))

  // measure card positions for the edge overlay
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
    const ro = new ResizeObserver(measure)
    if (containerRef.current) ro.observe(containerRef.current)
    window.addEventListener('resize', measure)
    return () => { ro.disconnect(); window.removeEventListener('resize', measure) }
  }, [containers, mounts, storage])

  const Dot = ({ ok, muted }: { ok?: boolean; muted?: boolean }) =>
    <span className={cn('h-1.5 w-1.5 rounded-full shrink-0', muted ? 'bg-muted-foreground/50' : ok ? 'bg-success' : 'bg-destructive')} />

  const RoleCard = ({ id }: { id: string }) => {
    const apps = appsOf(id)
    if (!apps.length) return null
    const title = CATS.find((c) => c.key === id)!.title
    return (
      <div ref={reg(id)} className="rounded-lg border border-border bg-card p-2 min-w-[140px]">
        <div className="text-[11px] font-medium text-muted-foreground mb-1">{title} ×{apps.length}</div>
        <div className="space-y-0.5">
          {apps.map((c: ContainerInfo) => (
            <div key={c.id} className="flex items-center gap-1.5" title={c.running ? 'running' : c.status}>
              <Dot ok={c.running} />
              <span className="font-mono text-[11px] text-foreground">{c.name}</span>
            </div>
          ))}
        </div>
      </div>
    )
  }

  return (
    <div ref={containerRef} className="relative inline-block min-w-full">
      {/* edge overlay */}
      <svg className="absolute inset-0 w-full h-full pointer-events-none overflow-visible">
        <defs>
          <marker id="sf-a" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
            <path d="M0 0L10 5L0 10z" className="fill-muted-foreground" />
          </marker>
        </defs>
        {EDGES.map(([from, to, label, ctrl], i) => {
          const a = rects[from], b = rects[to]
          if (!a || !b) return null
          const { d, lx, ly } = connect(a, b)
          return (
            <g key={i}>
              <path d={d} fill="none" className="stroke-muted-foreground" strokeWidth={1.5}
                strokeDasharray={ctrl ? '4 3' : undefined} markerEnd="url(#sf-a)" />
              <text x={lx} y={ly - 2} textAnchor="middle" className="fill-muted-foreground" fontSize={9}
                style={{ paintOrder: 'stroke' }} stroke="var(--color-background)" strokeWidth={3}>{label}</text>
            </g>
          )
        })}
      </svg>

      {/* columns */}
      <div className="relative flex flex-wrap items-center gap-x-12 gap-y-3">
        <div className="flex flex-col gap-3"><RoleCard id="requests" /><RoleCard id="indexers" /></div>
        <div className="flex flex-col gap-3"><RoleCard id="arr" /><RoleCard id="downloaders" /></div>

        {/* storage */}
        <div className="flex flex-col gap-2">
          <div className="flex gap-2">
            <span ref={reg('cloud')} title={remotes.map((r) => `${r.name} — ${r.ok ? 'ok' : 'down'}`).join('\n')}
              className="inline-flex items-center gap-1 rounded-md border border-border bg-card px-2 py-1 text-[11px] cursor-help">
              <Dot ok={remotes.length > 0 && remotes.every((r) => r.ok)} /> ☁ Cloud
              <span className="text-muted-foreground">{remotes.length}</span>
            </span>
            <span ref={reg('rclonebrowser')} className="inline-flex items-center gap-1 rounded-md border border-border bg-card px-2 py-1 text-[11px]">
              <Dot ok={rb?.running} muted={!rb?.running} /> rclonebrowser
            </span>
          </div>
          <div ref={reg('storage')} className="rounded-lg border-2 border-primary/60 p-2 space-y-2 min-w-[250px]">
            <div className="flex items-center justify-between text-[11px]">
              <span className="font-medium text-foreground">unionfs · mergerfs</span>
              <span className="text-muted-foreground">{union ? `${union.used}/${union.size} (${union.use_pct})` : '—'}</span>
            </div>
            {union && <Bar p={pct(union.use_pct)} />}
            <div className="rounded-md border border-border bg-card p-2">
              <div className="flex items-center gap-1.5 text-[11px]">
                <Dot ok={local?.ok} /><span className="font-medium text-foreground">Local · 8TB</span>
                <span className="ml-auto text-muted-foreground">{local ? `${local.used}/${local.size} (${local.use_pct})` : '—'}</span>
              </div>
              {local?.use_pct && <div className="mt-1.5"><Bar p={pct(local.use_pct)} /></div>}
            </div>
            <div className="rounded-md border border-border bg-card p-2">
              <div className="text-[11px] font-medium text-foreground mb-1">Remote mounts ×{remoteMounts.length}</div>
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
        </div>

        {/* serve */}
        <div className="flex flex-col gap-3"><RoleCard id="media" /><RoleCard id="scan" /><RoleCard id="stats" /></div>
      </div>
    </div>
  )
}
