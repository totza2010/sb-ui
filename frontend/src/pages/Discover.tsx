/**
 * Discover — browse popular movies/TV from Jellyseerr/Overseerr, open a rich detail
 * view (backdrop, overview, trailer, cast, seasons) and request titles not in the
 * library yet. Seerr reports each title's status (it syncs with the *arr apps + Plex).
 */
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { discoverHomeOpts, discoverSearchOpts, discoverExploreOpts, discoverGenresOpts, discoverCollectionOpts, discoverPersonOpts, discoverLibraryOpts, collectionSearchOpts, personSearchOpts, watchlistOpts, useWatchlistToggle, seerrDetailOpts, useSeerrRequest, requestOptionsOpts, type SeerrItem, type SeerrCompany, type SeerrDetail, type SeerrRequestBody, type DiscoverSection, type DiscoverFilters } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { Loader2, Film, Tv, Check, Plus, Star, Clock, ExternalLink, Search, ChevronLeft, ChevronRight, Bookmark, SlidersHorizontal } from 'lucide-react'

type Kind = 'movies' | 'tv'
const isAvail = (s: number) => s >= 4 // has at least some files
const isComplete = (s: number) => s >= 5
const isPartial = (s: number) => s === 4
const isPending = (s: number) => s >= 2 && s < 4

// StatusBadge renders the small corner pill on cards from an effective status code.
function StatusBadge({ status }: { status: number }) {
  if (isComplete(status)) return <span className="flex items-center gap-0.5 rounded bg-success/90 px-1.5 py-0.5 text-[10px] font-semibold text-white"><Check className="h-2.5 w-2.5" />In library</span>
  if (isPartial(status)) return <span className="flex items-center gap-0.5 rounded bg-sky-500/90 px-1.5 py-0.5 text-[10px] font-semibold text-white"><Check className="h-2.5 w-2.5" />Partial</span>
  if (isPending(status)) return <span className="flex items-center gap-0.5 rounded bg-[#e5a00d]/90 px-1.5 py-0.5 text-[10px] font-semibold text-white"><Clock className="h-2.5 w-2.5" />Requested</span>
  return null
}

const voteColor = (v: number) => (v >= 7 ? 'bg-success/90' : v >= 5 ? 'bg-[#e5a00d]/90' : 'bg-destructive/90')

function PosterCard({ it, requested, inWatch, onToggleWatch, onOpen }: { it: SeerrItem; requested: boolean; inWatch?: boolean; onToggleWatch?: () => void; onOpen: () => void }) {
  const st = requested && it.status < 2 ? 3 : it.status
  const titleIcon = isComplete(st) ? 'text-success' : isPartial(st) ? 'text-sky-400' : isPending(st) ? 'text-[#e5a00d]' : ''
  return (
    <div onClick={onOpen}
      className="group relative cursor-pointer overflow-hidden rounded-lg border border-border bg-muted shadow-sm transition-all duration-200 hover:-translate-y-1 hover:shadow-xl"
      style={{ contentVisibility: 'auto', containIntrinsicSize: '210px' }}>
      <div className="relative aspect-[2/3]">
        {it.poster ? (
          <img src={it.poster} alt={it.title} loading="lazy" decoding="async" className="h-full w-full object-cover" />
        ) : (
          <div className="flex h-full w-full items-center justify-center text-muted-foreground">{it.media_type === 'tv' ? <Tv className="h-8 w-8" /> : <Film className="h-8 w-8" />}</div>
        )}
        <div className="absolute left-1.5 top-1.5 flex flex-col items-start gap-1">
          {it.vote > 0 && (
            <span className={`flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-semibold text-white ${voteColor(it.vote)}`}>
              <Star className="h-2.5 w-2.5 fill-current" />{it.vote.toFixed(1)}
            </span>
          )}
          <StatusBadge status={st} />
        </div>
        {onToggleWatch && (
          <button title="Watchlist" onClick={(e) => { e.stopPropagation(); onToggleWatch() }}
            className={`absolute right-1.5 top-1.5 rounded-full p-1 transition-opacity ${inWatch ? 'bg-[#e5a00d] text-white' : 'bg-black/55 text-white opacity-0 group-hover:opacity-100'}`}>
            <Bookmark className="h-3 w-3" fill={inWatch ? 'currentColor' : 'none'} />
          </button>
        )}
        <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/95 via-black/50 to-transparent p-2 pt-8">
          <p className="flex items-center gap-1 text-xs font-semibold text-white" title={it.title}>
            {titleIcon && (isPending(st) ? <Clock className={`h-3 w-3 shrink-0 ${titleIcon}`} /> : <Check className={`h-3 w-3 shrink-0 ${titleIcon}`} />)}
            <span className="truncate">{it.title}</span>
          </p>
          <p className="text-[10px] text-white/65">{it.year || '—'} · {it.media_type === 'tv' ? 'TV' : 'Movie'}</p>
        </div>
      </div>
    </div>
  )
}

function Fact({ label, value }: { label: string; value: ReactNode }) {
  if (!value) return null
  return (
    <div className="flex items-center justify-between gap-3 border-b border-border/50 py-1.5 last:border-0">
      <span className="text-[11px] text-muted-foreground">{label}</span>
      <span className="text-right text-[11px] font-medium text-foreground">{value}</span>
    </div>
  )
}

function DetailRow({ label, value }: { label: string; value: ReactNode }) {
  if (!value) return null
  return (
    <div className="grid grid-cols-[110px_1fr] gap-2 py-1.5 text-xs">
      <span className="text-muted-foreground">{label}</span>
      <span className="text-foreground">{value}</span>
    </div>
  )
}

