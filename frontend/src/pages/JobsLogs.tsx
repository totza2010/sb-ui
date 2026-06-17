import { useState } from 'react'
import { useJobs, type Job } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { LogStream } from '@/components/LogStream'
import { Loader2 } from 'lucide-react'

const statusVariant: Record<Job['status'], 'default' | 'success' | 'destructive' | 'secondary'> = {
  pending: 'secondary', running: 'default', completed: 'success', failed: 'destructive', stopped: 'secondary',
}

export function JobsLogs() {
  const { data: jobs, isLoading } = useJobs()
  const [selected, setSelected] = useState<string | null>(null)
  const selectedJob = jobs?.find((j) => j.id === selected)

  return (
    <div className="p-6 h-[calc(100vh-1px)] flex flex-col gap-4">
      <h1 className="text-xl font-semibold text-foreground">Jobs & Logs</h1>
      <div className="flex gap-4 flex-1 overflow-hidden">
        {/* Job list */}
        <div className="w-72 shrink-0 border border-border rounded-lg overflow-auto">
          {isLoading && <p className="p-4 text-sm text-muted-foreground">Loading…</p>}
          {(jobs ?? []).length === 0 && !isLoading && (
            <p className="p-4 text-sm text-muted-foreground">No jobs yet</p>
          )}
          {(jobs ?? []).map((job) => (
            <button
              key={job.id}
              onClick={() => setSelected(job.id)}
              className={`w-full text-left px-4 py-3 border-b border-border last:border-0 hover:bg-card transition-colors ${selected === job.id ? 'bg-card' : ''}`}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="font-mono text-xs text-foreground truncate">{job.tag}</span>
                <Badge variant={statusVariant[job.status]} className="shrink-0">
                  {job.status === 'running' && <Loader2 className="h-2.5 w-2.5 mr-1 animate-spin" />}
                  {job.status}
                </Badge>
              </div>
              <p className="text-xs text-muted-foreground mt-0.5">{job.action} · {new Date(job.created_at).toLocaleTimeString()}</p>
            </button>
          ))}
        </div>

        {/* Log view */}
        <div className="flex-1 overflow-hidden">
          {selected ? (
            <div className="space-y-2 h-full flex flex-col">
              {selectedJob && (
                <p className="text-xs text-muted-foreground font-mono">{selectedJob.tag} — {selectedJob.action}</p>
              )}
              <div className="flex-1 overflow-hidden">
                <LogStream jobId={selected} />
              </div>
            </div>
          ) : (
            <div className="h-full flex items-center justify-center">
              <p className="text-muted-foreground text-sm">Select a job to view logs</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
