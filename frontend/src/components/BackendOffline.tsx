import { ServerCrash, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'

// Shown when the backend can't be reached (vs. ConnectionSetup, which is for a
// reachable-but-unconfigured backend). The setup-status query auto-retries, so
// this clears on its own once the backend is back.
export function BackendOffline({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="min-h-screen flex items-center justify-center p-6">
      <div className="max-w-md w-full text-center space-y-4">
        <div className="w-12 h-12 rounded-full bg-destructive/10 flex items-center justify-center mx-auto">
          <ServerCrash className="h-6 w-6 text-destructive" />
        </div>
        <div className="space-y-1">
          <h1 className="text-lg font-semibold">Can’t reach the backend</h1>
          <p className="text-sm text-muted-foreground">
            The sb-ui backend isn’t responding on <code>/api</code>. Make sure it’s
            running, then retry — this page reconnects automatically.
          </p>
        </div>
        <Button onClick={onRetry} variant="outline" className="gap-2">
          <RefreshCw className="h-4 w-4" /> Retry now
        </Button>
      </div>
    </div>
  )
}
