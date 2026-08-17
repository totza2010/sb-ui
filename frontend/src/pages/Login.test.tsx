/**
 * The login gate, from the browser's side.
 *
 * The server denies /api without a session or token, so these assert the UI does the right
 * thing with that answer: show a form, sign in, and — the case that matters most — react to a
 * session that dies mid-visit by asking for the password again rather than filling the screen
 * with broken panels.
 */
import { describe, it, expect } from 'vitest'
import { Login } from './Login'
import { renderWithProviders, screen, waitFor, userEvent } from '@/test/render'
import { server, http, HttpResponse } from '@/test/server'
import { setUnauthorizedHandler } from '@/lib/api'

const authed = { password_set: true, token_set: false, authenticated: true, trust_loopback: true, loopback_client: false }
const anon = { ...authed, authenticated: false }

describe('Login', () => {
  it('asks for a password when one is set', async () => {
    renderWithProviders(<Login status={anon} />)
    expect(await screen.findByLabelText(/password/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Sign in/ })).toBeInTheDocument()
  })

  // A form that cannot succeed is worse than no form: say what to run instead.
  it('explains how to set a password when none exists yet', async () => {
    renderWithProviders(<Login status={{ ...anon, password_set: false }} />)
    expect(await screen.findByText(/No password is set/i)).toBeInTheDocument()
    expect(screen.getByText(/--set-password/)).toBeInTheDocument()
    expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument()
  })

  it('signs in and shows progress while it waits', async () => {
    let release: (() => void) | undefined
    const held = new Promise<void>((r) => { release = r })
    let sent: unknown
    server.use(
      http.post('/api/auth/login', async ({ request }) => {
        sent = await request.json()
        await held
        return HttpResponse.json({ ok: true })
      }),
    )
    renderWithProviders(<Login status={anon} />)

    await userEvent.type(await screen.findByLabelText(/password/i), 'my-password')
    await userEvent.click(screen.getByRole('button', { name: /Sign in/ }))

    // Pressing it says something immediately, same rule as everywhere else.
    expect(await screen.findByRole('button', { name: /Signing in/ })).toBeDisabled()
    release!()
    await waitFor(() => expect(sent).toEqual({ password: 'my-password' }))
  })

  it('reports a wrong password without clearing the form silently', async () => {
    server.use(http.post('/api/auth/login', () => new HttpResponse('nope', { status: 401 })))
    renderWithProviders(<Login status={anon} />)

    await userEvent.type(await screen.findByLabelText(/password/i), 'wrong-one')
    await userEvent.click(screen.getByRole('button', { name: /Sign in/ }))

    expect(await screen.findByText(/Incorrect password/i)).toBeInTheDocument()
    // Still usable for another attempt.
    await waitFor(() => expect(screen.getByRole('button', { name: /Sign in/ })).toBeEnabled())
  })

  it('cannot be submitted empty', async () => {
    renderWithProviders(<Login status={anon} />)
    expect(await screen.findByRole('button', { name: /Sign in/ })).toBeDisabled()
  })

  it('mentions the token route only when a token is configured', async () => {
    const { unmount } = renderWithProviders(<Login status={anon} />)
    expect(screen.queryByText(/X-API-Key/)).not.toBeInTheDocument()
    unmount()

    renderWithProviders(<Login status={{ ...anon, token_set: true }} />)
    expect(await screen.findByText(/X-API-Key/)).toBeInTheDocument()
  })
})

// request() reports a 401 once, centrally, so the gate can react. Without this every panel
// would just show its own error and the user would never be offered a way back in.
describe('unauthorized handling', () => {
  it('reports a 401 from any endpoint to the registered handler', async () => {
    let fired = 0
    setUnauthorizedHandler(() => { fired++ })
    server.use(http.get('/api/tasks', () => new HttpResponse('no', { status: 401 })))

    const res = await fetch('/api/tasks')
    expect(res.status).toBe(401)

    // Drive it the way the app does — through the api helper — by rendering something that
    // fetches. Simplest faithful check: the helper is what wires this up, so call it.
    const { useTasks } = await import('@/lib/api')
    function Probe() {
      useTasks()
      return null
    }
    renderWithProviders(<Probe />)
    await waitFor(() => expect(fired).toBeGreaterThan(0))
    setUnauthorizedHandler(null)
  })
})
