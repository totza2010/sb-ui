import { Fragment, useEffect, useMemo, useState, useCallback, useRef, createContext, useContext, type MouseEvent } from 'react'
import {
  ReactFlow, ReactFlowProvider, Background, Controls, Panel, Handle, Position, MarkerType,
  BaseEdge, EdgeLabelRenderer, getBezierPath, ConnectionMode,
  addEdge, reconnectEdge, useNodesState, useEdgesState, useReactFlow, useNodesInitialized, useInternalNode,
  type Node, type Edge, type NodeProps, type EdgeProps, type InternalNode, type Connection, type XYPosition,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Link2, Unlink, Pencil, Check, Plus, RotateCcw, Loader2 } from 'lucide-react'
import { useMounts, useStorage, useContainers, useRcloneRemotes, type ContainerInfo, type RemoteInfo, type MountDetail } from '@/lib/api'
import { cn } from '@/lib/cn'

// `always`: keep this slot visible even with no matching container (e.g. cloudplow
// runs as a service/cron, not a container, and is often disabled — show it as a
// prepared slot). `note`: muted placeholder text shown when empty.
type Sub = { label: string; app: string; re: RegExp; companion?: RegExp; always?: boolean; note?: string }
const CATS: Record<string, { title: string; subs: Sub[] }> = {
  indexers: { title: 'Indexers', subs: [
    { label: 'Prowlarr', app: 'prowlarr', re: /^prowlarr/i }, { label: 'Jackett', app: 'jackett', re: /^jackett/i },
    { label: 'NZBHydra', app: 'nzbhydra', re: /^nzbhydra/i },
  ] },
  requesters: { title: 'Requesters', subs: [
    { label: 'Jellyseerr', app: 'jellyseerr', re: /^jellyseerr/i }, { label: 'Overseerr', app: 'overseerr', re: /^overseerr/i },
  ] },
  downloaders: { title: 'Downloaders', subs: [
    { label: 'Bittorrent', app: 'qbittorrent', re: /^(qbittorrent|deluge|transmission)/i },
    { label: 'Usenet', app: 'sabnzbd', re: /^(sabnzbd|nzbget)/i },
  ] },
  importers: { title: 'Importers', subs: [
    { label: 'TV', app: 'sonarr', re: /^sonarr/i }, { label: 'Movies', app: 'radarr', re: /^radarr/i },
    { label: 'Music', app: 'lidarr', re: /^lidarr/i }, { label: 'Books', app: 'readarr', re: /^readarr/i },
    { label: 'Adult', app: 'whisparr', re: /^whisparr/i }, { label: 'Subtitles', app: 'bazarr', re: /^bazarr/i },
  ] },
  scaners: { title: 'Scaners', subs: [{ label: 'Autoscan', app: 'autoscan', re: /^autoscan/i }] },
  media: { title: 'Media Servers', subs: [
    { label: 'Plex', app: 'plex', re: /^plex/i, companion: /^tautulli/i },
    { label: 'Jellyfin', app: 'jellyfin', re: /^jellyfin/i, companion: /^jellystat/i },
    { label: 'Emby', app: 'emby', re: /^emby/i, companion: /^embystat/i },
  ] },
  uploaders: { title: 'Uploaders', subs: [
    { label: 'Rclone Browser', app: 'rclonebrowser', re: /^rclonebrowser/i },
    { label: 'Cloudplow', app: 'cloudplow', re: /^cloudplow/i, always: true, note: 'disabled' },
  ] },
}

// Curated default layout (baked from the arranged dev layout). Prod uses this;
// dev can re-arrange (persisted to localStorage).
const POS: Record<string, XYPosition> = {
  indexers: { x: 32, y: 144 }, requesters: { x: 32, y: 336 },
  downloaders: { x: 288, y: 16 }, importers: { x: 288, y: 176 },
  storage: { x: 544, y: 0 }, scaners: { x: 608, y: 336 },
  uploaders: { x: 912, y: 0 }, media: { x: 912, y: 256 },
  clounds: { x: 1140, y: 0 },
}

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
const Dot = ({ ok, muted }: { ok?: boolean; muted?: boolean }) =>
  <span className={cn('h-1.5 w-1.5 rounded-full shrink-0', muted ? 'bg-muted-foreground/50' : ok ? 'bg-success' : 'bg-destructive')} />
