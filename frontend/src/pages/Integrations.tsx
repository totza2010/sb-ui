/**
 * Integrations — live connectivity to every client library, dense enough to fit one
 * screen. Each instance shows version, latency, URL and item counts (series/movies/
 * episodes/indexers); Plex additionally breaks down each library's size.
 */
import { useQueryClient } from '@tanstack/react-query'
import { useIntegrations, type ConnStatus, type IntegrationGroup } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Plug, Loader2, RefreshCw, CheckCircle2, XCircle, Star, CircleDashed, Film, Tv, Library as LibraryIcon } from 'lucide-react'

const n = (v: number) => v.toLocaleString()
const statsLine = (s?: { label: string; value: number }[]) => (s ?? []).map((x) => `${n(x.value)} ${x.label}`).join(' · ')

function StatusPill({ c }: { c: ConnStatus }) {
  return (
    <div className={`rounded-md border px-2.5 py-1.5 ${c.ok ? 'border-success/30 bg-success/5' : 'border-destructive/30 bg-destructive/5'}`}>
      <div className="flex items-center gap-1.5">
        {c.ok ? <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-success" /> : <XCircle className="h-3.5 w-3.5 shrink-0 text-destructive" />}
        <span className="truncate text-xs font-medium text-foreground">{c.name}</span>
        {c.recommended && (
          <span className="flex items-center gap-0.5 rounded bg-[#e5a00d]/15 px-1 py-px text-[9px] font-semibold text-[#e5a00d]">
            <Star className="h-2.5 w-2.5 fill-current" />BEST
          </span>
        )}
        <span className="ml-auto shrink-0 text-[10px] tabular-nums text-muted-foreground">
          {c.version ? `v${c.version} · ` : ''}{c.latency_ms}ms
        </span>
      </div>
      <p className="truncate font-mono text-[10px] text-muted-foreground" title={c.base_url}>{c.base_url || '—'}</p>
      {c.error && <p className="break-all text-[10px] text-destructive">{c.error}</p>}
      {!c.error && c.stats && c.stats.length > 0 && <p className="text-[10px] text-muted-foreground">{statsLine(c.stats)}</p>}
      {c.path_stats && c.path_stats.length > 0 && (
        <div className="mt-1 space-y-px border-t border-border/50 pt-1">
          {c.path_stats.map((ps) => (
            <div key={ps.path} className="flex items-baseline justify-between gap-2">
              <span className="truncate font-mono text-[10px] text-muted-foreground/80" title={ps.path}>{ps.path}</span>
              <span className="shrink-0 text-[10px] tabular-nums text-muted-foreground">{statsLine(ps.stats)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// groupTotals sums each instance stat label across the group (for the header).
function groupTotals(g: IntegrationGroup): string {
  const t = new Map<string, number>()
  for (const i of g.instances ?? []) for (const s of i.stats ?? []) t.set(s.label, (t.get(s.label) ?? 0) + s.value)
  return [...t].map(([l, v]) => `${n(v)} ${l}`).join(' · ')
}

function GroupCard({ g }: { g: IntegrationGroup }) {
  const instances = g.instances ?? []
  const okCount = instances.filter((i) => i.ok).length
  const totals = groupTotals(g)
  return (
    <div className="space-y-2 rounded-lg border border-border bg-card p-3">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
        <h2 className="text-sm font-semibold text-foreground">{g.label}</h2>
        {g.used ? (
          <span className="rounded bg-primary/10 px-1.5 py-px text-[10px] font-medium text-primary">in use</span>
        ) : (
          <span className="flex items-center gap-0.5 rounded bg-muted px-1.5 py-px text-[10px] font-medium text-muted-foreground">
            <CircleDashed className="h-2.5 w-2.5" />probe
          </span>
        )}
        {g.configured && instances.length > 0 && (
          <span className={`text-[11px] ${okCount === instances.length ? 'text-success' : okCount > 0 ? 'text-[#e5a00d]' : 'text-destructive'}`}>
            {okCount}/{instances.length} OK
          </span>
        )}
        {totals && <span className="text-[11px] text-muted-foreground">· {totals}</span>}
        <span className="ml-auto hidden max-w-[45%] truncate font-mono text-[10px] text-muted-foreground sm:inline" title={g.library}>{g.library}</span>
      </div>

      {instances.length > 0 && (
        <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
          {instances.map((c, i) => <StatusPill key={`${c.name}-${i}`} c={c} />)}
        </div>
      )}

      {g.note && !totals && <p className="text-[11px] text-muted-foreground">{g.note}</p>}

      {g.libraries && g.libraries.length > 0 && (
        <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-3">
          {g.libraries.map((l) => {
            const Icon = l.type === 'movie' ? Film : l.type === 'show' ? Tv : LibraryIcon
            return (
              <div key={l.title} className="rounded border border-border bg-muted/30 px-2 py-1">
                <div className="flex items-center gap-1">
                  <Icon className="h-3 w-3 shrink-0 text-muted-foreground" />
                  <span className="truncate text-[11px] font-medium text-foreground" title={l.title}>{l.title}</span>
                </div>
                <p className="text-sm font-semibold tabular-nums text-foreground">{n(l.count)}</p>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

export function Integrations() {
  const qc = useQueryClient()
  const { data, isLoading, isFetching, isError, error } = useIntegrations()
  const groups = data?.groups ?? []

  return (
    <div className="w-full space-y-3 p-4">
      <div className="flex items-center justify-between gap-4">
        <h1 className="flex items-center gap-2 text-base font-semibold text-foreground"><Plug className="h-4 w-4" />Integrations</h1>
        <Button size="sm" variant="outline" className="h-7 shrink-0 gap-1.5" disabled={isFetching}
          onClick={() => qc.invalidateQueries({ queryKey: ['integrations'] })}>
          {isFetching ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}Recheck
        </Button>
      </div>

      {isLoading && <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Probing connections…</div>}
      {isError && <p className="text-sm text-destructive">{(error as Error)?.message}</p>}

      <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
        {groups.map((g) => <GroupCard key={g.key} g={g} />)}
      </div>
    </div>
  )
}
