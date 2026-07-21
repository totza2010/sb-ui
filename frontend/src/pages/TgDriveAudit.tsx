/**
 * teldrive file-integrity audit.
 *
 * teldrive records a file's size as the client reported it but strips each part's size
 * before saving, so an upload cut short near the end still commits a full-size record
 * backed by too few parts. Reading such a file indexes past the end of the parts array
 * and panics the whole container, taking every other stream down with it.
 *
 * Findings split into two tiers that must not be conflated:
 *   Proven  — size exceeds what the parts could hold even at that instance's maximum part
 *             size. True for every possible chunk size, so there are no false positives.
 *   Suspect — only short under an assumed chunk size. A shortlist, not a verdict;
 *             confirming these needs the real part sizes fetched back from Telegram.
 *
 * Layout is instance-first: the question being answered is "which of my accounts has
 * broken files", so the instances are a permanent left rail carrying their own health,
 * and the pane beside it shows that selection's findings. Connection setup is done once,
 * so it lives in a dialog rather than occupying the screen forever.
 */
import { useEffect, useMemo, useRef, useState } from 'react'
import {
  useTeldriveRemotes, useTeldriveAuditConfig, useSaveTeldriveAuditConfig, useTeldriveDBTest,
  useTeldriveAudit, useTeldriveLastAudit, type TdAuditDB, type TdAuditFile, type TdAuditResult, type TdDBInfo, type TdServer,
} from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { Loader2, CheckCircle2, ShieldAlert, Save, Settings2, Server } from 'lucide-react'

const MIB = 1024 * 1024
const CHUNKS: [number, string][] = [[128 * MIB, '128M'], [256 * MIB, '256M'], [512 * MIB, '512M'], [1024 * MIB, '1G'], [2048 * MIB, '2G']]
const EMPTY_SERVER: TdServer = { host: '', port: 5432, user: '', password: '', sslmode: 'disable' }
const SEL = 'h-8 rounded-md border border-input bg-background px-2 text-xs text-foreground'

type Cat = 'proven' | 'broken' | 'orphans' | 'suspect' | 'stalled'
const CATS: [Cat, string][] = [['proven', 'Proven short'], ['broken', 'Unreadable parts'], ['orphans', 'Orphaned'], ['suspect', 'Suspect'], ['stalled', 'Stalled uploads']]
const BLURB: Record<Cat, string> = {
  proven: 'Size exceeds what the stored parts could hold even at the instance’s maximum part size — no chunk-size assumption involved. Re-upload these.',
  broken: 'The parts column is empty, null, or not a JSON array. teldrive expects an array here, so these fail in their own way.',
  orphans: 'The parent chain never reaches a root — the parent row is gone, or an ancestor higher up is itself detached. These files still take up space and still answer by id, but nothing can browse to them.',
  suspect: 'Short only if the chunk size really was the one guessed above — a shortlist, not a verdict. Confirming these needs the real part sizes read back from Telegram.',
  stalled: 'Uploads with no new part for over an hour, still inside teldrive’s retention window — the same failure, caught before it becomes a permanently broken file.',
}

const fmtB = (b: number): string => {
  if (!b || b <= 0) return '0'
  const u = ['B', 'K', 'M', 'G', 'T']; let i = 0, n = b
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }
  return `${n >= 100 || i === 0 ? Math.round(n) : n.toFixed(1)}${u[i]}`
}

// A 404 here means the running backend predates these endpoints — say so, rather than
// showing the raw JSON body and leaving it looking like a database failure.
function friendlyErr(e: unknown): string {
  const m = (e as Error)?.message ?? String(e)
  if (m.includes('not found: /api/teldrive')) {
    return 'This backend build has no audit endpoints yet — restart sb-ui (or wait for the dev server to rebuild), then try again. Nothing was sent to the database.'
  }
  const j = m.match(/"error"\s*:\s*"([^"]+)"/)
  return j ? j[1] : m
}

