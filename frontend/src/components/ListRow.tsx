import type { ReactNode } from 'react'

// ListRow is the shared full-width list item used across App Manager (Add app),
// Custom roles, and anywhere else needing the same row look. Slots keep it
// flexible: icon | title+subtitle | trailing (e.g. a status badge) | actions.
export function ListRow({ icon, title, subtitle, trailing, actions }: {
  icon?: ReactNode
  title: ReactNode
  subtitle?: ReactNode
  trailing?: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 hover:border-primary/30 transition-colors">
      {icon && <span className="shrink-0 text-muted-foreground [&>svg]:h-4 [&>svg]:w-4">{icon}</span>}
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 min-w-0">{title}</div>
        {subtitle && <div className="font-mono text-xs text-muted-foreground truncate">{subtitle}</div>}
      </div>
      {trailing}
      {actions && <div className="flex items-center gap-1 shrink-0">{actions}</div>}
    </div>
  )
}
