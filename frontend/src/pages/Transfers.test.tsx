/**
 * Pressing a button must visibly do something, immediately.
 *
 * The complaint these tests exist for: the action buttons on this page felt dead — a click
 * produced no visible change at all, because most of them had no pending state, and the
 * mutations didn't refetch the jobs list, so the result only appeared whenever the 3 s poll
 * next happened to fire. Both halves are asserted here: the button reacts on click, and the
 * created job shows up without waiting for a poll.
 */
import { describe, it, expect } from 'vitest'
import { Transfers, TransfersPanel } from './Transfers'
import { renderWithProviders, screen, waitFor, userEvent } from '@/test/render'
import { server, http, HttpResponse } from '@/test/server'

const task = {
  id: 't1', name: 'Nightly media', op: 'move',
  items: [{ path: '/mnt/local/Media', is_dir: true }], dst: 'rem:Media',
  dry_run: true, opts: {}, schedule: '', disabled: false, created_at: '2026-08-17T00:00:00Z',
}

// The panel's own queries. Within one server.use(...) call the FIRST matching handler wins,
// so a test overriding one of these must list its own handler BEFORE spreading these.
function baseHandlers(opts: { tasks?: unknown[] } = {}) {
  return [
    http.get('/api/tasks', () => HttpResponse.json(opts.tasks ?? [task])),
    http.get('/api/queue', () => HttpResponse.json({ running: true, current: null, items: [] })),
    http.get('/api/rclone/remotes', () => HttpResponse.json({ remotes: {}, text: '' })),
    http.get('/api/jobs', () => HttpResponse.json([])),
  ]
}

