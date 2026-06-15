import { useEffect, useRef, useState } from 'react'

export type JobStatus = 'pending' | 'running' | 'completed' | 'failed'

export function useJobSocket(jobId: string | null) {
  const [lines, setLines] = useState<string[]>([])
  const [status, setStatus] = useState<JobStatus>('pending')
  const wsRef = useRef<WebSocket | null>(null)

  useEffect(() => {
    if (!jobId) return
    setLines([])
    setStatus('running')

    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${location.host}/ws/jobs/${jobId}`)
    wsRef.current = ws

    // Track whether this effect instance is still the active one.
    // React StrictMode mounts → unmounts → remounts: the first ws gets
    // closed during cleanup and its async onerror fires AFTER the second
    // mount has already set status='running', overwriting it with 'failed'.
    // The `active` flag prevents stale callbacks from touching state.
    let active = true

    ws.onmessage = (e) => {
      if (!active) return
      try {
        const msg = JSON.parse(e.data)
        if (msg.type === 'log') {
          setLines((prev) => [...prev, msg.line])
        } else if (msg.type === 'status') {
          setStatus(msg.status)
        }
      } catch {
        // ignore malformed frames
      }
    }

    ws.onerror = () => {
      if (!active) return
      setStatus('failed')
    }

    ws.onclose = (e) => {
      if (!active) return
      // Only set failed on unexpected close (not our own cleanup close).
      // Code 1000 = normal closure (server finished streaming).
      if (e.code !== 1000) {
        setStatus((prev) => (prev === 'completed' ? 'completed' : 'failed'))
      }
    }

    return () => {
      active = false
      ws.close()
    }
  }, [jobId])

  return { lines, status }
}
