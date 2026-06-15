import { useState, type ComponentType } from 'react'
import { useContainers, useContainerStats, useContainerInspect, useContainerAction, useAppLogs, type ContainerStat, type ContainerInfo } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { RefreshCw, Play, Square, Cpu, MemoryStick, ArrowDownUp, HardDrive, Clock, RotateCw, Network, FolderInput, Terminal } from 'lucide-react'
import { cn } from '@/lib/cn'

const num = (s?: string) => { const n = parseFloat((s ?? '').replace('%', '')); return isNaN(n) ? 0 : n }

function StatCard({ icon: Icon, label, value, pct }: { icon: ComponentType<{ className?: string }>; label: string; value?: string; pct?: number }) {
  const bar = pct !== undefined
  const color = (pct ?? 0) > 90 ? 'bg-destructive' : (pct ?? 0) > 70 ? 'bg-warning' : 'bg-primary'
  return (
    <div className="rounded-lg border border-border bg-card p-2.5">
      <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground mb-1"><Icon className="h-3.5 w-3.5" />{label}</div>
      <div className="font-mono text-sm font-semibold text-foreground tabular-nums">{value ?? '—'}</div>
      {bar && <div className="mt-1.5 h-1 w-full rounded-full bg-secondary"><div className={cn('h-1 rounded-full', color)} style={{ width: `${Math.min(pct ?? 0, 100)}%` }} /></div>}
    </div>
  )
}

function Field({ icon: Icon, k, children }: { icon: ComponentType<{ className?: string }>; k: string; children: React.ReactNode }) {
  return (
    <div className="flex gap-3 px-3 py-2">
      <span className="flex items-center gap-1.5 text-xs text-muted-foreground w-24 shrink-0"><Icon className="h-3.5 w-3.5" />{k}</span>
      <span className="text-xs text-foreground min-w-0 break-all">{children}</span>
    </div>
  )
}

function ContainerDetail({ container, stat, onClose }: { container: ContainerInfo | null; stat?: ContainerStat; onClose: () => void }) {
  const name = container?.name ?? null
  const { data: ins } = useContainerInspect(name)
  const { data: logs } = useAppLogs(name)
  if (!container) return null
  return (
    <Dialog open={!!container} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-3xl max-h-[88vh] overflow-y-auto space-y-4">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 flex-wrap">
            <span className="font-mono text-sm">{container.name}</span>
            <Badge variant={container.running ? 'success' : 'secondary'}>{container.status}</Badge>
          </DialogTitle>
          <p className="font-mono text-[11px] text-muted-foreground break-all">{container.image}</p>
        </DialogHeader>

        <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
          <StatCard icon={Cpu} label="CPU" value={stat?.cpu} pct={num(stat?.cpu)} />
          <StatCard icon={MemoryStick} label="Memory" value={stat?.mem} pct={num(stat?.mem_pct)} />
          <StatCard icon={ArrowDownUp} label="Network I/O" value={stat?.net} />
          <StatCard icon={HardDrive} label="Block I/O" value={stat?.block} />
        </div>

        {ins && (
          <div className="rounded-lg border border-border bg-card divide-y divide-border">
            <Field icon={Clock} k="Created">{ins.created.slice(0, 19).replace('T', ' ')}</Field>
            <Field icon={RotateCw} k="Restart">{ins.restart || 'no'}</Field>
            <Field icon={Network} k="Networks">
              <div className="flex flex-wrap gap-1">{ins.networks.length ? ins.networks.map((n) => <Badge key={n} variant="secondary">{n}</Badge>) : '—'}</div>
            </Field>
            {ins.mounts.length > 0 && (
              <Field icon={FolderInput} k="Mounts">
                <div className="space-y-1">{ins.mounts.map((m, i) => (
                  <div key={i} className="font-mono text-[11px] flex items-center gap-1.5">
                    <span className="text-muted-foreground">{m.source}</span><span className="text-muted-foreground/50">→</span>
                    <span>{m.destination}</span>
                    <Badge variant="secondary" className="text-[9px]">{m.rw ? 'rw' : 'ro'}</Badge>
                  </div>
                ))}</div>
              </Field>
            )}
            {ins.env.length > 0 && (
              <Field icon={Terminal} k="Env">
                <div className="font-mono text-[11px] max-h-36 overflow-auto rounded bg-secondary/50 p-2 space-y-0.5">
                  {ins.env.map((e, i) => {
                    const eq = e.indexOf('=')
                    return <div key={i}><span className="text-primary">{eq > 0 ? e.slice(0, eq) : e}</span>{eq > 0 && <span className="text-muted-foreground">={e.slice(eq + 1)}</span>}</div>
                  })}
                </div>
              </Field>
            )}
          </div>
        )}

        <div>
          <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground mb-1.5"><Terminal className="h-3.5 w-3.5" />Logs <span className="text-muted-foreground/60">· last 200</span></div>
          <pre className="rounded-lg p-3 h-64 overflow-auto text-[11px] leading-5 font-mono whitespace-pre-wrap bg-[#0d1117] text-[#c9d1d9]">{logs?.logs ?? 'Loading…'}</pre>
        </div>
      </DialogContent>
    </Dialog>
  )
}

