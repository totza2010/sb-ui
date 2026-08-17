import { BrowserRouter, Routes, Route, Navigate, useLocation } from 'react-router-dom'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { Sidebar } from '@/components/layout/Sidebar'
import { Dashboard } from '@/pages/Dashboard'
import { AppManager } from '@/pages/AppManager'
import { Containers } from '@/pages/Containers'
import { SetupWizard } from '@/pages/SetupWizard'
import { JobsLogs } from '@/pages/JobsLogs'
import { Inventory } from '@/pages/Inventory'
import { Backup } from '@/pages/Backup'
import { Files } from '@/pages/Files'
import { Transfers } from '@/pages/Transfers'
import { Autoscan } from '@/pages/Autoscan'
import { Library } from '@/pages/Library'
import { Discover } from '@/pages/Discover'
import { Integrations } from '@/pages/Integrations'
import { TgDrive } from '@/pages/TgDrive'
import { Settings } from '@/pages/Settings'
import { ConnectionSetup } from '@/pages/ConnectionSetup'
import { BackendOffline } from '@/components/BackendOffline'
import { useSetupStatus, useAuthStatus, setUnauthorizedHandler } from '@/lib/api'
import { Login } from '@/pages/Login'
import { useEffect, type ReactNode } from 'react'
import { Loader2 } from 'lucide-react'

const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false, retry: 1 } },
})

// AuthGate decides between the app and the login screen, and is the single place that reacts
// to the server refusing a request. Any 401 anywhere re-asks who we are, so a session that
// expired mid-session lands on the login form instead of a page of broken panels.
function AuthGate({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const { data: auth, isLoading, isError, refetch } = useAuthStatus()

  useEffect(() => {
    setUnauthorizedHandler(() => qc.invalidateQueries({ queryKey: ['auth-status'] }))
    return () => setUnauthorizedHandler(null)
  }, [qc])

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    )
  }
  // /api/auth/status needs no credentials, so failing to reach it means the backend is down,
  // not that we are signed out.
  if (isError) return <BackendOffline onRetry={() => refetch()} />
  if (!auth?.authenticated) return <Login status={auth} />
  return <>{children}</>
}

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
            <Route path="/config" element={<Navigate to="/settings" replace />} />
            <Route path="/setup"  element={<SetupWizard />} />
            <Route path="/inventory" element={<Inventory />} />
            <Route path="/backup" element={<Backup />} />
            <Route path="/files" element={<Files />} />
            <Route path="/transfers" element={<Transfers />} />
            {/* The auto-upload rotation was lifted out to be rebuilt; keep the old path
                working so existing links and bookmarks land on transfers. */}
            <Route path="/uploader" element={<Navigate to="/transfers" replace />} />
            <Route path="/autoscan" element={<Autoscan />} />
            <Route path="/library" element={<Library />} />
            <Route path="/discover" element={<Discover />} />
            <Route path="/integrations" element={<Integrations />} />
            <Route path="/tgdrive" element={<TgDrive />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="/options" element={<Navigate to="/settings" replace />} />
            <Route path="/proxy" element={<Navigate to="/settings" replace />} />
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
        <AuthGate>
          <AppInner />
        </AuthGate>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
