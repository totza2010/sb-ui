import { useSystem, useContainers, useContainerAction } from '@/lib/api'
import { Card, CardHeader, CardTitle, CardValue, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { StorageFlow } from '@/components/StorageFlow'
import { Cpu, MemoryStick, HardDrive, Clock, RefreshCw, Play, Square } from 'lucide-react'

function formatBytes(bytes: number) {
  const gb = bytes / 1024 / 1024 / 1024
  return gb >= 1 ? `${gb.toFixed(1)} GB` : `${(bytes / 1024 / 1024).toFixed(0)} MB`
}

function formatUptime(seconds: number) {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return d > 0 ? `${d}d ${h}h` : `${h}h ${m}m`
}

function PercentBar({ value }: { value: number }) {
  const color = value > 90 ? 'bg-destructive' : value > 70 ? 'bg-warning' : 'bg-primary'
  return (
    <div className="w-full bg-secondary rounded-full h-1.5 mt-2">
      <div className={`h-1.5 rounded-full ${color}`} style={{ width: `${value}%` }} />
    </div>
  )
}

export function Dashboard() {
  const { data: sys } = useSystem()
  const { data: containers, isLoading: cLoading } = useContainers()
  const action = useContainerAction()

  return (
    <div className="p-6 space-y-6">
      <h1 className="text-xl font-semibold text-foreground">Dashboard</h1>

      {/* Stat cards */}
      <div className="grid grid-cols-2 xl:grid-cols-4 gap-4">
        <Card>
          <CardHeader><CardTitle><Cpu className="inline h-3.5 w-3.5 mr-1" />CPU</CardTitle></CardHeader>
          <CardContent>
            <CardValue>{sys ? `${sys.cpu_percent.toFixed(1)}%` : '…'}</CardValue>
            {sys && <PercentBar value={sys.cpu_percent} />}
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle><MemoryStick className="inline h-3.5 w-3.5 mr-1" />RAM</CardTitle></CardHeader>
          <CardContent>
            <CardValue>{sys ? `${sys.ram_percent.toFixed(0)}%` : '…'}</CardValue>
            {sys && <p className="text-xs text-muted-foreground mt-1">{formatBytes(sys.ram_used)} / {formatBytes(sys.ram_total)}</p>}
            {sys && <PercentBar value={sys.ram_percent} />}
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle><HardDrive className="inline h-3.5 w-3.5 mr-1" />Disk</CardTitle></CardHeader>
          <CardContent>
            <CardValue>{sys ? `${sys.disk_percent.toFixed(0)}%` : '…'}</CardValue>
            {sys && <p className="text-xs text-muted-foreground mt-1">{formatBytes(sys.disk_used)} / {formatBytes(sys.disk_total)}</p>}
            {sys && <PercentBar value={sys.disk_percent} />}
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle><Clock className="inline h-3.5 w-3.5 mr-1" />Uptime</CardTitle></CardHeader>
          <CardContent>
            <CardValue>{sys ? formatUptime(sys.uptime_seconds) : '…'}</CardValue>
          </CardContent>
        </Card>
      </div>

      {/* Storage flow */}
      <div>
        <h2 className="text-sm font-medium text-muted-foreground mb-3">Storage</h2>
        <div className="overflow-x-auto pb-1">
          <StorageFlow />
        </div>
      </div>

      {/* Container table */}
      <div>
        <h2 className="text-sm font-medium text-muted-foreground mb-3">Docker Containers</h2>
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
              {cLoading && (
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
    </div>
  )
}
