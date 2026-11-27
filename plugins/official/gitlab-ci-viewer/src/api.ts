export interface GitLabInstance {
  id: string
  name: string
  base_url: string
  host: string
  token_set: boolean
  masked_token?: string
  insecure_skip_verify?: boolean
  enabled: boolean
  is_default?: boolean
  created_at: string
  updated_at: string
}

export interface CreateGitLabInstanceRequest {
  name: string
  base_url: string
  token: string
  insecure_skip_verify?: boolean
  enabled?: boolean
  is_default?: boolean
}

export interface UpdateGitLabInstanceRequest {
  name?: string
  base_url?: string
  token?: string
  insecure_skip_verify?: boolean
  enabled?: boolean
  is_default?: boolean
}

export interface TestGitLabInstanceRequest {
  instance_id?: string
  base_url?: string
  token?: string
  insecure_skip_verify?: boolean
}

export interface TestGitLabInstanceResponse {
  ok: boolean
  username?: string
  name?: string
  version?: string
  message?: string
}

export interface GitLabRepoInfo {
  host: string
  port?: string
  project_path: string
  branch?: string
  head_sha?: string
  remote_name?: string
  remote_url?: string
}

export interface GitLabPipelineSummary {
  id: number
  iid?: number
  status: string
  source?: string
  ref: string
  sha: string
  short_sha: string
  web_url: string
  created_at?: string
  updated_at?: string
  started_at?: string
  finished_at?: string
  duration?: number
  commit_title?: string
  author_name?: string
  matches_head?: boolean
  matches_branch?: boolean
}

export interface GitLabJobSummary {
  id: number
  name: string
  stage: string
  status: string
  web_url?: string
  duration?: number
  allow_failure?: boolean
  failure_reason?: string
  runner_name?: string
  created_at?: string
  started_at?: string
  finished_at?: string
  is_bridge?: boolean
}

export interface GitLabStageGroup {
  name: string
  status: string
  jobs: GitLabJobSummary[]
}

export interface GitLabPipelinesResponse {
  configured: boolean
  matched: boolean
  reason?: string
  repo?: GitLabRepoInfo
  instance?: GitLabInstance
  latest_pipeline?: GitLabPipelineSummary
  pipelines: GitLabPipelineSummary[]
}

export interface GitLabPipelineDetailResponse {
  repo?: GitLabRepoInfo
  instance?: GitLabInstance
  pipeline: GitLabPipelineSummary
  stages: GitLabStageGroup[]
  jobs: GitLabJobSummary[]
}

export interface GitLabJobTraceResponse {
  job_id: number
  tail_lines: number
  total_lines: number
  truncated: boolean
  trace: string
}

export interface GitLabTargetParams {
  workspace?: string
  connection_id?: string | null
  session_id?: string
  instance_id?: string
  project?: string
  ref?: string
  sha?: string
  limit?: number
}

export type HostFetchFn = (path: string, init?: RequestInit) => Promise<Response>

function buildTargetQuery(params: GitLabTargetParams): URLSearchParams {
  const q = new URLSearchParams()
  if (params.workspace) q.set('workspace', params.workspace)
  if (params.connection_id) q.set('connection_id', params.connection_id)
  if (params.session_id) q.set('session_id', params.session_id)
  if (params.instance_id) q.set('instance_id', params.instance_id)
  if (params.project) q.set('project', params.project)
  if (params.ref) q.set('ref', params.ref)
  if (params.sha) q.set('sha', params.sha)
  if (params.limit && params.limit > 0) q.set('limit', String(params.limit))
  return q
}

export interface GitLabApiClient {
  listInstances(): Promise<{ items: GitLabInstance[] }>
  createInstance(payload: CreateGitLabInstanceRequest): Promise<{ instance: GitLabInstance }>
  updateInstance(id: string, payload: UpdateGitLabInstanceRequest): Promise<{ instance: GitLabInstance }>
  deleteInstance(id: string): Promise<{ ok: boolean; deleted: string }>
  testInstance(payload: TestGitLabInstanceRequest): Promise<TestGitLabInstanceResponse>
  listPipelines(params: GitLabTargetParams, signal?: AbortSignal): Promise<GitLabPipelinesResponse>
  getPipelineDetail(
    pipelineId: number,
    params: GitLabTargetParams,
    signal?: AbortSignal,
  ): Promise<GitLabPipelineDetailResponse>
  getJobTrace(
    jobId: number,
    params: GitLabTargetParams & { tail?: number },
    signal?: AbortSignal,
  ): Promise<GitLabJobTraceResponse>
  retryPipeline(
    pipelineId: number,
    params: GitLabTargetParams,
  ): Promise<{ pipeline: GitLabPipelineSummary }>
  cancelPipeline(
    pipelineId: number,
    params: GitLabTargetParams,
  ): Promise<{ pipeline: GitLabPipelineSummary }>
  retryJob(jobId: number, params: GitLabTargetParams): Promise<{ job: GitLabJobSummary }>
  playJob(jobId: number, params: GitLabTargetParams): Promise<{ job: GitLabJobSummary }>
}

