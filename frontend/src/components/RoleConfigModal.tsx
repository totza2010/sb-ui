/**
 * RoleConfigModal — inventory variable editor + role file editor per app/role.
 * Opens from AppManager AppCard gear button.
 */
import { useEffect, useMemo, useState } from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import {
  ChevronDown, ChevronRight, File, FileCode, Folder,
  Loader2, Plus, Save, Search, Settings2, Trash2, X,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogOverlay, DialogPortal } from '@/components/ui/dialog'
import {
  useInventory, useInventoryCatalog, useSaveInventory,
  useRoleFiles, useRoleFile, useSaveRoleFile, useRolePatches, useRebuildPatches, useRolePatch,
  useRebuildPreview,
} from '@/lib/api'
import type { AppInfo, RebuildPreviewItem } from '@/lib/api'
import { cn } from '@/lib/cn'

// ── Types (mirrored from Inventory.tsx) ───────────────────────────────────────

type VarType = 'boolean' | 'integer' | 'string' | 'list' | 'dict'

function inferType(v: unknown): VarType {
  if (typeof v === 'boolean') return 'boolean'
  if (typeof v === 'number') return 'integer'
  if (Array.isArray(v)) return 'list'
  if (typeof v === 'object' && v !== null) return 'dict'
  return 'string'
}

function plainValue(val: unknown, type: VarType): unknown {
  if (type === 'boolean') return Boolean(val)
  if (type === 'integer') return Number(val) || 0
  if (type === 'string') return val == null ? '' : String(val)
  if (type === 'list') return Array.isArray(val) ? (val as unknown[]).map(String) : ['']
  if (type === 'dict') {
    if (typeof val === 'object' && val !== null && !Array.isArray(val))
      return Object.fromEntries(Object.entries(val as object).map(([k, v]) => [k, String(v)]))
    return { '': '' }
  }
  return val ?? ''
}

const TYPE_STYLES: Record<VarType, string> = {
  boolean: 'bg-sky-100 text-sky-700 border-sky-200',
  integer: 'bg-violet-100 text-violet-700 border-violet-200',
  string: 'bg-emerald-100 text-emerald-700 border-emerald-200',
  list: 'bg-orange-100 text-orange-700 border-orange-200',
  dict: 'bg-amber-100 text-amber-700 border-amber-200',
}

function TypeBadge({ type }: { type: VarType }) {
  return (
    <span className={`text-[10px] px-1.5 py-0.5 rounded border font-mono shrink-0 ${TYPE_STYLES[type]}`}>
      {type}
    </span>
  )
}

function ValuePreview({ type, value }: { type: VarType; value: unknown }) {
  if (type === 'boolean')
    return <span className={`text-xs font-mono ${value ? 'text-emerald-600' : 'text-rose-500'}`}>{String(value)}</span>
  if (type === 'integer')
    return <span className="text-xs font-mono">{String(value)}</span>
  if (type === 'string')
    return <span className="text-xs font-mono text-muted-foreground truncate max-w-[14rem]">"{String(value)}"</span>
  if (type === 'list') {
    const n = (value as unknown[]).length
    return <span className="text-xs text-muted-foreground">[{n} item{n !== 1 ? 's' : ''}]</span>
  }
  if (type === 'dict') {
    const n = Object.keys(value as object).length
    return <span className="text-xs text-muted-foreground">{`{${n} key${n !== 1 ? 's' : ''}}`}</span>
  }
  return null
}

// ── Inline editors ─────────────────────────────────────────────────────────────

function BoolEditor({ value, onChange }: { value: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="flex gap-1.5">
      {[true, false].map(b => (
        <button key={String(b)} type="button" onClick={() => onChange(b)}
          className={`px-2.5 py-1 rounded text-xs font-mono border transition-colors ${
            value === b
              ? b ? 'bg-emerald-100 text-emerald-700 border-emerald-300 font-semibold'
                  : 'bg-rose-100 text-rose-700 border-rose-300 font-semibold'
              : 'text-muted-foreground border-border hover:bg-muted/50'
          }`}>
          {String(b)}
        </button>
      ))}
    </div>
  )
}

