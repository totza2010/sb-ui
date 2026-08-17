/**
 * MSW server for component tests. Handlers answer the same paths the real Go API
 * serves (under /api), so components run through their actual react-query fetch
 * path — the thing that keeps breaking is exactly this API-JSON → rendered-UI hop.
 *
 * Defaults here are the "nothing configured yet" baseline; individual tests call
 * server.use(...) to override a single endpoint with a specific fixture. Any
 * request a test forgets to mock throws (onUnhandledRequest below), so missing
 * coverage is loud rather than a silent hang.
 */
import { setupServer } from 'msw/node'
import { http, HttpResponse } from 'msw'

const A = '/api'

// Defaults are deliberately minimal/empty rather than domain fixtures: a test that cares
// about a payload states it itself via server.use(...), which keeps the intent visible in
// the test and keeps this file free of any one feature's types.
export const handlers = [
  http.get(`${A}/jobs`, () => HttpResponse.json([])),
]

export const server = setupServer(...handlers)

// re-export so tests import http/HttpResponse from one place
export { http, HttpResponse }