describe('TransfersPanel actions', () => {
  it('shows the task so its actions are reachable', async () => {
    server.use(...baseHandlers())
    renderWithProviders(<TransfersPanel onJobStart={() => {}} />)
    expect(await screen.findByText('Nightly media')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Run$/ })).toBeInTheDocument()
  })

  // The core assertion: the click is acknowledged before the server has answered.
  it('Run shows progress the moment it is pressed, not when the server replies', async () => {
    let release: (() => void) | undefined
    const held = new Promise<void>((r) => { release = r })
    server.use(
      ...baseHandlers(),
      // Hold the response open so the in-flight state is observable.
      http.post('/api/tasks/t1/run', async () => {
        await held
        return HttpResponse.json({ job_id: 'job-9' })
      }),
    )
    renderWithProviders(<TransfersPanel onJobStart={() => {}} />)
    const run = await screen.findByRole('button', { name: /^Run$/ })

    await userEvent.click(run)

    // Feedback while the request is still open — this is what was missing entirely.
    expect(await screen.findByRole('button', { name: /Starting/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Starting/ })).toBeDisabled()

    release!()
    // ...and it goes back to normal once the request settles.
    await waitFor(() => expect(screen.getByRole('button', { name: /^Run$/ })).toBeEnabled())
  })

  it('only the pressed row spins, not every Run button', async () => {
    const second = { ...task, id: 't2', name: 'Second task' }
    let release: (() => void) | undefined
    const held = new Promise<void>((r) => { release = r })
    server.use(
      ...baseHandlers({ tasks: [task, second] }),
      http.post('/api/tasks/t1/run', async () => { await held; return HttpResponse.json({ job_id: 'j' }) }),
    )
    renderWithProviders(<TransfersPanel onJobStart={() => {}} />)
    await screen.findByText('Second task')

    const runs = screen.getAllByRole('button', { name: /^Run$/ })
    expect(runs).toHaveLength(2)
    await userEvent.click(runs[0])

    // One row busy, the other still offering to run.
    expect(await screen.findByRole('button', { name: /Starting/ })).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /^Run$/ })).toHaveLength(1)
    release!()
  })

  // Without invalidating ['jobs'] the launched job only appeared on the next 3 s poll, so
  // the screen sat unchanged after a successful launch. Rendered as the whole page, because
  // the panel launches the job while the Activity list is what shows it — the seam the bug
  // lived in.
  it('the launched job appears in Activity without waiting for a poll', async () => {
    let launched = false
    server.use(
      // This must precede baseHandlers' /api/jobs, or that one shadows it and always wins.
      http.get('/api/jobs', () => HttpResponse.json(launched
        ? [{ id: 'job-9', tag: 'move: Media to rem:Media', action: 'move', status: 'running', created_at: '2026-08-17T00:00:00Z', log_lines: 0 }]
        : [])),
      http.post('/api/tasks/t1/run', () => { launched = true; return HttpResponse.json({ job_id: 'job-9' }) }),
      http.get('/api/transfers/:id/stats', () => HttpResponse.json({})),
      http.get('/api/transfers/:id/telemetry', () => HttpResponse.json({ samples: [], findings: [] })),
      ...baseHandlers(),
    )
    renderWithProviders(<Transfers />)
    await userEvent.click(await screen.findByRole('button', { name: /^Run$/ }))

    // No fake timers, no 3 s wait: the job is on screen because the launch refetched.
    expect(await screen.findByText(/move: Media/)).toBeInTheDocument()
  })

  it('reports the new job id so the Activity list can expand it', async () => {
    const seen: string[] = []
    server.use(
      ...baseHandlers(),
      http.post('/api/tasks/t1/run', () => HttpResponse.json({ job_id: 'job-42' })),
    )
    renderWithProviders(<TransfersPanel onJobStart={(id) => seen.push(id)} />)
    await userEvent.click(await screen.findByRole('button', { name: /^Run$/ }))
    await waitFor(() => expect(seen).toContain('job-42'))
  })

  it('re-enables the button when the request fails, instead of staying stuck', async () => {
    server.use(
      ...baseHandlers(),
      http.post('/api/tasks/t1/run', () => new HttpResponse('boom', { status: 500 })),
    )
    renderWithProviders(<TransfersPanel onJobStart={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: /^Run$/ }))
    await waitFor(() => expect(screen.getByRole('button', { name: /^Run$/ })).toBeEnabled())
  })

  // The server patches the stored task, so an omitted field means "leave it alone". Saving
  // therefore has to state every field, empty ones included — otherwise clearing a schedule
  // would silently do nothing.
  it('saving an edited task sends every field, including the empty ones', async () => {
    let sent: Record<string, unknown> | undefined
    server.use(
      http.put('/api/tasks/t1', async ({ request }) => {
        sent = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ ...task, ...sent })
      }),
      ...baseHandlers(),
    )
    renderWithProviders(<TransfersPanel onJobStart={() => {}} />)

    // Open the row's editor, then save it unchanged.
    await userEvent.click(await screen.findByTitle('Edit Nightly media'))
    await userEvent.click(await screen.findByRole('button', { name: /Update task/ }))

    await waitFor(() => expect(sent).toBeDefined())
    // Present as keys even when empty — absence would mean "keep", which is not what the
    // form means when a field has been cleared.
    for (const k of ['name', 'op', 'items', 'dst', 'dry_run', 'opts', 'schedule', 'run_mode']) {
      expect(Object.keys(sent!)).toContain(k)
    }
    // And the task's dry-run must survive a save that didn't touch it.
    expect(sent!.dry_run).toBe(true)
  })

  it('Queue shows its own progress, separate from Run', async () => {
    let release: (() => void) | undefined
    const held = new Promise<void>((r) => { release = r })
    server.use(
      ...baseHandlers(),
      http.post('/api/tasks/t1/queue', async () => { await held; return HttpResponse.json({ job_id: 'j' }) }),
    )
    renderWithProviders(<TransfersPanel onJobStart={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: /^Queue$/ }))

    expect(await screen.findByRole('button', { name: /Queuing/ })).toBeInTheDocument()
    // Run stays available — the spinner belongs to the button that was pressed.
    expect(screen.getByRole('button', { name: /^Run$/ })).toBeEnabled()
    release!()
  })
})

// The command preview must come from the server that will run it. The UI used to render this
// string from a TypeScript copy of the flag rules, so the two could drift and the preview
// would describe a command that was never going to run.
describe('command preview', () => {
  it('shows what the server says it will run, not something the UI built', async () => {
    let asked: Record<string, unknown> | undefined
    server.use(
      http.post('/api/rclone/preview', async ({ request }) => {
        asked = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ commands: ['rclone --config /etc/r.conf move /srv/data rem:Media --filter + /one'] })
      }),
      ...baseHandlers(),
    )
    renderWithProviders(<TransfersPanel onJobStart={() => {}} />)

    // Open the editor for the saved task, which fills in a source and destination.
    await userEvent.click(await screen.findByTitle('Edit Nightly media'))

    // The exact string the server returned is what appears — the UI does not compose it.
    expect(await screen.findByText(/rclone --config \/etc\/r\.conf move \/srv\/data rem:Media/)).toBeInTheDocument()
    // ...and it asked using the form's current values.
    expect(asked).toMatchObject({ op: 'move', dst: 'rem:Media' })
  })

  it('says what is missing instead of previewing an incomplete command', async () => {
    server.use(...baseHandlers({ tasks: [] }))
    renderWithProviders(<TransfersPanel onJobStart={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: /New transfer/ }))
    expect(await screen.findByText(/Pick a source and a destination/i)).toBeInTheDocument()
  })
})