function EpisodeBox({ label, ep }: { label: string; ep: { code: string; name: string; date: string } }) {
  return (
    <div className="rounded-md border border-border px-3 py-2">
      <p className="text-[10px] text-muted-foreground">{label}</p>
      <p className="text-xs font-medium text-foreground">{ep.code}{ep.name ? ` — ${ep.name}` : ''}</p>
      {ep.date && <p className="text-[10px] text-muted-foreground">{ep.date}</p>}
    </div>
  )
}

function CompanyList({ items }: { items?: SeerrCompany[] }) {
  if (!items || !items.length) return null
  return (
    <span className="flex flex-wrap items-center gap-2">
      {items.map((c) => c.logo
        ? <span key={c.name} className="rounded bg-white/90 px-1 py-0.5" title={c.name}><img src={c.logo} alt={c.name} className="h-4 max-w-[70px] object-contain" /></span>
        : <span key={c.name} className="rounded bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">{c.name}</span>)}
    </span>
  )
}

// RequestDialog mirrors Seerr's "Request" modal: choose destination *arr server,
// quality profile, root folder, and (for series) which seasons — then submit.
function RequestDialog({ d, onClose, onRequested }: { d: SeerrDetail; onClose: () => void; onRequested: () => void }) {
  const { data, isLoading, error } = useQuery(requestOptionsOpts(d.media_type))
  const req = useSeerrRequest()
  const isTv = d.media_type === 'tv'
  const servers = data?.servers ?? []
  const users = data?.users ?? []
  const [serverId, setServerId] = useState<number | null>(null)
  const server = servers.find((s) => s.id === serverId) ?? null
  const [profileId, setProfileId] = useState(0)
  const [root, setRoot] = useState('')
  const [langId, setLangId] = useState(0)
  const [userId, setUserId] = useState(0)
  const seasonList = d.season_list ?? []
  const incomplete = seasonList.filter((s) => !isComplete(s.status)).map((s) => s.number)
  const [seasons, setSeasons] = useState<Set<number>>(() => new Set(incomplete)) // default: all not-yet-complete seasons

  useEffect(() => { // default server once loaded
    if (serverId !== null || servers.length === 0) return
    setServerId((servers.find((s) => s.is_default) ?? servers[0]).id)
  }, [servers, serverId])
  useEffect(() => { // defaults follow the selected server
    const s = servers.find((x) => x.id === serverId)
    if (!s) return
    setProfileId(s.default_profile_id || s.profiles[0]?.id || 0)
    setRoot(s.default_root || s.root_folders[0]?.path || '')
    setLangId(s.default_lang_profile_id || s.lang_profiles[0]?.id || 0)
  }, [serverId, servers])

  const toggleSeason = (n: number) => setSeasons((s) => { const x = new Set(s); x.has(n) ? x.delete(n) : x.add(n); return x })
  const canSubmit = !isTv || seasonList.length === 0 || seasons.size > 0
  const submit = () => {
    const body: SeerrRequestBody = { media_type: d.media_type, tmdb_id: d.tmdb_id }
    if (server) { body.server_id = server.id; body.is4k = server.is4k || undefined }
    if (profileId) body.profile_id = profileId
    if (root) body.root_folder = root
    if (isTv && langId) body.language_profile_id = langId
    if (userId) body.user_id = userId
    if (isTv && seasons.size > 0) body.seasons = [...seasons].sort((a, b) => a - b)
    req.mutate(body, { onSuccess: () => { onRequested(); onClose() } })
  }
  const fld = 'h-9 w-full rounded-md border border-border bg-card px-2 text-sm outline-none focus:border-primary'
  const Field = ({ label, children }: { label: string; children: ReactNode }) => (
    <label className="block space-y-1"><span className="text-xs font-medium text-muted-foreground">{label}</span>{children}</label>
  )

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <h2 className="text-base font-semibold text-foreground">Request {isTv ? 'Series' : 'Movie'}</h2>
        <p className="-mt-1 text-sm text-muted-foreground">{d.title}{d.year ? ` (${d.year})` : ''}</p>

        {isLoading ? (
          <div className="flex justify-center py-6"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>
        ) : (
          <div className="space-y-3">
            {error && <p className="break-all rounded-md bg-destructive/10 p-2 text-[11px] text-destructive">Couldn’t load *arr options: {(error as Error).message}. Requesting will use Seerr’s defaults.</p>}
            {isTv && seasonList.length > 0 && (
              <div className="space-y-1">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-medium text-muted-foreground">Seasons ({seasons.size} selected)</span>
                  <div className="flex gap-2">
                    <button onClick={() => setSeasons(new Set(incomplete))} className="text-[11px] text-primary hover:underline">Select missing</button>
                    {seasons.size > 0 && <button onClick={() => setSeasons(new Set())} className="text-[11px] text-muted-foreground hover:underline">Clear</button>}
                  </div>
                </div>
                <div className="max-h-44 space-y-0.5 overflow-y-auto rounded-md border border-border p-1.5">
                  {seasonList.map((s) => {
                    const have = isComplete(s.status)
                    return (
                      <label key={s.number} className={`flex items-center gap-2 rounded px-1.5 py-1 text-sm ${have ? 'opacity-60' : 'cursor-pointer hover:bg-accent'}`}>
                        <input type="checkbox" disabled={have} checked={have || seasons.has(s.number)} onChange={() => !have && toggleSeason(s.number)} className="accent-primary" />
                        <span className="text-foreground">{s.name || `Season ${s.number}`}</span>
                        <span className="text-[11px] text-muted-foreground">{s.episodes} ep</span>
                        <span className="ml-auto text-[10px] font-medium">
                          {have ? <span className="text-success">Available</span> : isPartial(s.status) ? <span className="text-sky-500">Partial</span> : <span className="text-muted-foreground">Not in library</span>}
                        </span>
                      </label>
                    )
                  })}
                </div>
              </div>
            )}

            {/* Advanced — Seerr-style destination options */}
            {servers.length > 0 && (
              <div className="space-y-3 border-t border-border pt-3">
                <p className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Advanced</p>
                <Field label="Destination server">
                  <select className={fld} value={serverId ?? ''} onChange={(e) => setServerId(Number(e.target.value))}>
                    {servers.map((s) => <option key={s.id} value={s.id}>{s.name}{s.is_default ? ' (default)' : ''}{s.is4k ? ' · 4K' : ''}</option>)}
                  </select>
                </Field>
                {server && server.profiles.length > 0 && (
                  <Field label="Quality profile">
                    <select className={fld} value={profileId} onChange={(e) => setProfileId(Number(e.target.value))}>
                      {server.profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
                    </select>
                  </Field>
                )}
                {server && server.root_folders.length > 0 && (
                  <Field label="Root folder">
                    <select className={fld} value={root} onChange={(e) => setRoot(e.target.value)}>
                      {server.root_folders.map((f) => <option key={f.id} value={f.path}>{f.path}</option>)}
                    </select>
                  </Field>
                )}
                {isTv && server && server.lang_profiles.length > 0 && (
                  <Field label="Language profile">
                    <select className={fld} value={langId} onChange={(e) => setLangId(Number(e.target.value))}>
                      {server.lang_profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
                    </select>
                  </Field>
                )}
                {users.length > 1 && (
                  <Field label="Request as">
                    <select className={fld} value={userId} onChange={(e) => setUserId(Number(e.target.value))}>
                      <option value={0}>Default (me)</option>
                      {users.map((u) => <option key={u.id} value={u.id}>{u.name}{u.email ? ` (${u.email})` : ''}</option>)}
                    </select>
                  </Field>
                )}
              </div>
            )}

            {req.isError && <p className="break-all text-[11px] text-destructive">{req.error.message}</p>}
            <div className="flex justify-end gap-2 pt-1">
              <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
              <Button size="sm" className="gap-1.5" disabled={req.isPending || !canSubmit} onClick={submit}>
                {req.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}Request
              </Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

function DetailModal({ sel, onClose, requested, onRequested, inWatch, onToggleWatch }: { sel: { type: 'movie' | 'tv'; id: number }; onClose: () => void; requested: boolean; onRequested: () => void; inWatch: boolean; onToggleWatch: (it: SeerrItem) => void }) {
  const { data: d, isLoading, isError, error } = useQuery(seerrDetailOpts(sel.type, sel.id))
  const [vid, setVid] = useState('')
  const [showReq, setShowReq] = useState(false)
  const st = d?.status ?? 0
  const complete = isComplete(st)
  const partial = isPartial(st)
  const reqd = requested || isPending(st)
  const list = (a?: string[]) => (a && a.length ? a.join(', ') : '')
  const curVideo = vid || d?.trailer || ''

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="w-[94vw] max-w-5xl max-h-[90vh] overflow-y-auto overflow-x-hidden p-0">
        {isLoading && <div className="flex justify-center p-10"><Loader2 className="h-6 w-6 animate-spin text-muted-foreground" /></div>}
        {isError && <p className="p-6 text-sm text-destructive">{(error as Error)?.message}</p>}
        {d && (
          <>
            {/* backdrop header */}
            <div className="relative">
              {d.backdrop && <img src={d.backdrop} alt="" className="h-44 w-full object-cover sm:h-56" />}
              <div className="absolute inset-0 bg-gradient-to-t from-card via-card/70 to-card/10" />
              <div className="absolute inset-x-0 bottom-0 space-y-1 p-4">
                <span className="rounded bg-primary/80 px-1.5 py-0.5 text-[10px] font-semibold text-primary-foreground">{d.media_type === 'tv' ? 'TV Show' : 'Movie'}</span>
                <h2 className="text-xl font-bold text-foreground">{d.title}</h2>
                {d.tagline && <p className="text-xs italic text-muted-foreground">« {d.tagline} »</p>}
                <div className="flex flex-wrap items-center gap-x-2 text-[11px] text-muted-foreground">
                  <span>{d.year || '—'}</span>
                  <span>· {d.media_type === 'tv' ? `${d.seasons ?? 0} seasons · ${d.episodes ?? 0} episodes` : `${d.runtime ?? 0} min`}</span>
                  {d.status_text && <span>· {d.status_text}</span>}
                  {d.vote > 0 && <span className="flex items-center gap-0.5 rounded bg-success/15 px-1.5 py-0.5 font-medium text-success"><Star className="h-2.5 w-2.5 fill-current" />{d.vote.toFixed(1)}</span>}
                </div>
                <div className="flex flex-wrap gap-1">
                  {d.genres.map((g) => <span key={g} className="rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">{g}</span>)}
                </div>
              </div>
            </div>

            {/* body */}
            <div className="grid grid-cols-1 gap-5 p-4 md:grid-cols-[210px_minmax(0,1fr)]">
              {/* LEFT sidebar */}
              <div className="space-y-3">
                {d.poster && <img src={d.poster} alt={d.title} className="w-full rounded-lg border border-border" />}
                {complete ? (
                  <div className="flex items-center justify-center gap-1 rounded-md bg-success/15 py-2 text-xs font-medium text-success"><Check className="h-3.5 w-3.5" />In library</div>
                ) : partial ? (
                  <div className="space-y-2">
                    <div className="flex items-center justify-center gap-1 rounded-md bg-sky-500/15 py-2 text-xs font-medium text-sky-500"><Check className="h-3.5 w-3.5" />Partially available</div>
                    <Button className="w-full gap-1.5" onClick={() => setShowReq(true)}><Plus className="h-4 w-4" />Request more</Button>
                  </div>
                ) : reqd ? (
                  <div className="flex items-center justify-center gap-1 rounded-md bg-[#e5a00d]/15 py-2 text-xs font-medium text-[#e5a00d]"><Clock className="h-3.5 w-3.5" />Requested</div>
                ) : (
                  <Button className="w-full gap-1.5" onClick={() => setShowReq(true)}>
                    <Plus className="h-4 w-4" />Request
                  </Button>
                )}
                {showReq && <RequestDialog d={d} onClose={() => setShowReq(false)} onRequested={onRequested} />}

                <Button variant="outline" className={`w-full gap-1.5 ${inWatch ? 'text-[#e5a00d]' : ''}`}
                  onClick={() => onToggleWatch({ media_type: d.media_type, tmdb_id: d.tmdb_id, title: d.title, year: d.year, poster: d.poster, overview: '', vote: d.vote, status: d.status })}>
                  <Bookmark className="h-4 w-4" fill={inWatch ? 'currentColor' : 'none'} />{inWatch ? 'In watchlist' : 'Watchlist'}
                </Button>

                <div className="flex flex-wrap gap-1.5">
                  <a href={`https://www.themoviedb.org/${d.media_type === 'tv' ? 'tv' : 'movie'}/${d.tmdb_id}`} target="_blank" rel="noreferrer"
                    className="flex items-center gap-1 rounded border border-border px-2 py-1 text-[10px] text-muted-foreground hover:text-foreground">TMDb<ExternalLink className="h-2.5 w-2.5" /></a>
                  {d.imdb_id && (
                    <a href={`https://www.imdb.com/title/${d.imdb_id}`} target="_blank" rel="noreferrer"
                      className="flex items-center gap-1 rounded border border-border px-2 py-1 text-[10px] text-muted-foreground hover:text-foreground">IMDb<ExternalLink className="h-2.5 w-2.5" /></a>
                  )}
                  {d.homepage && (
                    <a href={d.homepage} target="_blank" rel="noreferrer"
                      className="flex items-center gap-1 rounded border border-border px-2 py-1 text-[10px] text-muted-foreground hover:text-foreground">Site<ExternalLink className="h-2.5 w-2.5" /></a>
                  )}
                </div>

                <div className="rounded-md border border-border p-2">
                  <p className="mb-1 text-[11px] font-semibold text-foreground">Quick facts</p>
                  <Fact label="Release" value={d.release_date} />
                  <Fact label="Status" value={d.status_text} />
                  <Fact label="Rating" value={d.rating} />
                  <Fact label="Original language" value={d.language?.toUpperCase()} />
                  <Fact label="Country" value={d.country} />
                  <Fact label="TMDb votes" value={d.vote_count ? d.vote_count.toLocaleString() : ''} />
                  <Fact label="Popularity" value={d.popularity ? Math.round(d.popularity).toLocaleString() : ''} />
                </div>

                {d.next_episode && <EpisodeBox label="Next episode" ep={d.next_episode} />}
                {d.last_episode && <EpisodeBox label="Last episode" ep={d.last_episode} />}

                {(d.watch_flatrate?.length || d.watch_buy?.length) ? (
                  <div className="rounded-md border border-border p-2">
                    <p className="mb-1 text-[11px] font-semibold text-foreground">Available on</p>
                    {d.watch_flatrate?.length ? (
                      <div className="mb-1.5">
                        <p className="mb-1 text-[9px] uppercase tracking-wide text-muted-foreground">Stream</p>
                        <div className="flex flex-wrap gap-1">
                          {d.watch_flatrate.map((p) => <img key={p.name} src={p.logo} alt={p.name} title={p.name} className="h-7 w-7 rounded object-contain" />)}
                        </div>
                      </div>
                    ) : null}
                    {d.watch_buy?.length ? (
                      <div>
                        <p className="mb-1 text-[9px] uppercase tracking-wide text-muted-foreground">Buy / Rent</p>
                        <div className="flex flex-wrap gap-1">
                          {d.watch_buy.map((p) => <img key={p.name} src={p.logo} alt={p.name} title={p.name} className="h-7 w-7 rounded object-contain" />)}
                        </div>
                      </div>
                    ) : null}
                  </div>
                ) : null}
              </div>

              {/* RIGHT main */}
              <div className="min-w-0 space-y-5">
                {d.overview && (
                  <div>
                    <h3 className="mb-1 text-sm font-semibold text-foreground">Overview</h3>
                    <p className="text-sm leading-relaxed text-muted-foreground">{d.overview}</p>
                  </div>
                )}
                {curVideo && (
                  <div className="space-y-1.5">
                    <div className="aspect-video overflow-hidden rounded-lg border border-border">
                      <iframe key={curVideo} className="h-full w-full" src={`https://www.youtube.com/embed/${curVideo}`} title="Trailer" allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture" allowFullScreen />
                    </div>
                    {d.videos && d.videos.length > 1 && (
                      <div className="flex flex-wrap gap-1.5">
                        {d.videos.slice(0, 6).map((v) => (
                          <button key={v.key} onClick={() => setVid(v.key)}
                            className={`rounded px-2 py-0.5 text-[10px] font-medium ${curVideo === v.key ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground hover:text-foreground'}`}>
                            {v.type || 'Video'}
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                )}
                {d.cast.length > 0 && (
                  <div>
                    <h3 className="mb-2 text-sm font-semibold text-foreground">Main cast</h3>
                    <div className="flex gap-3 overflow-x-auto pb-1">
                      {d.cast.map((c, i) => (
                        <div key={`${c.name}-${i}`} className="w-16 shrink-0 text-center">
                          <div className="mx-auto h-16 w-16 overflow-hidden rounded-full bg-muted">
                            {c.profile ? <img src={c.profile} alt={c.name} loading="lazy" className="h-full w-full object-cover" /> : null}
                          </div>
                          <p className="mt-1 truncate text-[10px] font-medium text-foreground" title={c.name}>{c.name}</p>
                          <p className="truncate text-[9px] text-muted-foreground" title={c.character}>{c.character}</p>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {(d.creators?.length || d.studios?.length || d.networks?.length || d.country || d.languages || d.tags?.length) ? (
                  <div className="rounded-lg border border-border p-3">
                    <h3 className="mb-1 text-sm font-semibold text-foreground">Details</h3>
                    <DetailRow label={d.media_type === 'tv' ? 'Creator' : 'Director'} value={list(d.creators)} />
                    <DetailRow label="Production" value={<CompanyList items={d.studios} />} />
                    {d.media_type === 'tv' && <DetailRow label="Network" value={<CompanyList items={d.networks} />} />}
                    <DetailRow label="Country" value={d.country} />
                    <DetailRow label="Languages" value={d.languages} />
                    {d.tags?.length ? (
                      <DetailRow label="Tags" value={
                        <span className="flex flex-wrap gap-1">{d.tags.map((t) => <span key={t} className="rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">{t}</span>)}</span>
                      } />
                    ) : null}
                  </div>
                ) : null}

                {d.season_list?.length ? (
                  <div>
                    <h3 className="mb-2 text-sm font-semibold text-foreground">Seasons</h3>
                    <div className="flex gap-2 overflow-x-auto pb-1">
                      {d.season_list.map((s) => (
                        <div key={s.number} className="w-24 shrink-0">
                          <div className="relative aspect-[2/3] overflow-hidden rounded bg-muted">
                            {s.poster ? <img src={s.poster} alt={s.name} loading="lazy" className="h-full w-full object-cover" /> : null}
                            {s.status >= 4 && (
                              <span className={`absolute right-1 top-1 flex items-center gap-0.5 rounded px-1 py-0.5 text-[9px] font-semibold text-white ${isComplete(s.status) ? 'bg-success/90' : 'bg-sky-500/90'}`}>
                                <Check className="h-2.5 w-2.5" />{isComplete(s.status) ? 'Have' : 'Partial'}
                              </span>
                            )}
                          </div>
                          <p className="mt-0.5 truncate text-[10px] font-medium text-foreground" title={s.name}>{s.name || `Season ${s.number}`}</p>
                          <p className="text-[9px] text-muted-foreground">{s.episodes} ep{s.date ? ` · ${s.date.slice(0, 4)}` : ''}</p>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
              </div>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v)
  useEffect(() => { const t = setTimeout(() => setD(v), ms); return () => clearTimeout(t) }, [v, ms])
  return d
}

// useInfiniteScroll fires onLoad when the returned sentinel scrolls into view, but
// only while canLoad is true (more pages + not already fetching). The effect re-arms
// whenever canLoad flips back to true after a page finishes loading.
function useInfiniteScroll(canLoad: boolean, onLoad: () => void) {
  const ref = useRef<HTMLDivElement>(null)
  const onLoadRef = useRef(onLoad)
  onLoadRef.current = onLoad
  useEffect(() => {
    const el = ref.current
    if (!el || !canLoad) return
    const io = new IntersectionObserver((e) => { if (e[0].isIntersecting) onLoadRef.current() }, { rootMargin: '500px' })
    io.observe(el)
    return () => io.disconnect()
  }, [canLoad])
  return ref
}

type OpenFn = (it: SeerrItem) => void

function HeroCard({ it, label, requested, onOpen }: { it?: SeerrItem; label: string; requested: boolean; onOpen: OpenFn }) {
  if (!it) return <div className="min-h-[150px] rounded-lg border border-border bg-card" />
  const st = requested && it.status < 2 ? 3 : it.status
  return (
    <div className="relative min-h-[150px] overflow-hidden rounded-lg border border-border bg-card">
      {it.backdrop && <img src={it.backdrop} alt="" className="absolute inset-0 h-full w-full object-cover" />}
      <div className="absolute inset-0 bg-gradient-to-r from-card via-card/85 to-card/30" />
      <div className="relative space-y-1.5 p-4">
        <p className="text-[10px] font-bold uppercase tracking-wide text-primary">{label}</p>
        <h2 className="text-lg font-bold text-foreground">{it.title}</h2>
        <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
          <span>{it.year}</span>
          {it.vote > 0 && <span className="flex items-center gap-0.5 font-medium text-foreground"><Star className="h-2.5 w-2.5 fill-[#e5a00d] text-[#e5a00d]" />{it.vote.toFixed(1)}</span>}
          <StatusBadge status={st} />
        </div>
        <p className="line-clamp-2 max-w-xl text-xs text-muted-foreground">{it.overview}</p>
        <Button size="sm" variant="outline" className="mt-1 h-7 gap-1" onClick={() => onOpen(it)}><Plus className="h-3.5 w-3.5" />Details</Button>
      </div>
    </div>
  )
}

function Carousel({ section, renderItem }: { section: DiscoverSection; renderItem: (it: SeerrItem) => ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  if (!section.items.length) return null
  const scroll = (dir: number) => ref.current?.scrollBy({ left: dir * 560, behavior: 'smooth' })
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between">
        <h2 className="text-sm font-semibold text-foreground">{section.title} <span className="text-xs font-normal text-muted-foreground">· {section.items.length}</span></h2>
        <div className="flex gap-1">
          <button onClick={() => scroll(-1)} className="rounded-full border border-border p-1 text-muted-foreground hover:text-foreground"><ChevronLeft className="h-3.5 w-3.5" /></button>
          <button onClick={() => scroll(1)} className="rounded-full border border-border p-1 text-muted-foreground hover:text-foreground"><ChevronRight className="h-3.5 w-3.5" /></button>
        </div>
      </div>
      <div ref={ref} className="flex gap-3 overflow-x-auto pb-2">
        {section.items.map((it) => (
          <div key={`${it.media_type}-${it.tmdb_id}`} className="w-[140px] shrink-0">{renderItem(it)}</div>
        ))}
      </div>
    </div>
  )
}

const SORTS: [string, string][] = [['popularity', 'Popularity ↓'], ['rating', 'Rating ↓'], ['release', 'Newest'], ['release.asc', 'Oldest']]
const EMPTY_FILTERS: DiscoverFilters = { type: 'movie', genres: '', year_min: '', year_max: '', vote_min: 0, sort: 'popularity' }

const PRESETS: [number, string][] = [[529892, 'MCU'], [86311, 'Avengers'], [748, 'X-Men'], [531241, 'Spider-Man'], [263, 'LOTR'], [1241, 'Harry Potter'], [656, 'Star Wars'], [645, 'James Bond'], [9485, 'Fast & Furious'], [2150, 'Alien'], [528, 'Terminator'], [2980, 'Back to the Future'], [404609, 'John Wick'], [295, 'Pirates of the Caribbean'], [87359, 'Mission Impossible'], [10, 'Batman']]
const DECADES = ['1980', '1990', '2000', '2010', '2020']
const pill = (on: boolean) => `rounded-full border px-2.5 py-0.5 text-[11px] ${on ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:text-foreground'}`

type Src = { kind: 'filter' } | { kind: 'collection'; id: number; label: string } | { kind: 'person'; id: number; label: string } | null

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <p className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-primary">{title}</p>
      {children}
    </div>
  )
}

function Explorer({ card }: { card: (it: SeerrItem) => ReactNode }) {
  const [type, setType] = useState<'movie' | 'tv'>('movie')
  const [genres, setGenres] = useState<Set<number>>(new Set())
  const [decade, setDecade] = useState('')
  const [sort, setSort] = useState('popularity')
  const [avail, setAvail] = useState<'all' | 'in' | 'partial' | 'out'>('all')
  const genreQ = useQuery(discoverGenresOpts(type))
  const [colQ, setColQ] = useState(''); const colSug = useQuery(collectionSearchOpts(useDebounced(colQ, 350)))
  const [perQ, setPerQ] = useState(''); const perSug = useQuery(personSearchOpts(useDebounced(perQ, 350)))

  const [src, setSrc] = useState<Src>(null)
  const [applied, setApplied] = useState<DiscoverFilters | null>(null)
  const [page, setPage] = useState(1)
  const [items, setItems] = useState<SeerrItem[]>([])
  const explore = useQuery({ ...discoverExploreOpts(applied ?? EMPTY_FILTERS, page), enabled: src?.kind === 'filter' && applied !== null })
  const collection = useQuery({ ...discoverCollectionOpts(src?.kind === 'collection' ? src.id : 0) })
  const person = useQuery({ ...discoverPersonOpts(src?.kind === 'person' ? src.id : 0) })

  useEffect(() => {
    if (!explore.data) return
    setItems((prev) => {
      const base = explore.data.page === 1 ? [] : prev
      const seen = new Set(base.map((i) => `${i.media_type}-${i.tmdb_id}`))
      const out = [...base]
      for (const it of explore.data.items) { const k = `${it.media_type}-${it.tmdb_id}`; if (!seen.has(k)) { seen.add(k); out.push(it) } }
      return out
    })
  }, [explore.data])

  const toggleGenre = (id: number) => setGenres((s) => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n })
  const decadeYears = (d: string) => (d ? { year_min: d, year_max: String(Number(d) + 9) } : { year_min: '', year_max: '' })
  const doSearch = () => { setApplied({ type, genres: [...genres].join(','), ...decadeYears(decade), vote_min: 0, sort }); setPage(1); setItems([]); setSrc({ kind: 'filter' }) }
  const reset = () => { setGenres(new Set()); setDecade(''); setSort('popularity'); setAvail('all'); setSrc(null); setItems([]); setApplied(null); setColQ(''); setPerQ('') }

  // Library mode: "In library" / "Partially available" source straight from
  // Sonarr/Radarr (fast) instead of paging all of TMDb and filtering. "All"/"Not in
  // library" stay on the TMDb discover/collection/person source.
  const libMode = avail === 'in' || avail === 'partial'
  const library = useQuery({ ...discoverLibraryOpts(type), enabled: libMode })

  const raw = src?.kind === 'filter' ? items : src?.kind === 'collection' ? (collection.data?.items ?? []) : src?.kind === 'person' ? (person.data?.items ?? []) : []
  const matchAvail = (s: number) => avail === 'all' || (avail === 'in' ? isComplete(s) : avail === 'partial' ? isPartial(s) : !isAvail(s))
  const results = libMode
    ? (library.data?.items ?? []).filter((it) => (avail === 'in' ? isComplete(it.status) : isPartial(it.status)))
    : raw.filter((it) => matchAvail(it.status))
  const showResults = libMode || !!src
  const loading = libMode ? library.isFetching : (explore.isFetching || collection.isFetching || person.isFetching)
  const heading = libMode ? (avail === 'in' ? 'In your library' : 'Partially available') : src?.kind === 'collection' ? (collection.data?.name ?? src.label) : src?.kind === 'person' ? src.label : null
  const canLoad = !libMode && src?.kind === 'filter' && items.length > 0 && !!explore.data && page < explore.data.total_pages && !explore.isFetching
  const sentinel = useInfiniteScroll(canLoad, () => setPage((p) => p + 1))

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-[250px_1fr]">
      <aside className="space-y-3">
        <div className="flex items-center gap-2">
          <Button size="sm" className="h-8 flex-1 gap-1.5" onClick={doSearch}><Search className="h-3.5 w-3.5" />Search</Button>
          <button onClick={reset} className="h-8 rounded-md border border-border px-3 text-xs text-muted-foreground hover:text-foreground">Reset</button>
        </div>

        <Section title="Type">
          <div className="flex rounded-md border border-border p-0.5">
            {(['movie', 'tv'] as const).map((t) => (
              <button key={t} onClick={() => { setType(t); setGenres(new Set()); if (t === 'movie' && avail === 'partial') setAvail('all') }}
                className={`flex-1 rounded px-2.5 py-1 text-xs font-medium ${type === t ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'}`}>{t === 'movie' ? 'Movies' : 'TV shows'}</button>
            ))}
          </div>
        </Section>

        <Section title="Availability">
          <div className="flex flex-wrap gap-1">
            {([['all', 'All'], ['out', 'Not in library'], ...(type === 'tv' ? [['partial', 'Partially available']] as const : []), ['in', 'In library']] as const).map(([v, l]) => (
              <button key={v} onClick={() => setAvail(v)} className={pill(avail === v)}>{l}</button>
            ))}
          </div>
        </Section>

        <Section title="Genre">
          <div className="flex flex-wrap gap-1">
            {(genreQ.data?.genres ?? []).map((g) => <button key={g.id} onClick={() => toggleGenre(g.id)} className={pill(genres.has(g.id))}>{g.name}</button>)}
          </div>
        </Section>

        <Section title="Decade">
          <div className="flex flex-wrap gap-1">
            {DECADES.map((d) => <button key={d} onClick={() => setDecade(decade === d ? '' : d)} className={pill(decade === d)}>{d.slice(2)}s</button>)}
          </div>
        </Section>

        <Section title="Sort">
          <select value={sort} onChange={(e) => setSort(e.target.value)} className="h-8 w-full rounded-md border border-border bg-card px-2 text-xs outline-none focus:border-primary">
            {SORTS.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
          </select>
        </Section>

        <Section title="Collections">
          <input value={colQ} onChange={(e) => setColQ(e.target.value)} placeholder="Search a collection…" className="mb-2 h-8 w-full rounded-md border border-border bg-card px-2 text-xs outline-none focus:border-primary" />
          {colSug.data?.results.length ? (
            <div className="mb-2 space-y-1">
              {colSug.data.results.map((c) => <button key={c.id} onClick={() => setSrc({ kind: 'collection', id: c.id, label: c.name })} className="block w-full truncate rounded px-2 py-1 text-left text-[11px] hover:bg-accent">{c.name}</button>)}
            </div>
          ) : null}
          <div className="flex flex-wrap gap-1">
            {PRESETS.map(([id, name]) => <button key={id} onClick={() => setSrc({ kind: 'collection', id, label: name })} className="rounded-full border border-border px-2 py-0.5 text-[10px] text-muted-foreground hover:border-primary hover:text-primary">{name}</button>)}
          </div>
        </Section>

        <Section title="By actor">
          <input value={perQ} onChange={(e) => setPerQ(e.target.value)} placeholder="Search an actor…" className="h-8 w-full rounded-md border border-border bg-card px-2 text-xs outline-none focus:border-primary" />
          {perSug.data?.results.length ? (
            <div className="mt-2 space-y-1">
              {perSug.data.results.map((p) => (
                <button key={p.id} onClick={() => setSrc({ kind: 'person', id: p.id, label: p.name })} className="flex w-full items-center gap-2 rounded px-2 py-1 text-left hover:bg-accent">
                  <div className="h-7 w-7 shrink-0 overflow-hidden rounded-full bg-muted">{p.image ? <img src={p.image} alt={p.name} className="h-full w-full object-cover" /> : null}</div>
                  <span className="truncate text-[11px] text-foreground">{p.name}</span>
                </button>
              ))}
            </div>
          ) : null}
        </Section>
      </aside>

      <main className="min-h-[200px]">
        {!showResults ? (
          <p className="text-sm text-muted-foreground">Pick some filters and click Search, or choose a collection / actor.</p>
        ) : (
          <div className="space-y-3">
            {heading && <h2 className="text-sm font-semibold text-foreground">{heading}{libMode && results.length > 0 ? <span className="ml-1.5 text-xs font-normal text-muted-foreground">({results.length})</span> : null}</h2>}
            {loading && results.length === 0 ? <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Loading…</div> : null}
            {libMode && library.isError ? <p className="break-all text-sm text-destructive">Library load failed: {(library.error as Error).message}</p> : null}
            {results.length === 0 && !loading && !(libMode && library.isError) ? <p className="text-sm text-muted-foreground">No results.</p> : null}
            <div className="grid gap-3" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(140px, 1fr))' }}>{results.map(card)}</div>
            {!libMode && src?.kind === 'filter' && (
              <div ref={sentinel} className="flex h-8 justify-center">
                {explore.isFetching && results.length > 0 ? <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /> : null}
              </div>
            )}
          </div>
        )}
      </main>
    </div>
  )
}

type Mode = 'home' | 'explore' | 'watchlist'

export function Discover() {
  const qc = useQueryClient()
  const [requested, setRequested] = useState<Set<string>>(new Set())
  const [sel, setSel] = useState<{ type: 'movie' | 'tv'; id: number } | null>(null)
  const [q, setQ] = useState('')
  const [mode, setMode] = useState<Mode>('home')
  const dq = useDebounced(q, 400)
  const searching = dq.trim().length > 1

  const home = useQuery(discoverHomeOpts())
  const search = useQuery(discoverSearchOpts(dq))
  const wl = useQuery(watchlistOpts())
  const wlSet = new Set((wl.data?.items ?? []).map((i) => `${i.media_type}-${i.tmdb_id}`))
  const wtoggle = useWatchlistToggle()

  const markRequested = (key: string) => setRequested((s) => new Set(s).add(key))
  const open: OpenFn = (it) => setSel({ type: it.media_type, id: it.tmdb_id })
  const toggleWatch = (it: SeerrItem) => wtoggle.mutate(it, { onSuccess: () => qc.invalidateQueries({ queryKey: ['watchlist'] }) })
  const card = (it: SeerrItem) => {
    const key = `${it.media_type}-${it.tmdb_id}`
    return <PosterCard key={key} it={it} requested={requested.has(key)} inWatch={wlSet.has(key)} onToggleWatch={() => toggleWatch(it)} onOpen={() => open(it)} />
  }
  const grid = (items: SeerrItem[]) => <div className="grid gap-3" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(140px, 1fr))' }}>{items.map(card)}</div>

  const err = home.error as Error | null
  const notConfigured = home.isError && /not configured/i.test(err?.message || '')
  const tab = (m: Mode, label: string, Icon: typeof Bookmark) => (
    <button onClick={() => setMode(mode === m ? 'home' : m)}
      className={`flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs font-medium ${mode === m ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:text-foreground'}`}>
      <Icon className="h-3.5 w-3.5" />{label}
    </button>
  )

  return (
    <div className="w-full space-y-4 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="shrink-0 text-lg font-semibold text-foreground">Discover</h1>
        <div className="relative min-w-[200px] flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search a movie or TV show on TMDb…"
            className="h-9 w-full rounded-md border border-border bg-card pl-8 pr-3 text-sm outline-none focus:border-primary" />
        </div>
        {tab('explore', 'Explorer', SlidersHorizontal)}
        {tab('watchlist', `Watchlist${wl.data?.items.length ? ` (${wl.data.items.length})` : ''}`, Bookmark)}
        {(home.isFetching || search.isFetching) && <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />}
      </div>

      {notConfigured && <p className="text-sm text-muted-foreground">TMDb not configured — add a TMDb API key in <span className="font-medium">Settings → Discover &amp; Requests</span>.</p>}
      {home.isError && !notConfigured && <p className="text-sm text-destructive">{err?.message}</p>}

      {searching ? (
        <div>
          <h2 className="mb-2 text-sm font-semibold text-foreground">Results for “{dq}”</h2>
          {search.data && search.data.items.length === 0 && !search.isFetching && <p className="text-sm text-muted-foreground">No matches.</p>}
          {grid(search.data?.items ?? [])}
        </div>
      ) : mode === 'watchlist' ? (
        <div>
          {wl.data && wl.data.items.length === 0 ? <p className="text-sm text-muted-foreground">Your watchlist is empty — tap the bookmark on any title.</p> : grid(wl.data?.items ?? [])}
        </div>
      ) : mode === 'explore' ? (
        <Explorer card={card} />
      ) : (
        <>
          {home.isLoading && <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Loading…</div>}
          {(home.data?.hero_movie || home.data?.hero_tv) && (
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <HeroCard it={home.data?.hero_movie} label="Trending movie · this week" requested={!!home.data?.hero_movie && requested.has(`movie-${home.data.hero_movie.tmdb_id}`)} onOpen={open} />
              <HeroCard it={home.data?.hero_tv} label="Trending TV · this week" requested={!!home.data?.hero_tv && requested.has(`tv-${home.data.hero_tv.tmdb_id}`)} onOpen={open} />
            </div>
          )}
          {(home.data?.sections ?? []).map((s) => <Carousel key={s.key} section={s} renderItem={card} />)}
        </>
      )}

      {sel && (
        <DetailModal sel={sel} onClose={() => setSel(null)}
          requested={requested.has(`${sel.type}-${sel.id}`)} onRequested={() => markRequested(`${sel.type}-${sel.id}`)}
          inWatch={wlSet.has(`${sel.type}-${sel.id}`)} onToggleWatch={toggleWatch} />
      )}
    </div>
  )
}
