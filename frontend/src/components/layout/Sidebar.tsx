import { NavLink } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { LayoutDashboard, Package, Settings, Wand2, Activity, PlugZap, ListTree, DatabaseBackup, FolderTree, Container, ArrowRightLeft, CloudUpload, Send, SlidersHorizontal } from 'lucide-react'
import { cn } from '@/lib/cn'
import { useSetupStatus, useTeldriveRemotes } from '@/lib/api'
import { SelfUpdate } from '@/components/SelfUpdate'
import { SystemStatus } from '@/components/SystemStatus'

const nav = [
  { to: '/',         label: 'Dashboard',    icon: LayoutDashboard },
  { to: '/apps',     label: 'App Manager',  icon: Package },
  { to: '/containers', label: 'Docker',     icon: Container },
  { to: '/config',     label: 'Config',       icon: Settings },
  { to: '/inventory',  label: 'Inventory',    icon: ListTree },
  { to: '/backup',     label: 'Backup',       icon: DatabaseBackup },
  { to: '/files',      label: 'Files',        icon: FolderTree },
  { to: '/transfers',  label: 'Transfers',    icon: ArrowRightLeft },
  { to: '/uploader',   label: 'Uploader',     icon: CloudUpload },
  { to: '/logs',     label: 'Jobs & Logs',  icon: Activity },
  { to: '/options',  label: 'Options',      icon: SlidersHorizontal },
]

export function Sidebar() {
  const qc = useQueryClient()
  const { data: status } = useSetupStatus()
  const { data: td } = useTeldriveRemotes()

  // tgDrive panel only appears when teldrive remotes are configured.
  const base = (td?.remotes?.length ?? 0) > 0
    ? [...nav.slice(0, -1), { to: '/tgdrive', label: 'tgDrive', icon: Send }, nav[nav.length - 1]]
    : nav

  // Setup Wizard is only relevant on a fresh box. Once Saltbox is provisioned,
  // drop it from the nav — re-linking a different host is "Reconfigure connection".
  const items = status && !status.saltbox_configured
    ? [...base.slice(0, -1), { to: '/setup', label: 'Setup Wizard', icon: Wand2 }, base[base.length - 1]]
    : base

  return (
    <aside className="w-56 shrink-0 border-r border-border bg-card flex flex-col h-screen">
      {/* Logo */}
      <div className="px-5 py-4 border-b border-border">
        <div className="flex items-center gap-2">
          <div className="w-7 h-7 rounded-md bg-primary flex items-center justify-center shrink-0">
            <span className="text-primary-foreground font-bold text-xs">SB</span>
          </div>
          <span className="text-foreground font-semibold text-sm tracking-tight">Saltbox UI</span>
        </div>
        {status && (
          <p className="text-xs text-muted-foreground mt-1.5 truncate pl-9">
            {status.mode === 'ssh' ? `ssh: ${status.host}` : 'local mode'}
          </p>
        )}
      </div>

      {/* Nav */}
      <nav className="flex-1 overflow-y-auto py-3 space-y-0.5 px-3">
        {items.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              cn(
                'flex items-center gap-2.5 px-3 py-2 rounded-md text-sm transition-colors',
                isActive
                  ? 'bg-primary text-primary-foreground font-medium shadow-sm'
                  : 'text-muted-foreground hover:text-foreground hover:bg-accent'
              )
            }
          >
            <Icon className="h-4 w-4 shrink-0" />
            {label}
          </NavLink>
        ))}
      </nav>

      {/* System status — visible on every page */}
      <div className="px-4 py-3 border-t border-border">
        <SystemStatus />
      </div>

      {/* Footer */}
      <div className="px-4 py-3 border-t border-border space-y-2">
        <button
          onClick={() =>
            qc.setQueryData(['setup-status'], (old: unknown) =>
              old ? { ...(old as object), configured: false } : old
            )
          }
          className="flex items-center gap-2 text-xs text-muted-foreground hover:text-foreground transition-colors w-full"
        >
          <PlugZap className="h-3.5 w-3.5" />
          Reconfigure connection
        </button>
        <SelfUpdate />
      </div>
    </aside>
  )
}
