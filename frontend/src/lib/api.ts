import { useQuery, useMutation } from '@tanstack/react-query'

const BASE = '/api'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

// ── Types ──────────────────────────────────────────────────────────────────────

export interface SystemInfo {
  cpu_percent: number
  ram_total: number
  ram_used: number
  ram_percent: number
  disk_total: number
  disk_used: number
  disk_percent: number
  uptime_seconds: number
}

export interface ContainerInfo {
  id: string
  name: string
  status: string        // human, e.g. "Up 20 minutes"
  running: boolean
  image: string
  ports: Record<string, string>
}

export interface InstanceInfo {
  name: string
  installed: boolean
  kind: 'container' | 'service' | 'unknown'
  container_status?: string | null
  image?: string | null
  unit?: string | null   // systemd unit name when kind === 'service'
}

export interface AppInfo {
  tag: string
  name: string
  repo: 'saltbox' | 'sandbox' | 'mod'
  installed: boolean
  kind: 'container' | 'service' | 'unknown'
  container_status?: string
  image?: string   // current docker image:tag
  instances?: InstanceInfo[]
  companions?: InstanceInfo[]   // attached dependency containers (e.g. n8n-postgres)
  category?: string
  on_demand?: boolean
}

export const useCategories = () =>
  useQuery<{ order: string[]; labels: Record<string, string> }>({
    queryKey: ['categories'],
    queryFn: () => request('/categories'),
    staleTime: 10 * 60_000,
  })

export interface SaltboxVersion {
  sha: string
  date: string | null
  tag: string | null
  behind: number
  commits: string[]
}

export interface Job {
  id: string
  tag: string
  action: string
  status: 'pending' | 'running' | 'completed' | 'failed' | 'stopped'
  created_at: string
  log_lines: number
}

export interface ConfigFile {
  filename: string
  data: Record<string, unknown>
}

export interface RoleSpec {
  name: string
  docker_image: string
  docker_tag: string
  port: string
  subdomain: string
  volumes: { host: string; container: string }[]
  env_vars: { key: string; value: string }[]
  auth_mode: 'sso' | 'bypass' | 'none'
}

// ── Hooks ──────────────────────────────────────────────────────────────────────

export const useSystem = () =>
  useQuery<SystemInfo>({ queryKey: ['system'], queryFn: () => request('/system'), refetchInterval: 5000 })

export const useContainers = () =>
  useQuery<ContainerInfo[]>({ queryKey: ['containers'], queryFn: () => request('/containers'), refetchInterval: 10000 })

export interface ContainerInspect {
  created: string; restart: string; env: string[]; networks: string[]
  mounts: { source: string; destination: string; type: string; rw: boolean }[]
}
export const useContainerInspect = (name: string | null) =>
  useQuery<ContainerInspect>({
    queryKey: ['inspect', name],
    queryFn: () => request(`/containers/${encodeURIComponent(name!)}/inspect`),
    enabled: !!name,
  })

export interface ContainerStat { name: string; cpu: string; mem: string; mem_pct: string; net: string; block: string }
export const useContainerStats = () =>
  useQuery<Record<string, ContainerStat>>({
    queryKey: ['container-stats'],
    queryFn: () => request('/containers/stats'),
    refetchInterval: 5000,
  })

export interface MountInfo { target: string; kind: string; ok: boolean; detail: string }
export interface StatusItem { ok: boolean; label: string; detail?: string; list?: MountInfo[] }
export interface SystemStatusData { connection: StatusItem; mounts: StatusItem; docker: StatusItem }

export const useStatus = () =>
  useQuery<SystemStatusData>({
    queryKey: ['status'],
    queryFn: () => request('/status'),
    refetchInterval: 20000,
    staleTime: 10000,
  })

export interface MountDetail {
  target: string; kind: string; ok: boolean; detail: string
  size?: string; used?: string; use_pct?: string
}

export const useMounts = () =>
  useQuery<MountDetail[]>({
    queryKey: ['mounts'],
    queryFn: () => request('/mounts'),
    refetchInterval: 30000,
  })

export interface RemoteInfo { name: string; type: string; ok: boolean; used?: string; total?: string }
export interface StorageData { remotes: RemoteInfo[]; local: MountDetail | null }

export const useStorage = () =>
  useQuery<StorageData>({
    queryKey: ['storage'],
    queryFn: () => request('/storage'),
    refetchInterval: 60000,
  })

export const useApps = () =>
  useQuery<AppInfo[]>({ queryKey: ['apps'], queryFn: () => request('/apps'), refetchInterval: 30000 })

export const useSaltboxVersion = () =>
  useQuery<SaltboxVersion>({ queryKey: ['saltbox-version'], queryFn: () => request('/apps/saltbox-version'), staleTime: 60_000 })

export const useUpdateStatus = () =>
  useQuery<Record<string, boolean | null>>({
    queryKey: ['update-status'],
    queryFn: () => request('/apps/update-status'),
    staleTime: 0,
  })

export interface BundlePull {
  role: string
  via: string
  conditional: boolean
}

export interface Bundle {
  tag: string
  label: string
  kind: 'profile' | 'bundle' | 'dynamic'
  description: string
  roles: string[]
  pulls: BundlePull[]
  computed: boolean
}

export interface CustomSet { id: string; name: string; tags: string[] }

export const useCustomSets = () =>
  useQuery<CustomSet[]>({ queryKey: ['custom-sets'], queryFn: () => request('/custom-sets') })

export const useSaveCustomSet = () =>
  useMutation<CustomSet, Error, { id?: string; name: string; tags: string[] }>({
    mutationFn: (body) => request('/custom-sets', { method: 'PUT', body: JSON.stringify(body) }),
  })

