/**
 * Login — the gate in front of everything.
 *
 * The API denies /api and /ws without a session or a token, so this screen is what the app
 * shows when the server says no. It is deliberately plain: one field, one button, and honest
 * messages about the two states a fresh install can be in.
 */
import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useLogin, type AuthStatus } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card } from '@/components/ui/card'
import { Loader2, LogIn, ShieldCheck, TriangleAlert } from 'lucide-react'

export function Login({ status }: { status?: AuthStatus }) {
  const qc = useQueryClient()
  const login = useLogin()
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')

  // No password has been set yet, so there is nothing to type. Say what to run instead of
  // showing a form that cannot succeed.
  const needsSetup = status && !status.password_set

  function submit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    login.mutate(password, {
      onSuccess: () => {
        setPassword('')
        // Re-ask the server who we are; the gate above re-renders into the app.
        qc.invalidateQueries()
      },
      onError: () => setError('Incorrect password.'),
    })
  }

  return (
    <div className="grid min-h-screen place-items-center bg-background p-6">
      <Card className="w-full max-w-sm space-y-5 rounded-xl border-border/70 p-6 shadow-sm">
        <div className="flex items-start gap-3">
          <div className="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-primary/10 text-primary">
            <ShieldCheck className="h-5 w-5" />
          </div>
          <div>
            <h1 className="text-lg font-semibold tracking-tight text-foreground">sb-ui</h1>
            <p className="text-sm text-muted-foreground">Sign in to continue.</p>
          </div>
        </div>

        {needsSetup ? (
          <div className="space-y-2 rounded-md border border-warning/50 bg-warning/10 p-3">
            <p className="flex items-center gap-1.5 text-sm font-medium text-warning">
              <TriangleAlert className="h-4 w-4" />No password is set
            </p>
            <p className="text-xs text-muted-foreground">
              Set one on the host, then reload this page. It is a CLI command on purpose — the
              API cannot change the password that protects it.
            </p>
            <code className="block rounded bg-secondary/60 px-2 py-1 text-[11px]">sb-ui --set-password</code>
          </div>
        ) : (
          <form onSubmit={submit} className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="sb-password">Password</Label>
              <Input
                id="sb-password"
                type="password"
                autoComplete="current-password"
                autoFocus
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
            {error && <p className="text-xs text-destructive">{error}</p>}
            <Button type="submit" className="w-full gap-1.5" disabled={login.isPending || !password}>
              {login.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <LogIn className="h-4 w-4" />}
              {login.isPending ? 'Signing in…' : 'Sign in'}
            </Button>
          </form>
        )}

        {status?.token_set && (
          <p className="border-t border-border pt-3 text-[11px] text-muted-foreground">
            Scripts can authenticate with the API token instead, as
            <span className="font-mono"> Authorization: Bearer …</span> or
            <span className="font-mono"> X-API-Key</span>.
          </p>
        )}
      </Card>
    </div>
  )
}
