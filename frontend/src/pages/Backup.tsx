/**
 * Backup — manage Saltbox's two backup systems.
 *   backup   : tar /opt locally, then upload to local/rclone/rsync destinations.
 *   backup2  : stream tars straight to an rclone remote (no local space, slower).
 * Both share backup_config.yml (destinations + schedule). Runs reuse the normal
 * `sb install <tag>` job flow (backup / backup2 / set-backup).
 */
import { useEffect, useState } from 'react'
import { useConfig, useSaveConfig, useInstallApp, useRcloneRemotes, useMountTemplates } from '@/lib/api'
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { LogStream } from '@/components/LogStream'
import { useQueryClient } from '@tanstack/react-query'
import {
  Archive, CloudUpload, CalendarClock, Save, Loader2, AlertTriangle, Check,
} from 'lucide-react'
import { cn } from '@/lib/cn'

const CRON_OPTIONS = ['reboot', 'hourly', 'daily', 'weekly', 'monthly', 'yearly']

type Dest = { enable?: boolean; destination?: string; template?: string; port?: number }
type BackupCfg = {
  cron?: { cron_time?: string }
  misc?: { snapshot?: boolean }
  local?: Dest
  rclone?: Dest
  rsync?: Dest
  restore_service?: { user?: string | null; pass?: string | null }
}

function Toggle({ on, onClick, label }: { on: boolean; onClick: () => void; label: string }) {
  return (
    <button type="button" onClick={onClick}
      className={cn('flex items-center gap-2 text-sm', on ? 'text-foreground' : 'text-muted-foreground')}>
      <span className={cn('w-9 h-5 rounded-full relative transition-colors shrink-0',
        on ? 'bg-primary' : 'bg-muted')}>
        <span className={cn('absolute top-0.5 h-4 w-4 rounded-full bg-white transition-all',
          on ? 'left-[18px]' : 'left-0.5')} />
      </span>
      {label}
    </button>
  )
}