const Bar = ({ p }: { p: number }) =>
  <div className="w-full bg-secondary rounded-full h-1.5"><div className={cn('h-1.5 rounded-full', barCls(p))} style={{ width: `${Math.min(p, 100)}%` }} /></div>

// hover tooltip rendered at top level (fixed, follows cursor) so React Flow's
// overflow:hidden can't clip it. Rows spread tipProps(text) onto their div.
type TipState = { text: string; x: number; y: number }
const TipCtx = createContext<(t: TipState | null) => void>(() => {})
function useTipProps() {
  const setTip = useContext(TipCtx)
  return (text: string) => ({
    onMouseEnter: (e: MouseEvent) => setTip({ text, x: e.clientX, y: e.clientY }),
    onMouseMove: (e: MouseEvent) => setTip({ text, x: e.clientX, y: e.clientY }),
    onMouseLeave: () => setTip(null),
  })
}

const SIDES = [['left', Position.Left], ['right', Position.Right], ['top', Position.Top], ['bottom', Position.Bottom]] as const
const hStyle = { opacity: 0, width: 8, height: 8, border: 0, background: 'transparent' }
function Handles() {
  return <>{SIDES.map(([id, pos]) => (
    <Fragment key={id}>
      <Handle id={`${id}-s`} type="source" position={pos} style={hStyle} />
      <Handle id={`${id}-t`} type="target" position={pos} style={hStyle} />
    </Fragment>
  ))}</>
}

// ── nodes ────────────────────────────────────────────────────────────────────
type CatData = { title: string; subs: { label: string; app: string; prim: ContainerInfo[]; comp: ContainerInfo[]; note?: string }[] }
function CategoryNode({ data }: NodeProps<Node<CatData>>) {
  const tip = useTipProps()
  const row = (c: ContainerInfo, app: string, companion = false) => (
    <div key={c.id} className={cn('flex items-center gap-1.5 cursor-help', companion && 'ml-3')}
      {...tip(`${c.name} · ${c.running ? 'running' : c.status}${c.image ? ' · ' + c.image : ''}`)}>
      <Dot ok={c.running} /><span className={cn('text-[11px]', companion ? 'text-muted-foreground' : 'text-foreground')}>{prettyName(c.name, app)}</span>
    </div>
  )
  return (
    <div className="rounded-lg border-2 border-border bg-card p-2 space-y-2 w-[170px] cursor-default">
      <div className="text-xs font-semibold text-foreground">{data.title}</div>
      {data.subs.map((s) => {
        const empty = s.prim.length === 0 && s.comp.length === 0
        return (
          <div key={s.label} className="rounded-md border border-border bg-secondary/50 p-1.5">
            <div className="text-[11px] font-medium text-foreground mb-1">{s.label}{!empty && <span className="text-muted-foreground"> ×{s.prim.length}</span>}</div>
            <div className="space-y-0.5">
              {s.prim.map((c) => row(c, s.app))}{s.comp.map((c) => row(c, s.app, true))}
              {empty && <div className="flex items-center gap-1.5"><Dot muted /><span className="text-[11px] text-muted-foreground">{s.note ?? 'not deployed'}</span></div>}
            </div>
          </div>
        )
      })}
      <Handles />
    </div>
  )
}

