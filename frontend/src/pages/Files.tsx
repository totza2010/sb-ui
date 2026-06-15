/**
 * Files — browse the server filesystem (/mnt mounts + /opt config) and edit
 * text config files under writable roots (/opt, /srv, /home). Useful for
 * verifying media folder structure (before/after a move) and tweaking app
 * configs like /opt/autoscan/config.yml.
 */
import { useEffect, useState } from 'react'
import { parse as yamlParse, stringify as yamlStringify } from 'yaml'
import { useFsList, useFsFile, useSaveFsFile } from '@/lib/api'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { YamlNode } from '@/components/YamlForm'
import {
  Folder, File as FileIcon, ChevronRight, Loader2, Save, Check, RefreshCw, Lock,
} from 'lucide-react'
import { cn } from '@/lib/cn'

function safeStringify(obj: unknown, fallback: string): string {
  try { return yamlStringify(obj) } catch { return fallback }
}

const ROOTS = [
  { path: '/mnt/unionfs/Media', label: 'Media (union)' },
  { path: '/mnt/unionfs', label: '/mnt/unionfs' },
  { path: '/mnt/local', label: '/mnt/local' },
  { path: '/mnt/remote', label: '/mnt/remote' },
  { path: '/opt', label: '/opt (config)' },
]

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(0)} KB`
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`
  return `${(n / 1024 ** 3).toFixed(1)} GB`
}

export function Files() {
  const [path, setPath] = useState('/mnt/unionfs/Media')
  const [file, setFile] = useState<string | null>(null)
  const { data, isLoading, isFetching, refetch } = useFsList(path)
  const segs = path.replace(/^\/+/, '').split('/')

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-xl font-semibold text-foreground">Files</h1>
        <p className="text-sm text-muted-foreground mt-0.5">
          Browse mounts &amp; config. Edit text files under /opt, /srv, /home.
        </p>
      </div>

      <div className="flex gap-1.5 flex-wrap">
        {ROOTS.map(r => (
          <Button key={r.path} size="sm"
            variant={path === r.path || path.startsWith(r.path + '/') ? 'default' : 'outline'}
            onClick={() => { setPath(r.path); setFile(null) }}>
            {r.label}
          </Button>
        ))}
      </div>

      <div className="grid lg:grid-cols-2 gap-4">
        {/* Browser */}
        <Card className="flex flex-col min-h-0">
          <div className="flex items-center gap-1 px-3 py-2 border-b border-border text-xs flex-wrap">
            <button className="font-mono text-muted-foreground hover:text-foreground" onClick={() => setPath('/' + segs[0])}>
              /{segs[0]}
            </button>
            {segs.slice(1).map((s, i) => (
              <span key={i} className="flex items-center gap-1">
                <ChevronRight className="h-3 w-3 text-muted-foreground/50" />
                <button className="font-mono text-muted-foreground hover:text-foreground"
                  onClick={() => setPath('/' + segs.slice(0, i + 2).join('/'))}>{s}</button>
              </span>
            ))}
            <button className="ml-auto text-muted-foreground hover:text-foreground" title="Refresh" onClick={() => refetch()}>
              <RefreshCw className={cn('h-3.5 w-3.5', isFetching && 'animate-spin')} />
            </button>
          </div>
          <div className="overflow-auto max-h-[65vh]">
            {isLoading ? (
              <div className="flex justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>
            ) : !data?.exists ? (
              <div className="text-sm text-muted-foreground py-12 text-center">Not accessible: {path}</div>
            ) : data.entries.length === 0 ? (
              <div className="text-sm text-muted-foreground py-12 text-center">Empty folder.</div>
            ) : data.entries.map(e => (
              <div key={e.name}
                className={cn('flex items-center justify-between gap-2 px-3 py-1.5 text-sm border-b border-border/40 last:border-0 cursor-pointer hover:bg-muted/40',
                  file === `${path}/${e.name}` && 'bg-primary/10')}
                onClick={() => e.type === 'dir' ? (setPath(`${path}/${e.name}`), setFile(null)) : setFile(`${path}/${e.name}`)}>
                <div className="flex items-center gap-2 min-w-0">
                  {e.type === 'dir' ? <Folder className="h-4 w-4 text-blue-400 shrink-0" /> : <FileIcon className="h-4 w-4 text-muted-foreground shrink-0" />}
                  <span className="font-mono truncate">{e.name}</span>
                </div>
                <span className="text-xs text-muted-foreground shrink-0">{e.type === 'file' ? fmtSize(e.size) : ''}</span>
              </div>
            ))}
          </div>
        </Card>

        {/* Editor / viewer */}
        <Card className="flex flex-col min-h-0">
          {file ? <FileEditor key={file} path={file} /> : (
            <div className="flex items-center justify-center h-full min-h-48 text-sm text-muted-foreground p-6 text-center">
              Select a file to view. Files under /opt, /srv, /home are editable.
            </div>
          )}
        </Card>
      </div>
    </div>
  )
}

