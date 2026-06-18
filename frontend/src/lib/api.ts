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
export interface UploaderConfig {
  enabled: boolean; source: string; threshold: string; strategy: 'lru' | 'round_robin' | 'most_free'; interval_minutes: number
  allowed_from?: string; allowed_until?: string; min_age?: string; delete_empty_src?: boolean; excludes?: string[]
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