function ServerFields({ srv, onChange }: { srv: TdServer; onChange: (s: TdServer) => void }) {
  return (
    <div className="flex flex-wrap items-end gap-2">
      <div className="w-64 space-y-0.5">
        <Label className="text-[10px] text-muted-foreground">Host</Label>
        <Input className="h-8 font-mono text-xs" placeholder="teldrive-db.tail1f0818.ts.net"
          value={srv.host} onChange={(e) => onChange({ ...srv, host: e.target.value })} />
      </div>
      <div className="w-20 space-y-0.5">
        <Label className="text-[10px] text-muted-foreground">Port</Label>
        <Input className="h-8 font-mono text-xs" placeholder="5432" inputMode="numeric"
          value={srv.port || ''} onChange={(e) => onChange({ ...srv, port: Number(e.target.value) || 0 })} />
      </div>
      <div className="w-28 space-y-0.5">
        <Label className="text-[10px] text-muted-foreground">Username</Label>
        <Input className="h-8 font-mono text-xs" placeholder="postgres"
          value={srv.user} onChange={(e) => onChange({ ...srv, user: e.target.value })} />
      </div>
      <div className="w-36 space-y-0.5">
        <Label className="text-[10px] text-muted-foreground">Password</Label>
        <Input className="h-8 font-mono text-xs" type="password" autoComplete="off"
          value={srv.password} onChange={(e) => onChange({ ...srv, password: e.target.value })} />
      </div>
      <div className="w-24 space-y-0.5">
        <Label className="text-[10px] text-muted-foreground">SSL</Label>
        <select className={cn(SEL, 'w-full')} value={srv.sslmode || 'disable'}
          onChange={(e) => onChange({ ...srv, sslmode: e.target.value })}>
          {['disable', 'prefer', 'require', 'verify-full'].map((m) => <option key={m} value={m}>{m}</option>)}
        </select>
      </div>
    </div>
  )
}

function SettingsRow({ db, shared, onChange }: { db: TdAuditDB; shared: TdServer; onChange: (d: TdAuditDB) => void }) {
  const test = useTeldriveDBTest()
  const [info, setInfo] = useState<TdDBInfo | null>(null)
  const srv = db.own_server ? (db.server ?? EMPTY_SERVER) : shared
  return (
    <>
      <tr className={cn('border-t border-border/50', db.disabled && 'opacity-50')}>
        <td className="py-1 pl-2 pr-1"><Checkbox checked={!db.disabled} onCheckedChange={(v) => onChange({ ...db, disabled: v !== true })} /></td>
        <td className="px-2 py-1 font-mono text-xs text-foreground">{db.remote}</td>
        <td className="px-2 py-1">
          <Input className="h-7 w-40 font-mono text-xs" placeholder="database"
            value={db.database} onChange={(e) => onChange({ ...db, database: e.target.value })} />
        </td>
        <td className="px-2 py-1">
          <select className={cn(SEL, 'h-7 w-24')} title="Largest part size this instance was configured to write"
            value={db.max_part_bytes} onChange={(e) => onChange({ ...db, max_part_bytes: Number(e.target.value) })}>
            {CHUNKS.map(([v, lbl]) => <option key={v} value={v}>{lbl}</option>)}
            <option value={0}>TG limit</option>
          </select>
        </td>
        <td className="px-2 py-1 text-center">
          <Checkbox checked={!!db.own_server}
            onCheckedChange={(v) => onChange({ ...db, own_server: v === true, server: db.server ?? { ...shared } })} />
        </td>
        <td className="px-2 py-1">
          <Button size="sm" variant="outline" className="h-7 gap-1 px-2 text-[11px]" disabled={test.isPending || !srv.host}
            onClick={() => test.mutate({ remote: db.remote, database: db.database, server: srv }, { onSuccess: setInfo })}>
            {test.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : <CheckCircle2 className="h-3 w-3" />}Test
          </Button>
        </td>
        <td className="px-2 py-1 text-[11px]">
          {test.isError ? <span className="text-destructive">failed</span>
            : info ? <span className={info.schema_ok ? 'text-success' : 'text-destructive'}>{info.schema_ok ? `${info.files.toLocaleString()} files` : 'failed'}</span>
            : null}
        </td>
      </tr>
      {(db.own_server || test.isError || (info && !info.schema_ok)) && (
        <tr><td /><td colSpan={6} className="px-2 pb-1.5">
          {db.own_server && (
            <div className="rounded-md border border-border/60 bg-secondary/20 p-2">
              <ServerFields srv={db.server ?? EMPTY_SERVER} onChange={(sv) => onChange({ ...db, server: sv })} />
            </div>
          )}
          {test.isError && <p className="mt-1 text-[11px] text-destructive">{friendlyErr(test.error)}</p>}
          {info && !info.schema_ok && info.error && <p className="mt-1 text-[11px] text-destructive">{info.error}</p>}
        </td></tr>
      )}
    </>
  )
}

