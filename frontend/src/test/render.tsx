/**
 * renderWithProviders — mount a component with the same context it has in the app:
 * a react-query client (retries off, no cache carried between tests) and a router.
 * Use this instead of RTL's bare render() for anything that calls a use…() hook.
 */
import type { ReactElement, ReactNode } from 'react'
import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'

export function makeClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  })
}

export function renderWithProviders(ui: ReactElement, opts?: { route?: string }) {
  const client = makeClient()
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[opts?.route ?? '/']}>{children}</MemoryRouter>
    </QueryClientProvider>
  )
  return { client, ...render(ui, { wrapper: Wrapper }) }
}

export * from '@testing-library/react'
export { default as userEvent } from '@testing-library/user-event'
