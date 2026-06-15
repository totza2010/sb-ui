import { useContainers, useContainerAction } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { RefreshCw, Play, Square } from 'lucide-react'

// Raw Docker container management (start / stop / restart any container).
export function Containers() {
  const { data: containers, isLoading } = useContainers()
  const action = useContainerAction()

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
              <th className="text-right px-4 py-2.5 text-muted-foreground font-medium">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading && (
              <tr><td colSpan={4} className="px-4 py-8 text-center text-muted-foreground">Loading containers…</td></tr>
            )}
            {containers?.map((c) => (
              <tr key={c.id} className="border-t border-border hover:bg-card/50">
                <td className="px-4 py-2.5 font-mono text-xs text-foreground">{c.name}</td>
                <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground truncate max-w-[200px]">{c.image}</td>
                <td className="px-4 py-2.5">
                  <Badge variant={c.running ? 'success' : 'secondary'}>{c.status}</Badge>
                </td>
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
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
