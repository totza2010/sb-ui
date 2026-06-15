import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { Sidebar } from '@/components/layout/Sidebar'
import { Dashboard } from '@/pages/Dashboard'
import { AppManager } from '@/pages/AppManager'
import { ConfigEditor } from '@/pages/ConfigEditor'
import { RoleBuilder } from '@/pages/RoleBuilder'
import { SetupWizard } from '@/pages/SetupWizard'
import { JobsLogs } from '@/pages/JobsLogs'
import { Inventory } from '@/pages/Inventory'
import { Backup } from '@/pages/Backup'
import { InstallTypes } from '@/pages/InstallTypes'
import { Files } from '@/pages/Files'
import { ConnectionSetup } from '@/pages/ConnectionSetup'
import { useSetupStatus } from '@/lib/api'
import { Loader2 } from 'lucide-react'

const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false, retry: 1 } },
})

function AppInner() {
  const qc = useQueryClient()
  const { data: status, isLoading } = useSetupStatus()

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    )
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
    <div className="flex min-h-screen">
      <Sidebar />
      <main className="flex-1 overflow-auto">
        <Routes>
          <Route path="/"       element={<Dashboard />} />
          <Route path="/apps"   element={<AppManager />} />
          <Route path="/config" element={<ConfigEditor />} />
          <Route path="/roles"  element={<RoleBuilder />} />
          <Route path="/setup"  element={<SetupWizard />} />
          <Route path="/inventory" element={<Inventory />} />
          <Route path="/install-types" element={<InstallTypes />} />
          <Route path="/backup" element={<Backup />} />
          <Route path="/files" element={<Files />} />
          <Route path="/logs"   element={<JobsLogs />} />
        </Routes>
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
