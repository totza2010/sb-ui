import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTestConnection, useSaveSetup } from '@/lib/api'
import type { SetupStatus } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Server, Laptop, ChevronRight, ChevronLeft,
  CheckCircle2, XCircle, Loader2, Wifi, Key, Lock,
} from 'lucide-react'
import { cn } from '@/lib/cn'

type Mode = 'ssh' | 'local'
type AuthType = 'key' | 'password'

interface SSHForm {
  host: string
  port: number
  user: string
  auth_type: AuthType
  // key auth
  key_path: string
  passphrase: string
  // password auth
  password: string
}

const STEPS_SSH   = ['Connection mode', 'SSH credentials', 'Test & confirm']
const STEPS_LOCAL = ['Connection mode', 'Confirm']

interface Props {
  onComplete: () => void
  initial?: SetupStatus
}

export function ConnectionSetup({ onComplete, initial }: Props) {
  const qc = useQueryClient()
  const testConn = useTestConnection()
  const saveSetup = useSaveSetup()

  const [step, setStep] = useState(0)
  const [mode, setMode] = useState<Mode>(initial?.mode ?? 'ssh')
  const [form, setForm] = useState<SSHForm>({
    host:       initial?.host ?? '',
    port:       initial?.port ?? 22,
    user:       initial?.user ?? 'seed',
    auth_type:  initial?.auth_type ?? 'password',
    key_path:   initial?.key ?? '~/.ssh/id_rsa',
    passphrase: '',
    password:   '',
  })

  const steps = mode === 'ssh' ? STEPS_SSH : STEPS_LOCAL
  const lastStep = steps.length - 1

  function set<K extends keyof SSHForm>(k: K, v: SSHForm[K]) {
    setForm((f) => ({ ...f, [k]: v }))
    testConn.reset()
  }

  function buildTestBody() {
    if (form.auth_type === 'password') {
      return {
        host: form.host, port: form.port, user: form.user,
        auth_type: 'password' as const,
        password: form.password,
      }
    }
    return {
      host: form.host, port: form.port, user: form.user,
      auth_type: 'key' as const,
      key_path: form.key_path,
      passphrase: form.passphrase || undefined,
    }
  }

  function buildSaveBody() {
    if (mode === 'local') return { mode: 'local' as const }
    if (form.auth_type === 'password') {
      return {
        mode: 'ssh' as const,
        host: form.host, port: form.port, user: form.user,
        auth_type: 'password' as const,
        password: form.password,
      }
    }
    return {
      mode: 'ssh' as const,
      host: form.host, port: form.port, user: form.user,
      auth_type: 'key' as const,
      key_path: form.key_path,
      passphrase: form.passphrase || undefined,
    }
  }

  async function handleSave() {
    try {
      await saveSetup.mutateAsync(buildSaveBody())
      await qc.invalidateQueries({ queryKey: ['setup-status'] })
      onComplete()
    } catch {
      // error is visible via saveSetup.error below
    }
  }

  const canProceedStep1 =
    mode === 'local' ||
    (form.host.trim() !== '' &&
      (form.auth_type === 'key' || form.password.trim() !== ''))

  return (
    <div className="min-h-screen bg-background flex items-center justify-center p-4">
      <div className="w-full max-w-lg">
        {/* Header */}
        <div className="mb-8 text-center">
          <div className="inline-flex items-center justify-center w-12 h-12 rounded-xl bg-primary/10 mb-3">
            <Wifi className="h-6 w-6 text-primary" />
          </div>
          <h1 className="text-2xl font-bold text-foreground">Connect to Saltbox</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Configure how this UI reaches your Saltbox server
          </p>
        </div>

        {/* Progress bar */}
        <div className="flex gap-1.5 mb-6">
          {steps.map((s, i) => (
            <div
              key={s}
              className={cn(
                'flex-1 h-1 rounded-full transition-colors',
                i <= step ? 'bg-primary' : 'bg-border'
              )}
            />
          ))}
        </div>
        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-5">
          Step {step + 1} of {steps.length} — {steps[step]}
        </p>

        {/* ── Step 0: Mode ── */}
        {step === 0 && (
          <div className="space-y-3">
            {([
              { v: 'ssh', icon: Server, title: 'Remote via SSH', desc: 'UI runs on your dev machine, connects to Saltbox over SSH' },
              { v: 'local', icon: Laptop, title: 'Local (same server)', desc: 'UI is deployed directly on the Saltbox server — no SSH needed' },
            ] as const).map(({ v, icon: Icon, title, desc }) => (
              <button
                key={v}
                onClick={() => setMode(v)}
                className={cn(
                  'w-full text-left rounded-xl border p-4 transition-all',
                  mode === v
                    ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
                    : 'border-border hover:border-primary/40'
                )}
              >
                <div className="flex items-center gap-3">
                  <Icon className="h-5 w-5 text-primary shrink-0" />
                  <div className="flex-1">
                    <p className="font-semibold text-sm text-foreground">{title}</p>
                    <p className="text-xs text-muted-foreground mt-0.5">{desc}</p>
                  </div>
                  {mode === v && <CheckCircle2 className="h-4 w-4 text-primary shrink-0" />}
                </div>
              </button>
            ))}
          </div>
        )}

        {/* ── Step 1 (SSH): Credentials ── */}
        {step === 1 && mode === 'ssh' && (
          <div className="space-y-4">
            {/* Host + port */}
            <div className="grid grid-cols-3 gap-3">
              <div className="col-span-2 space-y-1.5">
                <Label>Host / IP</Label>
                <Input
                  placeholder="saltbox-1 or 192.168.1.100"
                  value={form.host}
                  onChange={(e) => set('host', e.target.value)}
                  autoFocus
                />
              </div>
              <div className="space-y-1.5">
                <Label>Port</Label>
                <Input
                  type="number"
                  value={form.port}
                  onChange={(e) => set('port', parseInt(e.target.value) || 22)}
                />
              </div>
            </div>

            {/* Username */}
            <div className="space-y-1.5">
              <Label>SSH username</Label>
              <Input
                placeholder="seed"
                value={form.user}
                onChange={(e) => set('user', e.target.value)}
              />
            </div>

            {/* Auth type toggle */}
            <div className="space-y-2">
              <Label>Authentication</Label>
              <div className="flex rounded-lg border border-border p-0.5 gap-0.5">
                {([
                  { v: 'password', icon: Lock, label: 'Password' },
                  { v: 'key',      icon: Key,  label: 'SSH key' },
                ] as const).map(({ v, icon: Icon, label }) => (
                  <button
                    key={v}
                    onClick={() => set('auth_type', v)}
                    className={cn(
                      'flex-1 flex items-center justify-center gap-1.5 py-1.5 rounded-md text-sm transition-colors',
                      form.auth_type === v
                        ? 'bg-primary text-primary-foreground font-medium'
                        : 'text-muted-foreground hover:text-foreground'
                    )}
                  >
                    <Icon className="h-3.5 w-3.5" />
                    {label}
                  </button>
                ))}
              </div>
            </div>

            {/* Password auth */}
            {form.auth_type === 'password' && (
              <div className="space-y-1.5">
                <Label>Password</Label>
                <Input
                  type="password"
                  placeholder="SSH password"
                  value={form.password}
                  onChange={(e) => set('password', e.target.value)}
                />
              </div>
            )}

            {/* Key auth */}
            {form.auth_type === 'key' && (
              <>
                <div className="space-y-1.5">
                  <Label>SSH key path</Label>
                  <Input
                    placeholder="~/.ssh/id_rsa"
                    value={form.key_path}
                    onChange={(e) => set('key_path', e.target.value)}
                  />
                  <p className="text-xs text-muted-foreground">
                    Path on <em>this machine</em> to your private key
                  </p>
                </div>
                <div className="space-y-1.5">
                  <Label>
                    Passphrase{' '}
                    <span className="text-muted-foreground font-normal">(if any)</span>
                  </Label>
                  <Input
                    type="password"
                    placeholder="leave empty if key has no passphrase"
                    value={form.passphrase}
                    onChange={(e) => set('passphrase', e.target.value)}
                  />
                </div>
              </>
            )}
          </div>
        )}

        {/* ── Step 1 (local): Confirm ── */}
        {step === 1 && mode === 'local' && (
          <div className="rounded-xl border border-border bg-card p-5 space-y-3 text-sm">
            <p className="text-foreground font-medium">Running in local mode</p>
            <p className="text-muted-foreground text-xs leading-relaxed">
              The backend will call <code className="bg-muted px-1 rounded">ansible-playbook</code> and
              read files directly on this machine. Make sure the backend process has the required
              permissions (docker group, sudo for ansible).
            </p>
            <div className="pt-2 border-t border-border space-y-1.5">
              <div className="flex justify-between">
                <span className="text-muted-foreground">Mode</span>
                <span className="font-medium">Local</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Saltbox repo</span>
                <span className="font-mono text-xs">/srv/git/saltbox</span>
              </div>
            </div>
          </div>
        )}

        {/* ── Step 2 (SSH): Test + confirm ── */}
        {step === 2 && mode === 'ssh' && (
          <div className="space-y-4">
            {/* Summary */}
            <div className="rounded-xl border border-border bg-card p-4 space-y-2 text-sm">
              {[
                ['Host', `${form.host}:${form.port}`],
                ['User', form.user],
                ['Auth', form.auth_type === 'password' ? 'Password' : `Key: ${form.key_path}`],
              ].map(([k, v]) => (
                <div key={k} className="flex justify-between">
                  <span className="text-muted-foreground">{k}</span>
                  <span className="font-mono text-xs">{v}</span>
                </div>
              ))}
            </div>

            {/* Test button */}
            <Button
              variant="outline"
              className="w-full"
              onClick={() => testConn.mutate(buildTestBody())}
              disabled={testConn.isPending}
            >
              {testConn.isPending
                ? <><Loader2 className="h-4 w-4 mr-2 animate-spin" />Testing…</>
                : <><Wifi className="h-4 w-4 mr-2" />Test connection</>}
            </Button>

            {/* Result */}
            {testConn.data && (
              <div className={cn(
                'flex items-start gap-3 rounded-lg border p-3 text-sm',
                testConn.data.success
                  ? 'border-green-500/30 bg-green-500/5 text-green-400'
                  : 'border-red-500/30 bg-red-500/5 text-red-400'
              )}>
                {testConn.data.success
                  ? <CheckCircle2 className="h-4 w-4 shrink-0 mt-0.5" />
                  : <XCircle className="h-4 w-4 shrink-0 mt-0.5" />}
                <div>
                  {testConn.data.success
                    ? <p>Connected — {testConn.data.latency_ms}ms</p>
                    : <>
                        <p className="font-medium">Connection failed</p>
                        <p className="text-xs mt-0.5 opacity-80 break-all">{testConn.data.error}</p>
                      </>}
                </div>
              </div>
            )}
          </div>
        )}

        {/* Navigation */}
        <div className="flex justify-between mt-8">
          <Button
            variant="outline"
            onClick={() => setStep((s) => s - 1)}
            disabled={step === 0}
          >
            <ChevronLeft className="h-4 w-4 mr-1" />Back
          </Button>

          {step < lastStep ? (
            <Button
              onClick={() => setStep((s) => s + 1)}
              disabled={!canProceedStep1 && step === 1}
            >
              Next<ChevronRight className="h-4 w-4 ml-1" />
            </Button>
          ) : (
            <Button
              onClick={handleSave}
              disabled={saveSetup.isPending || (mode === 'ssh' && !testConn.data?.success)}
            >
              {saveSetup.isPending
                ? <><Loader2 className="h-4 w-4 mr-2 animate-spin" />Saving…</>
                : <><CheckCircle2 className="h-4 w-4 mr-1.5" />Save & enter</>}
            </Button>
          )}
        </div>

        {mode === 'ssh' && step === lastStep && !testConn.data?.success && (
          <p className="text-center text-xs text-muted-foreground mt-3">
            Test the connection first to continue
          </p>
        )}
        {saveSetup.error && (
          <div className="flex items-start gap-2 rounded-lg border border-red-500/30 bg-red-500/5 text-red-400 p-3 text-xs mt-3">
            <XCircle className="h-3.5 w-3.5 shrink-0 mt-0.5" />
            <span className="break-all">{saveSetup.error.message}</span>
          </div>
        )}
      </div>
    </div>
  )
}
