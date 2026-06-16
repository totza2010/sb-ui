/**
 * Files — disk file manager for mounted Saltbox paths, built on
 * @cubone/react-file-manager. Shortcuts jump between the merged unionfs view,
 * the local disk, the per-remote mounts, and app config under /opt. Folders are
 * loaded lazily on navigation (the media tree is huge).
 *
 * P1 (this): browse + size + shortcuts. File ops (create/rename/delete/move) and
 * upload/download land in later phases; rclone remote transfers live separately.
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { FileManager } from '@cubone/react-file-manager'
import '@cubone/react-file-manager/dist/style.css'
import { Boxes, HardDrive, Cloud, Package, Info } from 'lucide-react'
import { cn } from '@/lib/cn'

type CFile = { name: string; isDirectory: boolean; path: string; size?: number; updatedAt?: string }

const SHORTCUTS = [
  { key: 'merged', label: 'Merged (unionfs)', base: '/mnt/unionfs', icon: Boxes },
  { key: 'local',  label: 'Local disk',       base: '/mnt/local',   icon: HardDrive },
  { key: 'remote', label: 'Remotes',          base: '/mnt/remote',  icon: Cloud },
  { key: 'apps',   label: 'Apps (/opt)',      base: '/opt',         icon: Package },
] as const

export function Files() {
  const [base, setBase] = useState<string>(SHORTCUTS[0].base)
  // Accumulate loaded entries by path so cubone's folder-tree pane has the whole
  // explored hierarchy (not just the current folder).
  const [filesMap, setFilesMap] = useState<Record<string, CFile>>({})
  const [loading, setLoading] = useState(false)
  const [path, setPath] = useState('') // current folder, relative to base ('' = root)

  const files = useMemo(() => Object.values(filesMap), [filesMap])

  // Fetch one folder's direct children and merge them in. cubone paths are
  // relative to the chosen base; absolute host path = base + cubonePath.
  const loadFolder = useCallback(async (rel: string) => {
    setLoading(true)
    try {
      const abs = base + rel
      const r = await fetch(`/api/fs?path=${encodeURIComponent(abs)}`, { cache: 'no-store' })
      const d = await r.json()
      setFilesMap((prev) => {
        const next = { ...prev }
        for (const e of (d.entries ?? []) as { type: string; size: number; name: string }[]) {
          const p = `${rel}/${e.name}`
          next[p] = { name: e.name, isDirectory: e.type === 'dir', path: p, size: e.type === 'file' ? e.size : undefined }
        }
        return next
      })
    } catch {
      /* keep what we have */
    } finally {
      setLoading(false)
    }
  }, [base])

  // Reset + load root when the shortcut/base changes.
  useEffect(() => { setPath(''); setFilesMap({}); loadFolder('') }, [base, loadFolder])

  // cubone matches shortcuts on `e.key`, which becomes a Thai glyph under the Thai
  // layout (Ctrl+C → e.key='แ') so Ctrl+C/X/V/A stop working. Normalize the key
  // from the physical `e.code` in the capture phase (before cubone's listener).
  // Done on both keydown and keyup so cubone's held-keys set stays symmetric, and
  // it never changes the character actually typed into inputs (only the JS event).
  useEffect(() => {
    const norm = (e: KeyboardEvent) => {
      const m = /^Key([A-Z])$/.exec(e.code)
      if (!m) return
      const latin = m[1].toLowerCase()
      if (e.key.toLowerCase() !== latin) {
        Object.defineProperty(e, 'key', { configurable: true, value: latin })
      }
    }
    window.addEventListener('keydown', norm, true)
    window.addEventListener('keyup', norm, true)
    return () => {
      window.removeEventListener('keydown', norm, true)
      window.removeEventListener('keyup', norm, true)
    }
  }, [])

  const [err, setErr] = useState<string | null>(null)
  const abs = useCallback((rel: string) => base + rel, [base])

  const post = useCallback(async (url: string, body: unknown) => {
    setErr(null)
    const r = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    if (!r.ok) { const t = await r.text(); setErr(t || `${r.status}`); throw new Error(t) }
  }, [])

  // Drop cached entries at these rel paths (and their descendants) after delete/move.
  const prune = useCallback((rels: string[]) => {
    setFilesMap((prev) => {
      const next = { ...prev }
      for (const rel of rels) for (const k of Object.keys(next)) if (k === rel || k.startsWith(rel + '/')) delete next[k]
      return next
    })
  }, [])

  const onCreateFolder = useCallback(async (name: string, parent: CFile | undefined) => {
    try { await post('/api/fs/mkdir', { path: `${abs(parent?.path ?? path)}/${name}` }); await loadFolder(path) } catch { /* shown */ }
  }, [abs, path, post, loadFolder])

  const onRename = useCallback(async (file: CFile, newName: string) => {
    try { await post('/api/fs/rename', { path: abs(file.path), name: newName }); prune([file.path]); await loadFolder(path) } catch { /* shown */ }
  }, [abs, path, post, prune, loadFolder])

  const onDelete = useCallback(async (items: CFile[]) => {
    try { await post('/api/fs/delete', { paths: items.map((f) => abs(f.path)) }); prune(items.map((f) => f.path)); await loadFolder(path) } catch { /* shown */ }
  }, [abs, path, post, prune, loadFolder])

  const onPaste = useCallback(async (items: CFile[], dest: CFile | undefined, op: 'copy' | 'move') => {
    try {
      await post(op === 'move' ? '/api/fs/move' : '/api/fs/copy', { paths: items.map((f) => abs(f.path)), dest: abs(dest?.path ?? path) })
      if (op === 'move') prune(items.map((f) => f.path))
      await loadFolder(path)
    } catch { /* shown */ }
  }, [abs, path, post, prune, loadFolder])

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-xl font-semibold text-foreground">Files</h1>
        <p className="text-sm text-muted-foreground mt-0.5">
          Browse mounted storage. Remote-to-remote transfers live in the Transfers view.
        </p>
      </div>

      <div className="flex gap-4">
        {/* Shortcuts sidebar — jump between drives / merged view */}
        <aside className="w-48 shrink-0 space-y-1">
          {SHORTCUTS.map(({ key, label, base: b, icon: Icon }) => (
            <button
              key={key}
              onClick={() => setBase(b)}
              className={cn(
                'flex items-center gap-2.5 w-full px-3 py-2 rounded-md text-sm transition-colors text-left',
                base === b ? 'bg-primary text-primary-foreground font-medium' : 'text-muted-foreground hover:text-foreground hover:bg-accent',
              )}
            >
              <Icon className="h-4 w-4 shrink-0" />
              <span className="truncate">{label}</span>
            </button>
          ))}
          <p className="px-3 pt-2 text-[11px] text-muted-foreground/70 font-mono break-all">{base}{path}</p>
        </aside>

        {/* File manager */}
        <div className="flex-1 min-w-0">
          <div className="mb-2 flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <Info className="h-3 w-3 shrink-0" />
            Browse, upload/download, new folder, rename, delete, cut/copy/paste — plus keyboard shortcuts (Ctrl+C/X/V, Del, F2).
          </div>
          {err && (
            <div className="mb-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-1.5 text-xs text-destructive break-all">{err}</div>
          )}
          <FileManager
            files={files}
            initialPath=""
            isLoading={loading}
            onFolderChange={(p: string) => { setPath(p); loadFolder(p) }}
            onRefresh={() => loadFolder(path)}
            onCreateFolder={onCreateFolder}
            onRename={onRename}
            onDelete={onDelete}
            onPaste={onPaste}
            fileUploadConfig={{ url: '/api/fs/upload', method: 'POST' }}
            onFileUploading={(_file, parentFolder) => ({ path: abs(parentFolder?.path ?? path) })}
            onFileUploaded={() => loadFolder(path)}
            onDownload={(items) => items.filter((f) => !f.isDirectory).forEach((f) => {
              const a = document.createElement('a')
              a.href = `/api/fs/download?path=${encodeURIComponent(abs(f.path))}`
              a.download = f.name
              document.body.appendChild(a); a.click(); a.remove()
            })}
            height="70vh"
          />
        </div>
      </div>
    </div>
  )
}