function FileEditor({ path }: { path: string }) {
  const { data, isLoading } = useFsFile(path)
  const save = useSaveFsFile()
  const isYaml = /\.ya?ml$/i.test(path)
  const [draft, setDraft] = useState('')
  const [obj, setObj] = useState<unknown>(null)
  const [mode, setMode] = useState<'form' | 'raw'>('raw')
  const [saved, setSaved] = useState(false)
  const writable = data?.writable ?? false

  useEffect(() => {
    if (!data) return
    setDraft(data.content)
    if (isYaml) {
      try { setObj(yamlParse(data.content)); setMode('form') }
      catch { setObj(null); setMode('raw') }
    }
  }, [data, isYaml])

  const serialized = mode === 'form' && obj !== null ? safeStringify(obj, draft) : draft
  const dirty = data ? serialized !== data.content : false

  function switchMode(next: 'form' | 'raw') {
    if (next === 'form') {
      try { setObj(yamlParse(draft)); setMode('form') } catch { /* keep raw */ }
    } else {
      if (obj !== null) setDraft(safeStringify(obj, draft))
      setMode('raw')
    }
  }

  async function handleSave() {
    await save.mutateAsync({ path, content: serialized })
    if (mode === 'form') setDraft(serialized)
    setSaved(true); setTimeout(() => setSaved(false), 2500)
  }

  return (
    <>
      <div className="flex items-center justify-between gap-2 px-3 py-2 border-b border-border">
        <span className="text-xs font-mono text-muted-foreground truncate">{path}</span>
        <div className="flex items-center gap-2 shrink-0">
          {isYaml && obj !== null && (
            <div className="flex gap-0.5 bg-muted/50 rounded p-0.5">
              {(['form', 'raw'] as const).map(m => (
                <button key={m} onClick={() => switchMode(m)}
                  className={cn('px-2 py-0.5 rounded text-[11px] capitalize',
                    mode === m ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground')}>{m}</button>
              ))}
            </div>
          )}
          {saved && <span className="text-xs text-green-500 flex items-center gap-1"><Check className="h-3.5 w-3.5" />Saved</span>}
          {writable ? (
            <Button size="sm" className="h-6 text-xs gap-1" onClick={handleSave} disabled={!dirty || save.isPending}>
              {save.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : <Save className="h-3 w-3" />}Save
            </Button>
          ) : (
            <span className="text-xs text-muted-foreground flex items-center gap-1"><Lock className="h-3 w-3" />read-only</span>
          )}
        </div>
      </div>
      {isLoading ? (
        <div className="flex justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>
      ) : mode === 'form' && obj !== null ? (
        <div className="flex-1 min-h-[60vh] overflow-auto p-3">
          <YamlNode value={obj} onChange={writable ? setObj : () => {}} />
        </div>
      ) : (
        <textarea
          className="flex-1 min-h-[60vh] font-mono text-xs p-3 resize-none bg-transparent text-foreground outline-none"
          value={draft} onChange={e => setDraft(e.target.value)}
          readOnly={!writable} spellCheck={false}
        />
      )}
    </>
  )
}
