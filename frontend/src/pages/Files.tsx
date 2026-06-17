/**
 * Files — one browser for everything. The left rail is split into two groups:
 *   • Disk   — mounted paths (merged unionfs / local / remotes / /opt), full
 *              file ops + upload/download via the Go executor.
 *   • rclone  — rclone remotes browsed directly (mounted or not) via lsjson,
 *              browse-only here; transfers are managed on the Transfers page.
 * Folders load lazily on navigation (the media tree is huge).
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { FileManager } from '@cubone/react-file-manager'
import '@cubone/react-file-manager/dist/style.css'
import { useRcloneRemotes } from '@/lib/api'
import { Boxes, HardDrive, Cloud, Package, Info } from 'lucide-react'
import { cn } from '@/lib/cn'

type CFile = { name: string; isDirectory: boolean; path: string; size?: number }
type Source = { kind: 'disk'; base: string } | { kind: 'rclone'; remote: string }

const DISK = [
  { base: '/mnt/unionfs', label: 'Merged (unionfs)', icon: Boxes },
  { base: '/mnt/local',   label: 'Local disk',       icon: HardDrive },
  { base: '/mnt/remote',  label: 'Remotes (mounted)', icon: Cloud },
  { base: '/opt',         label: 'Apps (/opt)',      icon: Package },
] as const

export function Files() {
  const { data: conf } = useRcloneRemotes()
  const remotes = useMemo(() => Object.keys(conf?.remotes ?? {}), [conf])

  const [source, setSource] = useState<Source>({ kind: 'disk', base: DISK[0].base })
  const [filesMap, setFilesMap] = useState<Record<string, CFile>>({})
  const [path, setPath] = useState('')
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string | null>(null)

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

  useEffect(() => { setPath(''); setFilesMap({}); setErr(null); loadFolder('') }, [source, loadFolder])

  // Make Ctrl+C/X/V/A work under the Thai keyboard (cubone matches on e.key).
  useEffect(() => {
    const norm = (e: KeyboardEvent) => {
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

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-xl font-semibold text-foreground">Files</h1>
        <p className="text-sm text-muted-foreground mt-0.5">
          Browse mounted disks and rclone remotes. Bulk transfers live on the Transfers page.
        </p>
      </div>

      <div className="flex gap-4">
        <aside className="w-52 shrink-0 space-y-3">
          <Group title="Disk">
            {DISK.map(({ base, label, icon: Icon }) => (
              <RailButton key={base} active={isDisk && source.base === base} icon={<Icon className="h-4 w-4 shrink-0" />}
                label={label} onClick={() => setSource({ kind: 'disk', base })} />
            ))}
          </Group>
          <Group title="rclone">
            {remotes.length === 0 && <p className="px-3 text-[11px] text-muted-foreground/60">No remotes</p>}
            {remotes.map((r) => (
              <RailButton key={r} active={!isDisk && source.remote === r} icon={<Cloud className="h-4 w-4 shrink-0" />}
                label={r} onClick={() => setSource({ kind: 'rclone', remote: r })} />
            ))}
          </Group>
          <p className="px-3 text-[11px] text-muted-foreground/70 font-mono break-all">{abs(path)}</p>
        </aside>

        <div className="flex-1 min-w-0">
          <div className="mb-2 flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <Info className="h-3 w-3 shrink-0" />
            {isDisk
              ? 'Full file ops + upload/download. Keyboard shortcuts work (Ctrl+C/X/V, Del, F2).'
              : 'rclone remote — browse only here. Use Transfers to copy/move/sync.'}
          </div>
          {err && <div className="mb-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-1.5 text-xs text-destructive break-all">{err}</div>}
          <FileManager
            key={isDisk ? `disk:${source.base}` : `rclone:${source.remote}`}
            files={files}
            initialPath=""
            isLoading={loading}
            onFolderChange={(p: string) => { setPath(p); loadFolder(p) }}
            onRefresh={() => loadFolder(path)}
            height="70vh"
            {...diskHandlers}
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
