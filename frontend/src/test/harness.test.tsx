import { describe, it, expect } from 'vitest'
import { useQuery } from '@tanstack/react-query'
import { renderWithProviders, screen } from './render'
import { server, http, HttpResponse } from './server'

// Proves the plumbing: a component's react-query fetch is answered by MSW and the
// resulting JSON reaches the DOM. If this fails, every other component test's
// failure is suspect — start here.
function Probe() {
  const { data } = useQuery<{ hello: string }>({ queryKey: ['probe'], queryFn: () => fetch('/api/probe').then((r) => r.json()) })
  return <div>{data ? data.hello : 'loading'}</div>
}

describe('test harness', () => {
  it('renders JSON returned by MSW', async () => {
    server.use(http.get('/api/probe', () => HttpResponse.json({ hello: 'world' })))
    renderWithProviders(<Probe />)
    expect(await screen.findByText('world')).toBeInTheDocument()
  })
})
