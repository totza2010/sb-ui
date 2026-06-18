/**
 * tgDrive — enhancements for teldrive remotes. Federated search across every
 * teldrive remote at once (their own UI is single-instance and hides the folder
 * path); each hit shows which remote + folder it's in and jumps you there.
 * The page is only reachable when teldrive remotes are configured.
 */
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTeldriveRemotes, useTeldriveSearch } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Search, FolderOpen, Send, Loader2 } from 'lucide-react'

export function TgDrive() {
  const { data: rd } = useTeldriveRemotes()
  const remotes = rd?.remotes ?? []
  const [input, setInput] = useState('')
  const [q, setQ] = useState('')
  const { data, isFetching } = useTeldriveSearch(q)
  const nav = useNavigate()
  const results = data?.results ?? []

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-xl font-semibold text-foreground flex items-center gap-2"><Send className="h-5 w-5" />tgDrive</h1>
        <p className="text-sm text-muted-foreground mt-0.5">
          Search across all your teldrive remotes at once — results show the remote and folder, and jump straight there.
        </p>
      </div>

      {remotes.length === 0 ? (
        <div className="rounded-lg border border-border bg-card px-4 py-10 text-center text-sm text-muted-foreground">
          No teldrive remotes configured. Add a <span className="font-mono">type = teldrive</span> remote to rclone to use this page.
        </div>
      ) : (
        <>
          <form onSubmit={(e) => { e.preventDefault(); setQ(input.trim()) }} className="flex gap-2">
            <div className="relative flex-1">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              <Input className="h-9 pl-8" value={input} onChange={(e) => setInput(e.target.value)} placeholder={`Search ${remotes.length} teldrive remote(s)…`} />
            </div>
            <Button type="submit" className="gap-1.5" disabled={!input.trim()}>
              {isFetching ? <Loader2 className="h-4 w-4 animate-spin" /> : <Search className="h-4 w-4" />}Search
            </Button>
          </form>
          <p className="text-[11px] text-muted-foreground">Remotes: {remotes.join(' · ')}</p>

          {(data?.errors?.length ?? 0) > 0 && (
            <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">
              <p className="font-medium">Some remotes couldn’t be searched:</p>
              {data!.errors!.map((e, i) => <p key={i} className="font-mono break-all">{e}</p>)}
            </div>
          )}

          {q && (
            <div className="rounded-lg border border-border divide-y divide-border overflow-hidden">
              {isFetching && results.length === 0 && <div className="px-4 py-8 text-center text-sm text-muted-foreground">Searching…</div>}
              {!isFetching && results.length === 0 && <div className="px-4 py-8 text-center text-sm text-muted-foreground">No matches for “{q}”.</div>}
              {results.map((r, i) => (
                <div key={i} className="flex items-center gap-3 px-4 py-2 hover:bg-muted/40">
                  <span className="rounded bg-secondary px-1.5 py-0.5 text-[10px] font-medium text-foreground shrink-0">{r.remote}</span>
                  <div className="min-w-0 flex-1">
                    <p className="text-sm text-foreground truncate">{r.name}</p>
                    <p className="text-[11px] text-muted-foreground truncate">
                      <span className="font-mono">{r.dir || '/'}</span>
                      {!r.is_dir && <> · {r.human}</>}{r.category && <> · {r.category}</>}
                    </p>
                  </div>
                  <Button size="sm" variant="outline" className="gap-1.5 shrink-0" title="Open containing folder"
                    onClick={() => nav(`/files?remote=${encodeURIComponent(r.remote)}&path=${encodeURIComponent(r.dir || '')}`)}>
                    <FolderOpen className="h-3.5 w-3.5" />Open
                  </Button>
                </div>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  )
}