function ListEditor({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  return (
    <div className="space-y-1">
      {value.map((item, i) => (
        <div key={i} className="flex items-center gap-1.5">
          <Input value={item}
            onChange={e => { const n = [...value]; n[i] = e.target.value; onChange(n) }}
            className="font-mono text-xs h-7" placeholder="value" />
          <Button size="sm" variant="ghost" className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive"
            onClick={() => onChange(value.filter((_, j) => j !== i))}>
            <Trash2 className="h-3 w-3" />
          </Button>
        </div>
      ))}
      <Button size="sm" variant="outline" className="h-6 text-xs"
        onClick={() => onChange([...value, ''])}>
        <Plus className="h-3 w-3 mr-1" /> Add item
      </Button>
    </div>
  )
}

function DictEditor({ value, onChange }: { value: Record<string, string>; onChange: (v: Record<string, string>) => void }) {
  const entries = Object.entries(value)
  return (
    <div className="space-y-1">
      {entries.map(([k, v], i) => (
        <div key={i} className="flex items-center gap-1.5">
          <Input value={k}
            onChange={e => onChange(Object.fromEntries(entries.map(([ek, ev], j) => [j === i ? e.target.value : ek, ev])))}
            className="font-mono text-xs h-7 w-36 shrink-0" placeholder="KEY" />
          <span className="text-muted-foreground text-xs shrink-0">:</span>
          <Input value={v}
            onChange={e => onChange(Object.fromEntries(entries.map(([ek, ev], j) => [ek, j === i ? e.target.value : ev])))}
            className="font-mono text-xs h-7" placeholder="value" />
          <Button size="sm" variant="ghost" className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive"
            onClick={() => onChange(Object.fromEntries(entries.filter((_, j) => j !== i)))}>
            <Trash2 className="h-3 w-3" />
          </Button>
        </div>
      ))}
      <Button size="sm" variant="outline" className="h-6 text-xs"
        onClick={() => onChange({ ...value, '': '' })}>
        <Plus className="h-3 w-3 mr-1" /> Add key
      </Button>
    </div>
  )
}

function ValueEditor({ type, value, onChange }: { type: VarType; value: unknown; onChange: (v: unknown) => void }) {
  if (type === 'boolean') return <BoolEditor value={value as boolean} onChange={onChange} />
  if (type === 'integer')
    return <Input type="number" value={value as number} onChange={e => onChange(Number(e.target.value))} className="font-mono text-xs h-8 w-36" />
  if (type === 'string')
    return <Input value={value as string} onChange={e => onChange(e.target.value)} className="font-mono text-xs h-8" />
  if (type === 'list') return <ListEditor value={value as string[]} onChange={onChange} />
  if (type === 'dict') return <DictEditor value={value as Record<string, string>} onChange={onChange} />
  return null
}

// ── Variable row ───────────────────────────────────────────────────────────────

function VarRow({ name, type, defaultVal, overrideVal, hasOverride, onSet, onRemove }: {
  name: string
  type: VarType
  defaultVal: unknown
  overrideVal: unknown
  hasOverride: boolean
  onSet: (v: unknown) => void
  onRemove: () => void
}) {
  const [expanded, setExpanded] = useState(false)
  const [draft, setDraft] = useState<unknown>(overrideVal ?? plainValue(defaultVal, type))

  // Sync draft when override changes externally
  useEffect(() => {
    setDraft(overrideVal ?? plainValue(defaultVal, type))
  }, [overrideVal, defaultVal, type])

  const handleOverride = () => {
    setDraft(overrideVal ?? plainValue(defaultVal, type))
    setExpanded(true)
  }

  const handleApply = () => {
    onSet(draft)
    setExpanded(false)
  }

  const draftChanged = JSON.stringify(draft) !== JSON.stringify(overrideVal ?? plainValue(defaultVal, type))

  return (
    <div className={cn(
      'rounded-md border transition-colors',
      hasOverride ? 'border-primary/30 bg-primary/5' : 'border-border bg-card'
    )}>
      {/* Header row */}
      <div className="flex items-center gap-2 px-3 py-2">
        <button type="button" onClick={() => setExpanded(e => !e)}
          className="flex items-center gap-2 flex-1 min-w-0 text-left">
          {expanded
            ? <ChevronDown className="h-3 w-3 text-muted-foreground shrink-0" />
            : <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" />}
          <span className="font-mono text-xs font-medium truncate">{name}</span>
          <TypeBadge type={type} />
          {hasOverride ? (
            <span className="text-[10px] text-primary bg-primary/10 px-1.5 py-0.5 rounded border border-primary/20 shrink-0 font-medium">
              overridden
            </span>
          ) : (
            !expanded && <ValuePreview type={type} value={defaultVal} />
          )}
        </button>
        {hasOverride ? (
          <Button size="sm" variant="ghost"
            className="h-6 w-6 p-0 text-muted-foreground hover:text-destructive shrink-0"
            onClick={onRemove} title="Remove override">
            <Trash2 className="h-3 w-3" />
          </Button>
        ) : (
          <Button size="sm" variant="outline" className="h-6 text-[11px] px-2 shrink-0"
            onClick={handleOverride}>
            <Plus className="h-3 w-3 mr-0.5" /> Override
          </Button>
        )}
      </div>

      {/* Expanded editor */}
      {expanded && (
        <div className="px-4 pb-4 space-y-3 border-t border-border/40 pt-3">
          <div className="grid grid-cols-2 gap-4 text-xs">
            <div>
              <p className="text-muted-foreground mb-1 font-medium">Default value</p>
              <div className="font-mono text-muted-foreground/70 bg-muted/30 rounded px-2 py-1 text-[11px]">
                <ValuePreview type={type} value={defaultVal} />
              </div>
            </div>
            {hasOverride && (
              <div>
                <p className="text-primary font-medium mb-1">Saved override</p>
                <div className="font-mono text-primary/70 bg-primary/5 rounded px-2 py-1 text-[11px]">
                  <ValuePreview type={type} value={overrideVal} />
                </div>
              </div>
            )}
          </div>
          <div>
            <p className="text-xs font-medium mb-1.5">{hasOverride ? 'Edit override' : 'Set override'}</p>
            <ValueEditor type={type} value={draft} onChange={setDraft} />
          </div>
          <div className="flex items-center gap-2">
            <Button size="sm" className="h-7 text-xs" onClick={handleApply} disabled={!draftChanged && hasOverride}>
              Set override
            </Button>
            <Button size="sm" variant="ghost" className="h-7 text-xs" onClick={() => setExpanded(false)}>Cancel</Button>
            <span className="text-[10px] text-muted-foreground ml-auto">
              Then click <span className="font-semibold text-foreground">Save inventory</span> to write to file
            </span>
          </div>
        </div>
      )}
    </div>
  )
}

// ── File tree + editor (Files tab) ────────────────────────────────────────────

function fileIcon(path: string) {
  const ext = path.split('.').pop() ?? ''
  if (['yml', 'yaml', 'j2', 'py', 'sh'].includes(ext)) return <FileCode className="h-3 w-3 shrink-0" />
  return <File className="h-3 w-3 shrink-0" />
}

function buildTree(files: string[]): Record<string, unknown> {
  const tree: Record<string, unknown> = {}
  for (const f of files) {
    const parts = f.split('/')
    let node = tree
    for (let i = 0; i < parts.length - 1; i++) {
      if (!node[parts[i]]) node[parts[i]] = {}
      node = node[parts[i]] as Record<string, unknown>
    }
    node[parts[parts.length - 1]] = f
  }
  return tree
}

function FileTreeNode({
  name, node, depth, selected, onSelect, patchedFiles,
}: {
  name: string
  node: Record<string, unknown> | string
  depth: number
  selected: string | null
  onSelect: (path: string) => void
  patchedFiles: Set<string>
}) {
  const [open, setOpen] = useState(depth < 2)
  if (typeof node === 'string') {
    const isPatched = patchedFiles.has(node)
    return (
      <button
        type="button"
        onClick={() => onSelect(node)}
        className={cn(
          'flex items-center gap-1.5 w-full text-left px-2 py-0.5 rounded text-xs font-mono truncate transition-colors',
          selected === node
            ? 'bg-primary/15 text-primary'
            : 'text-muted-foreground hover:text-foreground hover:bg-muted/50',
        )}
        style={{ paddingLeft: `${depth * 12 + 8}px` }}
      >
        {fileIcon(name)}
        <span className="truncate flex-1">{name}</span>
        {isPatched && (
          <span className="shrink-0 h-1.5 w-1.5 rounded-full bg-amber-400" title="Patched — survives updates" />
        )}
      </button>
    )
  }
  const entries = Object.entries(node).sort(([, a], [, b]) => {
    const aIsDir = typeof a !== 'string'
    const bIsDir = typeof b !== 'string'
    if (aIsDir !== bIsDir) return aIsDir ? -1 : 1
    return 0
  })
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen(o => !o)}
        className="flex items-center gap-1.5 w-full text-left px-2 py-0.5 rounded text-xs text-muted-foreground hover:text-foreground hover:bg-muted/40 transition-colors"
        style={{ paddingLeft: `${depth * 12 + 8}px` }}
      >
        {open ? <ChevronDown className="h-3 w-3 shrink-0" /> : <ChevronRight className="h-3 w-3 shrink-0" />}
        <Folder className="h-3 w-3 shrink-0" />
        <span className="font-mono truncate">{name}</span>
      </button>
      {open && entries.map(([k, v]) => (
        <FileTreeNode key={k} name={k} node={v as string | Record<string, unknown>}
          depth={depth + 1} selected={selected} onSelect={onSelect} patchedFiles={patchedFiles} />
      ))}
    </div>
  )
}