// unionfs = group box; Local & Remote are child nodes → edges attach to their borders.
function UnionfsGroupNode({ data }: NodeProps<Node<{ union?: MountDetail }>>) {
  const u = data.union
  return (
    <div className="w-full h-full rounded-lg border-2 border-primary/60 bg-card cursor-default">
      <div className="p-2">
        <div className="flex items-center justify-between text-[11px]">
          <span className="font-semibold text-foreground">unionfs · mergerfs</span>
          <span className="text-muted-foreground">{u ? `${u.used}/${u.size} (${u.use_pct})` : '—'}</span>
        </div>
        {u && <div className="mt-1.5"><Bar p={pct(u.use_pct)} /></div>}
      </div>
      <Handles />
    </div>
  )
}
function LocalNode({ data }: NodeProps<Node<{ local?: MountDetail | null }>>) {
  const l = data.local
  const tip = useTipProps()
  return (
    <div className="w-[266px] rounded-md border border-border bg-secondary/70 p-1.5 cursor-default">
      <div className="flex items-center gap-1.5 text-[11px] cursor-help"
        {...(l ? tip(`${l.target} · ${l.used}/${l.size} (${l.use_pct}) · ${l.detail}`) : {})}>
        <Dot ok={l?.ok} /><span className="font-medium text-foreground">Local · 8TB</span>
        <span className="ml-auto text-muted-foreground">{l ? `${l.used}/${l.size} (${l.use_pct})` : '—'}</span>
      </div>
      {l?.use_pct && <div className="mt-1.5"><Bar p={pct(l.use_pct)} /></div>}
      <Handles />
    </div>
  )
}
function RemoteNode({ data }: NodeProps<Node<{ remotes: MountDetail[] }>>) {
  const tip = useTipProps()
  return (
    <div className="w-[266px] rounded-md border border-border bg-secondary/70 p-1.5 cursor-default">
      <div className="text-[11px] font-medium text-foreground mb-1">Remote mounts <span className="text-muted-foreground">×{data.remotes.length}</span></div>
      <div className="space-y-1.5">
        {data.remotes.map((m) => (
          <div key={m.target}>
            <div className="flex items-center gap-1.5 cursor-help"
              {...tip(`${m.target} · ${m.kind} · ${m.used}/${m.size} (${m.use_pct}) · ${m.detail}`)}>
              <Dot ok={m.ok} /><span className="font-mono text-[11px] text-foreground">{base(m.target)}</span>
              <span className="ml-auto text-[10px] text-muted-foreground">{m.used}/{m.size} ({m.use_pct})</span>
            </div>
            {m.use_pct && <div className="mt-1"><Bar p={pct(m.use_pct)} /></div>}
          </div>
        ))}
      </div>
      <Handles />
    </div>
  )
}

type CRemote = { name: string; type: string; ok: boolean; pending: boolean }
type CloundsData = { byType: Record<string, CRemote[]>; mounted: Set<string>; mountPath: Record<string, string> }
function CloundsNode({ data }: NodeProps<Node<CloundsData>>) {
  const tip = useTipProps()
  const types = Object.entries(data.byType)
  return (
    <div className="rounded-lg border-2 border-border bg-card p-2 space-y-2 w-[190px] cursor-default">
      <div className="text-xs font-semibold text-foreground">Clounds</div>
      {types.length === 0 && (
        <div className="rounded-md border border-border bg-secondary/50 p-1.5 flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <Loader2 className="h-3 w-3 animate-spin shrink-0" />Checking remotes…
        </div>
      )}
      {types.map(([type, rs]) => (
        <div key={type} className="rounded-md border border-border bg-secondary/50 p-1.5">
          <div className="text-[11px] font-medium text-foreground mb-1">{type} <span className="text-muted-foreground">×{rs.length}</span></div>
          <div className="space-y-0.5">
            {rs.map((r) => (
              <div key={r.name} className="flex items-center gap-1.5 cursor-help"
                {...tip(`${r.name} · ${r.type} · ${r.pending ? 'checking…' : r.ok ? 'reachable' : 'unreachable'} · ${data.mounted.has(r.name) ? 'mounted: ' + data.mountPath[r.name] : 'not mounted'}`)}>
                {r.pending
                  ? <Loader2 className="h-3 w-3 animate-spin text-muted-foreground shrink-0" />
                  : <Dot ok={r.ok} />}
                <span className="font-mono text-[11px] text-foreground">{r.name}</span>
                {data.mounted.has(r.name) ? <Link2 className="h-3 w-3 text-success shrink-0" /> : <Unlink className="h-3 w-3 text-muted-foreground/40 shrink-0" />}
              </div>
            ))}
          </div>
        </div>
      ))}
      <Handles />
    </div>
  )
}

