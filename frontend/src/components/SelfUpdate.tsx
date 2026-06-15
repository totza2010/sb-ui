import { useState } from 'react'
import { ArrowUpCircle, Loader2 } from 'lucide-react'
import { useSelfVersion, useSelfUpdate } from '@/lib/api'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { LogStream } from '@/components/LogStream'

// Shows the running sb-ui version and, when a newer GitHub release exists, an
// in-place update button. The update streams a job; the backend re-execs into
// the new binary, so the WS drops and reconnects to the new version.
export function SelfUpdate() {
  const { data, refetch } = useSelfVersion()
  const update = useSelfUpdate()
  const [jobId, setJobId] = useState<string | null>(null)

  const start = async () => {
    const { job_id } = await update.mutateAsync()
    setJobId(job_id)
  }

  if (!data) return <p className="text-xs text-muted-foreground/60">sb-ui …</p>

  return (
    <div className="space-y-1.5">
      <p className="text-xs text-muted-foreground/60">
        sb-ui {data.current}
        {data.update_available && <span className="text-orange-500"> → {data.latest}</span>}
      </p>

      {data.update_available && (
        <button
          onClick={start}
          disabled={update.isPending}
          className="flex items-center gap-1.5 text-xs font-medium text-orange-500 hover:text-orange-400 transition-colors disabled:opacity-60"
        >
          {update.isPending
            ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
            : <ArrowUpCircle className="h-3.5 w-3.5" />}
          Update to {data.latest}
        </button>
      )}

      <Dialog
        open={!!jobId}
        onOpenChange={(o) => { if (!o) { setJobId(null); refetch() } }}
      >
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle className="font-mono text-sm">Updating sb-ui → {data.latest}</DialogTitle>
          </DialogHeader>
          <p className="text-xs text-muted-foreground">
            sb-ui will restart into the new version when this finishes — the page
            may briefly disconnect, then reconnect automatically.
          </p>
          <LogStream jobId={jobId} />
        </DialogContent>
      </Dialog>
    </div>
  )
}