// Color-coded unified-diff renderer (shared by file editor + rebuild preview)
function DiffView({ patch }: { patch: string }) {
  return (
    <pre className="font-mono text-xs whitespace-pre-wrap break-all leading-relaxed">
      {patch.split('\n').map((line, i) => (
        <span key={i} className={
          line.startsWith('+') && !line.startsWith('+++') ? 'text-green-500' :
          line.startsWith('-') && !line.startsWith('---') ? 'text-red-500' :
          line.startsWith('@@') ? 'text-blue-400' :
          'text-muted-foreground'
        }>{line}{'\n'}</span>
      ))}
    </pre>
  )
}

// Preview modal: shows before / after / patch for each patched file
// before the rebuild is actually written, so the user can verify first.
function RebuildPreviewModal({
  roleName, repo, onCancel, onConfirm, confirming,
}: {
  roleName: string
  repo: string
  onCancel: () => void
  onConfirm: () => void
  confirming: boolean
}) {
  const { data, isLoading } = useRebuildPreview(roleName, repo, true)
  const [view, setView] = useState<Record<string, 'patch' | 'before' | 'after'>>({})
  const items = data?.items ?? []
  const viewFor = (f: string) => view[f] ?? 'patch'
  const setViewFor = (f: string, v: 'patch' | 'before' | 'after') =>
    setView(prev => ({ ...prev, [f]: v }))

  return (
    <DialogPrimitive.Root open onOpenChange={(o) => !o && onCancel()}>
      <DialogPortal>
        <DialogOverlay className="z-[60]" />
        <DialogPrimitive.Content
          className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-[60]
            w-full max-w-4xl h-[80vh] bg-card border border-border rounded-lg shadow-xl
            outline-none flex flex-col"
        >
          <div className="flex items-center justify-between px-4 py-3 border-b border-border shrink-0">
            <div>
              <h2 className="text-sm font-semibold">Review rebuild</h2>
              <p className="text-xs text-muted-foreground mt-0.5">
                Patches will be regenerated from current file content vs git HEAD.
              </p>
            </div>
            <button onClick={onCancel} className="text-muted-foreground hover:text-foreground">
              <X className="h-4 w-4" />
            </button>
          </div>

          <div className="flex-1 overflow-y-auto p-4 space-y-4">
            {isLoading ? (
              <div className="flex items-center justify-center py-16 gap-2 text-muted-foreground">
                <Loader2 className="h-5 w-5 animate-spin" />
                <span className="text-sm">Computing preview…</span>
              </div>
            ) : items.length === 0 ? (
              <div className="text-center text-sm text-muted-foreground py-12">No patches to rebuild</div>
            ) : items.map((item: RebuildPreviewItem) => {
              const v = viewFor(item.file)
              const noChange = item.mode === 'diff' && item.patch === ''
              return (
                <div key={item.file} className="border border-border rounded-md overflow-hidden">
                  <div className="flex items-center justify-between gap-2 px-3 py-1.5 bg-muted/30 border-b border-border">
                    <span className="text-xs font-mono truncate">{item.file}</span>
                    <div className="flex items-center gap-1 shrink-0">
                      {item.error ? (
                        <span className="text-xs text-red-500">{item.error}</span>
                      ) : item.mode === 'full-content' ? (
                        <span className="text-xs text-muted-foreground">full content (sandbox)</span>
                      ) : noChange ? (
                        <span className="text-xs text-muted-foreground">identical to HEAD — patch removed</span>
                      ) : (
                        (['patch', 'before', 'after'] as const).map(tab => (
                          <button key={tab}
                            onClick={() => setViewFor(item.file, tab)}
                            className={cn(
                              'text-xs px-1.5 py-0.5 rounded transition-colors',
                              v === tab ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:text-foreground',
                            )}>
                            {tab === 'patch' ? 'Patch' : tab === 'before' ? 'Before (HEAD)' : 'After (current)'}
                          </button>
                        ))
                      )}
                    </div>
                  </div>
                  {!item.error && !noChange && (
                    <div className="max-h-72 overflow-auto p-3 bg-background">
                      {item.mode === 'full-content' ? (
                        <pre className="font-mono text-xs whitespace-pre-wrap break-all">{item.current}</pre>
                      ) : v === 'patch' ? (
                        <DiffView patch={item.patch ?? ''} />
                      ) : (
                        <pre className="font-mono text-xs whitespace-pre-wrap break-all">
                          {v === 'before' ? (item.original ?? '') : item.current}
                        </pre>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>

          <div className="flex items-center justify-end gap-2 px-4 py-3 border-t border-border shrink-0">
            <Button size="sm" variant="outline" onClick={onCancel} disabled={confirming}>Cancel</Button>
            <Button size="sm" onClick={onConfirm} disabled={confirming || isLoading || items.length === 0}>
              {confirming ? <><Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />Rebuilding…</> : 'Confirm rebuild'}
            </Button>
          </div>
        </DialogPrimitive.Content>
      </DialogPortal>
    </DialogPrimitive.Root>
  )
}

function FilesTab({ roleName, repo }: { roleName: string; repo: string }) {
  const [selectedPath, setSelectedPath] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  const [savedContent, setSavedContent] = useState('')
  const [showPatch, setShowPatch] = useState(false)
  const [rebuildMsg, setRebuildMsg] = useState<string | null>(null)
  const [showRebuildPreview, setShowRebuildPreview] = useState(false)

  const { data: filesData, isLoading: filesLoading } = useRoleFiles(roleName, repo)
  const { data: patchesData, refetch: refetchPatches } = useRolePatches(roleName, repo)
  const { data: fileData, isLoading: fileLoading }   = useRoleFile(roleName, repo, selectedPath)
  const { data: patchData } = useRolePatch(roleName, repo, selectedPath)
  const saveFile = useSaveRoleFile()
  const rebuildPatches = useRebuildPatches()
  const patchedFiles = new Set(patchesData?.patches ?? [])
  const selectedIsPatched = selectedPath ? patchedFiles.has(selectedPath) : false

  async function confirmRebuild() {
    setRebuildMsg(null)
    const result = await rebuildPatches.mutateAsync({ role: roleName, repo })
    refetchPatches()
    const parts = []
    if (result.rebuilt.length) parts.push(`Rebuilt ${result.rebuilt.length} patch(es)`)
    if (result.failed.length) parts.push(`${result.failed.length} failed`)
    setRebuildMsg(parts.join(', ') || 'No patches to rebuild')
    setShowRebuildPreview(false)
    setTimeout(() => setRebuildMsg(null), 4000)
  }

  useEffect(() => {
    if (fileData) {
      setDraft(fileData.content)
      setSavedContent(fileData.content)
    }
  }, [fileData])

  const dirty = draft !== savedContent

  async function handleSave() {
    if (!selectedPath) return
    await saveFile.mutateAsync({ role: roleName, repo, path: selectedPath, content: draft })
    setSavedContent(draft)
    refetchPatches()
  }

  const tree = useMemo(() => buildTree(filesData?.files ?? []), [filesData])

  if (filesLoading) {
    return (
      <div className="flex items-center justify-center py-16 gap-2 text-muted-foreground">
        <Loader2 className="h-5 w-5 animate-spin" />
        <span className="text-sm">Loading files…</span>
      </div>
    )
  }

  if (!filesData?.files.length) {
    return (
      <div className="text-center py-12 text-muted-foreground text-sm">
        No role files found at <span className="font-mono">{filesData?.base}</span>
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0">
      {/* File tree */}
      <div className="w-52 shrink-0 border-r border-border flex flex-col min-h-0">
        <div className="overflow-y-auto flex-1 py-2">
          {Object.entries(tree).map(([k, v]) => (
            <FileTreeNode key={k} name={k} node={v as string | Record<string, unknown>}
              depth={0} selected={selectedPath} onSelect={setSelectedPath} patchedFiles={patchedFiles} />
          ))}
        </div>
        {patchedFiles.size > 0 && (
          <div className="shrink-0 border-t border-border p-2 space-y-1">
            <div className="text-xs text-muted-foreground px-1">
              {patchedFiles.size} patched file{patchedFiles.size > 1 ? 's' : ''}
            </div>
            <Button size="sm" variant="outline"
              className="w-full h-6 text-xs"
              onClick={() => setShowRebuildPreview(true)}
              disabled={rebuildPatches.isPending}>
              {rebuildPatches.isPending ? 'Rebuilding…' : 'Rebuild patches'}
            </Button>
            {rebuildMsg && (
              <div className="text-xs text-muted-foreground px-1">{rebuildMsg}</div>
            )}
          </div>
        )}
      </div>

      {showRebuildPreview && (
        <RebuildPreviewModal
          roleName={roleName}
          repo={repo}
          onCancel={() => setShowRebuildPreview(false)}
          onConfirm={confirmRebuild}
          confirming={rebuildPatches.isPending}
        />
      )}

      {/* Editor */}
      <div className="flex-1 flex flex-col min-w-0">
        {selectedPath ? (
          <>
            <div className="flex items-center justify-between gap-2 px-3 py-1.5 border-b border-border bg-muted/20 shrink-0">
              <div className="flex items-center gap-2 min-w-0">
                <span className="text-xs font-mono text-muted-foreground truncate">{selectedPath}</span>
                {selectedIsPatched && (
                  <span className="shrink-0 text-xs bg-yellow-500/15 text-yellow-600 dark:text-yellow-400 px-1.5 py-0.5 rounded">patched</span>
                )}
              </div>
              <div className="flex items-center gap-1 shrink-0">
                {selectedIsPatched && patchData?.patch && (
                  <Button size="sm" variant="ghost"
                    className="h-6 text-xs gap-1"
                    onClick={() => setShowPatch(p => !p)}>
                    {showPatch ? 'Edit' : 'View diff'}
                  </Button>
                )}
                <Button size="sm" className="h-6 text-xs gap-1"
                  onClick={handleSave} disabled={!dirty || saveFile.isPending || showPatch}>
                  <Save className="h-3 w-3" />
                  {saveFile.isPending ? 'Saving…' : 'Save'}
                </Button>
              </div>
            </div>
            {fileLoading ? (
              <div className="flex items-center justify-center py-8 gap-2 text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
              </div>
            ) : showPatch && patchData?.patch ? (
              <div className="flex-1 overflow-auto p-3">
                <DiffView patch={patchData.patch} />
              </div>
            ) : (
              <textarea
                className="flex-1 font-mono text-xs p-3 resize-none bg-transparent text-foreground outline-none"
                value={draft}
                onChange={e => setDraft(e.target.value)}
                spellCheck={false}
              />
            )}
          </>
        ) : (
          <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
            Select a file to edit
          </div>
        )}
      </div>
    </div>
  )
}

// ── Main modal ─────────────────────────────────────────────────────────────────

type Tab = 'variables' | 'files'

export function RoleConfigModal({ app, onClose }: {
  app: AppInfo | null
  onClose: () => void
}) {
  const open = !!app

  const { data: catalog, isLoading: catalogLoading } = useInventoryCatalog({ enabled: open })
  const { data: invData, isLoading: invLoading }     = useInventory()
  const saveInv = useSaveInventory()

  const [tab, setTab] = useState<Tab>('variables')
  const [section, setSection] = useState<string>('')
  const [localOverrides, setLocalOverrides] = useState<Record<string, unknown>>({})
  const [savedSnapshot, setSavedSnapshot]   = useState<string>('{}')
  const [search, setSearch] = useState('')

  // Strip repo prefix (sandbox-, mod-) to get the bare role name used in catalog + inventory
  const roleName = app ? app.tag.replace(/^(sandbox|mod)-/, '') : ''
  const repo = app?.repo ?? 'saltbox'

  // Initialise local overrides when data or app changes
  useEffect(() => {
    if (!app || !invData?.data) return
    const prefix = `${roleName}_`
    const roleVars = Object.fromEntries(
      Object.entries(invData.data).filter(([k]) => k.startsWith(prefix))
    )
    setLocalOverrides(roleVars)
    setSavedSnapshot(JSON.stringify(roleVars))
    setSearch('')
    setTab('variables')
  }, [app, invData, roleName])

  const dirty = useMemo(
    () => JSON.stringify(localOverrides) !== savedSnapshot,
    [localOverrides, savedSnapshot]
  )

  // Catalog variables for this role
  const catalogVars = useMemo(() => {
    if (!app || !catalog?.roles) return []
    const roleData = catalog.roles[roleName]
    if (!roleData) return []
    const q = search.toLowerCase().trim()
    return Object.entries(roleData.variables).filter(([name]) =>
      !q || name.toLowerCase().includes(q)
    )
  }, [app, catalog, search, roleName])

  // Group catalog vars by their defaults/main.yml section banner
  const sectionMap = catalog?.roles?.[roleName]?.sections ?? {}
  const SECTION_ORDER = ['Basics', 'Settings', 'Paths', 'Web', 'DNS', 'Traefik', 'Docker', 'Docker+']
  const grouped = useMemo(() => {
    const m = new Map<string, [string, unknown][]>()
    for (const [name, val] of catalogVars) {
      const sec = sectionMap[name] ?? 'Other'
      if (!m.has(sec)) m.set(sec, [])
      m.get(sec)!.push([name, val])
    }
    return [...m.entries()].sort(([a], [b]) => {
      const ia = SECTION_ORDER.indexOf(a), ib = SECTION_ORDER.indexOf(b)
      if (ia !== -1 || ib !== -1) return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib)
      if (a === 'Other') return 1
      if (b === 'Other') return -1
      return a.localeCompare(b)
    })
  }, [catalogVars, sectionMap])

  const sectionNames = grouped.map(([s]) => s)
  const activeSection = section && sectionNames.includes(section) ? section : (sectionNames[0] ?? '')
  const useSectionTabs = !search.trim() && grouped.length > 1
  const visibleCatalogVars = useSectionTabs
    ? (grouped.find(([s]) => s === activeSection)?.[1] ?? [])
    : catalogVars

  // Extra vars in inventory but NOT in catalog (custom or instance-scoped)
  const extraOverrides = useMemo(() => {
    const catalogNames = new Set(catalogVars.map(([n]) => n))
    const q = search.toLowerCase().trim()
    return Object.entries(localOverrides)
      .filter(([k]) => !catalogNames.has(k) && (!q || k.toLowerCase().includes(q)))
  }, [localOverrides, catalogVars, search])

  const overrideCount = Object.keys(localOverrides).length

  function setOverride(name: string, value: unknown) {
    setLocalOverrides(prev => ({ ...prev, [name]: value }))
  }

  function removeOverride(name: string) {
    setLocalOverrides(prev => {
      const next = { ...prev }
      delete next[name]
      return next
    })
  }

  async function handleSave() {
    if (!app) return
    const allInv = invData?.data ?? {}
    const prefix = `${roleName}_`
    // Drop all existing role vars, then add current local overrides
    const merged: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(allInv)) {
      if (!k.startsWith(prefix)) merged[k] = v
    }
    Object.assign(merged, localOverrides)
    await saveInv.mutateAsync(merged)
    setSavedSnapshot(JSON.stringify(localOverrides))
  }

  const isLoading = catalogLoading || invLoading

  const isFilesTab = tab === 'files'

  return (
    <Dialog open={open} onOpenChange={o => !o && onClose()}>
      <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Content
          className={cn(
            'fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2',
            'z-50 bg-card border border-border rounded-lg shadow-xl outline-none flex flex-col',
            isFilesTab
              ? 'w-full max-w-5xl h-[85vh]'
              : 'w-full max-w-3xl max-h-[85vh]',
          )}
        >
          {/* Header */}
          <div className="flex items-center gap-3 px-5 py-4 border-b border-border shrink-0">
            <Settings2 className="h-4 w-4 text-primary shrink-0" />
            <div className="flex-1 min-w-0">
              <h2 className="text-sm font-semibold">Configure: {app?.name}</h2>
              <p className="text-xs text-muted-foreground font-mono mt-0.5">
                {app?.tag} · {overrideCount} override{overrideCount !== 1 ? 's' : ''} active
              </p>
            </div>
            {/* Tab bar */}
            <div className="flex gap-1 bg-muted/50 rounded-md p-0.5">
              {(['variables', 'files'] as Tab[]).map(t => (
                <button key={t} type="button" onClick={() => setTab(t)}
                  className={cn(
                    'px-3 py-1 rounded text-xs font-medium transition-colors capitalize',
                    tab === t
                      ? 'bg-card text-foreground shadow-sm'
                      : 'text-muted-foreground hover:text-foreground',
                  )}>
                  {t}
                </button>
              ))}
            </div>
            <DialogPrimitive.Close asChild>
              <Button size="sm" variant="ghost" className="h-8 w-8 p-0 shrink-0">
                <X className="h-4 w-4" />
              </Button>
            </DialogPrimitive.Close>
          </div>

          {/* Variables tab: search bar + section sub-tabs */}
          {tab === 'variables' && (
            <div className="px-5 py-3 border-b border-border shrink-0 space-y-2.5">
              <div className="relative">
                <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
                <Input value={search} onChange={e => setSearch(e.target.value)}
                  placeholder="Search variables..." className="pl-8 h-8 text-sm" />
              </div>
              {useSectionTabs && (
                <div className="flex gap-1 flex-wrap">
                  {grouped.map(([sec, vars]) => (
                    <button key={sec} type="button" onClick={() => setSection(sec)}
                      className={cn(
                        'px-2.5 py-1 rounded text-xs font-medium transition-colors',
                        activeSection === sec
                          ? 'bg-primary text-primary-foreground'
                          : 'bg-muted/50 text-muted-foreground hover:text-foreground',
                      )}>
                      {sec} <span className="opacity-60">{vars.length}</span>
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}

          {/* Body */}
          {tab === 'variables' ? (
            <div className="flex-1 overflow-y-auto px-5 py-4 space-y-6">
              {isLoading ? (
                <div className="flex items-center justify-center py-16 gap-2 text-muted-foreground">
                  <Loader2 className="h-5 w-5 animate-spin" />
                  <span className="text-sm">Loading...</span>
                </div>
              ) : (
                <>
                  {/* Extra overrides not in catalog */}
                  {extraOverrides.length > 0 && (
                    <section>
                      <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-2">
                        Custom overrides
                      </h3>
                      <div className="space-y-1.5">
                        {extraOverrides.map(([name, val]) => {
                          const type = inferType(val)
                          return (
                            <VarRow key={name} name={name} type={type}
                              defaultVal={val} overrideVal={val} hasOverride
                              onSet={v => setOverride(name, v)}
                              onRemove={() => removeOverride(name)} />
                          )
                        })}
                      </div>
                    </section>
                  )}

                  {/* Catalog variables */}
                  {catalogVars.length > 0 ? (
                    <section>
                      {(extraOverrides.length > 0 || useSectionTabs) && (
                        <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-2">
                          {useSectionTabs ? activeSection : 'Role variables'}
                        </h3>
                      )}
                      <div className="space-y-1.5">
                        {visibleCatalogVars.map(([name, defVal]) => {
                          const type = inferType(defVal)
                          const hasOverride = name in localOverrides
                          return (
                            <VarRow key={name} name={name} type={type}
                              defaultVal={defVal}
                              overrideVal={hasOverride ? localOverrides[name] : undefined}
                              hasOverride={hasOverride}
                              onSet={v => setOverride(name, v)}
                              onRemove={() => removeOverride(name)} />
                          )
                        })}
                      </div>
                    </section>
                  ) : (
                    !extraOverrides.length && (
                      <div className="text-center py-12 text-muted-foreground text-sm">
                        {search ? 'No variables match the search.' : 'No catalog data found for this role.'}
                      </div>
                    )
                  )}
                </>
              )}
            </div>
          ) : (
            <div className="flex-1 min-h-0 overflow-hidden">
              <FilesTab roleName={roleName} repo={repo} />
            </div>
          )}

          {/* Footer */}
          <div className="flex items-center justify-between gap-3 px-5 py-3 border-t border-border shrink-0 bg-muted/20">
            {tab === 'variables' ? (
              <p className="text-xs text-muted-foreground">
                Changes are saved to <span className="font-mono">localhost.yml</span> inventory
              </p>
            ) : (
              <p className="text-xs text-muted-foreground">
                Editing role files directly — changes apply immediately
              </p>
            )}
            <div className="flex gap-2">
              <Button size="sm" variant="ghost" className="h-8" onClick={onClose}>Cancel</Button>
              {tab === 'variables' && (
                <Button size="sm" className="h-8 gap-1.5" onClick={handleSave}
                  disabled={!dirty || saveInv.isPending}>
                  <Save className="h-3.5 w-3.5" />
                  {saveInv.isPending ? 'Saving...' : 'Save inventory'}
                </Button>
              )}
            </div>
          </div>
        </DialogPrimitive.Content>
      </DialogPortal>
    </Dialog>
  )
}
