/**
 * PathPicker — browse disk mounts + rclone remotes and pick a target, using the
 * same @cubone browser as the Files page for a consistent look. Two modes:
 *   • multi  — select one or more files/folders (returns each as an endpoint)
 *   • folder — navigate to a folder and use it (returns that folder)
 * Endpoints are absolute local paths (/mnt/…) or rclone "remote:path".
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { FileManager } from '@cubone/react-file-manager'
import '@cubone/react-file-manager/dist/style.css'
import { useRcloneRemotes } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Cloud, HardDrive, Boxes, Package } from 'lucide-react'
import { cn } from '@/lib/cn'

export type PickItem = { path: string; is_dir: boolean }
type CFile = { name: string; isDirectory: boolean; path: string; size?: number }
type Source = { kind: 'disk'; base: string; label: string } | { kind: 'rclone'; remote: string }

const DISK = [
  { base: '/mnt/unionfs', label: 'Merged', icon: Boxes },
  { base: '/mnt/local', label: 'Local', icon: HardDrive },
  { base: '/mnt/remote', label: 'Remotes', icon: Cloud },
  { base: '/opt', label: 'Apps', icon: Package },
] as const

export function PathPicker({ mode, onPick, onClose }: {
  mode: 'multi' | 'folder'
  onPick: (items: PickItem[]) => void
  onClose: () => void
}) {
  const { data: conf } = useRcloneRemotes()
  const remotes = useMemo(() => Object.keys(conf?.remotes ?? {}), [conf])

  const [source, setSource] = useState<Source>({ kind: 'disk', base: DISK[0].base, label: DISK[0].label })
  const [filesMap, setFilesMap] = useState<Record<string, CFile>>({})
  const [path, setPath] = useState('')
  const [loading, setLoading] = useState(false)
  const [sel, setSel] = useState<CFile[]>([])
  const [err, setErr] = useState<string | null>(null)

  const files = useMemo(() => Object.values(filesMap), [filesMap])
  const endpoint = useCallback((rel: string) =>
    source.kind === 'disk' ? source.base + rel : `${source.remote}:${rel.replace(/^\//, '')}`, [source])

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

  useEffect(() => { setPath(''); setFilesMap({}); setSel([]); loadFolder('') }, [source, loadFolder])

  const onCreateFolder = async (name: string, parent: CFile | undefined) => {
    const rel = `${parent?.path ?? path}/${name}`
    setErr(null)
    try {
      const r = source.kind === 'disk'
        ? await fetch('/api/fs/mkdir', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path: source.base + rel }) })
        : await fetch('/api/rclone/mkdir', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ remote: source.remote, path: rel.replace(/^\//, '') }) })
      if (!r.ok) { setErr(await r.text() || 'mkdir failed'); return }
      await loadFolder(parent?.path ?? path)
    } catch { setErr('mkdir failed') }
  }

  function confirm() {
    if (mode === 'folder') onPick([{ path: endpoint(path), is_dir: true }])
    else onPick(sel.map((f) => ({ path: endpoint(f.path), is_dir: f.isDirectory })))
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="w-[94vw] max-w-[1200px]">
        <DialogHeader><DialogTitle>{mode === 'folder' ? 'Choose destination folder' : 'Choose source files / folders'}</DialogTitle></DialogHeader>

        <div className="flex gap-4">
          <aside className="w-40 shrink-0 space-y-3">
            <Grp title="Disk">
              {DISK.map(({ base, label, icon: Icon }) => (
                <Rail key={base} active={source.kind === 'disk' && source.base === base} onClick={() => setSource({ kind: 'disk', base, label })}>
                  <Icon className="h-3.5 w-3.5 shrink-0" />{label}
                </Rail>
              ))}
            </Grp>
            <Grp title="rclone">
              {remotes.length === 0 && <p className="px-2 text-[11px] text-muted-foreground/60">No remotes</p>}
              {remotes.map((r) => (
                <Rail key={r} active={source.kind === 'rclone' && source.remote === r} onClick={() => setSource({ kind: 'rclone', remote: r })}>
                  <Cloud className="h-3.5 w-3.5 shrink-0" /><span className="truncate">{r}</span>
                </Rail>
              ))}
            </Grp>
          </aside>

          <div className="flex-1 min-w-0">
            {err && <div className="mb-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-1.5 text-xs text-destructive break-all">{err}</div>}
            <FileManager
              key={source.kind === 'disk' ? `d:${source.base}` : `r:${source.remote}`}
              files={files}
              initialPath=""
              isLoading={loading}
              onFolderChange={(p: string) => { setPath(p); loadFolder(p); setSel([]) }}
              onRefresh={() => loadFolder(path)}
              onSelect={(f: CFile[]) => setSel(f)}
              onSelectionChange={(f: CFile[]) => setSel(f)}
              onCreateFolder={onCreateFolder}
              height="72vh"
            />
          </div>
        </div>

        <div className="flex items-center justify-between gap-2">
          <span className="text-[11px] text-muted-foreground font-mono truncate">{endpoint(path)}</span>
          <div className="flex gap-2 shrink-0">
            <Button size="sm" variant="outline" onClick={onClose}>Cancel</Button>
            {mode === 'folder'
              ? <Button size="sm" onClick={confirm}>Use this folder</Button>
              : <Button size="sm" onClick={confirm} disabled={sel.length === 0}>Add selected ({sel.length})</Button>}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function Grp({ title, children }: { title: string; children: React.ReactNode }) {
  return <div><p className="px-2 mb-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground/60">{title}</p><div className="space-y-0.5">{children}</div></div>
}
function Rail({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return <button onClick={onClick} className={cn('flex items-center gap-2 w-full px-2 py-1.5 rounded text-xs transition-colors text-left', active ? 'bg-primary text-primary-foreground font-medium' : 'text-muted-foreground hover:text-foreground hover:bg-accent')}>{children}</button>
}