export const useDeleteCustomSet = () =>
  useMutation<void, Error, string>({
    mutationFn: (id) => request(`/custom-sets/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  })

export const useInstallSet = () =>
  useMutation<{ job_id: string }, Error, string[]>({
    mutationFn: (tags) => request('/apps/install-set', { method: 'POST', body: JSON.stringify({ tags }) }),
  })

export const useBundles = () =>
  useQuery<Bundle[]>({
    queryKey: ['bundles'],
    queryFn: () => request('/bundles'),
    staleTime: 5 * 60_000,
  })

export const useUpdateMeta = () =>
  useQuery<{ last_checked: number | null; ts: Record<string, number> }>({
    queryKey: ['update-meta'],
    queryFn: () => request('/apps/update-meta'),
    refetchInterval: 30_000,
  })

export interface ImageInfo {
  image: string
  created?: string
  size?: number
  architecture?: string
  os?: string
  id?: string
  digest?: string | null
  tags?: string[]
  outdated?: boolean | null
  checked_at?: number | null
}
export const useImageInfo = (name: string | null, image: string | null | undefined) =>
  useQuery<ImageInfo>({
    queryKey: ['image-info', name, image],
    queryFn: () => request(`/apps/${encodeURIComponent(name!)}/image-info?image=${encodeURIComponent(image!)}`),
    enabled: !!name && !!image,
  })

export const useCheckUpdates = () =>
  useMutation<{ job_id: string }, Error, void>({
    mutationFn: () => request('/apps/check-updates', { method: 'POST' }),
  })

export const useSaltboxUpdate = () =>
  useMutation<{ job_id: string }, Error, void>({
    mutationFn: () => request('/apps/saltbox-update', { method: 'POST' }),
  })

// ── sb-ui self-update ────────────────────────────────────────────────────────
export interface SelfVersion {
  current: string
  latest: string
  update_available: boolean
  asset: string
  asset_url?: string
  release_url?: string
  note?: string
}

export const useSelfVersion = () =>
  useQuery<SelfVersion>({
    queryKey: ['self-version'],
    queryFn: () => request('/self/version'),
    staleTime: 5 * 60_000,
  })

export const useSelfUpdate = () =>
  useMutation<{ job_id: string }, Error, void>({
    mutationFn: () => request('/self/update', { method: 'POST' }),
  })

export const useApplyPatches = () =>
  useMutation<{ job_id: string }, Error, void>({
    mutationFn: () => request('/apps/apply-patches', { method: 'POST' }),
  })

export const useJobs = () =>
  useQuery<Job[]>({ queryKey: ['jobs'], queryFn: () => request('/jobs'), refetchInterval: 3000 })

export const useConfig = (file: string) =>
  useQuery<ConfigFile>({ queryKey: ['config', file], queryFn: () => request(`/config/${file}`) })

// ── Mutations ──────────────────────────────────────────────────────────────────

export const useInstallApp = () =>
  useMutation<{ job_id: string }, Error, { tag: string; action?: string }>({
    mutationFn: ({ tag, action = 'install' }) =>
      request(`/apps/${encodeURIComponent(tag)}/${action}`, { method: 'POST' }),
  })

export const useRemoveApp = () =>
  useMutation<{ job_id: string }, Error, { tag: string; purge: boolean }>({
    mutationFn: ({ tag, purge }) =>
      request(`/apps/${encodeURIComponent(tag)}/remove?purge=${purge}`, { method: 'POST' }),
  })

export const usePullImage = () =>
  useMutation<{ job_id: string }, Error, string>({
    mutationFn: (tag) => request(`/apps/${encodeURIComponent(tag)}/pull`, { method: 'POST' }),
  })

export const useContainerAction = () =>
  useMutation<void, Error, { name: string; action: 'start' | 'stop' | 'restart' }>({
    mutationFn: ({ name, action }) =>
      request(`/containers/${encodeURIComponent(name)}/${action}`, { method: 'POST' }),
  })

export const useAppLogs = (name: string | null, lines = 200) =>
  useQuery<{ name: string; logs: string }>({
    queryKey: ['app-logs', name, lines],
    queryFn: () => request(`/apps/${encodeURIComponent(name!)}/logs?lines=${lines}`),
    enabled: !!name,
    refetchInterval: 5000,
  })

export const useAppAppdata = (tag: string | null) =>
  useQuery<{ paths: { instance: string; path: string }[] }>({
    queryKey: ['app-appdata', tag],
    queryFn: () => request(`/apps/${encodeURIComponent(tag!)}/appdata`),
    enabled: !!tag,
  })

export interface OptEntry { type: 'dir' | 'file'; size: number; name: string }
export const useAppOpt = (name: string | null, path: string) =>
  useQuery<{ name: string; path: string; base: string; entries: OptEntry[]; exists: boolean }>({
    queryKey: ['app-opt', name, path],
    queryFn: () => request(`/apps/${encodeURIComponent(name!)}/opt?path=${encodeURIComponent(path)}`),
    enabled: !!name,
  })

export const useServiceAction = () =>
  useMutation<void, Error, { name: string; action: 'start' | 'stop' | 'restart' }>({
    mutationFn: ({ name, action }) =>
      request(`/services/${encodeURIComponent(name)}/${action}`, { method: 'POST' }),
  })

export const useSaveConfig = (file: string) =>
  useMutation<void, Error, Record<string, unknown>>({
    mutationFn: (data) => request(`/config/${file}`, { method: 'PUT', body: JSON.stringify(data) }),
  })

// ── rclone.conf ────────────────────────────────────────────────────────────────

export type RcloneRemotes = Record<string, Record<string, string>>

export interface RcloneConfData {
  path: string
  remotes: RcloneRemotes
}

export const useRcloneRemotes = () =>
  useQuery<RcloneConfData>({
    queryKey: ['rclone-remotes'],
    queryFn: () => request('/rclone/remotes'),
  })

export const useSaveRcloneRemotes = () =>
  useMutation<{ ok: boolean; path: string }, Error, RcloneRemotes>({
    mutationFn: (data) =>
      request('/rclone/remotes', { method: 'PUT', body: JSON.stringify(data) }),
  })

export interface RcloneUnit { unit: string; load: string; active: string; sub: string }
export interface RcloneTimer { unit: string; active: string; sub: string; activates: string; next: string | null }
export interface RcloneMount {
  target: string; source: string; fstype: string
  size?: string | null; used?: string | null; use_pct?: string | null
}
export interface RcloneStatus {
  version: string | null
  units: RcloneUnit[]
  timers: RcloneTimer[]
  mounts: RcloneMount[]
  remotes: string[]
}
export const useRcloneStatus = (enabled: boolean) =>
  useQuery<RcloneStatus>({
    queryKey: ['rclone-status'],
    queryFn: () => request('/rclone/status'),
    enabled,
    refetchInterval: 8000,
  })

export const useRcloneLogs = (unit: string | null, lines = 200) =>
  useQuery<{ unit: string; logs: string }>({
    queryKey: ['rclone-logs', unit, lines],
    queryFn: () => request(`/rclone/logs?unit=${encodeURIComponent(unit!)}&lines=${lines}`),
    enabled: !!unit,
    refetchInterval: 5000,
  })

export const useFsList = (path: string | null) =>
  useQuery<{ path: string; entries: OptEntry[]; exists: boolean }>({
    queryKey: ['fs-list', path],
    queryFn: () => request(`/fs?path=${encodeURIComponent(path!)}`),
    enabled: !!path,
  })

export const useFsFile = (path: string | null) =>
  useQuery<{ path: string; content: string; writable: boolean }>({
    queryKey: ['fs-file', path],
    queryFn: () => request(`/fs/file?path=${encodeURIComponent(path!)}`),
    enabled: !!path,
    staleTime: 0,
  })

export const useSaveFsFile = () =>
  useMutation<{ ok: boolean; path: string }, Error, { path: string; content: string }>({
    mutationFn: ({ path, content }) =>
      request(`/fs/file?path=${encodeURIComponent(path)}`, { method: 'PUT', body: JSON.stringify({ content }) }),
  })

export const useFsRead = (path: string | null) =>
  useQuery<{ path: string; content: string }>({
    queryKey: ['fs-read', path],
    queryFn: () => request(`/fs/read?path=${encodeURIComponent(path!)}`),
    enabled: !!path,
    staleTime: 0,
  })

export const useFsWrite = () =>
  useMutation<{ ok: boolean; path: string }, Error, { path: string; content: string }>({
    mutationFn: ({ path, content }) =>
      request(`/fs/write?path=${encodeURIComponent(path)}`, { method: 'PUT', body: JSON.stringify({ content }) }),
  })

export const useMountTemplates = () =>
  useQuery<{ templates: string[]; path: string }>({
    queryKey: ['mount-templates'],
    queryFn: () => request('/rclone/mount-templates'),
    staleTime: 60_000,
  })

// ── Install types ────────────────────────────────────────────────────────────────

export interface InstallTypeProfile {
  key: string
  roles: string[]
  default: string[]
  overridden: boolean
}
export interface EnabledList {
  value: string[]
  default: string[]
  options: string[]
  overridden: boolean
}
export interface InstallTypes {
  profiles: Record<'saltbox' | 'mediabox' | 'feederbox', InstallTypeProfile>
  enabled: Record<string, EnabledList>
  available_roles: string[]
}

export const useInstallTypes = () =>
  useQuery<InstallTypes>({ queryKey: ['install-types'], queryFn: () => request('/install-types') })

export const useSaveInstallTypes = () =>
  useMutation<{ ok: boolean; changed: string[] }, Error, InstallTypes>({
    mutationFn: (data) => request('/install-types', { method: 'PUT', body: JSON.stringify(data) }),
  })

// ── Inventory ──────────────────────────────────────────────────────────────────

export const useInventory = () =>
  useQuery<{ data: Record<string, unknown> }>({
    queryKey: ['inventory'],
    queryFn: () => request('/inventory'),
  })

export const useSaveInventory = () =>
  useMutation<{ ok: boolean }, Error, Record<string, unknown>>({
    mutationFn: (data) => request('/inventory', { method: 'PUT', body: JSON.stringify(data) }),
  })

export interface CatalogRole {
  role: string
  repo: 'saltbox' | 'sandbox'
  variables: Record<string, unknown>
  sections?: Record<string, string>
}

export const useInventoryCatalog = (options?: { enabled?: boolean }) =>
  useQuery<{ roles: Record<string, CatalogRole> }>({
    queryKey: ['inventory-catalog'],
    queryFn: () => request('/inventory/catalog'),
    staleTime: 5 * 60_000,
    enabled: options?.enabled ?? true,
  })

// ── Role file editor ───────────────────────────────────────────────────────────

export const useRoleFiles = (role: string, repo: string, options?: { enabled?: boolean }) =>
  useQuery<{ files: string[]; base: string }>({
    queryKey: ['role-files', role, repo],
    queryFn: () => request(`/roles/${encodeURIComponent(role)}/files?repo=${repo}`),
    staleTime: 30_000,
    enabled: options?.enabled ?? true,
  })

export const useRolePatches = (role: string, repo: string, options?: { enabled?: boolean }) =>
  useQuery<{ patches: string[] }>({
    queryKey: ['role-patches', role, repo],
    queryFn: () => request(`/roles/${encodeURIComponent(role)}/patches?repo=${repo}`),
    staleTime: 10_000,
    enabled: options?.enabled ?? true,
  })

export const useRoleFile = (role: string, repo: string, path: string | null) =>
  useQuery<{ path: string; content: string }>({
    queryKey: ['role-file', role, repo, path],
    queryFn: () => request(`/roles/${encodeURIComponent(role)}/file?path=${encodeURIComponent(path!)}&repo=${repo}`),
    enabled: !!path,
    staleTime: 0,
  })

export const useSaveRoleFile = () =>
  useMutation<{ ok: boolean; path: string }, Error, { role: string; repo: string; path: string; content: string }>({
    mutationFn: ({ role, repo, path, content }) =>
      request(`/roles/${encodeURIComponent(role)}/file?path=${encodeURIComponent(path)}&repo=${repo}`, {
        method: 'PUT',
        body: JSON.stringify({ content }),
      }),
  })

export const useRolePatch = (role: string, repo: string, path: string | null) =>
  useQuery<{ path: string; patch: string | null }>({
    queryKey: ['role-patch', role, repo, path],
    queryFn: () => request(`/roles/${encodeURIComponent(role)}/patch?path=${encodeURIComponent(path!)}&repo=${repo}`),
    enabled: !!path,
    staleTime: 0,
  })

export interface RebuildPreviewItem {
  file: string
  original: string | null
  current: string
  patch: string | null
  mode: 'diff' | 'full-content'
  error?: string
}

export const useRebuildPreview = (role: string, repo: string, enabled: boolean) =>
  useQuery<{ items: RebuildPreviewItem[] }>({
    queryKey: ['rebuild-preview', role, repo],
    queryFn: () => request(`/roles/${encodeURIComponent(role)}/patches/rebuild-preview?repo=${repo}`),
    enabled,
    staleTime: 0,
  })

export const useRebuildPatches = () =>
  useMutation<{ rebuilt: string[]; failed: { file: string; error: string }[] }, Error, { role: string; repo: string }>({
    mutationFn: ({ role, repo }) =>
      request(`/roles/${encodeURIComponent(role)}/patches/rebuild?repo=${repo}`, { method: 'POST' }),
  })

// ── Setup ──────────────────────────────────────────────────────────────────────

export interface SetupStatus {
  configured: boolean
  mode: 'ssh' | 'local'
  host: string | null
  user: string
  port: number
  key: string
  auth_type: 'key' | 'password'
  saltbox_configured: boolean
}

export interface TestResult {
  success: boolean
  error?: string
  latency_ms?: number
}

export type TestBody =
  | { host: string; port: number; user: string; auth_type: 'key'; key_path: string; passphrase?: string }
  | { host: string; port: number; user: string; auth_type: 'password'; password: string }

export type SaveBody =
  | { mode: 'local' }
  | { mode: 'ssh'; host: string; port: number; user: string; auth_type: 'key'; key_path: string; passphrase?: string }
  | { mode: 'ssh'; host: string; port: number; user: string; auth_type: 'password'; password: string }

export const useSetupStatus = () =>
  useQuery<SetupStatus>({
    queryKey: ['setup-status'],
    queryFn: () => request('/setup/status'),
    staleTime: 0,
    // When the backend is unreachable, keep polling so the app recovers on its own.
    refetchInterval: (q) => (q.state.status === 'error' ? 3000 : false),
  })

export const useTestConnection = () =>
  useMutation<TestResult, Error, TestBody>({
    mutationFn: (body) => request('/setup/test', { method: 'POST', body: JSON.stringify(body) }),
  })

export const useSaveSetup = () =>
  useMutation<{ success: boolean }, Error, SaveBody>({
    mutationFn: (body) => request('/setup/save', { method: 'POST', body: JSON.stringify(body) }),
  })

export const usePreviewRole = () =>
  useMutation<{ defaults: string; tasks: string }, Error, RoleSpec>({
    mutationFn: (spec) => request('/roles/preview', { method: 'POST', body: JSON.stringify(spec) }),
  })

export const useCommitRole = () =>
  useMutation<{ job_id: string }, Error, RoleSpec>({
    mutationFn: (spec) => request('/roles/commit', { method: 'POST', body: JSON.stringify(spec) }),
  })

export interface LsEntry { name: string; is_dir: boolean; size: number }
export interface TransferItem { path: string; is_dir: boolean }
export interface ExtraFlag { flag: string; value: string }
export interface TransferOpts {
  transfers?: number; checkers?: number; bwlimit?: string; tpslimit?: number; retries?: number
  ignore_existing?: boolean; update?: boolean; create_empty_src_dirs?: boolean; no_traverse?: boolean; one_file_system?: boolean
  fast_list?: boolean; compare?: 'checksum' | 'size-only' | 'ignore-size' | ''
  sync_delete?: 'during' | 'after' | 'before' | ''
  include?: string[]; exclude?: string[]
  extra?: ExtraFlag[]
}

export interface FileStat { name: string; size: number; bytes: number; percentage: number; speed: number; speedAvg: number; eta: number }
export interface TransferStats { bytes: number; totalBytes: number; speed: number; eta: number; transfers: number; totalTransfers: number; checks: number; totalChecks: number; elapsedTime: number; errors: number; transferring?: FileStat[]; started_at?: string; finished_at?: string }
export const useTransferStats = (id: string | null, live: boolean) =>
  useQuery<TransferStats>({
    queryKey: ['transfer-stats', id],
    queryFn: () => request(`/transfers/${id}/stats`),
    enabled: !!id, // fetch once even for finished jobs (final summary persists)
    refetchInterval: live ? 1000 : false,
  })

// Telemetry (P1): detailed per-job upload state + analysis findings.
export interface TelSample { t: number; speed: number; bytes: number; active: number; errors: number }
export interface TelFile { name: string; size: number; bytes: number; speed_avg: number }
export interface TelEvent { t: number; kind: 'flood' | 'auth' | 'quota' | 'checksum' | 'network' | 'retry' | 'error'; msg: string }
export interface TelSummary { duration_sec: number; bytes: number; files: number; avg_speed: number; peak_speed: number; peak_active: number; errors: number; flood_hits: number; throttled: boolean; per_conn_est: number; concurrency: number }
export interface TelFinding { severity: 'good' | 'warn' | 'bad'; title: string; detail: string; suggest?: Record<string, number> }
export interface TelemetryData { job_id: string; task_id?: string; started_at: string; dst: string; samples: TelSample[]; files: Record<string, TelFile>; events: TelEvent[]; summary?: TelSummary; findings: TelFinding[] }
export const useTransferTelemetry = (id: string | null, enabled: boolean, live = false) =>
  useQuery<TelemetryData>({ queryKey: ['telemetry', id], queryFn: () => request(`/transfers/${id}/telemetry`), enabled: !!id && enabled, retry: false, refetchInterval: live ? 2500 : false })
export const useDeleteTelemetry = () =>
  useMutation<{ ok: boolean }, Error, string>({ mutationFn: (id) => request(`/transfers/${id}/telemetry`, { method: 'DELETE' }) })
export const usePurgeTelemetry = () =>
  useMutation<{ ok: boolean }, Error, void>({ mutationFn: () => request('/telemetry/purge', { method: 'POST' }) })
export const useDeleteJob = () =>
  useMutation<{ ok: boolean }, Error, string>({ mutationFn: (id) => request(`/jobs/${id}`, { method: 'DELETE' }) })
export const useClearJobs = () =>
  useMutation<{ ok: boolean; removed: number }, Error, void>({ mutationFn: () => request('/jobs/clear', { method: 'POST' }) })

// tsdproxy (Tailscale proxy)
export interface ProxyStatus { installed: boolean; configured: boolean; active: boolean; status: string }
export const useProxyStatus = () =>
  useQuery<ProxyStatus>({ queryKey: ['proxy-status'], queryFn: () => request('/proxy/status'), refetchInterval: 10000 })
export interface TsAuth { mode: 'authkey' | 'oauth'; auth_key?: string; client_id?: string; client_secret?: string; tags?: string }
export const useProxyInstall = () =>
  useMutation<{ ok: boolean }, Error, TsAuth>({ mutationFn: (b) => request('/proxy/install', { method: 'POST', body: JSON.stringify(b) }) })
export const useProxyRekey = () =>
  useMutation<{ ok: boolean }, Error, TsAuth>({ mutationFn: (b) => request('/proxy/authkey', { method: 'PUT', body: JSON.stringify(b) }) })
export interface ProxyTestResult { ok: boolean; mode: string; scope?: string; expires_in?: number; tailnet?: string; user?: string; devices?: number; note?: string; looks_valid?: boolean }
export const useProxyTest = () =>
  useMutation<ProxyTestResult, Error, TsAuth>({ mutationFn: (b) => request('/proxy/test', { method: 'POST', body: JSON.stringify(b) }) })
export const useProxyRestart = () =>
  useMutation<{ ok: boolean }, Error, void>({ mutationFn: () => request('/proxy/restart', { method: 'POST' }) })
export interface ProxyEntry { name: string; target: string; label?: string; icon?: string; hidden?: boolean }
export const useProxyLists = () =>
  useQuery<{ entries: ProxyEntry[] }>({ queryKey: ['proxy-lists'], queryFn: () => request('/proxy/lists') })
export const useProxyAddList = () =>
  useMutation<{ ok: boolean }, Error, ProxyEntry>({ mutationFn: (e) => request('/proxy/lists', { method: 'POST', body: JSON.stringify(e) }) })
export const useProxyDelList = () =>
  useMutation<{ ok: boolean }, Error, string>({ mutationFn: (name) => request(`/proxy/lists/${encodeURIComponent(name)}`, { method: 'DELETE' }) })
export interface ProxySelf { enabled: boolean; name: string; target: string; label?: string; icon?: string; hidden?: boolean }
export interface ManagedPayload { enabled: boolean; name: string; label?: string; icon?: string; hidden?: boolean }
export const useProxySelf = () =>
  useQuery<ProxySelf>({ queryKey: ['proxy-self'], queryFn: () => request('/proxy/self') })
export const useProxySetSelf = () =>
  useMutation<{ ok: boolean }, Error, ManagedPayload>({ mutationFn: (b) => request('/proxy/self', { method: 'PUT', body: JSON.stringify(b) }) })
export const useProxyDash = () =>
  useQuery<ProxySelf>({ queryKey: ['proxy-dash'], queryFn: () => request('/proxy/dash') })
export const useProxySetDash = () =>
  useMutation<{ ok: boolean }, Error, ManagedPayload>({ mutationFn: (b) => request('/proxy/dash', { method: 'PUT', body: JSON.stringify(b) }) })
// Unified *arr library (Sonarr/Radarr across instances)
export interface ArrCopy { instance: string; item_id: number; profile: string; files: number; size: number; has_file: boolean; in_plex: boolean; folder?: string }
export const useArrPlexRefresh = () =>
  useMutation<{ ok: boolean }, Error, { path: string }>({ mutationFn: (b) => request('/arr/plex-refresh', { method: 'POST', body: JSON.stringify(b) }) })
export interface ArrItem {
  kind: string; key: string; title: string; year: number
  poster: string; overview: string; status: string; network: string
  runtime: number; rating: number; monitored: boolean; genres: string[] | null
  seasons: number; episodes: number; in_plex: boolean
  copies: ArrCopy[]
}
export interface ArrMedia { resolution?: string; video_codec?: string; dynamic_range?: string; audio_codec?: string; audio_channels?: number; audio_languages?: string; subtitles?: string; runtime?: string }
export interface ArrFile { season?: number; episode?: number; episode_id?: number; file_id?: number; title?: string; air_date?: string; monitored: boolean; has_file: boolean; in_plex: boolean; quality?: string; size: number; path?: string; full_path?: string; release_group?: string; languages?: string; date_added?: string; media?: ArrMedia }
export interface ArrCommand { kind: string; instance: string; id: number; action: string; episode_id?: number; file_id?: number; season?: number }
export const useArrLibrary = () =>
  useQuery<{ items: ArrItem[]; instances: { kind: string; name: string }[] }>({ queryKey: ['arr-library'], queryFn: () => request('/arr/library'), staleTime: 60000 })

export interface ConnStat { label: string; value: number }
export interface PathStat { path: string; stats: ConnStat[] }
export interface ConnStatus { name: string; base_url: string; ok: boolean; version?: string; detail?: string; error?: string; latency_ms: number; recommended?: boolean; primary?: boolean; stats?: ConnStat[]; path_stats?: PathStat[] }
export interface PlexLibInfo { title: string; type: string; count: number; locations?: string[] }
export interface IntegrationGroup { key: string; label: string; library: string; used: boolean; configured: boolean; note?: string; instances: ConnStatus[]; libraries?: PlexLibInfo[] }
export const useIntegrations = () =>
  useQuery<{ groups: IntegrationGroup[] }>({ queryKey: ['integrations'], queryFn: () => request('/integrations') })
export const arrFilesQueryOpts = (kind: string, instance: string, id: number, ext = '') => ({
  queryKey: ['arr-files', kind, instance, id],
  queryFn: () => request<{ files: ArrFile[] }>(`/arr/files?kind=${kind}&instance=${encodeURIComponent(instance)}&id=${id}&ext=${encodeURIComponent(ext)}`),
  staleTime: 60000,
})
export const useArrFiles = (kind: string, instance: string, id: number, enabled: boolean, ext = '') =>
  useQuery<{ files: ArrFile[] }>({ ...arrFilesQueryOpts(kind, instance, id, ext), enabled })
export const useArrCommand = () =>
  useMutation<{ ok: boolean }, Error, ArrCommand>({
    mutationFn: (b) => request('/arr/command', { method: 'POST', body: JSON.stringify(b) }),
  })

export interface AppTSState { tag: string; app: string; enabled: boolean; name: string; port: string; default_port: string; label: string; icon: string; hidden: boolean; instances: string[] | null }
export const useProxyApps = () =>
  useQuery<{ apps: AppTSState[] }>({ queryKey: ['proxy-apps'], queryFn: () => request('/proxy/apps') })
export interface AppTSPayload { enabled: boolean; name: string; port: string; label?: string; icon?: string; hidden?: boolean }
export const useProxyAppSet = (tag: string) =>
  useMutation<{ ok: boolean; job_id: string }, Error, AppTSPayload>({ mutationFn: (b) => request(`/apps/${encodeURIComponent(tag)}/tailscale`, { method: 'PUT', body: JSON.stringify(b) }) })
export interface ProxyOpts {
  log_level: string; log_json: boolean; dash_port: number; access_log: boolean; admin_localhost: boolean
  control_url: string; prevent_duplicates: boolean; max_cert_concurrency: number
  target_hostname: string; try_internal_net: boolean
  health_check: boolean; health_interval: number; health_failures: number; health_cooldown: number; auto_restart: boolean
}
export const useProxyOpts = () =>
  useQuery<ProxyOpts>({ queryKey: ['proxy-opts'], queryFn: () => request('/proxy/opts') })
export const useProxySetOpts = () =>
  useMutation<{ ok: boolean }, Error, ProxyOpts>({ mutationFn: (b) => request('/proxy/opts', { method: 'PUT', body: JSON.stringify(b) }) })

// Central options + Plex
export interface PathMapping { from: string; to: string }
export interface OptionsConfig { plex: { url: string; token: string; throttle: boolean; max_streams: number; scan_after_upload: boolean }; path_mappings?: PathMapping[]; seerr?: { url: string; api_key: string }; tmdb?: { api_key: string }; qbit?: { url: string; user: string; pass: string } }

// Discover (TMDb) — titles to request (status: 0/1 requestable · 2/3 requested · 4/5 available)
export interface SeerrItem { media_type: 'movie' | 'tv'; tmdb_id: number; title: string; year: string; poster: string; backdrop?: string; overview: string; vote: number; status: number }
export interface DiscoverSection { key: string; title: string; items: SeerrItem[] }
export interface DiscoverHome { hero_movie?: SeerrItem; hero_tv?: SeerrItem; sections: DiscoverSection[] }
export const discoverHomeOpts = () => ({ queryKey: ['discover-home'], queryFn: () => request<DiscoverHome>('/discover/home') })
export const discoverSearchOpts = (q: string) => ({
  queryKey: ['discover-search', q],
  queryFn: () => request<{ items: SeerrItem[] }>(`/discover/search?q=${encodeURIComponent(q)}`),
  enabled: q.trim().length > 1,
})
export interface Genre { id: number; name: string }
export interface DiscoverFilters { type: 'movie' | 'tv'; genres: string; year_min: string; year_max: string; vote_min: number; sort: string }
export const discoverGenresOpts = (type: 'movie' | 'tv') => ({ queryKey: ['discover-genres', type], queryFn: () => request<{ genres: Genre[] }>(`/discover/genres?type=${type}`) })
const exploreParams = (f: DiscoverFilters, page: number) =>
  new URLSearchParams({ type: f.type, genres: f.genres, year_min: f.year_min, year_max: f.year_max, vote_min: f.vote_min ? String(f.vote_min) : '', sort: f.sort, page: String(page) }).toString()
export const discoverExploreOpts = (f: DiscoverFilters, page: number) => ({
  queryKey: ['discover-explore', f, page],
  queryFn: () => request<{ items: SeerrItem[]; page: number; total_pages: number }>(`/discover/explore?${exploreParams(f, page)}`),
})
export interface TmdbSuggestion { id: number; name: string; image: string; known_for?: string }
export const discoverLibraryOpts = (type: 'movie' | 'tv') => ({ queryKey: ['discover-library', type], queryFn: () => request<{ items: SeerrItem[] }>(`/discover/library?type=${type}`), staleTime: 60_000 })
export const discoverCollectionOpts = (id: number) => ({ queryKey: ['discover-collection', id], queryFn: () => request<{ name: string; items: SeerrItem[] }>(`/discover/collection?id=${id}`), enabled: id > 0 })
export const discoverPersonOpts = (id: number) => ({ queryKey: ['discover-person', id], queryFn: () => request<{ items: SeerrItem[] }>(`/discover/person?id=${id}`), enabled: id > 0 })
export const collectionSearchOpts = (q: string) => ({ queryKey: ['collection-search', q], queryFn: () => request<{ results: TmdbSuggestion[] }>(`/discover/collections?q=${encodeURIComponent(q)}`), enabled: q.trim().length > 1 })
export const personSearchOpts = (q: string) => ({ queryKey: ['person-search', q], queryFn: () => request<{ results: TmdbSuggestion[] }>(`/discover/persons?q=${encodeURIComponent(q)}`), enabled: q.trim().length > 1 })
export const watchlistOpts = () => ({ queryKey: ['watchlist'], queryFn: () => request<{ items: SeerrItem[] }>('/watchlist') })
export const useWatchlistToggle = () =>
  useMutation<{ action: string }, Error, SeerrItem>({ mutationFn: (it) => request('/watchlist/toggle', { method: 'POST', body: JSON.stringify(it) }) })
export interface RequestProfile { id: number; name: string }
export interface RequestFolder { id: number; path: string }
export interface RequestServer {
  id: number; name: string; is4k: boolean; is_default: boolean
  default_profile_id: number; default_root: string; default_lang_profile_id: number
  profiles: RequestProfile[]; root_folders: RequestFolder[]; lang_profiles: RequestProfile[]
}
export interface RequestUser { id: number; name: string; email: string }
export const requestOptionsOpts = (type: 'movie' | 'tv') => ({
  queryKey: ['request-options', type],
  queryFn: () => request<{ servers: RequestServer[]; users: RequestUser[] }>(`/seerr/request-options?type=${type}`),
})
export interface SeerrRequestBody {
  media_type: string; tmdb_id: number; tvdb_id?: number
  server_id?: number; profile_id?: number; root_folder?: string
  language_profile_id?: number; is4k?: boolean; user_id?: number; seasons?: number[]
}
export const useSeerrRequest = () =>
  useMutation<{ ok: boolean }, Error, SeerrRequestBody>({
    mutationFn: (b) => request('/seerr/request', { method: 'POST', body: JSON.stringify(b) }),
  })
export interface SeerrCast { name: string; character: string; profile: string }
export interface SeerrEpisode { code: string; name: string; date: string }
export interface SeerrSeason { number: number; name: string; episodes: number; poster: string; date: string; status: number }
export interface SeerrCompany { name: string; logo?: string }
export interface SeerrVideo { name: string; key: string; type: string }
export interface SeerrDetail {
  media_type: 'movie' | 'tv'; tmdb_id: number; imdb_id?: string; title: string; tagline: string; year: string; overview: string
  backdrop: string; poster: string; genres: string[]; vote: number; vote_count: number; popularity: number
  status: number; status_text: string; release_date: string; language: string; languages: string; country: string
  rating?: string; homepage?: string; runtime?: number; seasons?: number; episodes?: number; trailer?: string; videos?: SeerrVideo[]
  creators?: string[]; studios?: SeerrCompany[]; networks?: SeerrCompany[]; tags?: string[]
  next_episode?: SeerrEpisode; last_episode?: SeerrEpisode; season_list?: SeerrSeason[]
  watch_flatrate?: SeerrCompany[]; watch_buy?: SeerrCompany[]; cast: SeerrCast[]
}
export const seerrDetailOpts = (type: 'movie' | 'tv', id: number) => ({
  queryKey: ['discover-detail', type, id],
  queryFn: () => request<SeerrDetail>(`/discover/detail?type=${type}&id=${id}`),
})
export const usePathmapSuggest = () =>
  useQuery<{ arr_roots: string[]; plex_roots: string[] }>({ queryKey: ['pathmap-suggest'], queryFn: () => request('/arr/pathmap-suggest') })
export const useOptions = () =>
  useQuery<OptionsConfig>({ queryKey: ['options'], queryFn: () => request('/options') })
export const useSaveOptions = () =>
  useMutation<{ ok: boolean }, Error, OptionsConfig>({ mutationFn: (c) => request('/options', { method: 'PUT', body: JSON.stringify(c) }) })
export const usePlexTest = () =>
  useMutation<{ ok: boolean; streams: number; sections: string[] }, Error, { url: string; token: string } | void>({ mutationFn: (b) => request('/plex/test', { method: 'POST', body: JSON.stringify(b ?? {}) }) })

// Built-in autoscan (Settings → Autoscan): a debounced Plex partial-scan service fed
// by arr webhooks / manual triggers / post-upload. See docs/autoscan-plan.md.
export interface AutoscanConfig { enabled: boolean; delay_sec: number; scan_gap_sec?: number; on_upload: boolean; webhook_token: string; log_skipped?: boolean; anchors?: string[]; wait_completion?: boolean; idle_sec?: number; timeout_sec?: number; exclude_exts?: string[]; exclude_paths?: string[]; include_paths?: string[] }
export type ScanStatus = 'pending' | 'scanning' | 'completed' | 'skipped' | 'failed' | 'ignored'
export interface ScanHit { time: string; source: string; event?: string; path: string }
export interface ScanRecord { id: number; path: string; section: string; status: ScanStatus; source: string; event?: string; error?: string; hits?: ScanHit[]; fire_at?: string; created_at: string; started_at?: string; ended_at?: string }
export interface AutoscanStatus { enabled: boolean; paused?: boolean; queued: number; counts: Record<ScanStatus, number>; scans: ScanRecord[]; port?: string }
export const useAutoscanConfig = () =>
  useQuery<AutoscanConfig>({ queryKey: ['autoscan-config'], queryFn: () => request('/autoscan/config') })
export const useSaveAutoscanConfig = () =>
  useMutation<AutoscanConfig, Error, AutoscanConfig>({ mutationFn: (c) => request('/autoscan/config', { method: 'PUT', body: JSON.stringify(c) }) })
export const useAutoscanStatus = () =>
  useQuery<AutoscanStatus>({ queryKey: ['autoscan-status'], queryFn: () => request('/autoscan/status'), refetchInterval: 5000 })
export const useAutoscanTrigger = () =>
  useMutation<{ ok: boolean; queued: number }, Error, string[]>({ mutationFn: (paths) => request('/autoscan/trigger', { method: 'POST', body: JSON.stringify({ paths }) }) })
export const useAutoscanClear = () =>
  useMutation<{ ok: boolean }, Error, void>({ mutationFn: () => request('/autoscan/clear', { method: 'POST' }) })
export const useAutoscanPause = () =>
  useMutation<{ ok: boolean; paused: boolean }, Error, boolean>({ mutationFn: (pause) => request(`/autoscan/${pause ? 'pause' : 'resume'}`, { method: 'POST' }) })

// Seerr multi-instance config (Integrations page): every detected Jellyseerr/Overseerr/
// Seerr container, each with its own URL + API key.
export interface SeerrInstance { name: string; url: string; api_key: string; default?: boolean }
export const useSeerrInstances = () =>
  useQuery<{ instances: SeerrInstance[] }>({ queryKey: ['seerr-instances'], queryFn: () => request('/seerr/instances') })
export const useSaveSeerrInstances = () =>
  useMutation<{ ok: boolean }, Error, SeerrInstance[]>({ mutationFn: (list) => request('/seerr/instances', { method: 'PUT', body: JSON.stringify({ instances: list }) }) })

// teldrive (tgdrive) panel — only active when teldrive remotes exist.
export const useTeldriveRemotes = () =>
  useQuery<{ remotes: string[] }>({ queryKey: ['teldrive-remotes'], queryFn: () => request('/teldrive/remotes'), staleTime: 60_000 })
export interface TdResult { remote: string; name: string; is_dir: boolean; size: number; human: string; category: string; modified: string; dir: string }
export interface TdCat { category: string; bytes: number; human: string; files: number }
export interface TdStorage { remotes: { remote: string; bytes: number; human: string; files: number; categories: TdCat[] }[]; categories: TdCat[]; total_bytes: number; total_human: string; total_files: number }
export const useTeldriveStorage = () =>
  useQuery<TdStorage>({ queryKey: ['teldrive-storage'], queryFn: () => request('/teldrive/storage'), staleTime: 60_000 })
export const useTeldriveSearch = (q: string) =>
  useQuery<{ results: TdResult[]; count: number; errors?: string[] }>({ queryKey: ['teldrive-search', q], queryFn: () => request(`/teldrive/search?q=${encodeURIComponent(q)}`), enabled: q.trim().length > 0 })

export interface FlagInfo { flag: string; help: string; type: string }
export const useRcloneProviders = () =>
  useQuery<{ global: FlagInfo[]; backends: Record<string, FlagInfo[]> }>({
    queryKey: ['rclone-providers'],
    queryFn: () => request('/rclone/providers'),
    staleTime: 60 * 60_000,
  })

export const useRcloneTransfer = () =>
  useMutation<{ job_id: string }, Error, { op: 'copy' | 'move' | 'sync'; items: TransferItem[]; dst: string; dry_run?: boolean; opts?: TransferOpts; queue?: boolean }>({
    mutationFn: (b) => request('/rclone/transfer', { method: 'POST', body: JSON.stringify(b) }),
  })

export interface TransferTask {
  id: string; name: string; op: 'copy' | 'move' | 'sync'
  items: TransferItem[]; dst: string; dry_run?: boolean; opts?: TransferOpts
  schedule?: string; disabled?: boolean; run_mode?: 'queue' | 'now'; created_at?: string; next_run?: string
}
type TaskInput = Omit<TransferTask, 'id' | 'created_at'>

export const useTasks = () =>
  useQuery<TransferTask[]>({ queryKey: ['tasks'], queryFn: () => request('/tasks'), refetchInterval: 5000 })
export const useCreateTask = () =>
  useMutation<TransferTask, Error, TaskInput>({ mutationFn: (t) => request('/tasks', { method: 'POST', body: JSON.stringify(t) }) })
export const useUpdateTask = () =>
  useMutation<TransferTask, Error, { id: string } & TaskInput>({ mutationFn: ({ id, ...t }) => request(`/tasks/${id}`, { method: 'PUT', body: JSON.stringify(t) }) })
export const useDeleteTask = () =>
  useMutation<{ ok: boolean }, Error, string>({ mutationFn: (id) => request(`/tasks/${id}`, { method: 'DELETE' }) })
export const useStopTransfer = () =>
  useMutation<{ ok: boolean }, Error, string>({ mutationFn: (id) => request(`/transfers/${id}/stop`, { method: 'POST' }) })
export const useRunTask = () =>
  useMutation<{ job_id: string }, Error, string>({ mutationFn: (id) => request(`/tasks/${id}/run`, { method: 'POST' }) })
export const useQueueTask = () =>
  useMutation<{ job_id: string }, Error, string>({ mutationFn: (id) => request(`/tasks/${id}/queue`, { method: 'POST' }) })
export const useToggleTask = () =>
  useMutation<TransferTask, Error, string>({ mutationFn: (id) => request(`/tasks/${id}/toggle`, { method: 'POST' }) })

export interface UploaderRemote { task_id?: string; name: string; dest: string; cap: string; cap_files?: number; gap_min: number; bwlimit: string; tpslimit: number }
export interface BalanceConfig { enabled: boolean; max_streak: number; no_repeat: boolean }
export interface QbitConfig { enabled: boolean; action: 'pause' | 'throttle'; dl_kbps: number; up_kbps: number }
export interface PauseConfig { arr_disable: boolean; plex_kill_transcode: boolean; autoscan_hold: boolean; qbit: QbitConfig }
export interface UploaderConfig {
  enabled: boolean; source: string; subpath?: string; cap?: string; cap_files?: number; gap_min?: number; threshold: string; strategy: 'lru' | 'round_robin' | 'most_free'; balance?: BalanceConfig; pause?: PauseConfig; interval_minutes: number
  allowed_from?: string; allowed_until?: string; min_age?: string; delete_empty_src?: boolean; opts?: TransferOpts; excludes?: string[]
  remotes: UploaderRemote[]
}
export interface UploaderStatus {
  enabled: boolean; source: string; threshold: string; last_size: string; last_size_bytes: number
  last_check: string | null; message: string
  remotes: { name: string; task_id?: string; cap: string; used_today: string; used_bytes: number; cap_files?: number; files_today?: number; last_upload: string | null; paused_until?: string | null }[]
}
export const useUploader = () =>
  useQuery<UploaderConfig>({ queryKey: ['uploader'], queryFn: () => request('/uploader') })
export const useSaveUploader = () =>
  useMutation<{ ok: boolean }, Error, UploaderConfig>({ mutationFn: (c) => request('/uploader', { method: 'PUT', body: JSON.stringify(c) }) })
export const useUploaderStatus = () =>
  useQuery<UploaderStatus>({ queryKey: ['uploader-status'], queryFn: () => request('/uploader/status'), refetchInterval: 5000 })
export const useUploaderRun = () =>
  useMutation<{ ok: boolean }, Error, void>({ mutationFn: () => request('/uploader/run', { method: 'POST' }) })
export interface BlockReport { action: string; qbit: string; arr: string; plex: string; autoscan: string }
export const useUploaderTestBlock = () =>
  useMutation<BlockReport, Error, { action: 'apply' | 'restore'; pause?: PauseConfig }>({ mutationFn: (b) => request('/uploader/test-block', { method: 'POST', body: JSON.stringify(b) }) })

export interface SimStep { kind: 'move' | 'wait' | 'blocked'; at: string; until?: string; remote?: string; task_id?: string; bytes?: string; files?: number; max_transfer?: string; remaining?: string; rate?: string; took_min?: number; paused?: boolean; note?: string }
export interface SimRemote { name: string; task_id?: string; bytes: string; files: number; cap: string; cap_files: number }
export interface SimResult { steps: SimStep[]; summary: SimRemote[]; total: string; moved: string; done: boolean; elapsed_min: number }
export interface CalibrationRemote { remote: string; runs: number; avg_speed: string; avg_speed_bytes: number; throttle_rate: number }
export const useUploaderCalibration = () =>
  useQuery<CalibrationRemote[]>({ queryKey: ['uploader-calibration'], queryFn: () => request('/uploader/calibration'), staleTime: 60_000 })
export const useUploaderSimulate = () =>
  useMutation<SimResult, Error, { total: string; avg_file: string; per_conn: string; scenario: string; flood_remote: string; config: UploaderConfig }>({
    mutationFn: (b) => request('/uploader/simulate', { method: 'POST', body: JSON.stringify(b) }),
  })

export interface QueueState { running: boolean; current: { job_id: string; label: string } | null; items: { job_id: string; label: string }[] }
export const useQueue = () =>
  useQuery<QueueState>({ queryKey: ['queue'], queryFn: () => request('/queue'), refetchInterval: 3000 })
export const useQueueAction = () =>
  useMutation<{ ok: boolean }, Error, string>({ mutationFn: (path) => request(`/queue${path}`, { method: 'POST' }) })

export interface ModRole {
  name: string
  registered: boolean
}

// User-managed saltbox_mod roles (sb-ui's own `sbui` role is excluded server-side).
export const useModRoles = () =>
  useQuery<{ roles: ModRole[]; base: string }>({
    queryKey: ['mod-roles'],
    queryFn: () => request('/roles/mod'),
  })

export const useRemoveRole = () =>
  useMutation<{ ok: boolean }, Error, { role: string }>({
    mutationFn: ({ role }) => request(`/roles/${encodeURIComponent(role)}`, { method: 'DELETE' }),
  })
