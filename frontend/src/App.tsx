import { BrowserRouter, Routes, Route, useLocation } from 'react-router-dom'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { Sidebar } from '@/components/layout/Sidebar'
import { Dashboard } from '@/pages/Dashboard'
import { AppManager } from '@/pages/AppManager'
import { Containers } from '@/pages/Containers'
import { ConfigEditor } from '@/pages/ConfigEditor'
import { SetupWizard } from '@/pages/SetupWizard'
import { JobsLogs } from '@/pages/JobsLogs'
import { Inventory } from '@/pages/Inventory'
import { Backup } from '@/pages/Backup'
import { Files } from '@/pages/Files'
import { Transfers } from '@/pages/Transfers'
import { Uploader } from '@/pages/Uploader'
import { TgDrive } from '@/pages/TgDrive'
import { Options } from '@/pages/Options'
import { ConnectionSetup } from '@/pages/ConnectionSetup'
import { BackendOffline } from '@/components/BackendOffline'
import { useSetupStatus } from '@/lib/api'
import { Loader2 } from 'lucide-react'

const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false, retry: 1 } },
})

function AppInner() {
  const qc = useQueryClient()
  const pathname = useLocation().pathname
  const { data: status, isLoading, isError, refetch } = useSetupStatus()

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    )
  }

  if (isError) {
    return <BackendOffline onRetry={() => refetch()} />
  }

  if (!status?.configured) {
    return (
      <ConnectionSetup
        initial={status}
        onComplete={() => qc.invalidateQueries({ queryKey: ['setup-status'] })}
      />
    )
  }

  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar />
      <main className="flex-1 overflow-y-auto">
        <ErrorBoundary key={pathname}>
          <Routes>
            <Route path="/"       element={<Dashboard />} />
            <Route path="/apps"   element={<AppManager />} />
            <Route path="/containers" element={<Containers />} />
            <Route path="/config" element={<ConfigEditor />} />
            <Route path="/setup"  element={<SetupWizard />} />
            <Route path="/inventory" element={<Inventory />} />
            <Route path="/backup" element={<Backup />} />
            <Route path="/files" element={<Files />} />
            <Route path="/transfers" element={<Transfers />} />
            <Route path="/uploader" element={<Uploader />} />
            <Route path="/tgdrive" element={<TgDrive />} />
            <Route path="/options" element={<Options />} />
            <Route path="/logs"   element={<JobsLogs />} />
          </Routes>
        </ErrorBoundary>
      </main>
    </div>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <AppInner />
      </BrowserRouter>
    </QueryClientProvider>
  )
}
