import { useState } from 'react'
import { useSaveConfig, useInstallApp } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { LogStream } from '@/components/LogStream'
import { ChevronRight, ChevronLeft, Rocket } from 'lucide-react'

type InstallType = 'saltbox' | 'mediabox' | 'feederbox'

const INSTALL_TYPES: { key: InstallType; label: string; desc: string }[] = [
  { key: 'saltbox',   label: 'Saltbox',    desc: 'Full media server — Plex/Emby/Jellyfin + downloaders on a single box' },
  { key: 'mediabox',  label: 'Mediabox',   desc: 'Media server only — streams from a separate Feederbox' },
  { key: 'feederbox', label: 'Feederbox',  desc: 'Downloader box only — feeds a separate Mediabox' },
]

const STEPS = ['Install Type', 'User Config', 'Settings', 'Review & Install']

export function SetupWizard() {
  const [step, setStep] = useState(0)
  const [installType, setInstallType] = useState<InstallType>('saltbox')
  const [accounts, setAccounts] = useState({ domain: '', email: '', name: '', pass: '' })
  const [settings, setSettings] = useState({ downloads: '/mnt/unionfs/downloads', shell: 'bash' })
  const saveAccounts = useSaveConfig('accounts')
  const saveSettings = useSaveConfig('settings')
  const install = useInstallApp()
  const [jobId, setJobId] = useState<string | null>(null)

  function handleInstall() {
    saveAccounts.mutate({ user: accounts }, {
      onSuccess: () => {
        saveSettings.mutate({ downloads: settings.downloads, shell: settings.shell }, {
          onSuccess: () => {
            install.mutate({ tag: installType }, { onSuccess: (d) => setJobId(d.job_id) })
          },
        })
      },
    })
  }

  return (
    <div className="p-6">
      <h1 className="text-xl font-semibold text-foreground mb-1">Setup Wizard</h1>
      <p className="text-sm text-muted-foreground mb-5">Initial Saltbox configuration</p>

      <div className="flex gap-1 mb-6">
        {STEPS.map((s, i) => (
          <div key={s} className={`flex-1 h-1 rounded-full transition-colors ${i <= step ? 'bg-primary' : 'bg-border'}`} />
        ))}
      </div>
      <p className="text-sm font-medium text-foreground mb-4">Step {step + 1}: {STEPS[step]}</p>

      {/* Step 0 */}
      {step === 0 && (
        <div className="space-y-3">
          {INSTALL_TYPES.map(({ key, label, desc }) => (
            <button
              key={key}
              onClick={() => setInstallType(key)}
              className={`w-full text-left rounded-lg border p-4 transition-colors ${installType === key ? 'border-primary bg-primary/5' : 'border-border hover:border-primary/40'}`}
            >
              <p className="font-medium text-sm text-foreground">{label}</p>
              <p className="text-xs text-muted-foreground mt-1">{desc}</p>
            </button>
          ))}
        </div>
      )}

      {/* Step 1 */}
      {step === 1 && (
        <div className="space-y-4">
          {(['domain', 'email', 'name', 'pass'] as const).map((k) => (
            <div key={k} className="space-y-1.5">
              <Label className="capitalize">{k === 'pass' ? 'Password' : k}</Label>
              <Input
                type={k === 'pass' ? 'password' : 'text'}
                placeholder={k === 'domain' ? 'yourdomain.com' : k === 'email' ? 'you@example.com' : k}
                value={accounts[k]}
                onChange={(e) => setAccounts((a) => ({ ...a, [k]: e.target.value }))}
              />
            </div>
          ))}
        </div>
      )}

      {/* Step 2 */}
      {step === 2 && (
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label>Downloads path</Label>
            <Input value={settings.downloads} onChange={(e) => setSettings((s) => ({ ...s, downloads: e.target.value }))} />
          </div>
          <div className="space-y-1.5">
            <Label>Shell</Label>
            <div className="flex gap-2">
              {(['bash', 'zsh'] as const).map((sh) => (
                <Button key={sh} size="sm" variant={settings.shell === sh ? 'default' : 'outline'} onClick={() => setSettings((s) => ({ ...s, shell: sh }))}>
                  {sh}
                </Button>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* Step 3 */}
      {step === 3 && (
        <div className="rounded-lg border border-border bg-card p-4 space-y-2 text-sm">
          <div className="flex justify-between"><span className="text-muted-foreground">Install type</span><span className="font-medium capitalize">{installType}</span></div>
          <div className="flex justify-between"><span className="text-muted-foreground">Domain</span><span className="font-mono">{accounts.domain}</span></div>
          <div className="flex justify-between"><span className="text-muted-foreground">Email</span><span className="font-mono">{accounts.email}</span></div>
          <div className="flex justify-between"><span className="text-muted-foreground">Username</span><span className="font-mono">{accounts.name}</span></div>
          <div className="flex justify-between"><span className="text-muted-foreground">Downloads</span><span className="font-mono">{settings.downloads}</span></div>
          <div className="flex justify-between"><span className="text-muted-foreground">Shell</span><span className="font-mono">{settings.shell}</span></div>
        </div>
      )}

      <div className="flex justify-between mt-6">
        <Button variant="outline" onClick={() => setStep((s) => s - 1)} disabled={step === 0}>
          <ChevronLeft className="h-4 w-4 mr-1" />Back
        </Button>
        {step < 3 ? (
          <Button onClick={() => setStep((s) => s + 1)}>
            Next<ChevronRight className="h-4 w-4 ml-1" />
          </Button>
        ) : (
          <Button onClick={handleInstall} disabled={install.isPending}>
            <Rocket className="h-4 w-4 mr-1.5" />Start Installation
          </Button>
        )}
      </div>

      <Dialog open={!!jobId} onOpenChange={(o) => { if (!o) setJobId(null) }}>
        <DialogContent className="max-w-3xl">
          <DialogHeader><DialogTitle>Installing {installType}</DialogTitle></DialogHeader>
          <LogStream jobId={jobId} />
        </DialogContent>
      </Dialog>
    </div>
  )
}
