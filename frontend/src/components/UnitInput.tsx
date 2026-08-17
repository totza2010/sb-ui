/**
 * UnitInput — a number field with a unit dropdown beside it, so a size or duration is
 * entered as "2000" + "K" or "2" + "M" instead of typing the suffix by hand. The value it
 * reads and writes is the same suffixed string the backend already parses ("500G", "15m"),
 * so nothing downstream changes — this is purely how the value is typed.
 */
import { useEffect, useState } from 'react'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/cn'

export type Unit = readonly [suffix: string, label: string]

// Sizes/speeds map to rclone's K/M/G/T (the backend's parseSize is case-insensitive and
// treats them as binary multiples). Speeds reuse the same set; the "/s" is implied by the
// field's label.
export const SIZE_UNITS: Unit[] = [['K', 'KB'], ['M', 'MB'], ['G', 'GB'], ['T', 'TB']]
// Durations map to Go/rclone duration suffixes (s/m/h/d), valid for --min-age and friends.
export const DUR_UNITS: Unit[] = [['s', 'sec'], ['m', 'min'], ['h', 'hr'], ['d', 'day']]

// splitValue pulls the number and any trailing unit out of e.g. "500G" → ["500","G"].
function splitValue(v: string): { num: string; unit?: string } {
  const m = /^\s*(-?[\d.]+)?\s*([a-zA-Z]+)?\s*$/.exec(v ?? '')
  return { num: m?.[1] ?? '', unit: m?.[2] || undefined }
}

export function UnitInput({
  value, onChange, units, defaultUnit, placeholder, className, numClassName, min = 0,
}: {
  value: string
  onChange: (v: string) => void
  units?: Unit[]
  defaultUnit?: string
  placeholder?: string
  className?: string
  numClassName?: string
  min?: number
}) {
  const opts = units ?? SIZE_UNITS
  const { num, unit: rawUnit } = splitValue(value)
  // Match the value's suffix to a known unit (case-insensitively) so "15m" selects "m".
  const matched = rawUnit ? opts.find((u) => u[0].toLowerCase() === rawUnit.toLowerCase())?.[0] : undefined
  const [unit, setUnit] = useState(matched ?? defaultUnit ?? opts[0][0])

  // If the value later arrives with a different explicit unit (e.g. loaded config), follow it.
  useEffect(() => { if (matched && matched !== unit) setUnit(matched) }, [matched]) // eslint-disable-line react-hooks/exhaustive-deps

  // Empty number → empty value (so "unlimited"/"auto" placeholders still work); otherwise
  // recombine number + the selected unit into the suffixed string the backend expects.
  const emit = (n: string, u: string) => {
    const t = n.trim()
    onChange(t === '' ? '' : `${t}${u}`)
  }

  return (
    <div className={cn('flex', className)}>
      <Input
        type="number" inputMode="decimal" min={min}
        className={cn('h-8 min-w-0 flex-1 rounded-r-none', numClassName)}
        value={num} placeholder={placeholder}
        onChange={(e) => emit(e.target.value, unit)}
      />
      <select
        aria-label="unit"
        className="h-8 shrink-0 rounded-md rounded-l-none border border-l-0 border-input bg-background px-1.5 text-xs text-foreground"
        value={unit}
        onChange={(e) => { setUnit(e.target.value); emit(num, e.target.value) }}
      >
        {opts.map(([s, l]) => <option key={s} value={s}>{l}</option>)}
      </select>
    </div>
  )
}