function NoteNode({ data }: NodeProps<Node<{ label: string }>>) {
  return (
    <div className="rounded-lg border-2 border-dashed border-warning bg-card px-3 py-2 text-xs font-medium text-foreground min-w-[90px] cursor-default">
      {data.label}
      <Handles />
    </div>
  )
}

const nodeTypes = { category: CategoryNode, ugroup: UnionfsGroupNode, lnode: LocalNode, rnode: RemoteNode, clounds: CloundsNode, note: NoteNode }

// ── floating edges (attach to the nearest box border, follow boxes) ───────────
function intersect(a: InternalNode, b: InternalNode) {
  const w = (a.measured?.width ?? 0) / 2, h = (a.measured?.height ?? 0) / 2
  const ax = a.internals.positionAbsolute.x + w, ay = a.internals.positionAbsolute.y + h
  const bx = b.internals.positionAbsolute.x + (b.measured?.width ?? 0) / 2
  const by = b.internals.positionAbsolute.y + (b.measured?.height ?? 0) / 2
  const xx = (bx - ax) / (2 * w) - (by - ay) / (2 * h)
  const yy = (bx - ax) / (2 * w) + (by - ay) / (2 * h)
  const k = 1 / (Math.abs(xx) + Math.abs(yy) || 1)
  return { x: w * (k * xx + k * yy) + ax, y: h * (-k * xx + k * yy) + ay }
}
function sideOf(n: InternalNode, p: { x: number; y: number }) {
  const nx = n.internals.positionAbsolute.x, ny = n.internals.positionAbsolute.y
  const w = n.measured?.width ?? 0, h = n.measured?.height ?? 0
  if (p.x <= nx + 1) return Position.Left
  if (p.x >= nx + w - 1) return Position.Right
  if (p.y <= ny + 1) return Position.Top
  return Position.Bottom
}
function FloatingEdge({ id, source, target, markerEnd, style, label, labelStyle, labelBgStyle }: EdgeProps) {
  const s = useInternalNode(source), t = useInternalNode(target)
  if (!s || !t) return null
  const sp = intersect(s, t), tp = intersect(t, s)
  const [path, lx, ly] = getBezierPath({
    sourceX: sp.x, sourceY: sp.y, sourcePosition: sideOf(s, sp),
    targetX: tp.x, targetY: tp.y, targetPosition: sideOf(t, tp),
  })
  return (
    <>
      <BaseEdge id={id} path={path} markerEnd={markerEnd} style={style} />
      {label && (
        <EdgeLabelRenderer>
          <div className="nodrag nopan" style={{ position: 'absolute', transform: `translate(-50%,-50%) translate(${lx}px,${ly}px)`, padding: '0 3px', borderRadius: 3, ...labelBgStyle, ...labelStyle }}>
            {label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
}
const edgeTypes = { floating: FloatingEdge }

// edges: [source, target, label, control?, sourceHandle?, targetHandle?]
// when handles are given → fixed (smoothstep, attaches at that side's center);
// otherwise floating (attaches to the facing border).
const E: [string, string, string, boolean?, string?, string?][] = [
  ['requesters', 'importers', 'request'],
  ['indexers', 'importers', 'search'],
  ['importers', 'downloaders', 'grab'],
  ['downloaders', 'local', 'download', false, 'right-s', 'left-t'],
  ['importers', 'storage', 'import'],
  ['storage', 'media', 'read'],
  ['importers', 'scaners', 'webhook', true],
  ['scaners', 'media', 'scan', true],
  ['local', 'uploaders', 'move', true, 'right-s', 'left-t'],
  ['uploaders', 'clounds', 'upload'],
  ['clounds', 'remote', 'rclone_vfs', false, 'left-s', 'right-t'],
]
const edgeStyle = (ctrl?: boolean, fixed?: boolean): Partial<Edge> => ({
  type: fixed ? 'smoothstep' : 'floating',
  markerEnd: { type: MarkerType.ArrowClosed, color: 'var(--color-primary)' },
  style: { stroke: 'var(--color-primary)', strokeWidth: 1.75, strokeDasharray: ctrl ? '5 4' : undefined },
  labelStyle: { fontSize: 9, fill: 'var(--color-foreground)' },
  labelBgStyle: { fill: 'var(--color-background)' },
})

// ── persistence ──────────────────────────────────────────────────────────────
const EDGE_VER = 6 // bump when the default edge wiring changes → new storage key, old data abandoned
const LS = `sb-ui:storageflow-${EDGE_VER}` // key carries the version so stale layouts can't be re-saved under a new version
type SavedEdge = { source: string; target: string; label?: string; ctrl?: boolean; sh?: string | null; th?: string | null }
type Saved = { v?: number; positions?: Record<string, XYPosition>; edges?: SavedEdge[]; hidden?: string[]; notes?: { id: string; position: XYPosition; label: string }[] }
// Editing + layout persistence are dev-only; production shows the baked default layout.
const isDev = import.meta.env.DEV
const load = (): Saved => { if (!isDev) return {}; try { return JSON.parse(localStorage.getItem(LS) || '{}') } catch { return {} } }
const persist = (s: Saved) => { if (!isDev) return; try { localStorage.setItem(LS, JSON.stringify(s)) } catch { /* ignore */ } }

export function StorageFlow() {
  const { data: storage } = useStorage()
  const { data: mounts } = useMounts()
  const { data: containers } = useContainers()
  const { data: rconf } = useRcloneRemotes() // fast: remote names + types from rclone.conf

  const cs = containers ?? []
  const list = mounts ?? []
  const remoteMounts = list.filter((m) => m.target.startsWith('/mnt/remote'))
  const union = list.find((m) => m.kind === 'mergerfs' || m.target.includes('unionfs'))
  const remotes = storage?.remotes ?? []

  const dataNodes = useMemo<Node[]>(() => {
    // Remote list comes from rclone.conf (fast) so the box renders full-size
    // immediately; per-remote reachability is merged from the storage probe
    // (slow `rclone about`) — until it lands each row shows a spinner.
    const statusByName: Record<string, RemoteInfo> = {}
    for (const r of remotes) statusByName[r.name] = r
    const byType: Record<string, CRemote[]> = {}
    for (const [name, props] of Object.entries(rconf?.remotes ?? {})) {
      const type = props.type || 'other'
      const st = statusByName[name]
      ;(byType[type] ??= []).push({ name, type, ok: st?.ok ?? false, pending: !storage && !st })
    }
    const mounted = new Set(remoteMounts.map((m) => base(m.target)))
    const mountPath: Record<string, string> = {}
    remoteMounts.forEach((m) => { mountPath[base(m.target)] = m.target })

    const out: Node[] = []
    for (const [key, cat] of Object.entries(CATS)) {
      const subs = cat.subs.map((s) => ({
        label: s.label, app: s.app, note: s.note, always: s.always,
        prim: cs.filter((c) => s.re.test(c.name)),
        comp: s.companion ? cs.filter((c) => s.companion!.test(c.name)) : [],
      })).filter((x) => x.prim.length || x.comp.length || x.always)
      if (subs.length) out.push({ id: key, type: 'category', position: POS[key], data: { title: cat.title, subs } })
    }
    const gH = 156 + remoteMounts.length * 36 // generous so the Remote card never overflows
    out.push({ id: 'storage', type: 'ugroup', position: POS.storage, data: { union }, style: { width: 292, height: gH } })
    out.push({ id: 'local', type: 'lnode', parentId: 'storage', extent: 'parent', position: { x: 12, y: 46 }, data: { local: storage?.local } })
    out.push({ id: 'remote', type: 'rnode', parentId: 'storage', extent: 'parent', position: { x: 12, y: 100 }, data: { remotes: remoteMounts } })
    // Always render Clounds so it appears immediately (list from rclone.conf);
    // per-remote status fills in once the storage probe returns.
    out.push({ id: 'clounds', type: 'clounds', position: POS.clounds, data: { byType, mounted, mountPath } })
    return out
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [containers, mounts, storage, rconf])

  const defaultEdges = useMemo<Edge[]>(() => {
    const present = new Set(dataNodes.map((n) => n.id)) // includes storage/local/remote/clounds
    return E.filter(([s, t]) => present.has(s) && present.has(t)).map(([s, t, label, ctrl, sh, th], i) => ({
      id: `e${i}`, source: s, target: t, label, sourceHandle: sh, targetHandle: th, ...edgeStyle(ctrl, !!(sh && th)),
    }))
  }, [dataNodes])

  return (
    <div className="w-full h-full">
      <ReactFlowProvider>
        <Flow dataNodes={dataNodes} defaultEdges={defaultEdges} />
      </ReactFlowProvider>
    </div>
  )
}

function Flow({ dataNodes, defaultEdges }: { dataNodes: Node[]; defaultEdges: Edge[] }) {
  const rf = useReactFlow()
  const inited = useNodesInitialized()
  const [edit, setEdit] = useState(false)
  const [tip, setTip] = useState<TipState | null>(null)
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])

  // reconcile live data into node state, keeping user positions + notes
  useEffect(() => {
    const saved = load()
    const hidden = new Set(saved.hidden ?? [])
    setNodes((prev) => {
      const prevPos = new Map(prev.map((n) => [n.id, n.position]))
      const built = dataNodes
        .filter((n) => !hidden.has(n.id))
        .map((n) => n.parentId
          ? { ...n, draggable: false, selectable: false, deletable: false } // child nodes (Local/Remote) are fixed inside the box
          : { ...n, position: prevPos.get(n.id) ?? saved.positions?.[n.id] ?? n.position, draggable: edit, deletable: edit })
      const prevNotes = prev.filter((n) => n.type === 'note')
      const notes = (prevNotes.length ? prevNotes : (saved.notes ?? []).map((nt) => ({ id: nt.id, type: 'note', position: nt.position, data: { label: nt.label } } as Node)))
        .map((n) => ({ ...n, draggable: edit, deletable: edit }))
      return [...built, ...notes]
    })
  }, [dataNodes, edit, setNodes])

  // sync edges from saved (current version) or defaults — re-runs if the default
  // wiring changes, unless the user has hand-edited edges this session.
  const edgesEdited = useRef(false)
  useEffect(() => {
    if (edgesEdited.current) return
    const saved = load()
    if (saved.v === EDGE_VER && saved.edges?.length) {
      setEdges(saved.edges.map((e, i) => ({ id: `s${i}`, source: e.source, target: e.target, label: e.label, sourceHandle: e.sh, targetHandle: e.th, ...edgeStyle(e.ctrl, !!(e.sh && e.th)) })))
    } else {
      setEdges(defaultEdges)
    }
  }, [defaultEdges, setEdges])

  // fit once measured
  useEffect(() => { if (inited) rf.fitView({ padding: 0.12 }) }, [inited, rf])

  // persist (debounced)
  useEffect(() => {
    const id = setTimeout(() => {
      const positions: Record<string, XYPosition> = {}
      nodes.forEach((n) => { if (!n.parentId) positions[n.id] = n.position }) // child nodes are fixed
      const notes = nodes.filter((n) => n.type === 'note').map((n) => ({ id: n.id, position: n.position, label: (n.data as { label: string }).label }))
      const visible = new Set(nodes.map((n) => n.id))
      const hidden = dataNodes.map((n) => n.id).filter((id) => !visible.has(id))
      const savedEdges: SavedEdge[] = edges.map((e) => ({
        source: e.source, target: e.target, label: e.label as string | undefined, ctrl: !!(e.style?.strokeDasharray),
        sh: e.sourceHandle, th: e.targetHandle,
      }))
      persist({ v: EDGE_VER, positions, notes, hidden, edges: savedEdges })
    }, 500)
    return () => clearTimeout(id)
  }, [nodes, edges, dataNodes])

  const onConnect = useCallback((c: Connection) => {
    edgesEdited.current = true
    setEdges((eds) => addEdge({ ...c, ...edgeStyle(false) }, eds))
  }, [setEdges])

  // drag an edge endpoint to a different handle to re-route it
  const onReconnect = useCallback((oldEdge: Edge, newConn: Connection) => {
    edgesEdited.current = true
    setEdges((eds) => reconnectEdge(oldEdge, newConn, eds))
  }, [setEdges])

  const onDelete = useCallback(() => { edgesEdited.current = true }, [])

  const addNote = () => {
    const label = window.prompt('Box label?')
    if (!label) return
    const c = rf.screenToFlowPosition({ x: window.innerWidth / 2, y: 300 })
    setNodes((ns) => [...ns, { id: `note-${Date.now()}`, type: 'note', position: c, data: { label }, draggable: true, deletable: true }])
  }

  const reset = () => {
    localStorage.removeItem(LS)
    edgesEdited.current = false
    setNodes(dataNodes.map((n) => ({ ...n, draggable: edit && !n.parentId, deletable: edit && !n.parentId })))
    setEdges(defaultEdges)
    setTimeout(() => rf.fitView({ padding: 0.12 }), 50)
  }

  const btn = 'flex items-center gap-1 rounded-md border border-border bg-card px-2 py-1 text-xs text-foreground hover:bg-accent'

  return (
    <TipCtx.Provider value={setTip}>
      <ReactFlow
        className={edit ? 'sf-edit' : undefined}
        nodes={nodes} edges={edges} nodeTypes={nodeTypes} edgeTypes={edgeTypes}
        onNodesChange={onNodesChange} onEdgesChange={onEdgesChange} onConnect={onConnect} onReconnect={onReconnect} onDelete={onDelete}
        connectionMode={ConnectionMode.Loose}
        snapToGrid snapGrid={[16, 16]}
        fitView fitViewOptions={{ padding: 0.12 }}
        nodesDraggable={edit} nodesConnectable={edit} elementsSelectable={edit} edgesReconnectable={edit}
        deleteKeyCode={edit ? ['Backspace', 'Delete'] : null}
        zoomOnScroll={false} preventScrolling={false}
        proOptions={{ hideAttribution: true }} minZoom={0.2}
      >
        <Background gap={16} className="opacity-50" />
        <Controls showInteractive={false} />
        {isDev && (
          <Panel position="top-right" className="flex gap-1">
            {edit && <button className={btn} onClick={addNote}><Plus className="h-3.5 w-3.5" />Box</button>}
            {edit && <button className={btn} onClick={reset}><RotateCcw className="h-3.5 w-3.5" />Reset</button>}
            <button className={cn(btn, edit && 'bg-primary text-primary-foreground')} onClick={() => setEdit((e) => !e)}>
              {edit ? <><Check className="h-3.5 w-3.5" />Done</> : <><Pencil className="h-3.5 w-3.5" />Edit</>}
            </button>
          </Panel>
        )}
      </ReactFlow>
      {tip && (() => {
        const flip = tip.x > window.innerWidth - 320
        return (
          <div className="pointer-events-none fixed z-50 max-w-[300px] break-words rounded-md border border-border bg-card px-2 py-1 text-[11px] text-foreground shadow-lg"
            style={{ left: tip.x + (flip ? -14 : 14), top: tip.y + 14, transform: flip ? 'translateX(-100%)' : undefined }}>
            {tip.text}
          </div>
        )
      })()}
    </TipCtx.Provider>
  )
}
