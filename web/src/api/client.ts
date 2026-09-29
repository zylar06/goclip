import type { AnalysisOptions, Cue, Draft, Language, ModelKind, ModelSettings, ModelStatus, Project, ProjectDetail, Settings, Task } from './contracts'
import type { Error as WireError } from '../generated/api'

const ROOT = '/api/v1'
const part = encodeURIComponent
export const projectPath = (id: string) => `/projects/${part(id)}`
export const urls = {
  source: (id: string) => ROOT + projectPath(id) + '/source',
  events: (id: string) => `${ROOT}/tasks/${part(id)}/events`,
  video: (pid: string, tid: string, download = false) =>
    ROOT + projectPath(pid) + `/exports/${part(tid)}/video${download ? '?download=true' : ''}`,
}
export class ApiError extends Error {
  constructor(message: string, public status: number, public code = '', public retryable = false, public requestId = '') {
    super(message)
    this.name = 'ApiError'
  }
}
export function errorText(error: unknown): string {
  if (error instanceof ApiError) return `${error.message}${error.requestId ? ` (${error.requestId})` : ''}`
  return error instanceof Error ? error.message : 'Request failed. Please retry.'
}

/** No automatic mutation retries: imports/model calls/exports may incur costs.
 * Timeouts are explicit errors; reconcile project/task state before retrying.
 */
export async function request<T>(path: string, init: RequestInit = {}, timeout = 30000, blob = false): Promise<T> {
  const controller = new AbortController()
  const abort = () => controller.abort(init.signal?.reason)
  if (init.signal?.aborted) abort()
  init.signal?.addEventListener('abort', abort, { once: true })
  const timer = setTimeout(() => controller.abort(new Error('Request timed out. Check task state before retrying.')), timeout)
  try {
    const response = await fetch(ROOT + path, { ...init, signal: controller.signal, credentials: 'same-origin' })
    if (!response.ok) {
      const text = await response.text()
      let detail: Partial<WireError> = {}
      if (response.headers.get('content-type')?.includes('application/json')) {
        try { detail = JSON.parse(text) }
        catch (cause) { throw new ApiError(`HTTP ${response.status}: invalid error response (${errorText(cause)})`, response.status) }
      }
      throw new ApiError(detail.message || `HTTP ${response.status}: ${text.slice(0, 240) || response.statusText}`, response.status,
        detail.code, detail.retryable, detail.request_id)
    }
    if (response.status === 204) return undefined as T
    if (blob) return await response.blob() as T
    const text = await response.text()
    return (text ? JSON.parse(text) : undefined) as T
  } catch (error) {
    if (controller.signal.aborted && !init.signal?.aborted) {
      throw new Error('Request timed out. Check task state before retrying.')
    }
    throw error
  } finally {
    clearTimeout(timer)
    init.signal?.removeEventListener('abort', abort)
  }
}
const json = (method: string, body: unknown): RequestInit => ({
  method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
})

export function normalizeDetail(data: ProjectDetail): ProjectDetail {
  return { ...data, drafts: data.drafts ?? [], tasks: data.tasks ?? [], candidates: data.candidates ?? [], exports: data.exports ?? [] }
}
export const api = {
  projects: async (signal?: AbortSignal) => (await request<Project[] | null>('/projects', { signal })) ?? [],
  project: async (id: string, signal?: AbortSignal) => normalizeDetail(await request<ProjectDetail>(projectPath(id), { signal })),
  importFile: (form: FormData) => request<Project>('/projects', { method: 'POST', body: form }, 15 * 60 * 1000),
  importURL: (name: string, url: string) => request<Project>('/projects', json('POST', { name, url })),
  removeProject: (id: string) => request<void>(projectPath(id), json('DELETE', { confirm: true })),
  analyze: (id: string, body: AnalysisOptions) => request<Task>(projectPath(id) + '/analyze', json('POST', body)),
  subtitles: async (id: string, signal?: AbortSignal) => (await request<Cue[] | null>(projectPath(id) + '/subtitles', { signal })) ?? [],
  createDraft: (id: string, draft: Draft) => request<Draft>(projectPath(id) + '/drafts', json('POST', draft)),
  saveDraft: (id: string, draft: Draft) => request<Draft>(projectPath(id) + `/drafts/${part(draft.id)}`, json('PUT', draft)),
  duplicate: (id: string, draftId: string, title: string, language: Language) =>
    request<Draft>(projectPath(id) + `/drafts/${part(draftId)}/duplicate`, json('POST', { title, language })),
  rewrite: (id: string, draft: Draft, instruction: string) =>
    request<Draft>(projectPath(id) + '/rewrite', json('POST', { draft, instruction }), 310000),
  titlePreview: (id: string, draft: Draft, signal?: AbortSignal) =>
    request<Blob>(projectPath(id) + '/title-preview', { ...json('POST', draft), signal }, 30000, true),
  export: (id: string, draftId: string, revision: number) =>
    request<Task>(projectPath(id) + `/drafts/${part(draftId)}/export`, json('POST', { revision })),
  task: (id: string, signal?: AbortSignal) => request<Task>(`/tasks/${part(id)}`, { signal }),
  cancel: (id: string) => request<Task>(`/tasks/${part(id)}/cancel`, { method: 'POST' }),
  retry: (id: string) => request<Task>(`/tasks/${part(id)}/retry`, { method: 'POST' }),
  settings: (signal?: AbortSignal) => request<Settings>('/settings', { signal }),
  saveModel: (kind: ModelKind, settings: ModelSettings) => request<ModelStatus>(`/settings/${kind}`, json('PUT', settings)),
  testModel: (kind: ModelKind) => request<{ ok: boolean; message: string }>(`/settings/${kind}/test`, { method: 'POST' }, 90000),
  saveCookies: (text: string) => request<void>('/settings/cookies', {
    method: 'PUT', headers: { 'Content-Type': 'text/plain; charset=utf-8' }, body: text,
  }),
  removeCookies: () => request<void>('/settings/cookies', { method: 'DELETE' }),
}
