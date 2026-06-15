import { useEffect, useMemo, useState } from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import {
  BookOpen, ChevronDown, ChevronRight, ExternalLink,
  Info, Loader2, Plus, Save, Search, Trash2, X,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogClose, DialogOverlay, DialogPortal } from '@/components/ui/dialog'
import { useInventory, useInventoryCatalog, useSaveInventory } from '@/lib/api'
import type { CatalogRole } from '@/lib/api'
import { cn } from '@/lib/cn'

// ── Types ──────────────────────────────────────────────────────────────────────

type VarType = 'boolean' | 'integer' | 'string' | 'list' | 'dict'

interface InvVar {
  key: string
  type: VarType
  value: unknown
}

function inferType(v: unknown): VarType {
  if (typeof v === 'boolean') return 'boolean'
  if (typeof v === 'number') return 'integer'
  if (Array.isArray(v)) return 'list'
  if (typeof v === 'object' && v !== null) return 'dict'
  return 'string'
}

function defaultValue(type: VarType): unknown {
  switch (type) {
    case 'boolean': return false
    case 'integer': return 0
    case 'list': return ['']
    case 'dict': return { '': '' }
    default: return ''
  }
}

function plainValue(val: unknown, type: VarType): unknown {
  if (type === 'boolean') return Boolean(val)
  if (type === 'integer') return Number(val) || 0
  if (type === 'string') return val == null ? '' : String(val)
  if (type === 'list') return Array.isArray(val) ? (val as unknown[]).map(String) : ['']
  if (type === 'dict') {
    if (typeof val === 'object' && val !== null && !Array.isArray(val))
      return Object.fromEntries(Object.entries(val as object).map(([k, v]) => [k, String(v)]))
    return {}
  }
  return val
}

function toVars(data: Record<string, unknown>): InvVar[] {
  return Object.entries(data)
    .map(([key, value]) => ({ key, type: inferType(value), value }))
    .sort((a, b) => a.key.localeCompare(b.key))
}

function fromVars(vars: InvVar[]): Record<string, unknown> {
  return Object.fromEntries(vars.map(v => [v.key, v.value]))
}

// ── Role extraction from variable name ────────────────────────────────────────
// Patterns:
//   sonarr_role_docker_image_tag  → sonarr
//   global_themepark_theme        → global_themepark
//   use_cloudplow                 → general
//   nvidia_enabled                → nvidia

const GENERAL_PREFIXES = ['use_', 'skip_', 'enable_', 'disable_']

function extractRole(key: string): string {
  if (GENERAL_PREFIXES.some(p => key.startsWith(p))) return 'general'
  const roleIdx = key.indexOf('_role_')
  if (roleIdx !== -1) return key.slice(0, roleIdx)
  if (key.startsWith('global_')) return key.split('_').slice(0, 2).join('_')
  const first = key.split('_')[0]
  return first || 'general'
}

// ── Type badge ─────────────────────────────────────────────────────────────────

const TYPE_STYLES: Record<VarType, string> = {
  boolean: 'bg-sky-100 text-sky-700 border-sky-200',
  integer: 'bg-violet-100 text-violet-700 border-violet-200',
  string: 'bg-emerald-100 text-emerald-700 border-emerald-200',
  list: 'bg-orange-100 text-orange-700 border-orange-200',
  dict: 'bg-amber-100 text-amber-700 border-amber-200',
}

function TypeBadge({ type }: { type: VarType }) {
  return (
    <span className={`text-xs px-1.5 py-0.5 rounded border font-mono shrink-0 ${TYPE_STYLES[type]}`}>
      {type}
    </span>
  )
}

// ── Value summary (collapsed) ──────────────────────────────────────────────────

