/**
 * UploadPlanCard renders whatever /uploader/status returns. Every case here is a
 * shape the Go API really produces; the recurring failure mode is the card showing
 * nothing (or the wrong empty message) even though the status JSON is correct, so
 * these assert the JSON → visible-text hop directly.
 */
import { describe, it, expect } from 'vitest'
import { UploadPlanCard } from './Uploader'
import { renderWithProviders, screen } from '@/test/render'
import { server, http, HttpResponse } from '@/test/server'
import { makeStatus, makePlan, makePlanRemote } from '@/test/fixtures'

const statusOf = (s: ReturnType<typeof makeStatus>) =>
  server.use(http.get('/api/uploader/status', () => HttpResponse.json(s)))

describe('UploadPlanCard', () => {
  it('shows "No plan yet" before a check has run', async () => {
    statusOf(makeStatus({ plan: null, checking: false }))
    renderWithProviders(<UploadPlanCard />)
    expect(await screen.findByText(/No plan yet/i)).toBeInTheDocument()
  })

  it('renders each planned remote with its human size', async () => {
    statusOf(
      makeStatus({
        plan: makePlan({
          remotes: [
            makePlanRemote({ remote: 'main_01', human: '10.0G' }),
            makePlanRemote({ remote: 'main_02', human: '8.5G', at_sec: 1800 }),
          ],
        }),
      }),
    )
    renderWithProviders(<UploadPlanCard />)
    expect(await screen.findByText('main_01')).toBeInTheDocument()
    expect(screen.getByText('main_02')).toBeInTheDocument()
    // sizes can appear in more than one column (transfer + fill), so just assert present
    expect(screen.getAllByText('10.0G').length).toBeGreaterThan(0)
    expect(screen.getAllByText('8.5G').length).toBeGreaterThan(0)
  })

  it('explains an empty source rather than showing a blank card', async () => {
    statusOf(makeStatus({ plan: makePlan({ files_total: 0, remotes: [], meets_threshold: true }) }))
    renderWithProviders(<UploadPlanCard />)
    expect(await screen.findByText(/Source has no uploadable files/i)).toBeInTheDocument()
  })

  // The exact bug from this session: files exist but each is larger than the biggest
  // daily cap, so nothing fits. The card must surface leftover_why, not spin blank.
  it('surfaces leftover_why when files are larger than every cap', async () => {
    const why = '"big1.mkv" (10.5G) is larger than the biggest daily cap (5.0G) — it can\'t upload; raise a remote\'s cap above your largest file'
    statusOf(
      makeStatus({
        plan: makePlan({ files_total: 2, remotes: [], leftover_why: why, meets_threshold: true }),
      }),
    )
    renderWithProviders(<UploadPlanCard />)
    expect(await screen.findByText(/larger than the biggest daily cap/i)).toBeInTheDocument()
  })

  // Run now kicks off async work and returns immediately; the running job isn't in the
  // next poll yet. The card must show instant feedback so it doesn't look idle after a click.
  it('shows an instant "Starting upload" banner while starting, even before a plan exists', async () => {
    statusOf(makeStatus({ plan: null, checking: false }))
    renderWithProviders(<UploadPlanCard starting />)
    expect(await screen.findByText(/Starting upload/i)).toBeInTheDocument()
  })

  it('overlays the starting banner on an existing plan', async () => {
    statusOf(makeStatus({ plan: makePlan() }))
    renderWithProviders(<UploadPlanCard starting />)
    // wait for the plan to load, then the banner still overlays it (starting stays true)
    expect(await screen.findByText('main_01')).toBeInTheDocument()
    expect(screen.getByText(/Starting upload/i)).toBeInTheDocument()
  })

  it('shows the resume banner when an upload was stopped mid-way', async () => {
    statusOf(makeStatus({ resume: 'main_02', plan: makePlan() }))
    renderWithProviders(<UploadPlanCard />)
    expect(await screen.findByText(/Resuming/i)).toBeInTheDocument()
    expect(screen.getByText('main_02')).toBeInTheDocument()
  })
})