export function TgDriveAudit() {
  const { data: rd } = useTeldriveRemotes()
  const { data: cfg, isError: cfgFailed, error: cfgError, isPending: cfgLoading } = useTeldriveAuditConfig()
  const save = useSaveTeldriveAuditConfig()
  const audit = useTeldriveAudit()
  const last = useTeldriveLastAudit()

  const [dbs, setDbs] = useState<TdAuditDB[] | null>(null)
  const [shared, setShared] = useState<TdServer | null>(null)
  const [chunk, setChunk] = useState(512 * MIB)
  const [sel, setSel] = useState('')      // '' = every instance
  const [cat, setCat] = useState<Cat>('proven')
  const [setup, setSetup] = useState(false)
  const [activeOnly, setActiveOnly] = useState(true)
  const [manualAt, setManualAt] = useState(0)

  // Seed a row per discovered remote, merging saved settings. Deliberately not gated on
  // the config request: if that fails the instances must still appear.
  const hydrated = useRef(false)
  useEffect(() => {
    if (!rd || hydrated.current) return
    const saved = cfg?.dbs ?? []
    const byName = new Map(saved.map((d) => [d.remote, d]))
    const next: TdAuditDB[] = (rd.remotes ?? []).map((r) => byName.get(r) ?? { remote: r, database: '', max_part_bytes: 512 * MIB })
    for (const d of saved) if (!(rd.remotes ?? []).includes(d.remote)) next.push(d)
    setDbs(next)
    setShared(cfg?.shared?.host ? cfg.shared : { ...EMPTY_SERVER })
    if (!cfgLoading) hydrated.current = true
  }, [cfg, rd, cfgLoading])

  const rows = dbs ?? []
  const sharedSrv = shared ?? EMPTY_SERVER
  const configured = rows.filter((d) => !d.disabled && (d.dsn?.trim() || (d.own_server ? d.server?.host : sharedSrv.host)))
  // Findings come from the persisted snapshot, so a reload shows the last scan rather than
  // an empty table. A manual scan's own response wins until the watcher stores something
  // newer — otherwise an automatic rescan would sit invisible behind a stale manual result.
  const snapAt = last.data?.saved_at ? Date.parse(last.data.saved_at) : 0
  const r: TdAuditResult | undefined =
    snapAt > manualAt ? (last.data?.result ?? audit.data) : (audit.data ?? last.data?.result)

  // Match the toolbar to the settings the shown result was actually produced with, so the
  // chunk guess on screen isn't describing a different scan.
  const restored = useRef(false)
  useEffect(() => {
    if (restored.current || !last.data?.result?.ran_at) return
    restored.current = true
    if (last.data.result.chunk_guess) setChunk(last.data.result.chunk_guess)
    setActiveOnly(last.data.result.active_only)
  }, [last.data])

  // Per-instance tallies drive the rail, so the rail is the health summary rather than a
  // plain list of names.
  const tally = useMemo(() => {
    const m = new Map<string, { proven: number; broken: number; orphans: number; suspect: number; stalled: number; total: number }>()
    const bump = (remote: string, k: Cat) => {
      const e = m.get(remote) ?? { proven: 0, broken: 0, orphans: 0, suspect: 0, stalled: 0, total: 0 }
      e[k]++; e.total++; m.set(remote, e)
    }
    r?.proven.forEach((f) => bump(f.remote, 'proven'))
    r?.broken.forEach((f) => bump(f.remote, 'broken'))
    r?.orphans.forEach((f) => bump(f.remote, 'orphans'))
    r?.suspect.forEach((f) => bump(f.remote, 'suspect'))
    r?.stalled.forEach((s) => bump(s.remote, 'stalled'))
    return m
  }, [r])

  const pick = <T extends { remote: string }>(arr: T[] | undefined) => (arr ?? []).filter((x) => !sel || x.remote === sel)
  const files: TdAuditFile[] = cat === 'stalled' ? [] : pick(r?.[cat])
  const stalled = pick(r?.stalled)
  const shown = cat === 'stalled' ? stalled.length : files.length
  const problems = (r?.proven.length ?? 0) + (r?.broken.length ?? 0) + (r?.orphans.length ?? 0) + (r?.suspect.length ?? 0) + (r?.stalled.length ?? 0)

  const runScan = () => audit.mutate({ chunk, all: !activeOnly }, {
    onSuccess: (res) => (setManualAt(Date.now()), last.refetch(), setCat(res.proven.length ? 'proven' : res.broken.length ? 'broken' : res.orphans.length ? 'orphans' : res.suspect.length ? 'suspect' : 'stalled')),
  })

  const railItem = (key: string, label: string, sub: string, count: number, err?: string) => (
    <button key={key} onClick={() => setSel(key)}
      className={cn('flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors',
        sel === key ? 'bg-accent' : 'hover:bg-accent/50')}>
      <span className={cn('h-1.5 w-1.5 shrink-0 rounded-full', err ? 'bg-destructive' : count ? 'bg-warning' : 'bg-success')} />
      <span className="min-w-0 flex-1">
        <span className="block truncate font-mono text-[11px] text-foreground">{label}</span>
        <span className="block truncate text-[10px] tabular-nums text-muted-foreground">{err ? 'unreachable' : sub}</span>
      </span>
      {count > 0 && <span className="shrink-0 rounded bg-destructive/15 px-1 text-[10px] font-medium tabular-nums text-destructive">{count}</span>}
    </button>
  )

  return (
    <div className="space-y-3">
      {/* one toolbar for the whole screen */}
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" className="h-8 gap-1.5 text-xs" disabled={audit.isPending || configured.length === 0} onClick={runScan}>
          {audit.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <ShieldAlert className="h-3.5 w-3.5" />}
          Scan {configured.length} instance{configured.length === 1 ? '' : 's'}
        </Button>
        <select className={SEL} title="Chunk size assumed by the suspect list" value={chunk} onChange={(e) => setChunk(Number(e.target.value))}>
          {CHUNKS.map(([v, lbl]) => <option key={v} value={v}>guess {lbl}</option>)}
        </select>
        <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground"
          title="teldrive keeps deleted files as rows with a non-active status; skipping them avoids reporting files you already removed">
          <Checkbox id="td-active-only" checked={activeOnly} onCheckedChange={(v) => setActiveOnly(v === true)} />
          <label htmlFor="td-active-only">active files only</label>
        </div>
        {r && (
          <span className="text-[11px] tabular-nums text-muted-foreground">
            {r.scanned.toLocaleString()} {r.active_only ? 'active ' : ''}files · <span className={problems ? 'font-medium text-destructive' : 'text-success'}>{problems} problem{problems === 1 ? '' : 's'}</span>
          </span>
        )}
        <span className="flex-1" />
        {/* Say when these findings are from and what the watcher is doing, so a stored
            result is never mistaken for a live one. */}
        {last.data?.saved_at && (
          <span className="text-[11px] text-muted-foreground" title={last.data.watch_msg || ''}>
            {last.data.pending && <Loader2 className="mr-1 inline h-3 w-3 animate-spin text-warning" />}
            scanned {new Date(last.data.saved_at).toLocaleString()}
            {last.data.auto && ' (auto)'}
          </span>
        )}
        <Button size="sm" variant="outline" className="h-8 gap-1.5 text-xs" onClick={() => setSetup(true)}>
          <Settings2 className="h-3.5 w-3.5" />Connections
        </Button>
      </div>

      {audit.isError && <p className="rounded-md bg-destructive/10 px-3 py-2 text-[11px] text-destructive">{friendlyErr(audit.error)}</p>}
      {last.data?.watch_msg && !audit.isPending && (
        <p className={cn('rounded-md px-3 py-2 text-[11px]',
          last.data.error ? 'bg-destructive/10 text-destructive'
            : last.data.pending ? 'bg-warning/10 text-warning' : 'bg-muted/50 text-muted-foreground')}>
          {last.data.watch_msg}{last.data.error ? ` — ${last.data.error}` : ''}
        </p>
      )}
      {configured.length === 0 && !audit.isError && (
        <p className="rounded-md bg-warning/10 px-3 py-2 text-[11px] text-warning">
          No instance has a database yet — open <span className="font-medium">Connections</span> to set them up.
        </p>
      )}

      {/* master (instances) / detail (that instance's findings) */}
      <div className="grid gap-3 lg:grid-cols-[13rem_minmax(0,1fr)]">
        <aside className="space-y-0.5 rounded-md border border-border p-1.5">
          {railItem('', 'All instances', r ? `${r.scanned.toLocaleString()} files` : `${rows.length} configured`, problems)}
          <div className="my-1 border-t border-border/60" />
          {rows.map((d) => {
            const t = tally.get(d.remote)
            const inst = r?.instances.find((i) => i.remote === d.remote)
            const sub = inst
              ? `${inst.scanned.toLocaleString()} files${inst.took_ms ? ` · ${(inst.took_ms / 1000).toFixed(1)}s` : ''}`
              : d.database || 'not configured'
            return railItem(d.remote, d.remote, sub, t?.total ?? 0, inst?.error)
          })}
        </aside>

        <section className="min-w-0 space-y-2">
          <div className="flex flex-wrap items-center gap-1.5">
            {CATS.map(([k, lbl]) => {
              const n = sel
                ? (tally.get(sel)?.[k] ?? 0)
                : k === 'stalled' ? (r?.stalled.length ?? 0) : (r?.[k].length ?? 0)
              return (
                <Button key={k} size="sm" variant={cat === k ? 'default' : 'outline'} className="h-7 gap-1.5 px-2 text-[11px]" onClick={() => setCat(k)}>
                  {lbl}<span className={cn('tabular-nums', cat === k ? 'opacity-80' : 'text-muted-foreground')}>{n}</span>
                </Button>
              )
            })}
            <span className="flex-1" />
            {sel && <span className="font-mono text-[10px] text-muted-foreground">filtered to {sel}</span>}
          </div>

          <p className="text-[11px] text-muted-foreground">{BLURB[cat]}</p>
          {cat === 'orphans' && r && !r.paths_ok && (
            <p className="rounded-md bg-warning/10 px-3 py-2 text-[11px] text-warning">
              No root folder was found (none has a NULL parent), so the chain to the root can&rsquo;t be judged — this check was skipped rather than flagging every file. Paths may also be incomplete.
            </p>
          )}

          <div className="max-h-[calc(100vh-22rem)] overflow-y-auto rounded-md border border-border">
            {!r ? (
              <p className="px-4 py-12 text-center text-xs text-muted-foreground">Run a scan to see findings.</p>
            ) : shown === 0 ? (
              <p className="px-4 py-12 text-center text-xs text-muted-foreground">Nothing here{sel && ' for this instance'}.</p>
            ) : cat === 'stalled' ? (
              <table className="w-full text-xs">
                <thead className="sticky top-0 z-10 bg-secondary/95 backdrop-blur">
                  <tr className="text-[10px] uppercase tracking-wide text-muted-foreground">
                    {!sel && <th className="px-3 py-1.5 text-left font-normal">Instance</th>}
                    <th className="px-3 py-1.5 text-left font-normal">Name</th>
                    <th className="px-3 py-1.5 text-right font-normal">Parts</th>
                    <th className="px-3 py-1.5 text-right font-normal">Uploaded</th>
                    <th className="px-3 py-1.5 text-right font-normal">Last part</th>
                  </tr>
                </thead>
                <tbody>
                  {stalled.map((s) => (
                    <tr key={`${s.remote}:${s.upload_id}`} className="border-t border-border/50 hover:bg-accent/40">
                      {!sel && <td className="whitespace-nowrap px-3 py-1 font-mono text-[10px] text-muted-foreground">{s.remote}</td>}
                      <td className="max-w-0 px-3 py-1"><div className="truncate text-foreground" title={s.upload_id}>{s.name || s.upload_id}</div></td>
                      <td className="whitespace-nowrap px-3 py-1 text-right tabular-nums text-muted-foreground">{s.parts}</td>
                      <td className="whitespace-nowrap px-3 py-1 text-right tabular-nums text-muted-foreground">{fmtB(s.bytes)}</td>
                      <td className="whitespace-nowrap px-3 py-1 text-right tabular-nums text-muted-foreground">{new Date(s.last_part_at).toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              // table-fixed with the numeric columns pinned narrow, so the File column keeps
              // every pixel that's left and the path can be shown in full rather than cut.
              <table className="w-full table-fixed text-xs">
                <thead className="sticky top-0 z-10 bg-secondary/95 backdrop-blur">
                  <tr className="text-[10px] uppercase tracking-wide text-muted-foreground">
                    {!sel && <th className="w-32 px-3 py-1.5 text-left font-normal">Instance</th>}
                    <th className="px-3 py-1.5 text-left font-normal">File</th>
                    <th className="w-20 px-3 py-1.5 text-right font-normal">Size</th>
                    {cat !== 'orphans' && <th className="w-16 px-3 py-1.5 text-right font-normal">Parts</th>}
                    {cat === 'proven' && <th className="w-24 px-3 py-1.5 text-right font-normal">Short by</th>}
                    {cat !== 'orphans' && <th className="w-16 px-3 py-1.5 text-right font-normal">Needs</th>}
                  </tr>
                </thead>
                <tbody>
                  {files.map((f) => (
                    <tr key={`${f.remote}:${f.id}`} className="border-t border-border/50 align-top hover:bg-accent/40">
                      {!sel && <td className="truncate px-3 py-1.5 font-mono text-[10px] text-muted-foreground" title={f.remote}>{f.remote}</td>}
                      <td className="px-3 py-1.5">
                        <div className="break-words text-foreground" title={f.id}>{f.name || f.id}</div>
                        <div className="break-all font-mono text-[10px] leading-relaxed text-muted-foreground">
                          {f.path
                            ? f.path.startsWith('?')
                              // The chain breaks above this point — mark where, then show the
                              // names that do exist instead of hiding them behind a bare "?".
                              ? <><span className="text-destructive">?</span>{f.path.slice(1)}</>
                              : f.path
                            : '—'}
                        </div>
                      </td>
                      <td className="whitespace-nowrap px-3 py-1.5 text-right tabular-nums text-muted-foreground">{fmtB(f.size)}</td>
                      {cat !== 'orphans' && <td className="whitespace-nowrap px-3 py-1.5 text-right tabular-nums text-foreground">{f.parts}</td>}
                      {cat === 'proven' && <td className="whitespace-nowrap px-3 py-1.5 text-right font-medium tabular-nums text-destructive">≥ {fmtB(f.short_by)}</td>}
                      {cat !== 'orphans' && <td className="whitespace-nowrap px-3 py-1.5 text-right tabular-nums text-muted-foreground">{f.min_needed || f.guess_need || '—'}</td>}
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </section>
      </div>

      {/* set up once, then get out of the way */}
      <Dialog open={setup} onOpenChange={setSetup}>
        <DialogContent className="max-w-4xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-1.5 text-sm"><Server className="h-4 w-4 text-muted-foreground" />teldrive databases</DialogTitle>
          </DialogHeader>
          <p className="text-[11px] text-muted-foreground">
            Fill the server once and give each instance its database; tick <span className="text-foreground">own</span> only where one lives elsewhere.
            Read-only (<span className="font-mono">default_transaction_read_only</span>, 3 connections) so it never competes with teldrive.
            <span className="text-foreground"> Max part</span> is the largest part that instance was configured to write — bigger than parts × that value means short however it was chunked, which is what makes a finding provable.
          </p>
          {cfgFailed && (
            <p className="rounded-md bg-warning/10 px-3 py-2 text-[11px] text-warning">
              Saved connections could not be loaded — {friendlyErr(cfgError)} Instances come from rclone.conf; anything stored is not shown, so saving now would overwrite it.
            </p>
          )}
          <div className="rounded-md border border-border bg-secondary/20 p-2.5">
            <ServerFields srv={sharedSrv} onChange={setShared} />
          </div>
          {rows.length === 0 ? (
            <p className="rounded-md border border-dashed border-border px-4 py-8 text-center text-xs text-muted-foreground">
              {rd ? 'No teldrive remotes in rclone.conf — add a remote with type = teldrive.' : 'Loading teldrive remotes…'}
            </p>
          ) : (
            <div className="max-h-[50vh] overflow-y-auto rounded-md border border-border">
              <table className="w-full">
                <thead className="sticky top-0 bg-secondary/95 backdrop-blur">
                  <tr className="text-[10px] uppercase tracking-wide text-muted-foreground">
                    <th className="w-8 py-1.5" />
                    <th className="px-2 py-1.5 text-left font-normal">Instance</th>
                    <th className="px-2 py-1.5 text-left font-normal">Database</th>
                    <th className="px-2 py-1.5 text-left font-normal">Max part</th>
                    <th className="w-12 px-2 py-1.5 text-center font-normal">Own</th>
                    <th className="w-20" />
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {rows.map((d, i) => (
                    <SettingsRow key={d.remote} db={d} shared={sharedSrv}
                      onChange={(nd) => setDbs(rows.map((x, j) => (j === i ? nd : x)))} />
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <div className="flex justify-end gap-1.5">
            <Button size="sm" variant="ghost" className="h-8" onClick={() => setSetup(false)}>Close</Button>
            <Button size="sm" className="h-8 gap-1.5" disabled={save.isPending || cfgFailed}
              onClick={() => save.mutate({ shared: sharedSrv, dbs: rows }, { onSuccess: () => setSetup(false) })}>
              {save.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}Save
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