function ValueSummary({ type, value }: { type: VarType; value: unknown }) {
  if (type === 'boolean')
    return <span className={`text-xs font-mono font-medium ${value ? 'text-emerald-600' : 'text-rose-500'}`}>{String(value)}</span>
  if (type === 'integer')
    return <span className="text-xs font-mono text-foreground">{String(value)}</span>
  if (type === 'string') {
    const s = String(value)
    return <span className="text-xs font-mono text-muted-foreground truncate max-w-[18rem]">"{s}"</span>
  }
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

// ── Type selector ──────────────────────────────────────────────────────────────

function TypeSelect({ value, onChange }: { value: VarType; onChange: (t: VarType) => void }) {
  return (
    <select value={value} onChange={e => onChange(e.target.value as VarType)}
      className="h-9 rounded-md border border-input bg-background px-3 text-sm focus:outline-none focus:ring-1 focus:ring-ring">
      {(['boolean', 'integer', 'string', 'list', 'dict'] as VarType[]).map(t => (
        <option key={t} value={t}>{t}</option>
      ))}
    </select>
  )
}

// ── Value editors ──────────────────────────────────────────────────────────────

function BoolEditor({ value, onChange }: { value: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="flex items-center gap-2">
      {[true, false].map(b => (
        <button key={String(b)} type="button" onClick={() => onChange(b)}
          className={`px-3 py-1.5 rounded text-sm font-mono border transition-colors ${
            value === b
              ? b ? 'bg-emerald-100 text-emerald-700 border-emerald-300 font-medium'
                  : 'bg-rose-100 text-rose-700 border-rose-300 font-medium'
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
    <div className="space-y-1.5">
      {value.map((item, i) => (
        <div key={i} className="flex items-center gap-2">
          <Input value={item}
            onChange={e => { const n = [...value]; n[i] = e.target.value; onChange(n) }}
            placeholder="/host/path:container_path" className="font-mono text-sm" />
          <Button size="sm" variant="ghost" className="h-8 w-8 p-0 text-muted-foreground hover:text-destructive shrink-0"
            onClick={() => onChange(value.filter((_, j) => j !== i))}>
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      ))}
      <Button size="sm" variant="outline" className="h-7 text-xs"
        onClick={() => onChange([...value, ''])}>
        <Plus className="h-3 w-3 mr-1" /> Add item
      </Button>
    </div>
  )
}

function DictEditor({ value, onChange }: { value: Record<string, string>; onChange: (v: Record<string, string>) => void }) {
  const entries = Object.entries(value)
  return (
    <div className="space-y-1.5">
      {entries.map(([k, v], i) => (
        <div key={i} className="flex items-center gap-2">
          <Input value={k}
            onChange={e => onChange(Object.fromEntries(entries.map(([ek, ev], j) => [j === i ? e.target.value : ek, ev])))}
            placeholder="KEY" className="font-mono text-sm w-48 shrink-0" />
          <span className="text-muted-foreground text-sm shrink-0">:</span>
          <Input value={v}
            onChange={e => onChange(Object.fromEntries(entries.map(([ek, ev], j) => [ek, j === i ? e.target.value : ev])))}
            placeholder="value" className="font-mono text-sm" />
          <Button size="sm" variant="ghost" className="h-8 w-8 p-0 text-muted-foreground hover:text-destructive shrink-0"
            onClick={() => onChange(Object.fromEntries(entries.filter((_, j) => j !== i)))}>
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      ))}
      <Button size="sm" variant="outline" className="h-7 text-xs"
        onClick={() => onChange({ ...value, '': '' })}>
        <Plus className="h-3 w-3 mr-1" /> Add key
      </Button>
    </div>
  )
}

function ValueEditor({ type, value, onChange }: { type: VarType; value: unknown; onChange: (v: unknown) => void }) {
  if (type === 'boolean') return <BoolEditor value={value as boolean} onChange={onChange} />
  if (type === 'integer') return <Input type="number" value={value as number} onChange={e => onChange(Number(e.target.value))} className="font-mono text-sm w-40" />
  if (type === 'string') return <Input value={value as string} onChange={e => onChange(e.target.value)} placeholder='""' className="font-mono text-sm" />
  if (type === 'list') return <ListEditor value={value as string[]} onChange={onChange} />
  if (type === 'dict') return <DictEditor value={value as Record<string, string>} onChange={onChange} />
  return null
}

// ── Variable row ───────────────────────────────────────────────────────────────

function VarRow({ v, isExpanded, onToggle, onChange, onDelete }: {
  v: InvVar; isExpanded: boolean
  onToggle: () => void
  onChange: (updated: InvVar) => void
  onDelete: () => void
}) {
  return (
    <div className="border border-border rounded-lg overflow-hidden">
      <div className="flex items-center gap-2 px-3 py-2 bg-card hover:bg-muted/20 transition-colors">
        <button type="button" onClick={onToggle} className="flex items-center gap-2 flex-1 text-left min-w-0">
          {isExpanded
            ? <ChevronDown className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
            : <ChevronRight className="h-3.5 w-3.5 text-muted-foreground shrink-0" />}
          <span className="font-mono text-sm font-medium truncate">{v.key}</span>
          <TypeBadge type={v.type} />
          {!isExpanded && <ValueSummary type={v.type} value={v.value} />}
        </button>
        <Button size="sm" variant="ghost" className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive shrink-0" onClick={onDelete}>
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>

      {isExpanded && (
        <div className="p-4 border-t border-border/50 space-y-4 bg-muted/5">
          <div className="flex items-end gap-4">
            <div className="flex-1">
              <label className="text-xs text-muted-foreground block mb-1">Variable name</label>
              <Input value={v.key} onChange={e => onChange({ ...v, key: e.target.value })} className="font-mono text-sm" />
            </div>
            <div>
              <label className="text-xs text-muted-foreground block mb-1">Type</label>
              <TypeSelect value={v.type} onChange={t => onChange({ ...v, type: t, value: defaultValue(t) })} />
            </div>
          </div>
          <div>
            <label className="text-xs text-muted-foreground block mb-1.5">Value</label>
            <ValueEditor type={v.type} value={v.value} onChange={val => onChange({ ...v, value: val })} />
          </div>
        </div>
      )}
    </div>
  )
}

// ── Add variable form ──────────────────────────────────────────────────────────

function AddVarForm({ onAdd, onCancel }: { onAdd: (v: InvVar) => void; onCancel: () => void }) {
  const [key, setKey] = useState('')
  const [type, setType] = useState<VarType>('string')
  const [value, setValue] = useState<unknown>('')

  const changeType = (t: VarType) => { setType(t); setValue(defaultValue(t)) }
  const submit = () => {
    if (!key.trim()) return
    onAdd({ key: key.trim(), type, value })
    setKey(''); setType('string'); setValue('')
  }

  return (
    <div className="border-2 border-primary/30 border-dashed rounded-lg p-4 bg-primary/5 space-y-4">
      <p className="text-xs text-muted-foreground">
        <span className="font-medium text-foreground">Naming pattern: </span>
        <code className="font-mono bg-muted px-1 rounded">rolename_role_variable_custom</code>
        {' or '}
        <code className="font-mono bg-muted px-1 rounded">rolename_setting</code>
      </p>
      <div className="flex items-end gap-4">
        <div className="flex-1">
          <label className="text-xs text-muted-foreground block mb-1">Variable name</label>
          <Input autoFocus value={key} onChange={e => setKey(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && submit()}
            placeholder="sonarr_role_docker_image_tag" className="font-mono text-sm" />
        </div>
        <div>
          <label className="text-xs text-muted-foreground block mb-1">Type</label>
          <TypeSelect value={type} onChange={changeType} />
        </div>
      </div>
      <div>
        <label className="text-xs text-muted-foreground block mb-1.5">Value</label>
        <ValueEditor type={type} value={value} onChange={setValue} />
      </div>
      <div className="flex gap-2">
        <Button size="sm" onClick={submit} disabled={!key.trim()} className="h-8">
          <Plus className="h-3.5 w-3.5 mr-1" /> Add variable
        </Button>
        <Button size="sm" variant="ghost" onClick={onCancel} className="h-8">Cancel</Button>
      </div>
    </div>
  )
}

// ── Catalog panel ──────────────────────────────────────────────────────────────

function CatalogPanel({ open, onClose, currentVars, onAdd }: {
  open: boolean
  onClose: () => void
  currentVars: InvVar[]
  onAdd: (v: InvVar) => void
}) {
  const { data, isLoading } = useInventoryCatalog({ enabled: open })
  const [search, setSearch] = useState('')
  const [selectedRole, setSelectedRole] = useState<string | null>(null)
  // per-variable scope: 'role' = role-scoped, or an instance name
  const [scopeMap, setScopeMap] = useState<Record<string, string>>({})

  // Build instances map from current inventory: { sonarr: ['sonarrhd', ...] }
  const instancesMap = useMemo(() => {
    const map: Record<string, string[]> = {}
    for (const v of currentVars) {
      const m = v.key.match(/^(.+)_instances$/)
      if (m && Array.isArray(v.value)) {
        map[m[1]] = (v.value as string[]).filter(Boolean)
      }
    }
    return map
  }, [currentVars])

  const addedKeys = useMemo(() => new Set(currentVars.map(v => v.key)), [currentVars])

  const roles: CatalogRole[] = useMemo(() => {
    if (!data?.roles) return []
    const q = search.toLowerCase().trim()
    return Object.values(data.roles).filter(r =>
      !q ||
      r.role.toLowerCase().includes(q) ||
      Object.keys(r.variables).some(k => k.toLowerCase().includes(q))
    )
  }, [data, search])

  // Auto-select first visible role
  useEffect(() => {
    if (roles.length > 0 && (!selectedRole || !roles.find(r => r.role === selectedRole))) {
      setSelectedRole(roles[0].role)
    }
  }, [roles, selectedRole])

  const roleData = useMemo(
    () => (selectedRole ? data?.roles[selectedRole] ?? null : null),
    [data, selectedRole]
  )

  const filteredVars = useMemo(() => {
    if (!roleData) return []
    const q = search.toLowerCase().trim()
    return Object.entries(roleData.variables).filter(([k]) => !q || k.toLowerCase().includes(q))
  }, [roleData, search])

  // Count how many variables from each role are already in inventory
  const addedByRole = useMemo(() => {
    if (!data?.roles) return {}
    const counts: Record<string, number> = {}
    for (const [rname, role] of Object.entries(data.roles)) {
      counts[rname] = Object.keys(role.variables).filter(k => addedKeys.has(k)).length
    }
    return counts
  }, [data, addedKeys])

  return (
    <Dialog open={open} onOpenChange={o => !o && onClose()}>
      <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Content className="fixed inset-0 z-50 bg-card flex flex-col outline-none">
          {/* Header */}
          <div className="flex items-center gap-3 px-5 py-3 border-b border-border shrink-0 bg-card">
            <BookOpen className="h-4 w-4 text-primary shrink-0" />
            <h2 className="text-sm font-semibold">Variable Catalog</h2>
            <div className="relative flex-1 max-w-sm">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
              <Input value={search} onChange={e => setSearch(e.target.value)}
                placeholder="Search variables or roles..."
                className="pl-8 h-8 text-sm" />
            </div>
            {data && (
              <span className="text-xs text-muted-foreground">
                {Object.keys(data.roles).length} roles
              </span>
            )}
            <DialogClose asChild>
              <Button size="sm" variant="ghost" className="h-8 w-8 p-0 ml-auto shrink-0">
                <X className="h-4 w-4" />
              </Button>
            </DialogClose>
          </div>

          {/* Body */}
          {isLoading ? (
            <div className="flex-1 flex flex-col items-center justify-center gap-3 text-muted-foreground">
              <Loader2 className="h-6 w-6 animate-spin" />
              <span className="text-sm">Scanning role defaults...</span>
              <span className="text-xs">This may take a moment on first load</span>
            </div>
          ) : (
            <div className="flex flex-1 overflow-hidden">

              {/* Role sidebar */}
              <div className="w-56 shrink-0 border-r border-border overflow-y-auto bg-muted/10">
                <div className="py-1">
                  {roles.length === 0 && (
                    <p className="px-4 py-3 text-xs text-muted-foreground">No roles found</p>
                  )}
                  {roles.map(role => {
                    const count = addedByRole[role.role] ?? 0
                    const isSelected = selectedRole === role.role
                    return (
                      <button key={role.role} onClick={() => setSelectedRole(role.role)}
                        className={cn(
                          'w-full text-left px-3 py-2 text-sm flex items-center gap-2 transition-colors border-l-2',
                          isSelected
                            ? 'bg-primary/10 text-primary border-primary font-medium'
                            : 'text-foreground border-transparent hover:bg-muted/50 hover:border-border'
                        )}>
                        <span className="font-mono truncate flex-1 text-xs">{role.role}</span>
                        {count > 0 && (
                          <span className={cn('text-xs rounded-full w-5 h-5 flex items-center justify-center shrink-0 font-medium',
                            isSelected ? 'bg-primary text-primary-foreground' : 'bg-primary/10 text-primary')}>
                            {count}
                          </span>
                        )}
                        {role.repo === 'sandbox' && (
                          <span className="text-[9px] text-muted-foreground shrink-0 font-medium border border-border rounded px-0.5">SB</span>
                        )}
                      </button>
                    )
                  })}
                </div>
              </div>

              {/* Variable list */}
              <div className="flex-1 overflow-y-auto">
                {roleData ? (
                  <div className="p-4">
                    <div className="flex items-center gap-2 mb-3 pb-2 border-b border-border/50">
                      <h3 className="font-mono font-semibold text-sm">{roleData.role}</h3>
                      <span className="text-xs text-muted-foreground">
                        {filteredVars.length} variable{filteredVars.length !== 1 ? 's' : ''}
                      </span>
                      {roleData.repo === 'sandbox' && (
                        <span className="text-xs text-muted-foreground border border-border px-1.5 py-0.5 rounded">sandbox</span>
                      )}
                    </div>

                    <div className="space-y-1">
                      {filteredVars.map(([name, defVal]) => {
                        const type = inferType(defVal)

                        // Instance-scoped support
                        // e.g. sonarr_role_setting → instances: [sonarrhd, sonarruhd]
                        const roleMatch = name.match(/^(.+?)_role_(.+)$/)
                        const instances = roleMatch
                          ? (instancesMap[roleMatch[1]] ?? [])
                          : []
                        const scope = scopeMap[name] ?? 'role'

                        // Effective key: role-scoped = original, instance = prefix swap
                        const effectiveKey = (scope === 'role' || !roleMatch)
                          ? name
                          : `${scope}_${roleMatch[2]}`

                        const inInv = addedKeys.has(effectiveKey)

                        return (
                          <div key={name} className={cn(
                            'flex items-center gap-3 px-3 py-2 rounded-md border transition-colors',
                            inInv
                              ? 'bg-emerald-50 border-emerald-200'
                              : 'bg-card border-border hover:bg-muted/30'
                          )}>
                            {/* Variable name — shows effective key if instance-scoped */}
                            <div className="flex-1 min-w-0">
                              <span className="font-mono text-xs font-medium truncate block">{name}</span>
                              {scope !== 'role' && (
                                <span className="font-mono text-[10px] text-primary truncate block">
                                  → {effectiveKey}
                                </span>
                              )}
                            </div>

                            <TypeBadge type={type} />

                            <span className="text-xs text-muted-foreground truncate max-w-[10rem] shrink-0 hidden lg:block">
                              <ValueSummary type={type} value={defVal} />
                            </span>

                            {/* Scope selector (only for role-pattern vars that have instances) */}
                            {instances.length > 0 && (
                              <select
                                value={scope}
                                onChange={e => setScopeMap(m => ({ ...m, [name]: e.target.value }))}
                                className="h-6 text-[11px] border border-input rounded px-1.5 bg-background font-mono shrink-0 focus:outline-none focus:ring-1 focus:ring-ring"
                              >
                                <option value="role">All instances</option>
                                {instances.map(inst => (
                                  <option key={inst} value={inst}>{inst}</option>
                                ))}
                              </select>
                            )}

                            {inInv ? (
                              <span className="text-xs text-emerald-700 bg-emerald-100 px-2 py-0.5 rounded border border-emerald-200 shrink-0 font-medium">
                                ✓ Added
                              </span>
                            ) : (
                              <Button size="sm" variant="outline" className="h-6 text-xs px-2 shrink-0"
                                onClick={() => onAdd({ key: effectiveKey, type, value: plainValue(defVal, type) })}>
                                + Add
                              </Button>
                            )}
                          </div>
                        )
                      })}
                    </div>
                  </div>
                ) : (
                  <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
                    Select a role from the sidebar
                  </div>
                )}
              </div>
            </div>
          )}
        </DialogPrimitive.Content>
      </DialogPortal>
    </Dialog>
  )
}

// ── Main page ──────────────────────────────────────────────────────────────────

export function Inventory() {
  const { data, isLoading } = useInventory()
  const saveInv = useSaveInventory()

  const [vars, setVars] = useState<InvVar[]>([])
  const [savedSnapshot, setSavedSnapshot] = useState<string>('{}')
  const [search, setSearch] = useState('')
  const [expandedKey, setExpandedKey] = useState<string | null>(null)
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set())
  const [showAdd, setShowAdd] = useState(false)
  const [showCatalog, setShowCatalog] = useState(false)

  useEffect(() => {
    if (data?.data) {
      const v = toVars(data.data)
      setVars(v)
      setSavedSnapshot(JSON.stringify(fromVars(v)))
    }
  }, [data])

  const dirty = useMemo(
    () => JSON.stringify(fromVars(vars)) !== savedSnapshot,
    [vars, savedSnapshot]
  )

  const filtered = useMemo(() => {
    const q = search.toLowerCase().trim()
    if (!q) return vars
    return vars.filter(v => v.key.toLowerCase().includes(q) || String(v.value).toLowerCase().includes(q))
  }, [vars, search])

  const grouped = useMemo(() => {
    const map = new Map<string, InvVar[]>()
    for (const v of filtered) {
      const role = extractRole(v.key)
      if (!map.has(role)) map.set(role, [])
      map.get(role)!.push(v)
    }
    return Array.from(map.entries()).sort(([a], [b]) => a.localeCompare(b))
  }, [filtered])

  const toggleGroup = (role: string) =>
    setCollapsedGroups(prev => {
      const next = new Set(prev)
      if (next.has(role)) next.delete(role); else next.add(role)
      return next
    })

  const handleChange = (oldKey: string, updated: InvVar) => {
    setVars(prev => prev.map(v => v.key === oldKey ? updated : v))
    if (expandedKey === oldKey && updated.key !== oldKey) setExpandedKey(updated.key)
  }

  const handleDelete = (key: string) => {
    setVars(prev => prev.filter(v => v.key !== key))
    if (expandedKey === key) setExpandedKey(null)
  }

  const handleAdd = (newVar: InvVar) => {
    setVars(prev => {
      const without = prev.filter(v => v.key !== newVar.key)
      return [...without, newVar].sort((a, b) => a.key.localeCompare(b.key))
    })
    setShowAdd(false)
    setExpandedKey(newVar.key)
  }

  const handleCatalogAdd = (newVar: InvVar) => {
    setVars(prev => {
      if (prev.some(v => v.key === newVar.key)) return prev
      return [...prev, newVar].sort((a, b) => a.key.localeCompare(b.key))
    })
  }

  const handleSave = async () => {
    const payload = fromVars(vars)
    await saveInv.mutateAsync(payload)
    setSavedSnapshot(JSON.stringify(payload))
  }

  if (isLoading) {
    return (
      <div className="p-6 space-y-2">
        <div className="h-6 bg-muted/30 rounded animate-pulse w-32 mb-6" />
        {[...Array(6)].map((_, i) => (
          <div key={i} className="h-10 bg-muted/20 rounded animate-pulse" />
        ))}
      </div>
    )
  }

  return (
    <div className="p-6">
      {/* Catalog panel */}
      <CatalogPanel
        open={showCatalog}
        onClose={() => setShowCatalog(false)}
        currentVars={vars}
        onAdd={handleCatalogAdd}
      />

      {/* Page title */}
      <div className="flex items-start justify-between mb-5">
        <div>
          <h1 className="text-xl font-semibold">Inventory</h1>
          <p className="text-xs text-muted-foreground font-mono mt-0.5">
            /srv/git/saltbox/inventories/host_vars/localhost.yml
          </p>
        </div>
        <a href="https://docs.saltbox.dev/saltbox/inventory/" target="_blank" rel="noopener noreferrer"
          className="flex items-center gap-1 text-xs text-muted-foreground hover:text-primary transition-colors">
          <ExternalLink className="h-3.5 w-3.5" /> Docs
        </a>
      </div>

      {/* Save bar */}
      {dirty && (
        <div className="sticky top-0 z-20 -mx-6 px-6 py-2 mb-4 bg-background/95 backdrop-blur border-b border-border flex items-center justify-between">
          <span className="text-sm text-muted-foreground">Unsaved changes</span>
          <Button size="sm" onClick={handleSave} disabled={saveInv.isPending} className="h-8 gap-1.5">
            <Save className="h-3.5 w-3.5" />
            {saveInv.isPending ? 'Saving...' : 'Save'}
          </Button>
        </div>
      )}

      {/* Toolbar */}
      <div className="flex items-center gap-2 mb-4">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          <Input value={search} onChange={e => setSearch(e.target.value)}
            placeholder="Search variables..." className="pl-8 h-8 text-sm" />
        </div>
        <span className="text-xs text-muted-foreground">
          {vars.length} variable{vars.length !== 1 ? 's' : ''}
        </span>
        <div className="flex items-center gap-2 ml-auto">
          <Button size="sm" variant="outline" className="h-8 gap-1.5"
            onClick={() => setShowCatalog(true)}>
            <BookOpen className="h-3.5 w-3.5" /> Browse catalog
          </Button>
          <Button size="sm" variant="outline" className="h-8 gap-1.5"
            onClick={() => setShowAdd(s => !s)}>
            <Plus className="h-3.5 w-3.5" /> Add variable
          </Button>
        </div>
      </div>

      {/* Add form */}
      {showAdd && (
        <div className="mb-4">
          <AddVarForm onAdd={handleAdd} onCancel={() => setShowAdd(false)} />
        </div>
      )}

      {/* Grouped variable list */}
      <div className="space-y-4">
        {filtered.length === 0 && (
          <div className="text-center py-12 text-muted-foreground text-sm">
            {search
              ? 'No variables match the search.'
              : 'No variables yet. Click "Browse catalog" to add from role defaults, or "Add variable" to enter manually.'}
          </div>
        )}
        {grouped.map(([role, groupVars]) => {
          const collapsed = collapsedGroups.has(role)
          return (
            <div key={role}>
              {/* Group header */}
              <button
                type="button"
                onClick={() => toggleGroup(role)}
                className="flex items-center gap-2 w-full text-left mb-1.5 group"
              >
                {collapsed
                  ? <ChevronRight className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                  : <ChevronDown className="h-3.5 w-3.5 text-muted-foreground shrink-0" />}
                <span className="font-mono text-sm font-semibold text-foreground">{role}</span>
                <span className="text-xs text-muted-foreground bg-muted px-1.5 py-0.5 rounded-full">
                  {groupVars.length}
                </span>
                <span className="flex-1 border-t border-border/50 ml-1" />
              </button>

              {/* Variables in group */}
              {!collapsed && (
                <div className="space-y-1.5">
                  {groupVars.map(v => (
                    <VarRow key={v.key} v={v}
                      isExpanded={expandedKey === v.key}
                      onToggle={() => setExpandedKey(k => k === v.key ? null : v.key)}
                      onChange={updated => handleChange(v.key, updated)}
                      onDelete={() => handleDelete(v.key)}
                    />
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </div>

      {/* Info box (empty state) */}
      {vars.length === 0 && !search && (
        <div className="mt-8 p-4 rounded-lg border border-border bg-muted/20 flex gap-3">
          <Info className="h-4 w-4 text-muted-foreground shrink-0 mt-0.5" />
          <div className="text-sm text-muted-foreground space-y-2">
            <p>Inventory lets you override Ansible role variables persistently — changes survive git updates.</p>
            <ul className="text-xs font-mono space-y-0.5 text-muted-foreground/80">
              <li>sonarr_role_docker_image_tag: "nightly"</li>
              <li>use_cloudplow: false</li>
              <li>code_server_role_docker_volumes_custom: ["/srv:/host_srv"]</li>
            </ul>
          </div>
        </div>
      )}
    </div>
  )
}
