/**
 * API fixtures — the single source of truth for what the Go backend sends.
 *
 * Each factory returns a payload shaped exactly like the JSON the Go handlers
 * emit (the TS interfaces in lib/api.ts mirror the Go struct json tags). Because
 * the whole test suite feeds the UI through these, a field renamed on the Go side
 * that isn't reflected here — or in lib/api.ts — surfaces as a compile error or a
 * failing render assertion, which is the drift we keep getting bitten by.
 *
 * Keep every field the real handler returns present here, even when a given test
 * doesn't assert on it: the point is that this is a faithful stand-in for the API.
 */
import type { UploaderStatus, UploadPlan, UploadPlanRemote, Job } from '@/lib/api'

type Overrides<T> = Partial<T>

export function makePlanRemote(o: Overrides<UploadPlanRemote> = {}): UploadPlanRemote {
  return {
    remote: 'main_01',
    dest: 'main_01:Media',
    bytes: 10 * 1024 ** 3,
    human: '10.0G',
    files: 3,
    stop_file: '',
    eta_sec: 1800,
    at_sec: 0,
    round: 0,
    capped: true,
    fill_human: '10.0G',
    cmd: 'rclone move /mnt/local/Media main_01:Media --max-transfer 10737418240B --cutoff-mode cautious',
    ...o,
  }
}

export function makePlan(o: Overrides<UploadPlan> = {}): UploadPlan {
  const remotes = o.remotes ?? [makePlanRemote()]
  return {
    at: '2026-08-11T00:00:00Z',
    source_bytes: 30 * 1024 ** 3,
    source_human: '30.0G',
    files_total: 3,
    threshold_human: '1.0G',
    meets_threshold: true,
    leftover_bytes: 0,
    leftover_human: '0',
    leftover_why: undefined,
    transfer_sec: 1800,
    total_eta_sec: 1800,
    ...o,
    remotes,
  }
}

export function makeStatus(o: Overrides<UploaderStatus> = {}): UploaderStatus {
  return {
    enabled: true,
    source: '/mnt/local/Media',
    threshold: '1G',
    last_size: '30.0G',
    last_size_bytes: 30 * 1024 ** 3,
    last_check: '2026-08-11T00:00:00Z',
    message: '',
    plan: makePlan(),
    checking: false,
    history: [],
    balance_next: null,
    resume: undefined,
    remotes: [
      { name: 'main_01', cap: '500G', used_today: '0', used_bytes: 0, last_upload: null },
    ],
    ...o,
  }
}

export function makeJob(o: Overrides<Job> = {}): Job {
  return {
    id: 'job-1',
    tag: 'uploader:check',
    action: 'uploader',
    status: 'completed',
    created_at: '2026-08-11T00:00:00Z',
    log_lines: 0,
    ...o,
  }
}
