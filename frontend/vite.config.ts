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
const NOISE = ['ws proxy error', 'ws proxy socket error', 'ECONNABORTED', 'ECONNRESET', 'EPIPE']
logger.error = (msg, options) => {
  if (typeof msg === 'string' && NOISE.some((n) => msg.includes(n))) return
  origError(msg, options)
}

export default defineConfig({
  customLogger: logger,
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  server: {
    proxy: {
      '/api': { target: 'http://localhost:8000', changeOrigin: true },
      '/ws': { target: 'ws://localhost:8000', ws: true, changeOrigin: true },
    },
  },
})
