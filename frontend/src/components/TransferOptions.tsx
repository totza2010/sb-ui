/**
 * TransferOptions — the shared rclone-flags editor (transfers/checkers/tps/retries/
 * bandwidth, compare method, include/exclude, extra flags + the teldrive ban-avoidance
 * banner). Used by both Transfers (per task) and the Uploader (global for every
 * destination), so the two stay in sync.
 */
import { useMemo, useState } from 'react'
import { useRcloneProviders, type TransferOpts, type FlagInfo } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/cn'
import { Zap, Plus, X } from 'lucide-react'

// A caller-supplied checkbox rendered alongside the built-in flags (e.g. the Uploader's
// "delete emptied source folders", which is a move flag but not part of TransferOpts).
export interface ExtraCheck { label: string; hint: string; v: boolean; on: (b: boolean) => void }

export function TransferOptions({ opts, setOpts, remoteTypes, op, extraChecks }: {
  opts: TransferOpts
  setOpts: (updater: (o: TransferOpts) => TransferOpts) => void
  remoteTypes: string[]
  op?: string
  extraChecks?: ExtraCheck[]
}) {
  const { data: providers } = useRcloneProviders()
  const { available, catalog } = useMemo(() => {
    const list: FlagInfo[] = [...(providers?.global ?? [])]
    for (const t of remoteTypes) for (const fl of providers?.backends?.[t] ?? []) list.push(fl)
    return { available: list, catalog: new Map(list.map((f) => [f.flag, f])) }
  }, [remoteTypes, providers])

  const setOpt = <K extends keyof TransferOpts>(k: K, v: TransferOpts[K]) => setOpts((o) => ({ ...o, [k]: v }))
  const [flagList, setFlagList] = useState(false)
  const [flagSearch, setFlagSearch] = useState('')
  const addExtraFlag = (flag: string) => setOpts((o) => (o.extra?.some((e) => e.flag === flag) ? o : { ...o, extra: [...(o.extra ?? []), { flag, value: '' }] }))
  const updExtra = (i: number, v: string) => setOpts((o) => { const e = [...(o.extra ?? [])]; e[i] = { ...e[i], value: v }; return { ...o, extra: e } })
  const rmExtra = (i: number) => setOpts((o) => ({ ...o, extra: (o.extra ?? []).filter((_, j) => j !== i) }))

  return (
    <div className="space-y-3 rounded-md border border-border p-3">
      {remoteTypes.includes('teldrive') && (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs space-y-1.5">
          <p className="font-medium text-foreground flex items-center gap-1.5"><Zap className="h-3.5 w-3.5 text-amber-500" />teldrive destination — ban-avoidance recommended</p>
          <p className="text-muted-foreground">Telegram throttles by API request rate (not bytes). Pace rclone with <span className="font-mono text-foreground">tps 8 · transfers 4 · checkers 4</span> so it stays under flood limits — on FLOOD_WAIT the Uploader also auto-pauses the remote.</p>
          <Button size="sm" variant="outline" className="h-7" onClick={() => setOpts((o) => ({ ...o, tpslimit: 8, transfers: 4, checkers: 4 }))}>Apply recommended</Button>
        </div>
      )}
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-5">
        <NumField label="Transfers" v={opts.transfers} on={(n) => setOpt('transfers', n)} ph="4 (default)" />
        <NumField label="Checkers" v={opts.checkers} on={(n) => setOpt('checkers', n)} ph="8 (default)" />
        <NumField label="tps limit" v={opts.tpslimit} on={(n) => setOpt('tpslimit', n)} ph="off" />
        <NumField label="Retries" v={opts.retries} on={(n) => setOpt('retries', n)} ph="3 (default)" />
        <div className="space-y-1">
          <Label className="text-[11px]">Bandwidth</Label>
          <Input className="h-8" value={opts.bwlimit ?? ''} onChange={(e) => setOpt('bwlimit', e.target.value)} placeholder="unlimited" />
        </div>
      </div>
      <p className="text-[11px] text-muted-foreground">Blank = rclone defaults: transfers <span className="font-mono">4</span>, checkers <span className="font-mono">8</span>, retries <span className="font-mono">3</span>, tps <span className="font-mono">off</span>, bandwidth <span className="font-mono">unlimited</span>.</p>
      <div className="grid grid-cols-1 gap-x-4 gap-y-2 sm:grid-cols-2 xl:grid-cols-3">
        <Chk label="Skip existing" flag="--ignore-existing" hint="Never re-upload a file that already exists at the destination, even if it changed. Fastest for append-only libraries." v={!!opts.ignore_existing} on={(b) => setOpt('ignore_existing', b)} />
        <Chk label="Skip newer on dest" flag="--update" hint="Don't overwrite a destination file that's newer than the source — protects edits already made on the remote." v={!!opts.update} on={(b) => setOpt('update', b)} />
        <Chk label="Create empty src dirs" flag="--create-empty-src-dirs" hint="Recreate empty directories from the source on the destination, so the folder structure matches even where there are no files." v={!!opts.create_empty_src_dirs} on={(b) => setOpt('create_empty_src_dirs', b)} />
        <Chk label="No traverse" flag="--no-traverse" hint="Don't list the whole destination first; look up only the files being sent. Much faster when adding a few files to a large remote." v={!!opts.no_traverse} on={(b) => setOpt('no_traverse', b)} />
        <Chk label="Fast list" flag="--fast-list" hint="Fetch the remote's listing in one bulk call (fewer API requests, more memory). Helps on remotes that support it; teldrive does not." v={!!opts.fast_list} on={(b) => setOpt('fast_list', b)} />
        {extraChecks?.map((c) => (
          <Chk key={c.label} label={c.label} hint={c.hint} v={c.v} on={c.on} />
        ))}
      </div>
      <div className="space-y-1">
        <Label className="text-[11px]">Compare method</Label>
        <div className="flex flex-wrap gap-1.5">
          {([['', 'Size & mod-time'], ['checksum', 'Checksum'], ['size-only', 'Size only'], ['ignore-size', 'Ignore size']] as const).map(([v, lbl]) => (
            <Button key={v || 'default'} size="sm" variant={(opts.compare ?? '') === v ? 'default' : 'outline'} onClick={() => setOpt('compare', v)}>{lbl}</Button>
          ))}
        </div>
      </div>
      {op === 'sync' && (
        <div className="space-y-1">
          <Label className="text-[11px]">Sync delete order</Label>
          <div className="flex gap-1.5">
            {(['during', 'after', 'before'] as const).map((d) => (
              <Button key={d} size="sm" variant={opts.sync_delete === d ? 'default' : 'outline'} className="capitalize" onClick={() => setOpt('sync_delete', opts.sync_delete === d ? '' : d)}>{d}</Button>
            ))}
          </div>
        </div>
      )}
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
        <PatField label="Include (comma sep)" v={opts.include} on={(a) => setOpt('include', a)} ph="*.mkv, *.mp4" />
        <PatField label="Exclude (comma sep)" v={opts.exclude} on={(a) => setOpt('exclude', a)} ph="*.nfo, *.txt" />
      </div>

      {/* Extra rclone flags — pick from rclone's own flag list (global + the chosen
          remotes' backends, e.g. teldrive) with descriptions. */}
      <div className="space-y-1.5">
        <Label className="text-[11px]">
          Extra flags{remoteTypes.length > 0 && <span className="text-muted-foreground/70"> · incl. {remoteTypes.join(', ')}</span>}
        </Label>
        {(opts.extra ?? []).map((e, i) => {
          const info = catalog.get(e.flag)
          const isBool = info?.type === 'bool'
          return (
            <div key={i} className="rounded border border-border p-2 space-y-1">
              <div className="flex items-center gap-2">
                <span className="font-mono text-xs text-foreground">{e.flag}</span>
                {info?.type && <Badge variant="secondary" className="text-[9px]">{info.type}</Badge>}
                <button onClick={() => rmExtra(i)} className="ml-auto text-muted-foreground hover:text-destructive"><X className="h-3.5 w-3.5" /></button>
              </div>
              {info?.help && <p className="text-[11px] text-muted-foreground">{info.help}</p>}
              {!isBool && <Input className="h-7" value={e.value} placeholder="value" onChange={(ev) => updExtra(i, ev.target.value)} />}
            </div>
          )
        })}
        {flagList ? (
          <div className="rounded border border-border p-2 space-y-2">
            <Input className="h-8" autoFocus placeholder="Search flags…" value={flagSearch} onChange={(e) => setFlagSearch(e.target.value)} />
            <div className="max-h-56 overflow-auto space-y-0.5">
              {available
                .filter((f) => !flagSearch || f.flag.includes(flagSearch.toLowerCase()) || f.help.toLowerCase().includes(flagSearch.toLowerCase()))
                .slice(0, 80)
                .map((f) => (
                  <button key={f.flag} onClick={() => { addExtraFlag(f.flag); setFlagList(false); setFlagSearch('') }}
                    className="w-full text-left rounded px-2 py-1 hover:bg-accent">
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-xs text-foreground">{f.flag}</span>
                      {f.type && <Badge variant="secondary" className="text-[9px]">{f.type}</Badge>}
                    </div>
                    {f.help && <p className="text-[11px] text-muted-foreground truncate">{f.help}</p>}
                  </button>
                ))}
              {available.length === 0 && <p className="text-[11px] text-muted-foreground px-2 py-1">Flag list unavailable.</p>}
            </div>
            <Button size="sm" variant="ghost" onClick={() => { setFlagList(false); setFlagSearch('') }}>Close</Button>
          </div>
        ) : (
          <Button size="sm" variant="outline" className="gap-1.5" onClick={() => setFlagList(true)}><Plus className="h-3.5 w-3.5" />Add flag…</Button>
        )}
      </div>
    </div>
  )
}