export function createGitLabApi(hostFetch: HostFetchFn): GitLabApiClient {
  const requestJson = async <T,>(path: string, init?: RequestInit): Promise<T> => {
    const response = await hostFetch(`/api/v1${path}`, init)
    const raw = await response.text()
    let payload: unknown = null
    if (raw) {
      try {
        payload = JSON.parse(raw)
      } catch {
        payload = raw
      }
    }
    if (!response.ok) {
      const detail =
        payload && typeof payload === 'object' && 'detail' in payload && typeof (payload as { detail?: unknown }).detail === 'string'
          ? (payload as { detail: string }).detail
          : payload && typeof payload === 'object' && 'error' in payload && typeof (payload as { error?: unknown }).error === 'string'
            ? (payload as { error: string }).error
            : `请求失败（${response.status}）`
      throw new Error(detail)
    }
    return payload as T
  }

  const postJson = <T,>(path: string, body: unknown, method: 'POST' | 'PATCH' = 'POST'): Promise<T> =>
    requestJson<T>(path, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })

  return {
    listInstances: () => requestJson<{ items: GitLabInstance[] }>('/gitlab/instances'),

    createInstance: (payload) =>
      postJson<{ instance: GitLabInstance }>('/gitlab/instances', payload, 'POST'),

    updateInstance: (id, payload) =>
      postJson<{ instance: GitLabInstance }>(
        `/gitlab/instances/${encodeURIComponent(id)}`,
        payload,
        'PATCH',
      ),

    deleteInstance: (id) =>
      requestJson<{ ok: boolean; deleted: string }>(
        `/gitlab/instances/${encodeURIComponent(id)}`,
        { method: 'DELETE' },
      ),

    testInstance: (payload) =>
      postJson<TestGitLabInstanceResponse>('/gitlab/instances/test', payload, 'POST'),

    listPipelines: (params, signal) => {
      const q = buildTargetQuery(params)
      const suffix = q.toString() ? `?${q.toString()}` : ''
      return requestJson<GitLabPipelinesResponse>(`/gitlab/pipelines${suffix}`, { signal })
    },

    getPipelineDetail: (pipelineId, params, signal) => {
      const q = buildTargetQuery(params)
      const suffix = q.toString() ? `?${q.toString()}` : ''
      return requestJson<GitLabPipelineDetailResponse>(
        `/gitlab/pipelines/${pipelineId}${suffix}`,
        { signal },
      )
    },

    getJobTrace: (jobId, params, signal) => {
      const q = buildTargetQuery(params)
      if (params.tail && params.tail > 0) q.set('tail', String(params.tail))
      const suffix = q.toString() ? `?${q.toString()}` : ''
      return requestJson<GitLabJobTraceResponse>(`/gitlab/jobs/${jobId}/trace${suffix}`, { signal })
    },

    retryPipeline: (pipelineId, params) => {
      const q = buildTargetQuery(params)
      const suffix = q.toString() ? `?${q.toString()}` : ''
      return postJson<{ pipeline: GitLabPipelineSummary }>(
        `/gitlab/pipelines/${pipelineId}/retry${suffix}`,
        {},
        'POST',
      )
    },

    cancelPipeline: (pipelineId, params) => {
      const q = buildTargetQuery(params)
      const suffix = q.toString() ? `?${q.toString()}` : ''
      return postJson<{ pipeline: GitLabPipelineSummary }>(
        `/gitlab/pipelines/${pipelineId}/cancel${suffix}`,
        {},
        'POST',
      )
    },

    retryJob: (jobId, params) => {
      const q = buildTargetQuery(params)
      const suffix = q.toString() ? `?${q.toString()}` : ''
      return postJson<{ job: GitLabJobSummary }>(
        `/gitlab/jobs/${jobId}/retry${suffix}`,
        {},
        'POST',
      )
    },

    playJob: (jobId, params) => {
      const q = buildTargetQuery(params)
      const suffix = q.toString() ? `?${q.toString()}` : ''
      return postJson<{ job: GitLabJobSummary }>(
        `/gitlab/jobs/${jobId}/play${suffix}`,
        {},
        'POST',
      )
    },
  }
}
