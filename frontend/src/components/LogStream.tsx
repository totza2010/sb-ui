import { useEffect, useRef } from 'react'
import { useJobSocket, type JobStatus } from '@/hooks/useJobSocket'
import { Badge } from '@/components/ui/badge'
import { Loader2 } from 'lucide-react'

interface Props {
  jobId: string | null
}

const statusVariant: Record<JobStatus, 'default' | 'success' | 'destructive' | 'secondary'> = {
  pending: 'secondary',
  running: 'default',
  completed: 'success',
  failed: 'destructive',
  stopped: 'secondary',
}

export function LogStream({ jobId }: Props) {
  const { lines, status } = useJobSocket(jobId)
  const boxRef = useRef<HTMLDivElement>(null)
  const stick = useRef(true)

  // Auto-scroll only when the user is already at the bottom, so they can scroll
  // up to read without being yanked back down on every new line.
  const onScroll = () => {
    const el = boxRef.current
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
  }
  useEffect(() => {
    const el = boxRef.current
    if (el && stick.current) el.scrollTop = el.scrollHeight
  }, [lines])

  if (!jobId) return null

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <span className="text-sm text-muted-foreground">Status:</span>
        <Badge variant={statusVariant[status]}>
          {status === 'running' && <Loader2 className="h-3 w-3 mr-1 animate-spin" />}
          {status}
        </Badge>
      </div>
      <div ref={boxRef} onScroll={onScroll} className="bg-background border border-border rounded-md p-3 h-96 overflow-y-auto font-mono text-xs leading-5">
        {lines.length === 0 ? (
          <span className="text-muted-foreground">Waiting for output...</span>
        ) : (
          lines.map((line, i) => (
            <div key={i}>{renderLine(line)}</div>
          ))
        )}
      </div>
    </div>
  )
}

// Ansible forces colour even without a TTY, so log lines carry raw ANSI SGR
// codes (\x1b[0;32m …). Render them as coloured spans; fall back to semantic
// colouring for our own (non-ANSI) log lines.
const SGR_RE = /\x1b\[([0-9;]*)m/g
const ANSI_OTHER = /\x1b\[[0-9;]*[A-HJKSTfminsu]|\x1b[()][0-9A-Za-z]|\x1b./g
const COLOR_MAP: Record<string, string> = {
  '30': 'text-muted-foreground', '31': 'text-red-400', '32': 'text-green-400',
  '33': 'text-yellow-400', '34': 'text-blue-400', '35': 'text-purple-400',
  '36': 'text-cyan-400', '37': 'text-foreground',
  '90': 'text-muted-foreground', '91': 'text-red-400', '92': 'text-green-400',
  '93': 'text-yellow-400', '94': 'text-blue-400', '95': 'text-purple-400',
  '96': 'text-cyan-400', '97': 'text-foreground',
}

function clsFromCodes(spec: string): string {
  let color = '', bold = false
  for (const c of spec.split(';')) {
    if (c === '' || c === '0') { color = ''; bold = false }
    else if (c === '1') bold = true
    else if (COLOR_MAP[c]) color = COLOR_MAP[c]
  }
  return [color, bold ? 'font-semibold' : ''].filter(Boolean).join(' ')
}

const clean = (t: string) => t.replace(ANSI_OTHER, '')

function renderLine(line: string) {
  if (!line) return null
  if (!line.includes('\x1b[')) {
    return <span className={lineColor(line)}>{line}</span>
  }
  const segs: { text: string; cls: string }[] = []
  let last = 0, cls = ''
  SGR_RE.lastIndex = 0
  let m: RegExpExecArray | null
  while ((m = SGR_RE.exec(line))) {
    if (m.index > last) segs.push({ text: clean(line.slice(last, m.index)), cls })
    cls = clsFromCodes(m[1])
    last = SGR_RE.lastIndex
  }
  if (last < line.length) segs.push({ text: clean(line.slice(last)), cls })
  return segs.map((s, j) => <span key={j} className={s.cls}>{s.text}</span>)
}

function lineColor(line: string): string {
  if (line.includes('TASK [') || line.includes('PLAY [')) return 'text-primary font-semibold'
  if (line.includes('ok:') || line.includes('changed:')) return 'text-success'
  if (line.includes('failed:') || line.includes('FAILED')) return 'text-destructive'
  if (line.includes('PLAY RECAP')) return 'text-warning font-semibold'
  return 'text-foreground'
}