function NumField({ label, v, on, ph }: { label: string; v?: number; on: (n: number | undefined) => void; ph?: string }) {
  return (
    <div className="space-y-1">
      <Label className="text-[11px]">{label}</Label>
      <Input className="h-8" type="number" min={0} value={v ?? ''} placeholder={ph}
        onChange={(e) => on(e.target.value === '' ? undefined : Math.max(0, parseInt(e.target.value, 10) || 0))} />
    </div>
  )
}

function Chk({ label, flag, hint, v, on }: { label: string; flag?: string; hint: string; v: boolean; on: (b: boolean) => void }) {
  return (
    <label className={cn('flex cursor-pointer gap-2 rounded-md border p-2 transition-colors', v ? 'border-primary/40 bg-primary/5' : 'border-border hover:bg-accent/30')}>
      <input type="checkbox" className="mt-0.5 shrink-0" checked={v} onChange={(e) => on(e.target.checked)} />
      <span className="min-w-0">
        <span className="flex flex-wrap items-baseline gap-1.5">
          <span className="text-xs font-medium text-foreground">{label}</span>
          {flag && <span className="font-mono text-[10px] text-muted-foreground">{flag}</span>}
        </span>
        <span className="mt-0.5 block text-[10px] leading-snug text-muted-foreground">{hint}</span>
      </span>
    </label>
  )
}

function PatField({ label, v, on, ph }: { label: string; v?: string[]; on: (a: string[]) => void; ph?: string }) {
  return (
    <div className="space-y-1">
      <Label className="text-[11px]">{label}</Label>
      <Input className="h-8" value={(v ?? []).join(', ')} placeholder={ph}
        onChange={(e) => on(e.target.value.split(',').map((s) => s.trim()).filter(Boolean))} />
    </div>
  )
}
