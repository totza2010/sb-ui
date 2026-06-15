/**
 * YamlForm — render a parsed YAML value as an editable form (objects become
 * collapsible sections, scalars become inputs/toggles, arrays become add/remove
 * lists). Used by the file editor so YAML config can be edited structurally
 * instead of as raw text.
 */
import { useState } from 'react'
import { ChevronDown, ChevronRight, Plus, X } from 'lucide-react'
import { cn } from '@/lib/cn'

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type J = any

function defaultLike(sample: J): J {
  if (typeof sample === 'boolean') return false
  if (typeof sample === 'number') return 0
  if (Array.isArray(sample)) return []
  if (sample && typeof sample === 'object') return Object.fromEntries(Object.keys(sample).map(k => [k, defaultLike(sample[k])]))
  return ''
}

function Scalar({ value, onChange }: { value: J; onChange: (v: J) => void }) {
  if (typeof value === 'boolean') {
    return (
      <button type="button" onClick={() => onChange(!value)}
        className={cn('w-9 h-5 rounded-full relative transition-colors shrink-0', value ? 'bg-primary' : 'bg-muted')}>
        <span className={cn('absolute top-0.5 h-4 w-4 rounded-full bg-white transition-all', value ? 'left-[18px]' : 'left-0.5')} />
      </button>
    )
  }
  if (typeof value === 'number') {
    return <input type="number" value={value}
      onChange={e => onChange(e.target.value === '' ? null : Number(e.target.value))}
      className="h-7 w-full text-xs font-mono bg-background border border-border rounded px-2" />
  }
  return <input value={value ?? ''} onChange={e => onChange(e.target.value)}
    className="h-7 w-full text-xs font-mono bg-background border border-border rounded px-2" />
}

export function YamlNode({ value, onChange, depth = 0 }: {
  value: J; onChange: (v: J) => void; depth?: number
}) {
  if (Array.isArray(value)) {
    return (
      <div className="space-y-1">
        {value.map((item, i) => (
          <div key={i} className="flex gap-1 items-start">
            <div className="flex-1 min-w-0">
              <YamlNode value={item} depth={depth + 1} onChange={v => { const a = [...value]; a[i] = v; onChange(a) }} />
            </div>
            <button type="button" className="h-7 w-6 grid place-items-center text-muted-foreground hover:text-red-500 shrink-0"
              onClick={() => { const a = [...value]; a.splice(i, 1); onChange(a) }}>
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        ))}
        <button type="button" className="text-xs text-muted-foreground hover:text-foreground flex items-center gap-1"
          onClick={() => onChange([...value, defaultLike(value[0])])}>
          <Plus className="h-3 w-3" />add
        </button>
      </div>
    )
  }
  if (value && typeof value === 'object') {
    return (
      <div className={cn('space-y-1', depth > 0 && 'pl-3 border-l border-border/60')}>
        {Object.entries(value).map(([k, v]) => (
          <Field key={k} label={k} value={v} depth={depth} onChange={nv => onChange({ ...value, [k]: nv })} />
        ))}
      </div>
    )
  }
  return <Scalar value={value} onChange={onChange} />
}

function Field({ label, value, depth, onChange }: {
  label: string; value: J; depth: number; onChange: (v: J) => void
}) {
  const nested = value && typeof value === 'object'
  const [open, setOpen] = useState(depth < 2)
  if (nested) {
    return (
      <div>
        <button type="button" onClick={() => setOpen(o => !o)}
          className="flex items-center gap-1 text-xs font-medium text-foreground py-0.5">
          {open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
          {label}
          <span className="text-muted-foreground/60 font-normal">
            {Array.isArray(value) ? `[${value.length}]` : ''}
          </span>
        </button>
        {open && <div className="mt-1">
          <YamlNode value={value} depth={depth + 1} onChange={onChange} />
        </div>}
      </div>
    )
  }
  return (
    <div className="flex items-center gap-2">
      <label className="text-xs text-muted-foreground font-mono w-2/5 truncate shrink-0" title={label}>{label}</label>
      <div className="flex-1 min-w-0"><Scalar value={value} onChange={onChange} /></div>
    </div>
  )
}
