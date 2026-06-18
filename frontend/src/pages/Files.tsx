/**
 * Files — one browser for everything. The left rail is split into two groups:
 *   • Disk   — mounted paths (merged unionfs / local / remotes / /opt), full
 *              file ops + upload/download via the Go executor.
 *   • rclone  — rclone remotes browsed directly (mounted or not) via lsjson,
 *              browse-only here; transfers are managed on the Transfers page.
 * Folders load lazily on navigation (the media tree is huge).
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { FileManager } from '@cubone/react-file-manager'
import '@cubone/react-file-manager/dist/style.css'
import { useRcloneRemotes, useTeldriveRemotes, useTeldriveSearch } from '@/lib/api'
import { Boxes, HardDrive, Cloud, Package, Info, Search, X, FolderOpen, Loader2 } from 'lucide-react'
import { cn } from '@/lib/cn'

type CFile = { name: string; isDirectory: boolean; path: string; size?: number }
type Source = { kind: 'disk'; base: string } | { kind: 'rclone'; remote: string }

// Seed ancestor folder entries so cubone can resolve a deep initialPath (it builds
// its tree from the flat files list — without the chain present AT MOUNT it falls
// back to root).
function seedAncestors(p: string): Record<string, CFile> {
  const seed: Record<string, CFile> = {}
  let acc = ''
  for (const part of p.split('/').filter(Boolean)) { acc += '/' + part; seed[acc] = { name: part, isDirectory: true, path: acc } }
  return seed
}

const DISK = [
  { base: '/mnt/unionfs', label: 'Merged (unionfs)', icon: Boxes },
  { base: '/mnt/local',   label: 'Local disk',       icon: HardDrive },
  { base: '/mnt/remote',  label: 'Remotes (mounted)', icon: Cloud },
  { base: '/opt',         label: 'Apps (/opt)',      icon: Package },
] as const

export function Files() {
  const { data: conf } = useRcloneRemotes()
  const remotes = useMemo(() => Object.keys(conf?.remotes ?? {}), [conf])

  // Deep-link from tgDrive search: /files?remote=X&path=/dir opens that folder.
  const [params, setParams] = useSearchParams()
  const linkRemote = params.get('remote')
  const linkPath = params.get('path') ?? ''

  const [source, setSource] = useState<Source>(linkRemote ? { kind: 'rclone', remote: linkRemote } : { kind: 'disk', base: DISK[0].base })
  const [initialPath, setInitialPath] = useState(linkRemote ? linkPath : '')
  const [filesMap, setFilesMap] = useState<Record<string, CFile>>(() => seedAncestors(linkRemote ? linkPath : ''))
  const [path, setPath] = useState(initialPath)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [feats, setFeats] = useState<Record<string, boolean> | null>(null)
  const [quota, setQuota] = useState<Record<string, number | string> | null>(null)
  const [fsize, setFsize] = useState<{ human: string; count: number } | null>(null)
  const [cats, setCats] = useState<{ category: string; human: string; files: number }[] | null>(null)
  const [sel, setSel] = useState<CFile[]>([])
  const [link, setLink] = useState<string | null>(null)
  const remoteType = source.kind === 'rclone' ? (conf?.remotes as Record<string, Record<string, string>> | undefined)?.[source.remote]?.type : undefined

  // Inline teldrive federated search (so you can find + jump without leaving Files).
  const { data: tdr } = useTeldriveRemotes()
  const hasTeldrive = (tdr?.remotes?.length ?? 0) > 0
  const [sq, setSq] = useState('')
  const [sterm, setSterm] = useState('')
  const { data: sres, isFetching: searching } = useTeldriveSearch(sterm)
  const jump = (remote: string, dir: string) => {
    const p = dir || ''
    setSterm(''); setSq(''); setParams({}, { replace: true })
    setFilesMap(seedAncestors(p)); setPath(p); setInitialPath(p); setSource({ kind: 'rclone', remote })
  }

  const files = useMemo(() => Object.values(filesMap), [filesMap])
  const isDisk = source.kind === 'disk'
  const abs = useCallback((rel: string) => (source.kind === 'disk' ? source.base + rel : `${source.remote}:${rel.replace(/^\//, '')}`), [source])

  const loadFolder = useCallback(async (rel: string) => {
    setLoading(true)
    try {
      const url = source.kind === 'disk'
        ? `/api/fs?path=${encodeURIComponent(source.base + rel)}`
        : `/api/rclone/ls?remote=${encodeURIComponent(source.remote)}&path=${encodeURIComponent(rel)}`
      const d = await (await fetch(url, { cache: 'no-store' })).json()
      setFilesMap((prev) => {
        const next = { ...prev }
        for (const e of (d.entries ?? []) as { type?: string; is_dir?: boolean; size: number; name: string }[]) {
          const dir = e.type === 'dir' || e.is_dir === true
          const p = `${rel}/${e.name}`
          next[p] = { name: e.name, isDirectory: dir, path: p, size: dir ? undefined : e.size }
        }
        return next
      })
    } catch { /* keep */ } finally { setLoading(false) }
  }, [source])

  useEffect(() => { setPath(initialPath); setFilesMap(seedAncestors(initialPath)); setErr(null); loadFolder(initialPath) }, [source, initialPath, loadFolder])

  // React to deep-link URL changes (navigating here from tgDrive search).
  useEffect(() => {
    if (linkRemote) { setSource({ kind: 'rclone', remote: linkRemote }); setInitialPath(linkPath) }
  }, [linkRemote, linkPath])

  // rclone remote: load capabilities (to gate ops) + quota + category breakdown.
  useEffect(() => {
    setFeats(null); setQuota(null); setFsize(null); setCats(null); setLink(null)
    if (source.kind !== 'rclone') return
    const r = encodeURIComponent(source.remote)
    fetch(`/api/rclone/fsinfo?remote=${r}`).then((x) => x.ok ? x.json() : null).then((d) => d && setFeats(d.features ?? {})).catch(() => {})
    fetch(`/api/rclone/about?remote=${r}`).then((x) => x.ok ? x.json() : null).then((d) => d && setQuota(d)).catch(() => {})
    if (remoteType === 'teldrive') fetch(`/api/rclone/categories?remote=${r}`).then((x) => x.ok ? x.json() : null).then((d) => d && setCats(d.categories)).catch(() => {})
  }, [source, remoteType])
  useEffect(() => { setFsize(null) }, [path])

  const calcSize = useCallback(async () => {
    setFsize(null); setErr(null)
    try {
      const url = source.kind === 'disk'
        ? `/api/fs/du?path=${encodeURIComponent(source.base + path)}`
        : `/api/rclone/size?remote=${encodeURIComponent(source.remote)}&path=${encodeURIComponent(path)}`
      const d = await (await fetch(url)).json()
      setFsize({ human: d.human, count: d.count })
    } catch { setErr('size failed') }
  }, [source, path])

  // Make Ctrl+C/X/V/A work under the Thai keyboard (cubone matches on e.key).
  useEffect(() => {
    const norm = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
      const m = /^Key([A-Z])$/.exec(e.code)
      if (!m) return
      const latin = m[1].toLowerCase()
      if (e.key.toLowerCase() !== latin) Object.defineProperty(e, 'key', { configurable: true, value: latin })
    }
    window.addEventListener('keydown', norm, true)
    window.addEventListener('keyup', norm, true)
    return () => { window.removeEventListener('keydown', norm, true); window.removeEventListener('keyup', norm, true) }
  }, [])

  // ── disk-only mutating ops ──────────────────────────────────────────────────
  const post = useCallback(async (url: string, body: unknown) => {
    setErr(null)
    const r = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    if (!r.ok) { const t = await r.text(); setErr(t || `${r.status}`); throw new Error(t) }
  }, [])
  const prune = useCallback((rels: string[]) => {
    setFilesMap((prev) => {
      const next = { ...prev }
      for (const rel of rels) for (const k of Object.keys(next)) if (k === rel || k.startsWith(rel + '/')) delete next[k]
      return next
    })
  }, [])

  const remoteAct = useCallback(async (kind: 'cleanup' | 'dedupe' | 'link') => {
    if (source.kind !== 'rclone') return
    const remote = source.remote
    if (kind === 'cleanup' && !confirm('Empty trash / remove old versions on this remote?')) return
    if (kind === 'dedupe' && !confirm('Merge duplicate files in this folder?')) return
    setErr(null); setLink(null)
    try {
      if (kind === 'link') {
        const d = await (await fetch('/api/rclone/link', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ remote, path: sel[0].path }) })).json()
        setLink(d.url)
      } else {
        await post(`/api/rclone/${kind}`, { remote, path })
        if (kind === 'dedupe') await loadFolder(path)
      }
    } catch { /* shown */ }
  }, [source, path, sel, post, loadFolder])

  const diskHandlers = isDisk ? {
    onCreateFolder: async (name: string, parent: CFile | undefined) => {
      try { await post('/api/fs/mkdir', { path: `${abs(parent?.path ?? path)}/${name}` }); await loadFolder(path) } catch { /* shown */ }
    },
    onRename: async (file: CFile, newName: string) => {
      try { await post('/api/fs/rename', { path: abs(file.path), name: newName }); prune([file.path]); await loadFolder(path) } catch { /* shown */ }
    },
    onDelete: async (items: CFile[]) => {
      try { await post('/api/fs/delete', { paths: items.map((f) => abs(f.path)) }); prune(items.map((f) => f.path)); await loadFolder(path) } catch { /* shown */ }
    },
    onPaste: async (items: CFile[], dest: CFile | undefined, op: 'copy' | 'move') => {
      try {
        await post(op === 'move' ? '/api/fs/move' : '/api/fs/copy', { paths: items.map((f) => abs(f.path)), dest: abs(dest?.path ?? path) })
        if (op === 'move') prune(items.map((f) => f.path))
        await loadFolder(path)
      } catch { /* shown */ }
    },
    fileUploadConfig: { url: '/api/fs/upload', method: 'POST' },
    onFileUploading: (_file: unknown, parent?: CFile) => ({ path: abs(parent?.path ?? path) }),
    onFileUploaded: () => loadFolder(path),
    onDownload: (items: CFile[]) => items.filter((f) => !f.isDirectory).forEach((f) => {
      const a = document.createElement('a')
      a.href = `/api/fs/download?path=${encodeURIComponent(abs(f.path))}`
      a.download = f.name
      document.body.appendChild(a); a.click(); a.remove()
    }),
  } : {}

  // rclone-side ops (within one remote) — wired to the rclone fs endpoints.
  const dirOf = (p: string) => p.slice(0, p.lastIndexOf('/'))
  const rcloneHandlers = source.kind === 'rclone' ? (() => {
    const remote = source.remote
    return {
      onCreateFolder: async (name: string, parent: CFile | undefined) => {
        try { await post('/api/rclone/mkdir', { remote, path: `${parent?.path ?? path}/${name}` }); await loadFolder(path) } catch { /* shown */ }
      },
      onRename: async (file: CFile, newName: string) => {
        try { await post('/api/rclone/moveto', { remote, src: file.path, dst: `${dirOf(file.path)}/${newName}` }); prune([file.path]); await loadFolder(path) } catch { /* shown */ }
      },
      onDelete: async (items: CFile[]) => {
        try {
          for (const f of items) await post('/api/rclone/delete', { remote, path: f.path, is_dir: f.isDirectory })
          prune(items.map((f) => f.path)); await loadFolder(path)
        } catch { /* shown */ }
      },
      onPaste: async (items: CFile[], dest: CFile | undefined, op: 'copy' | 'move') => {
        const d = dest?.path ?? path
        try {
          for (const f of items) await post(op === 'move' ? '/api/rclone/moveto' : '/api/rclone/copyto', { remote, src: f.path, dst: `${d}/${f.name}` })
          if (op === 'move') prune(items.map((f) => f.path))
          await loadFolder(path)
        } catch { /* shown */ }
      },
    }
  })() : {}

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-foreground">Files</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            Browse mounted disks and rclone remotes. Bulk transfers live on the Transfers page.
          </p>
        </div>
        {hasTeldrive && (
          <div className="relative w-96 shrink-0">
            <form onSubmit={(e) => { e.preventDefault(); setSterm(sq.trim()) }}>
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              <input
                value={sq} onChange={(e) => setSq(e.target.value)}
                onKeyDown={(e) => e.stopPropagation()} onPaste={(e) => e.stopPropagation()}
                placeholder="Search teldrive remotes…"
                className="h-9 w-full rounded-md border border-border bg-background pl-8 pr-8 text-sm" />
              {sterm && <button type="button" onClick={() => { setSterm(''); setSq('') }} className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"><X className="h-4 w-4" /></button>}
            </form>
            {sterm && (
              <div className="absolute z-30 mt-1 w-full max-h-[70vh] overflow-y-auto rounded-md border border-border bg-popover shadow-lg divide-y divide-border">
                {searching && (sres?.results.length ?? 0) === 0 && <div className="px-3 py-4 text-center text-xs text-muted-foreground flex items-center justify-center gap-1.5"><Loader2 className="h-3.5 w-3.5 animate-spin" />Searching…</div>}
                {!searching && (sres?.results.length ?? 0) === 0 && <div className="px-3 py-4 text-center text-xs text-muted-foreground">No matches for “{sterm}”.</div>}
                {sres?.results.map((r, i) => (
                  <button key={i} onClick={() => jump(r.remote, r.dir)} className="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-accent">
                    <span className="rounded bg-secondary px-1.5 py-0.5 text-[10px] font-medium shrink-0">{r.remote}</span>
                    <span className="min-w-0 flex-1">
                      <span className="block text-xs text-foreground truncate">{r.name}</span>
                      <span className="block text-[10px] text-muted-foreground truncate font-mono">{r.dir || '/'}{!r.is_dir && ` · ${r.human}`}</span>
                    </span>
                    <FolderOpen className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
      </div>

      <div className="flex gap-4">
        <aside className="w-52 shrink-0 space-y-3">
          <Group title="Disk">
            {DISK.map(({ base, label, icon: Icon }) => (
              <RailButton key={base} active={isDisk && source.base === base} icon={<Icon className="h-4 w-4 shrink-0" />}
                label={label} onClick={() => { setInitialPath(''); setParams({}, { replace: true }); setSource({ kind: 'disk', base }) }} />
            ))}
          </Group>
          <Group title="rclone">
            {remotes.length === 0 && <p className="px-3 text-[11px] text-muted-foreground/60">No remotes</p>}
            {remotes.map((r) => (
              <RailButton key={r} active={!isDisk && source.remote === r} icon={<Cloud className="h-4 w-4 shrink-0" />}
                label={r} onClick={() => { setInitialPath(''); setParams({}, { replace: true }); setSource({ kind: 'rclone', remote: r }) }} />
            ))}
          </Group>
          <p className="px-3 text-[11px] text-muted-foreground/70 font-mono break-all">{abs(path)}</p>
        </aside>

        <div className="flex-1 min-w-0">
          <div className="mb-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
            <span className="flex items-center gap-1.5">
              <Info className="h-3 w-3 shrink-0" />
              {isDisk
                ? 'Full file ops + upload/download. Shortcuts: Ctrl+C/X/V, Del, F2.'
                : 'rclone remote — create / rename / move / copy / delete. Use Transfers for cross-remote.'}
            </span>
            {!isDisk && quota && quota.used_human != null && (
              (quota.total as number) > 0
                ? <span>Quota: <span className="text-foreground">{quota.used_human as string}</span> / {quota.total_human as string} used · {quota.free_human as string} free</span>
                : <span>Used: <span className="text-foreground">{quota.used_human as string}</span> <span className="text-muted-foreground/60">(unlimited)</span></span>
            )}
            <span className="flex items-center gap-1.5">
              <button onClick={calcSize} className="text-primary hover:underline">Folder size</button>
              {fsize && <span className="text-foreground">{fsize.human} · {fsize.count} files</span>}
            </span>
            {!isDisk && feats?.CleanUp && <button onClick={() => remoteAct('cleanup')} className="text-primary hover:underline">Cleanup trash</button>}
            {!isDisk && feats?.MergeDirs && <button onClick={() => remoteAct('dedupe')} className="text-primary hover:underline">Dedupe</button>}
            {!isDisk && feats?.PublicLink && sel.length === 1 && <button onClick={() => remoteAct('link')} className="text-primary hover:underline">Public link</button>}
          </div>
          {link && (
            <div className="mb-2 flex items-center gap-2 rounded-md border border-border bg-secondary/30 px-3 py-1.5 text-xs">
              <span className="font-mono truncate flex-1">{link}</span>
              <button onClick={() => navigator.clipboard?.writeText(link)} className="text-primary hover:underline shrink-0">Copy</button>
              <button onClick={() => setLink(null)} className="text-muted-foreground hover:text-foreground shrink-0">✕</button>
            </div>
          )}
          {!isDisk && cats && cats.length > 0 && (
            <div className="mb-2 flex flex-wrap gap-1.5">
              {cats.map((c) => (
                <span key={c.category} className="rounded-md border border-border bg-secondary/30 px-2 py-0.5 text-[11px]">
                  <span className="capitalize text-foreground">{c.category}</span> <span className="text-muted-foreground">{c.human} · {c.files}</span>
                </span>
              ))}
            </div>
          )}
          {err && <div className="mb-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-1.5 text-xs text-destructive break-all">{err}</div>}
          <FileManager
            key={isDisk ? `disk:${source.base}` : `rclone:${source.remote}:${initialPath}`}
            files={files}
            initialPath={initialPath}
            isLoading={loading}
            onFolderChange={(p: string) => { setPath(p); loadFolder(p); setSel([]) }}
            onRefresh={() => loadFolder(path)}
            onSelect={(f: CFile[]) => setSel(f)}
            onSelectionChange={(f: CFile[]) => setSel(f)}
            height="70vh"
            {...diskHandlers}
            {...rcloneHandlers}
          />
        </div>
      </div>
    </div>
  )
}

function Group({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="px-3 mb-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground/60">{title}</p>
      <div className="space-y-1">{children}</div>
    </div>
  )
}

function RailButton({ active, icon, label, onClick }: { active: boolean; icon: React.ReactNode; label: string; onClick: () => void }) {
  return (
    <button onClick={onClick} className={cn(
      'flex items-center gap-2.5 w-full px-3 py-2 rounded-md text-sm transition-colors text-left',
      active ? 'bg-primary text-primary-foreground font-medium' : 'text-muted-foreground hover:text-foreground hover:bg-accent',
    )}>
      {icon}<span className="truncate">{label}</span>
    </button>
  )
}
