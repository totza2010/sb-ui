import { useStatus, type StatusItem } from '@/lib/api'
import { cn } from '@/lib/cn'

// Always-visible (sidebar) health indicators: SSH/local connection, FUSE mounts,
// Docker. Each is a colored dot + label + detail. Polls every 20s.
function Row({ item }: { item: StatusItem }) {
  const list = item.list ?? []
  const hasList = list.length > 0
  return (
    <div className={cn('relative group flex items-center gap-2 text-xs', hasList && 'cursor-help')}>
      <span
        className={cn('h-2 w-2 rounded-full shrink-0', item.ok ? 'bg-green-500' : 'bg-red-500')}
      />
      <span className="text-muted-foreground">{item.label}</span>
      {item.detail && <span className="ml-auto text-muted-foreground/60 truncate">{item.detail}</span>}

      {hasList && (
        <div className="absolute left-0 bottom-full mb-1.5 z-30 hidden group-hover:block
                        w-64 rounded-md border border-border bg-card p-2 shadow-lg">
          <p className="text-[11px] font-medium mb-1.5 text-foreground">{item.label}</p>
          <ul className="space-y-1">
            {list.map((m, i) => (
              <li key={i} className="flex items-center gap-1.5 text-[11px]" title={m.detail}>
                <span className={cn('h-1.5 w-1.5 rounded-full shrink-0', m.ok ? 'bg-green-500' : 'bg-red-500')} />
                <span className="font-mono text-muted-foreground break-all">{m.target}</span>
                <span className="ml-auto shrink-0 text-muted-foreground/50">{m.kind}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

export function SystemStatus() {
  const { data, isError } = useStatus()

  if (isError) {
    return (
      <div className="flex items-center gap-2 text-xs">
        <span className="h-2 w-2 rounded-full bg-red-500 shrink-0" />
        <span className="text-muted-foreground">Status unavailable</span>
      </div>
    )
  }
  if (!data) return null

  return (
    <div className="space-y-1.5">
      <Row item={data.connection} />
      <Row item={data.mounts} />
      <Row item={data.docker} />
    </div>
  )
}
