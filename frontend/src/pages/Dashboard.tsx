import { useSystem } from '@/lib/api'
import { Card, CardHeader, CardTitle, CardValue, CardContent } from '@/components/ui/card'
import { StorageFlow } from '@/components/StorageFlow'
import { Cpu, MemoryStick, HardDrive, Clock } from 'lucide-react'

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
        <div className="h-[600px] rounded-lg border border-border bg-card/30">
          <StorageFlow />
        </div>
      </div>
    </div>
  )
}
