import { defineConfig, createLogger } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

// Vite logs http-proxy WS teardown errors (ECONNABORTED / ECONNRESET / EPIPE)
// via its own internal handler — a `configure` proxy.on('error') can't stop it.
// Filter them out at the logger level. These are dev-proxy noise only:
// production serves WS directly from the backend without a proxy.
const logger = createLogger()
const origError = logger.error
// While `air` rebuilds the backend (~5s) it's briefly down, so the proxy throws
// ECONNREFUSED — transient dev noise, not a real failure. Swallow it so the dev
// window doesn't look broken. Production serves /api + /ws directly (no proxy).
const NOISE = [
  'ws proxy error', 'ws proxy socket error', 'http proxy error',
  'ECONNABORTED', 'ECONNRESET', 'ECONNREFUSED', 'EPIPE',
]
logger.error = (msg, options) => {
  if (typeof msg === 'string' && NOISE.some((n) => msg.includes(n))) return
  origError(msg, options)
}

// When the backend is mid-rebuild, return a quick 503 instead of letting the
// proxy error bubble up — the frontend's react-query just retries and recovers.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const onProxyError = (proxy: any) => {
  proxy.on('error', (_err: unknown, _req: unknown, res: any) => {
    if (res && typeof res.writeHead === 'function' && !res.headersSent) {
      res.writeHead(503, { 'Content-Type': 'application/json' })
      res.end('{"error":"backend restarting"}')
    }
  })
}

export default defineConfig({
  customLogger: logger,
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  server: {
    proxy: {
      '/api': { target: 'http://localhost:9180', changeOrigin: true, configure: onProxyError },
      '/ws': { target: 'ws://localhost:9180', ws: true, changeOrigin: true },
    },
  },
})