// Raw Docker container management (start / stop / restart) + live cpu/mem stats + detail.
export function Containers() {
  const { data: containers, isLoading } = useContainers()
  const { data: stats } = useContainerStats()
  const action = useContainerAction()
  const [sel, setSel] = useState<string | null>(null)

  return (
    <div className="p-6 space-y-4">
      <h1 className="text-xl font-semibold text-foreground">Docker Containers</h1>
      <div className="border border-border rounded-lg overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-card border-b border-border">
            <tr>
              <th className="text-left px-4 py-2.5 text-muted-foreground font-medium">Name</th>
              <th className="text-left px-4 py-2.5 text-muted-foreground font-medium">Image</th>
              <th className="text-left px-4 py-2.5 text-muted-foreground font-medium">Status</th>
              <th className="text-right px-4 py-2.5 text-muted-foreground font-medium">CPU</th>
              <th className="text-left px-4 py-2.5 text-muted-foreground font-medium">MEM</th>
              <th className="text-right px-4 py-2.5 text-muted-foreground font-medium">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading && (
              <tr><td colSpan={6} className="px-4 py-8 text-center text-muted-foreground">Loading containers…</td></tr>
            )}
            {containers?.map((c) => {
              const st = stats?.[c.name]
              return (
              <tr key={c.id} className="border-t border-border hover:bg-card/50">
                <td className="px-4 py-2.5">
                  <button className="font-mono text-xs text-foreground hover:text-primary hover:underline" onClick={() => setSel(c.name)}>{c.name}</button>
                </td>
                <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground truncate max-w-[200px]">{c.image}</td>
                <td className="px-4 py-2.5">
                  <Badge variant={c.running ? 'success' : 'secondary'}>{c.status}</Badge>
                </td>
                <td className="px-4 py-2.5 text-right font-mono text-xs text-muted-foreground">{st?.cpu ?? '—'}</td>
                <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground">{st ? `${st.mem} (${st.mem_pct})` : '—'}</td>
                <td className="px-4 py-2.5 text-right">
                  <div className="flex justify-end gap-1">
                    {!c.running && (
                      <Button size="icon" variant="ghost" className="h-7 w-7" onClick={() => action.mutate({ name: c.name, action: 'start' })}>
                        <Play className="h-3.5 w-3.5 text-success" />
                      </Button>
                    )}
                    {c.running && (
                      <>
                        <Button size="icon" variant="ghost" className="h-7 w-7" onClick={() => action.mutate({ name: c.name, action: 'restart' })}>
                          <RefreshCw className="h-3.5 w-3.5 text-warning" />
                        </Button>
                        <Button size="icon" variant="ghost" className="h-7 w-7" onClick={() => action.mutate({ name: c.name, action: 'stop' })}>
                          <Square className="h-3.5 w-3.5 text-destructive" />
                        </Button>
                      </>
                    )}
                  </div>
                </td>
              </tr>
            )})}
          </tbody>
        </table>
      </div>
      <ContainerDetail container={sel ? containers?.find((c) => c.name === sel) ?? null : null} stat={sel ? stats?.[sel] : undefined} onClose={() => setSel(null)} />
    </div>
  )
}