export function Backup() {
  const { data, isLoading } = useConfig('backup_config')
  const saveConfig = useSaveConfig('backup_config')
  const install = useInstallApp()
  const { data: rcloneData } = useRcloneRemotes()
  const { data: tmplData } = useMountTemplates()
  const qc = useQueryClient()

  const remoteNames = Object.keys(rcloneData?.remotes ?? {})
  const templates = tmplData?.templates ?? []

  const [cfg, setCfg] = useState<BackupCfg>({})
  const [dirty, setDirty] = useState(false)
  const [saved, setSaved] = useState(false)
  const [jobId, setJobId] = useState<string | null>(null)
  const [jobTitle, setJobTitle] = useState('')

  useEffect(() => {
    const b = (data?.data as { backup?: BackupCfg } | undefined)?.backup
    if (b) { setCfg(b); setDirty(false) }
  }, [data])

  function patch(section: keyof BackupCfg, key: string, value: unknown) {
    setCfg(prev => ({ ...prev, [section]: { ...(prev[section] as object), [key]: value } }))
    setDirty(true); setSaved(false)
  }

  async function handleSave() {
    await saveConfig.mutateAsync({ backup: cfg })
    setDirty(false); setSaved(true)
    setTimeout(() => setSaved(false), 2500)
  }

  function runTag(tag: string, title: string, warn?: string) {
    if (warn && !confirm(warn)) return
    install.mutate({ tag, action: 'install' }, {
      onSuccess: (d) => {
        setJobId(d.job_id); setJobTitle(title)
        qc.invalidateQueries({ queryKey: ['jobs'] })
      },
    })
  }

  const cron = cfg.cron?.cron_time ?? 'weekly'

  if (isLoading) {
    return <div className="p-6 flex items-center gap-2 text-muted-foreground">
      <Loader2 className="h-5 w-5 animate-spin" /> Loading backup config…</div>
  }

  return (
    <div className="p-6 space-y-5 max-w-4xl">
      <div>
        <h1 className="text-xl font-semibold text-foreground">Backup</h1>
        <p className="text-sm text-muted-foreground mt-0.5">
          Saltbox has two backup systems sharing one config.
        </p>
      </div>

      {/* Two systems explainer */}
      <div className="grid md:grid-cols-2 gap-3">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="flex items-center gap-2 text-foreground">
              <Archive className="h-4 w-4" /> Standard backup
            </CardTitle>
          </CardHeader>
          <CardContent className="text-xs text-muted-foreground space-y-1">
            <p>Tars <code>/opt</code> to local disk, then uploads to every enabled destination.</p>
            <p>Faster, but needs free space ≈ size of <code>/opt</code>. Containers go offline during the tar.</p>
            <code className="text-[11px] text-foreground/70">sb install backup</code>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="flex items-center gap-2 text-foreground">
              <CloudUpload className="h-4 w-4" /> Backup2 (stream)
            </CardTitle>
          </CardHeader>
          <CardContent className="text-xs text-muted-foreground space-y-1">
            <p>Tars each directory straight to an rclone remote — no local archive.</p>
            <p>Use when disk space is tight. Slower, and rclone destination only.</p>
            <code className="text-[11px] text-foreground/70">sb install backup2</code>
          </CardContent>
        </Card>
      </div>

      {/* Config */}
      <Card>
        <CardHeader className="pb-3 flex-row items-center justify-between">
          <CardTitle className="text-foreground">Destinations &amp; schedule</CardTitle>
          <div className="flex items-center gap-2">
            {saved && <span className="text-xs text-green-500 flex items-center gap-1"><Check className="h-3.5 w-3.5" />Saved</span>}
            <Button size="sm" onClick={handleSave} disabled={!dirty || saveConfig.isPending}>
              {saveConfig.isPending ? <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" /> : <Save className="h-3.5 w-3.5 mr-1.5" />}
              Save config
            </Button>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          {/* Schedule + snapshot */}
          <div className="flex flex-wrap items-center gap-6">
            <label className="flex items-center gap-2 text-sm">
              <CalendarClock className="h-4 w-4 text-muted-foreground" />
              Schedule
              <select
                className="bg-background border border-border rounded-md px-2 py-1 text-sm"
                value={cron}
                onChange={e => patch('cron', 'cron_time', e.target.value)}
              >
                {CRON_OPTIONS.map(o => <option key={o} value={o}>{o}</option>)}
              </select>
            </label>
            <Toggle on={!!cfg.misc?.snapshot} label="BTRFS snapshot (less downtime)"
              onClick={() => patch('misc', 'snapshot', !cfg.misc?.snapshot)} />
          </div>

          <div className="border-t border-border" />

          {/* Local */}
          <DestRow title="Local" enabled={!!cfg.local?.enable}
            onToggle={() => patch('local', 'enable', !cfg.local?.enable)}>
            <Field label="Destination" value={cfg.local?.destination ?? ''}
              onChange={v => patch('local', 'destination', v)} placeholder="/home/user/Backups/Saltbox" />
          </DestRow>

          {/* Rclone — linked to the rclone remotes + mount-templates we manage */}
          <DestRow title="Rclone" badge="backup2 uses this" enabled={!!cfg.rclone?.enable}
            onToggle={() => patch('rclone', 'enable', !cfg.rclone?.enable)}>
            <RemotePathField
              label="Destination" remotes={remoteNames}
              value={cfg.rclone?.destination ?? ''}
              onChange={v => patch('rclone', 'destination', v)} />
            <SelectField label="Template" value={cfg.rclone?.template ?? ''}
              options={templates} onChange={v => patch('rclone', 'template', v)} className="w-44" />
          </DestRow>

          {/* Rsync */}
          <DestRow title="Rsync" enabled={!!cfg.rsync?.enable}
            onToggle={() => patch('rsync', 'enable', !cfg.rsync?.enable)}>
            <Field label="Destination" value={cfg.rsync?.destination ?? ''}
              onChange={v => patch('rsync', 'destination', v)} placeholder="rsync://host/Backups/Saltbox" />
            <Field label="Port" value={String(cfg.rsync?.port ?? '')}
              onChange={v => patch('rsync', 'port', Number(v) || undefined)} placeholder="22" className="w-24" />
          </DestRow>
        </CardContent>
      </Card>

      {/* Actions */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-foreground">Run</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex items-start gap-2 text-xs text-amber-500 bg-amber-500/5 border border-amber-500/20 rounded-md px-3 py-2">
            <AlertTriangle className="h-4 w-4 shrink-0 mt-0.5" />
            <span>A backup stops containers during the tar and can take hours. Run it when the server is idle.</span>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="outline" disabled={install.isPending}
              onClick={() => runTag('backup', 'Standard backup',
                'Run standard backup now? Containers will go offline during the tar (can take hours).')}>
              <Archive className="h-3.5 w-3.5 mr-1.5" />Run standard backup
            </Button>
            <Button size="sm" variant="outline" disabled={install.isPending}
              onClick={() => runTag('backup2', 'Backup2 (stream)',
                'Run backup2 now? Streams directly to the rclone remote (slower, containers offline).')}>
              <CloudUpload className="h-3.5 w-3.5 mr-1.5" />Run backup2
            </Button>
            <Button size="sm" variant="outline" disabled={install.isPending}
              onClick={() => runTag('set-backup', 'Install schedule')}>
              <CalendarClock className="h-3.5 w-3.5 mr-1.5" />Install schedule ({cron})
            </Button>
            <Button size="sm" variant="outline" disabled={install.isPending}
              onClick={() => runTag('unset-backup', 'Remove schedule',
                'Remove the scheduled backup cron entry?')}>
              <CalendarClock className="h-3.5 w-3.5 mr-1.5" />Remove schedule
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">
            “Install schedule” writes a cron entry running the standard backup on the selected interval
            (save the config first). “Remove schedule” deletes that cron entry.
          </p>
        </CardContent>
      </Card>

      <Dialog open={!!jobId} onOpenChange={(o) => { if (!o) setJobId(null) }}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle className="font-mono text-sm">{jobTitle}</DialogTitle>
          </DialogHeader>
          <LogStream jobId={jobId} />
        </DialogContent>
      </Dialog>
    </div>
  )
}

function DestRow({ title, badge, enabled, onToggle, children }: {
  title: string; badge?: string; enabled: boolean; onToggle: () => void; children: React.ReactNode
}) {
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Toggle on={enabled} label={title} onClick={onToggle} />
        {badge && <span className="text-[10px] text-muted-foreground bg-muted/50 px-1.5 py-0.5 rounded">{badge}</span>}
      </div>
      {enabled && <div className="flex flex-wrap gap-3 pl-11">{children}</div>}
    </div>
  )
}

function Field({ label, value, onChange, placeholder, className }: {
  label: string; value: string; onChange: (v: string) => void; placeholder?: string; className?: string
}) {
  return (
    <label className="text-xs text-muted-foreground space-y-1">
      <span>{label}</span>
      <Input className={cn('h-8 text-sm font-mono', className)} value={value}
        placeholder={placeholder} onChange={e => onChange(e.target.value)} />
    </label>
  )
}

// Native <select>, keeps the current value selectable even if it isn't in the
// fetched options (e.g. a custom template / removed remote).
function SelectField({ label, value, options, onChange, className }: {
  label: string; value: string; options: string[]; onChange: (v: string) => void; className?: string
}) {
  const opts = value && !options.includes(value) ? [value, ...options] : options
  return (
    <label className="text-xs text-muted-foreground space-y-1">
      <span>{label}</span>
      <select
        className={cn('h-8 text-sm font-mono bg-background border border-border rounded-md px-2 block', className)}
        value={value} onChange={e => onChange(e.target.value)}
      >
        <option value="">—</option>
        {opts.map(o => <option key={o} value={o}>{o}</option>)}
      </select>
    </label>
  )
}

// Destination as `<remote>:<path>` — remote picked from configured rclone
// remotes, path edited freely. Stores the combined string.
function RemotePathField({ label, value, remotes, onChange }: {
  label: string; value: string; remotes: string[]; onChange: (v: string) => void
}) {
  const idx = value.indexOf(':')
  const remote = idx >= 0 ? value.slice(0, idx) : ''
  const path = idx >= 0 ? value.slice(idx + 1) : value
  const opts = remote && !remotes.includes(remote) ? [remote, ...remotes] : remotes
  return (
    <label className="text-xs text-muted-foreground space-y-1">
      <span>{label}</span>
      <div className="flex items-center gap-1">
        <select
          className="h-8 text-sm font-mono bg-background border border-border rounded-md px-2 max-w-36"
          value={remote} onChange={e => onChange(`${e.target.value}:${path}`)}
        >
          <option value="">remote…</option>
          {opts.map(o => <option key={o} value={o}>{o}</option>)}
        </select>
        <span className="text-muted-foreground">:</span>
        <Input className="h-8 text-sm font-mono w-52" value={path}
          placeholder="/Backups/Saltbox"
          onChange={e => onChange(`${remote}:${e.target.value}`)} />
      </div>
    </label>
  )
}
